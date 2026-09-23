// Package campaign runs Broadcast and WA Story.
//
// One scheduler for both, because they are the same problem wearing two hats: a
// campaign becomes due, a worker takes it, work items are claimed one at a time,
// each is attempted, and the outcome is written down before the next one starts.
// Two schedulers would mean two places to get leases, cancellation and restart
// recovery right, and they would drift.
//
// Nothing here holds the queue in memory. The scheduler asks the database what
// is due, the database hands out exclusive claims through `for update skip
// locked`, and a lease expires on its own if this process dies. That is what
// makes "the backend restarted mid-campaign" an ordinary event rather than an
// incident: the next tick picks the campaign up where it stopped, reconciles
// whatever was in flight, and carries on.
//
// What this package deliberately does NOT do:
//
//   - It does not fake activity. No typing indicators, no read receipts, no
//     invented presence. The only pause between two sends is the delay profile
//     the operator chose, which is queue pacing and is described as nothing more.
//   - It does not invent outcomes. A send whose result is unknown is recorded as
//     unknown and left for a person, because the alternative is sending somebody
//     the same message twice.
package campaign

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/salesan/omnichannel/backend/internal/compose"
	"github.com/salesan/omnichannel/backend/internal/config"
	"github.com/salesan/omnichannel/backend/internal/media"
	"github.com/salesan/omnichannel/backend/internal/mediafetch"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

// Runner is the scheduler plus its workers.
type Runner struct {
	cfg  *config.Config
	repo *repository.Repo
	wa   *wa.Manager
	hub  *realtime.Hub
	log  *slog.Logger

	// owner identifies this process instance on the leases it takes. Recorded
	// for diagnosis; exclusion comes from the lease expiry, not from this.
	owner uuid.UUID

	// inFlight stops one process from starting a second worker for a campaign it
	// is already running. The database prevents two *processes* colliding; this
	// prevents one process colliding with itself between ticks.
	mu       sync.Mutex
	inFlight map[uuid.UUID]bool

	// sendSlots bounds how many recipients are being sent to at once across every
	// campaign and every number this process is running. See
	// config.CampaignSendConcurrency for why it exists.
	sendSlots chan struct{}
	// storySlots bounds how many Story publications are being pushed at once.
	// Separate from sendSlots because one Story holds its slot for minutes, not
	// seconds, and sharing would let two Stories stall every broadcast. See
	// config.CampaignStoryConcurrency.
	storySlots chan struct{}

	wake   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// acquireSend takes a send slot, or reports false if the run was cancelled while
// waiting. Waiting is the point: a number that has to queue for its turn simply
// sends a moment later, which is invisible next to a delay profile measured in
// seconds or minutes.
func (r *Runner) acquireSend(ctx context.Context) bool {
	select {
	case r.sendSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *Runner) releaseSend() { <-r.sendSlots }

func (r *Runner) acquireStory(ctx context.Context) bool {
	select {
	case r.storySlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *Runner) releaseStory() { <-r.storySlots }

// New builds a Runner. Start must be called to make it do anything.
func New(
	cfg *config.Config,
	repo *repository.Repo,
	manager *wa.Manager,
	hub *realtime.Hub,
	log *slog.Logger,
) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	slots := cfg.CampaignSendConcurrency
	if slots < 1 {
		slots = 1
	}
	storySlots := cfg.CampaignStoryConcurrency
	if storySlots < 1 {
		storySlots = 1
	}
	return &Runner{
		cfg:        cfg,
		repo:       repo,
		wa:         manager,
		hub:        hub,
		log:        log.With("component", "campaign"),
		owner:      uuid.New(),
		inFlight:   map[uuid.UUID]bool{},
		sendSlots:  make(chan struct{}, slots),
		storySlots: make(chan struct{}, storySlots),
		wake:       make(chan struct{}, 1),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start launches the scheduler and the Story expiry sweep.
func (r *Runner) Start() {
	r.log.Info("campaign scheduler starting",
		"owner", r.owner,
		"poll", r.cfg.CampaignPollInterval,
		"lease", r.cfg.CampaignLease)

	r.wg.Add(2)
	go r.scheduleLoop()
	go r.expiryLoop()
}

// Stop asks every worker to finish the target it is on and release its lease.
func (r *Runner) Stop() {
	r.cancel()
	r.wg.Wait()
}

// Wake nudges the scheduler, so a campaign started from the interface begins
// within a moment rather than at the next tick. Never blocks: a wake already
// queued is as good as a second one.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) scheduleLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(r.cfg.CampaignPollInterval)
	defer ticker.Stop()

	for {
		r.tick()
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
		case <-r.wake:
		}
	}
}

func (r *Runner) tick() {
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()

	jobs, err := r.repo.ClaimDueCampaigns(ctx, r.owner, r.cfg.CampaignLease, r.cfg.CampaignConcurrency)
	if err != nil {
		r.log.Error("claim due campaigns", "err", err)
		return
	}
	for _, job := range jobs {
		if !r.begin(job.ID) {
			// Already running here. The claim renewed the lease, which is
			// harmless — the worker holding it keeps going.
			continue
		}
		job := job
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer r.end(job.ID)
			r.run(job)
		}()
	}
}

func (r *Runner) begin(id uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inFlight[id] {
		return false
	}
	r.inFlight[id] = true
	return true
}

func (r *Runner) end(id uuid.UUID) {
	r.mu.Lock()
	delete(r.inFlight, id)
	r.mu.Unlock()
}

// expiryLoop closes out Stories past their 24 hours.
func (r *Runner) expiryLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
		}

		ctx, cancel := context.WithTimeout(r.ctx, time.Minute)
		n, err := r.repo.ExpireStories(ctx)
		cancel()
		if err != nil {
			r.log.Error("expire stories", "err", err)
			continue
		}
		if n > 0 {
			r.log.Info("stories expired", "count", n)
		}

		// Recipients a finished campaign left mid-send. Swept here rather than
		// by a worker because the scheduler will never claim their campaign
		// again: it is already cancelled or settled.
		ctx, cancel = context.WithTimeout(r.ctx, time.Minute)
		orphans, err := r.repo.CloseOrphanedTargets(ctx)
		cancel()
		if err != nil {
			r.log.Error("close orphaned targets", "err", err)
		} else if orphans > 0 {
			r.log.Warn("closed recipients left mid-send by a finished campaign",
				"count", orphans)
		}
	}
}

// run executes one campaign from claim to settled status.
func (r *Runner) run(job repository.CampaignJob) {
	log := r.log.With("campaign_id", job.ID, "type", job.CampaignType)
	log.Info("campaign started", "name", job.Name, "devices", len(job.AccountIDs))

	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()

	// Keep the lease alive while the campaign runs. A campaign on the Santai
	// profile with a thousand recipients takes days; a fixed lease would expire
	// under it and invite a second worker in.
	stopRenew := r.renewLease(ctx, job.ID)
	defer stopRenew()

	r.activity(ctx, job, "execution_started", "", "")

	if len(job.AccountIDs) == 0 {
		r.fail(ctx, job, "Tidak ada perangkat pengirim yang dipilih.")
		return
	}

	// Settle whatever a previous run left in flight before adding to it.
	if sent, unknown, err := r.repo.ReconcileStuckTargets(ctx, job.ID); err != nil {
		log.Error("reconcile stuck targets", "err", err)
	} else if sent > 0 || unknown > 0 {
		log.Warn("reconciled interrupted sends", "confirmed_sent", sent, "unknown", unknown)
	}

	// A cancel that arrived while an earlier worker held this campaign is
	// settled before anything else. Pressing cancel can only close a campaign
	// nobody is running; one pressed mid-flight just sets the flag and waits
	// for the worker to notice. That worker may never have: a restart, or a
	// send that hung before this release bounded them. The campaign is claimed
	// again precisely so it can be closed here, and closing it first means no
	// media is fetched and no recipient is sent to on the way out.
	if cancelled, err := r.repo.CancelRequested(ctx, job.ID); err != nil {
		log.Error("check cancel request", "err", err)
	} else if cancelled {
		r.settleCancelled(ctx, job)
		return
	}

	// Media is fetched once for the whole campaign and uploaded once per device.
	// The temporary file is removed on every path out of this function, which is
	// what keeps "jangan simpan file media" true in practice and not just in the
	// schema.
	var fetched *mediafetch.Result
	switch {
	case job.MediaStoragePath != nil && *job.MediaStoragePath != "":
		// An uploaded document, read back out of the private bucket. It was
		// classified when it was uploaded, so nothing is re-sniffed here; what is
		// restored is the filename, because that is what the recipient sees.
		res, err := r.openStoredDocument(ctx, job)
		if err != nil {
			r.fail(ctx, job, fmt.Sprintf("Dokumen tidak dapat dibaca: %s", err))
			return
		}
		fetched = res
		defer fetched.Cleanup()

	case job.MediaURL != nil && *job.MediaURL != "":
		res, err := mediafetch.Fetch(ctx, *job.MediaURL, r.mediaLimits(), false)
		if err != nil {
			r.fail(ctx, job, fmt.Sprintf("Media tidak dapat diambil: %s", err))
			return
		}
		fetched = res
		defer fetched.Cleanup()
	}

	switch job.CampaignType {
	case models.CampaignStory:
		r.runStory(ctx, job, fetched)
	default:
		r.runBroadcast(ctx, job, fetched)
	}
}

func (r *Runner) mediaLimits() mediafetch.Limits {
	return mediafetch.Limits{
		Timeout:      r.cfg.CampaignMediaTimeout,
		MaxBytes:     r.cfg.CampaignMediaMaxBytes,
		MaxRedirects: 3,
		TempDir:      r.cfg.CampaignTempDir,
	}
}

// renewLease keeps the campaign's hold fresh until the returned stop is called.
func (r *Runner) renewLease(ctx context.Context, campaignID uuid.UUID) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(r.cfg.CampaignLease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := r.repo.RenewCampaignLease(c, campaignID, r.owner, r.cfg.CampaignLease); err != nil {
					r.log.Warn("renew campaign lease", "campaign_id", campaignID, "err", err)
				}
				cancel()
			}
		}
	}()
	return func() { close(done) }
}

// --- broadcast ---------------------------------------------------------------

func (r *Runner) runBroadcast(ctx context.Context, job repository.CampaignJob, fetched *mediafetch.Result) {
	log := r.log.With("campaign_id", job.ID)

	// Devices send in parallel — they are separate phone numbers on separate
	// sockets, and serialising them would make a two-number campaign take twice
	// as long for no reason. Within one device, sends are strictly sequential so
	// the delay profile means what it says.
	var wg sync.WaitGroup
	online := 0
	for _, accountID := range job.AccountIDs {
		if !r.wa.DeviceOnline(accountID) {
			// Not a failure of the recipients: their rows stay pending and the
			// campaign is picked up again once the device reconnects. This is
			// the "pause when a device disconnects" the specification asks for.
			log.Warn("sender device offline, its share is paused", "account_id", accountID)
			continue
		}
		online++

		var prepared *wa.PreparedMedia
		if fetched != nil {
			p, err := r.wa.PrepareCampaignMedia(ctx, accountID, fetched.File, fetched.Info)
			if err != nil {
				log.Error("prepare media for device", "account_id", accountID, "err", err)
				_ = r.repo.MarkDeviceFailed(ctx, job.ID, accountID,
					fmt.Sprintf("Media gagal diunggah: %s", err))
				continue
			}
			prepared = p
		}

		wg.Add(1)
		go func(accountID uuid.UUID, prepared *wa.PreparedMedia) {
			defer wg.Done()
			r.runDevice(ctx, job, accountID, prepared)
		}(accountID, prepared)
	}
	wg.Wait()

	if online == 0 {
		// Nothing could be attempted. Waiting is the right answer when the
		// cause is a phone that will come back, so the lease is dropped and the
		// campaign is left running for the next tick to retry.
		//
		// It is the wrong answer when the number has been logged out of
		// WhatsApp, because then the retry never succeeds and the campaign
		// spins for ever, sending nothing, while the screen says "Berjalan".
		// So the wait is bounded: the first tick that finds nothing online
		// stamps the campaign, a number coming back clears the stamp, and a
		// campaign still stranded after the grace period is failed with the
		// numbers named.
		since, err := r.repo.MarkCampaignStalled(ctx, job.ID)
		if err != nil {
			log.Error("mark campaign stalled", "err", err)
		} else if stalled := time.Since(since); stalled >= r.cfg.CampaignOfflineGrace {
			log.Warn("no sender device online past the grace period, campaign failed",
				"stalled_for", stalled.Round(time.Minute))
			r.fail(ctx, job, r.offlineReason(ctx, job))
			return
		}
		log.Warn("no sender device online, campaign paused", "since", since)
		if err := r.repo.ReleaseCampaign(ctx, job.ID, r.owner); err != nil {
			log.Error("release campaign", "err", err)
		}
		r.publish(ctx, job)
		return
	}

	// At least one number answered, so whatever outage there was is over.
	if err := r.repo.ClearCampaignStalled(ctx, job.ID); err != nil {
		log.Error("clear campaign stalled", "err", err)
	}

	r.settleBroadcast(ctx, job)
}

// runDevice sends one device's share, one recipient at a time.
func (r *Runner) runDevice(
	ctx context.Context,
	job repository.CampaignJob,
	accountID uuid.UUID,
	prepared *wa.PreparedMedia,
) {
	log := r.log.With("campaign_id", job.ID, "account_id", accountID)
	if err := r.repo.MarkDeviceRunning(ctx, job.ID, accountID); err != nil {
		log.Warn("mark device running", "err", err)
	}

	// The operator's own min/max when they set one, otherwise the named profile.
	delay := job.DelayRange()
	// Seeded per device so two numbers do not spin identical text in lockstep,
	// which would be its own pattern.
	rng := rand.New(rand.NewSource(time.Now().UnixNano() ^ int64(accountID[0])<<32))
	first := true

	for {
		if ctx.Err() != nil {
			return
		}
		// Cancellation is read between recipients. Stopping mid-send would leave
		// somebody in a state nobody can describe.
		if cancelled, err := r.repo.CancelRequested(ctx, job.ID); err != nil {
			log.Warn("read cancel flag", "err", err)
		} else if cancelled {
			log.Info("campaign cancelled, device stopping")
			return
		}
		if !r.wa.DeviceOnline(accountID) {
			if job.AutoRetryOnDisconnect {
				// The share stays pending and is picked up when the number
				// reconnects. This is the "pause when a device disconnects" the
				// specification asks for.
				log.Warn("device went offline mid-campaign, pausing its share")
				return
			}
			// Auto-retry off: the remaining recipients are released rather than
			// left waiting, because whoever turned it off said this campaign is
			// worth less late than not at all. They are marked, not deleted —
			// the report has to be able to say what did not go out and why.
			log.Warn("device went offline and auto-retry is off, releasing its share")
			if n, err := r.repo.AbandonDeviceShare(ctx, job.ID, accountID,
				"nomor terputus dan auto-retry dimatikan"); err != nil {
				log.Error("release device share", "err", err)
			} else if n > 0 {
				log.Info("released recipients", "count", n)
			}
			return
		}

		// One at a time: claiming a batch would mean holding leases on
		// recipients this device will not reach for the next twenty minutes.
		targets, err := r.repo.ClaimTargets(ctx, job.ID, accountID, r.cfg.CampaignLease, 1)
		if err != nil {
			log.Error("claim targets", "err", err)
			return
		}
		if len(targets) == 0 {
			return
		}

		if !first {
			sleep(ctx, jitterBetween(rng, delay.Min, delay.Max))
			if ctx.Err() != nil {
				return
			}
		}
		first = false

		// Bounded here rather than around the whole device loop. A number spends
		// almost all of its time asleep between recipients, and holding a slot
		// through that would let six numbers monopolise the queue while the rest
		// waited hours. Held only across the send itself, the slot throttles the
		// expensive part — the WhatsApp round trip and the writes that record it
		// — and, because the loop cannot come round again until this send
		// finishes, it throttles the polling queries with it.
		if !r.acquireSend(ctx) {
			return
		}
		r.sendOne(ctx, job, targets[0], prepared, rng)
		r.releaseSend()
	}
}

// sendOne attempts one recipient and records what happened.
func (r *Runner) sendOne(
	ctx context.Context,
	job repository.CampaignJob,
	t repository.QueuedTarget,
	prepared *wa.PreparedMedia,
	rng *rand.Rand,
) {
	log := r.log.With("campaign_id", job.ID, "target_id", t.ID)

	body := ""
	if t.RenderedBody != nil {
		body = *t.RenderedBody
	}
	if body == "" {
		// Spun per recipient, so a spintax template genuinely varies across the
		// list rather than being resolved once for everybody.
		built, err := compose.Build(job.Template, t.Variables, rng)
		if err != nil {
			r.failTarget(ctx, job, t, uuid.Nil, "template", err.Error())
			return
		}
		if len(built.Missing) > 0 {
			r.failTarget(ctx, job, t, uuid.Nil, "variabel_kosong",
				"Variabel tanpa nilai: "+strings.Join(built.Missing, ", "))
			return
		}
		body = built.Body
	}

	msg, err := wa.CampaignMessage(body, prepared)
	if err != nil {
		r.failTarget(ctx, job, t, uuid.Nil, "pesan", err.Error())
		return
	}

	waID, err := r.wa.NewMessageID(t.AccountID)
	if err != nil {
		r.failTarget(ctx, job, t, uuid.Nil, "perangkat", err.Error())
		return
	}

	// The attempt row goes in BEFORE the network call. Its unique index is what
	// refuses a second successful send to the same recipient from the same
	// device, whatever races or retries lead here.
	attemptID, err := r.repo.BeginAttempt(ctx, t, waID)
	if err != nil {
		if errors.Is(err, repository.ErrAlreadySent) {
			log.Warn("target already sent, skipping")
			return
		}
		log.Error("begin attempt", "err", err)
		return
	}

	// The message row is written pending first, so an interrupted send leaves
	// evidence that reconciliation can read. It carries sender_source =
	// 'broadcast' and the campaign id, which is what keeps campaign traffic out
	// of Pesan Terkirim, Kontak Terlayani, SLA and follow-up while still showing
	// the operator, in the thread, exactly what their customer received.
	messageID := r.recordOutgoing(ctx, job, t, body, waID, prepared)

	// Bounded, because whatsmeow's wait for the server acknowledgement is not.
	// The deadline is on the network call alone: the writes that record what
	// happened run on the campaign's own context, which is still live.
	sendCtx, cancelSend := context.WithTimeout(ctx, r.cfg.CampaignSendTimeout)
	res, sendErr := r.wa.SendCampaignMessage(sendCtx, t.AccountID, t.ChatJID, msg, waID)
	cancelSend()
	if sendErr != nil {
		if messageID != nil {
			detail := sendErr.Error()
			if _, err := r.repo.SetMessageOutcome(ctx, *messageID, models.MessageStatusFailed, nil, &detail); err != nil {
				log.Warn("record message failure", "err", err)
			}
		}
		if ctx.Err() == nil && isSendTimeout(sendErr) {
			// The message may or may not have reached WhatsApp, and a broadcast
			// retry mints a fresh message id, so re-sending could deliver the
			// same thing twice. Recorded as unknown and left for a person,
			// exactly as an interrupted send is.
			r.failTargetFinal(ctx, job, t, attemptID, repository.ErrCodeUnknownOutcome,
				"Pengiriman tidak dijawab WhatsApp dalam batas waktu. Hasilnya tidak diketahui; periksa percakapan sebelum mencoba ulang.")
			return
		}
		if errors.Is(sendErr, wa.ErrGroupAdminsOnly) {
			// Not a transient failure: the group's settings refuse this number.
			// Retrying three times only delays the campaign's verdict.
			r.failTargetFinal(ctx, job, t, attemptID, "grup_admin", wa.ErrGroupAdminsOnly.Error())
			return
		}
		r.failTarget(ctx, job, t, attemptID, "kirim", sendErr.Error())
		return
	}

	at := res.Timestamp
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if messageID != nil {
		if _, err := r.repo.SetMessageOutcome(ctx, *messageID, models.MessageStatusSent, &at, nil); err != nil {
			log.Warn("record message sent", "err", err)
		}
	}
	if err := r.repo.FinishAttemptSent(ctx, attemptID, t, waID, messageID, at); err != nil {
		log.Error("record attempt sent", "err", err)
	}
	r.surfaceOnPhone(ctx, job, t)
	r.publish(ctx, job)
}

// surfaceOnPhone takes a delivered chat back out of the archive.
//
// The message has arrived by the time this runs. What this fixes is not
// delivery but visibility: with "Keep chats archived" on, which is WhatsApp's
// default, an archived chat stays in the archive folder even when a new message
// lands in it. Two thirds of the groups these numbers broadcast to are archived,
// so the operator saw "Terkirim" in salesan, opened WhatsApp, and found nothing
// on the front screen.
//
// Only chats this campaign actually delivered to are touched, and only when
// they are archived. Archiving one again from the phone sticks until the next
// campaign that asks for this.
//
// Best effort throughout. The message is already on the recipient's phone; a
// chat list that stayed tidy is not a reason to call a delivered message
// anything other than delivered.
func (r *Runner) surfaceOnPhone(ctx context.Context, job repository.CampaignJob, t repository.QueuedTarget) {
	if !job.SurfaceOnPhone || t.ConversationID == nil {
		return
	}
	archived, err := r.repo.ConversationIsArchived(ctx, *t.ConversationID)
	if err != nil {
		r.log.Warn("read archive state", "conversation_id", *t.ConversationID, "err", err)
		return
	}
	if !archived {
		return
	}

	// Its own deadline: this is tidying, and it must not hold the number's
	// queue behind an app state round trip that is going slowly.
	surfaceCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if err := r.wa.SurfaceChat(surfaceCtx, t.AccountID, t.ChatJID); err != nil {
		r.log.Warn("surface chat on phone",
			"campaign_id", job.ID, "chat_jid", t.ChatJID, "err", err)
		return
	}
	if err := r.repo.SetConversationArchived(ctx, *t.ConversationID, false); err != nil {
		r.log.Warn("record unarchive", "conversation_id", *t.ConversationID, "err", err)
	}
	r.log.Info("chat taken out of the archive so the broadcast shows on the phone",
		"campaign_id", job.ID, "chat_jid", t.ChatJID)
}

// recordOutgoing writes the campaign message into the customer's thread.
//
// Returns nil when the thread could not be resolved — a manual number nobody has
// ever spoken to, on a device that has no conversation for it yet. The send
// still happens; only the local copy is missing, and the target row keeps the
// rendered text either way.
func (r *Runner) recordOutgoing(
	ctx context.Context,
	job repository.CampaignJob,
	t repository.QueuedTarget,
	body, waID string,
	prepared *wa.PreparedMedia,
) *uuid.UUID {
	convID := uuid.Nil
	if t.ConversationID != nil {
		convID = *t.ConversationID
	} else {
		kind := models.ConversationTypePersonal
		if strings.HasSuffix(t.ChatJID, "@"+types.GroupServer) {
			kind = models.ConversationTypeGroup
		}
		name := ""
		if t.DisplayName != nil {
			name = *t.DisplayName
		}
		// A number is also the thread's pn_jid. Passing it as such lets the
		// lookup find a thread keyed by the person's LID, instead of opening a
		// second one keyed by the number beside it.
		pnJID := ""
		if kind == models.ConversationTypePersonal && strings.HasSuffix(t.ChatJID, "@"+types.DefaultUserServer) {
			pnJID = t.ChatJID
		}
		id, err := r.repo.UpsertConversation(ctx, repository.UpsertConversationInput{
			WorkspaceID: t.WorkspaceID,
			AccountID:   t.AccountID,
			ChatJID:     t.ChatJID,
			PNJID:       pnJID,
			Type:        kind,
			Name:        name,
			ContactID:   t.ContactID,
		})
		if err != nil {
			r.log.Warn("resolve conversation for campaign target",
				"target_id", t.ID, "err", err)
			return nil
		}
		convID = id
	}

	msgType := "text"
	var mime *string
	if prepared != nil {
		msgType = string(prepared.Kind)
		m := prepared.MIME
		mime = &m
	}

	senderJID := r.wa.OwnJID(t.AccountID)
	var senderPtr *string
	if senderJID != "" {
		senderPtr = &senderJID
	}
	var bodyPtr, captionPtr *string
	if prepared == nil {
		bodyPtr = &body
	} else if body != "" {
		captionPtr = &body
	}

	msg, _, err := r.repo.InsertMessage(ctx, repository.InsertMessageInput{
		WorkspaceID:    t.WorkspaceID,
		AccountID:      t.AccountID,
		ConversationID: convID,
		WAMessageID:    waID,
		SenderJID:      senderPtr,
		FromMe:         true,
		Type:           msgType,
		Body:           bodyPtr,
		Caption:        captionPtr,
		MediaMime:      mime,
		Status:         models.MessageStatusPending,
		Timestamp:      time.Now().UTC(),
		// SentBy stays nil on purpose. The campaign has a creator, recorded on
		// the campaign row and in the activity log, but this message was not
		// typed by anybody — crediting it to the creator would put broadcast
		// volume into their personal chat performance.
		SenderSource: models.SourceBroadcast,
		CampaignID:   &job.ID,
	})
	if err != nil {
		r.log.Warn("record campaign message", "target_id", t.ID, "err", err)
		return nil
	}

	// The file, so the thread shows the photo and not the word "Foto".
	//
	// Written as a pending attachment carrying WhatsApp's own download material,
	// which is what the upload handed back a moment ago. From here it is an
	// ordinary attachment: the chat screen asks for a URL, the existing pipeline
	// fetches it once and stores it, and every later open is served from our own
	// bucket. No second copy of the bytes is made here, and nothing is fetched
	// until somebody actually looks at the thread.
	if prepared != nil {
		r.attachLocalCopy(ctx, t.WorkspaceID, t.AccountID, msg.ID, prepared.LocalCopy())
	}
	return &msg.ID
}

// attachLocalCopy records the campaign's media against one sent message.
//
// Failures are logged and swallowed: the message went out, the customer has it,
// and a missing local thumbnail is not a reason to mark the send failed.
func (r *Runner) attachLocalCopy(
	ctx context.Context,
	workspaceID, accountID, messageID uuid.UUID,
	c wa.LocalCopy,
) {
	in := repository.InsertAttachmentInput{
		WorkspaceID: workspaceID,
		AccountID:   accountID,
		MessageID:   messageID,
		Index:       0,
		Kind:        c.Kind,
		Status:      models.AttachmentPending,
	}
	if c.Name != "" {
		in.FileName = &c.Name
	}
	if c.MIME != "" {
		in.MimeType = &c.MIME
	}
	if c.SizeBytes > 0 {
		in.SizeBytes = &c.SizeBytes
	}
	if c.Width > 0 {
		in.Width = &c.Width
	}
	if c.Height > 0 {
		in.Height = &c.Height
	}
	if c.Thumbnail != "" {
		in.Thumbnail = &c.Thumbnail
	}

	att, _, err := r.repo.InsertAttachment(ctx, in)
	if err != nil {
		r.log.Warn("record campaign attachment", "message_id", messageID, "err", err)
		return
	}
	if c.DirectPath == "" || len(c.MediaKey) == 0 {
		return
	}
	if err := r.repo.SaveMediaRef(ctx, att.ID, repository.MediaRef{
		DirectPath:    c.DirectPath,
		MediaKey:      c.MediaKey,
		FileEncSHA256: c.FileEncSHA256,
		FileSHA256:    c.FileSHA256,
		MediaType:     c.MediaType,
	}); err != nil {
		r.log.Warn("record campaign media ref", "attachment_id", att.ID, "err", err)
	}
}

func (r *Runner) failTarget(
	ctx context.Context,
	job repository.CampaignJob,
	t repository.QueuedTarget,
	attemptID uuid.UUID,
	code, reason string,
) {
	r.failTargetWith(ctx, job, t, attemptID, job.MaxAttempts, code, reason)
}

// isSendTimeout reports whether a send ended because it ran out of time rather
// than because WhatsApp refused it. Both forms occur: the deadline can fire in
// whatsmeow, which reports its own error, or in the context wrapped around it.
func isSendTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, whatsmeow.ErrMessageTimedOut)
}

// failTargetFinal records a failure that no retry can cure, so the target is
// settled on this attempt regardless of the campaign's retry budget.
func (r *Runner) failTargetFinal(
	ctx context.Context,
	job repository.CampaignJob,
	t repository.QueuedTarget,
	attemptID uuid.UUID,
	code, reason string,
) {
	r.failTargetWith(ctx, job, t, attemptID, t.Attempt, code, reason)
}

func (r *Runner) failTargetWith(
	ctx context.Context,
	job repository.CampaignJob,
	t repository.QueuedTarget,
	attemptID uuid.UUID,
	maxAttempts int,
	code, reason string,
) {
	retrying, err := r.repo.FinishAttemptFailed(
		ctx, attemptID, t, maxAttempts, job.RetryGapSeconds, code, truncate(reason, 400))
	if err != nil {
		r.log.Error("record attempt failure", "target_id", t.ID, "err", err)
		return
	}
	r.log.Warn("campaign send failed",
		"campaign_id", job.ID, "target_id", t.ID,
		"attempt", t.Attempt, "retrying", retrying, "reason", reason)
	r.publish(ctx, job)
}

// settleCancelled closes a campaign whose cancellation was never completed.
//
// The recipients that never went out are marked cancelled rather than left
// pending, because "dibatalkan" with forty recipients still reading "menunggu"
// is a report nobody can act on.
func (r *Runner) settleCancelled(ctx context.Context, job repository.CampaignJob) {
	log := r.log.With("campaign_id", job.ID, "type", job.CampaignType)

	n, err := r.repo.CancelRemainingWork(ctx, job.ID)
	if err != nil {
		log.Error("cancel remaining work", "err", err)
		return
	}

	var status string
	if job.CampaignType == models.CampaignStory {
		status, err = r.repo.FinishStoryCampaign(ctx, job.ID)
	} else {
		status, err = r.repo.FinishCampaign(ctx, job.ID)
	}
	if errors.Is(err, repository.ErrNotFound) {
		log.Info("campaign removed while being cancelled")
		return
	}
	if err != nil {
		log.Error("finish cancelled campaign", "err", err)
		return
	}

	log.Info("campaign cancellation completed", "status", status, "recipients_cancelled", n)
	r.activity(ctx, job, activityFor(status), status, "")
	r.publish(ctx, job)
}

// settleBroadcast decides the campaign's final status once no device has work.
func (r *Runner) settleBroadcast(ctx context.Context, job repository.CampaignJob) {
	pending, err := r.repo.PendingTargetCount(ctx, job.ID)
	if err != nil {
		r.log.Error("count pending targets", "campaign_id", job.ID, "err", err)
		return
	}
	if pending > 0 {
		// Recipients are waiting on a retry gap or on a device that is offline.
		// Release the lease and let the next tick take it up: this is the normal
		// path for a campaign with retries, not an error.
		if err := r.repo.ReleaseCampaign(ctx, job.ID, r.owner); err != nil {
			r.log.Error("release campaign", "campaign_id", job.ID, "err", err)
		}
		r.publish(ctx, job)
		return
	}

	status, err := r.repo.FinishCampaign(ctx, job.ID)
	if err != nil {
		r.log.Error("finish campaign", "campaign_id", job.ID, "err", err)
		return
	}
	r.log.Info("campaign finished", "campaign_id", job.ID, "status", status)
	r.activity(ctx, job, activityFor(status), status, "")
	r.publish(ctx, job)

	// The next occurrence is cloned before the document is purged, so the copy
	// inherits a key that still points at a file. A recurring broadcast keeps its
	// attachment for the life of the series.
	spawned := r.spawnNextOccurrence(ctx, job.ID)
	if !spawned {
		r.purgeDocument(ctx, job)
	}
}

// openStoredDocument reads an uploaded document back into a temporary file, in
// the shape the rest of the send path already understands.
//
// The temporary file is the same arrangement a fetched URL produces, so devices
// prepare and send it through exactly one code path. Cleanup deletes it, which
// is what keeps the bytes off this machine once the campaign is done.
func (r *Runner) openStoredDocument(
	ctx context.Context, job repository.CampaignJob,
) (*mediafetch.Result, error) {
	data, mime, err := r.wa.OpenCampaignDocument(ctx, *job.MediaStoragePath)
	if err != nil {
		return nil, err
	}

	temp, err := os.CreateTemp(r.cfg.CampaignTempDir, "salesan-doc-*")
	if err != nil {
		return nil, err
	}
	res := &mediafetch.Result{File: temp}
	if _, err := temp.Write(data); err != nil {
		res.Cleanup()
		return nil, err
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		res.Cleanup()
		return nil, err
	}

	name := "dokumen"
	if job.MediaFileName != nil && *job.MediaFileName != "" {
		name = *job.MediaFileName
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	res.Info = media.File{
		Name: name,
		MIME: mime,
		Kind: media.KindDocument,
		Size: int64(len(data)),
	}
	return res, nil
}

// purgeDocument deletes a finished campaign's uploaded document.
//
// Called once the campaign is over, because a file that has been sent has no
// further use and keeping it would make "berkas dihapus setelah selesai" untrue.
// The row keeps the name, so the report can still say what was sent.
func (r *Runner) purgeDocument(ctx context.Context, job repository.CampaignJob) {
	if job.MediaStoragePath == nil || *job.MediaStoragePath == "" {
		return
	}
	if err := r.wa.RemoveCampaignDocument(ctx, *job.MediaStoragePath); err != nil {
		r.log.Error("remove campaign document", "campaign_id", job.ID, "err", err)
		return
	}
	if err := r.repo.MarkDocumentPurged(ctx, job.ID); err != nil {
		r.log.Warn("record document purge", "campaign_id", job.ID, "err", err)
	}
}

// spawnNextOccurrence continues a recurring series, once its run is over.
//
// Scheduled after the run rather than before it, so a series can never get ahead
// of itself: two occurrences of the same broadcast in flight at once would send
// the same message twice to the same people.
//
// A whole copy rather than a re-run of the same row. Re-running would overwrite
// last week's report — its totals, its per-number breakdown, its recipient list
// — with this week's, and the point of keeping a send history is that it can be
// looked at afterwards.
// Reports whether a successor was created, which is what decides if this run's
// attachment may be deleted.
func (r *Runner) spawnNextOccurrence(ctx context.Context, campaignID uuid.UUID) bool {
	series, err := r.repo.RecurringSeries(ctx, campaignID)
	if err != nil || series == nil {
		if err != nil {
			r.log.Error("read recurrence", "campaign_id", campaignID, "err", err)
		}
		return false
	}

	rule := Recurrence{
		Frequency: series.Frequency,
		Hour:      series.Hour,
		Minute:    series.Minute,
		Weekday:   series.Weekday,
		Day:       series.Day,
	}
	next, ok := rule.NextOccurrence(time.Now())
	if !ok {
		r.log.Warn("unrecognised recurrence, series stops",
			"campaign_id", campaignID, "frequency", series.Frequency)
		return false
	}
	if series.Until != nil && next.After(*series.Until) {
		r.log.Info("recurring series reached its end date", "campaign_id", campaignID)
		return false
	}

	newID, err := r.repo.CloneCampaignForNextRun(ctx, campaignID, next)
	if err != nil {
		r.log.Error("spawn next occurrence", "campaign_id", campaignID, "err", err)
		return false
	}
	if newID == uuid.Nil {
		// Somebody else already spawned it. Their copy owns the attachment now,
		// so this run must not delete it either.
		return true
	}
	r.log.Info("next occurrence scheduled",
		"from", campaignID, "campaign_id", newID, "at", next.Format(time.RFC3339))
	r.Wake()
	return true
}

// --- story -------------------------------------------------------------------

func (r *Runner) runStory(ctx context.Context, job repository.CampaignJob, fetched *mediafetch.Result) {
	log := r.log.With("campaign_id", job.ID)

	// Publications are created on the campaign's own devices. Idempotent, so a
	// re-run after a restart does not duplicate them.
	if err := r.repo.SeedStoryPublications(ctx, jobWorkspace(job), job.ID, job.AccountIDs); err != nil {
		log.Error("seed story publications", "err", err)
		r.fail(ctx, job, "Publikasi Story gagal disiapkan.")
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}
		if cancelled, err := r.repo.CancelRequested(ctx, job.ID); err == nil && cancelled {
			break
		}

		pubs, err := r.repo.ClaimStoryPublications(ctx, job.ID, r.cfg.CampaignLease, 4)
		if err != nil {
			log.Error("claim story publications", "err", err)
			return
		}
		if len(pubs) == 0 {
			break
		}
		// The numbers of one Story go out side by side, each on its own
		// socket, bounded process-wide by storySlots. They used to go one after
		// another, and with ten minutes of encryption per number that made a
		// ten-number Story a two-hour wait in which every number after the
		// first read "pending" the whole time.
		var wg sync.WaitGroup
		for _, p := range pubs {
			if !r.acquireStory(ctx) {
				// Cancelled while queueing. Whatever was claimed and not
				// started is left for the lease to expire and the next tick.
				break
			}
			wg.Add(1)
			go func(p repository.QueuedPublication) {
				defer wg.Done()
				defer r.releaseStory()
				r.publishStory(ctx, job, p, fetched)
			}(p)
		}
		wg.Wait()
		if ctx.Err() != nil {
			return
		}
	}

	pending, err := r.repo.PendingPublicationCount(ctx, job.ID)
	if err != nil {
		log.Error("count pending publications", "err", err)
		return
	}
	if pending > 0 {
		if err := r.repo.ReleaseCampaign(ctx, job.ID, r.owner); err != nil {
			log.Error("release campaign", "err", err)
		}
		r.publish(ctx, job)
		return
	}

	status, err := r.repo.FinishStoryCampaign(ctx, job.ID)
	if errors.Is(err, repository.ErrNotFound) {
		// Deleting a running Story cancels it first, but a push already in
		// flight cannot be recalled: it finishes against a campaign row that
		// has gone. The Story is on the phones either way. Not an error.
		log.Info("story campaign removed while publishing")
		return
	}
	if err != nil {
		log.Error("finish story campaign", "err", err)
		return
	}
	log.Info("story campaign finished", "status", status)
	r.activity(ctx, job, activityFor(status), status, "")
	r.publish(ctx, job)

	// A recurring Story continues exactly the way a recurring broadcast does: the
	// next occurrence is a whole new campaign, so this one's report — its outcome
	// per number and the viewers detected against it — stays what it was instead
	// of being overwritten tomorrow.
	if !r.spawnNextOccurrence(ctx, job.ID) {
		r.purgeDocument(ctx, job)
	}
}

func (r *Runner) publishStory(
	ctx context.Context,
	job repository.CampaignJob,
	p repository.QueuedPublication,
	fetched *mediafetch.Result,
) {
	log := r.log.With("campaign_id", job.ID, "account_id", p.AccountID)

	if !r.wa.DeviceOnline(p.AccountID) {
		if _, err := r.repo.MarkStoryFailed(ctx, p.ID, p.Attempt, job.MaxAttempts,
			job.RetryGapSeconds, "perangkat", "Perangkat sedang tidak terhubung."); err != nil {
			log.Error("mark story failed", "err", err)
		}
		return
	}

	var prepared *wa.PreparedMedia
	if fetched != nil {
		got, err := r.wa.PrepareCampaignMedia(ctx, p.AccountID, fetched.File, fetched.Info)
		if err != nil {
			if _, e := r.repo.MarkStoryFailed(ctx, p.ID, p.Attempt, job.MaxAttempts,
				job.RetryGapSeconds, "media", err.Error()); e != nil {
				log.Error("mark story failed", "err", e)
			}
			return
		}
		prepared = got
	}

	// A Story is one message; there is no per-recipient rendering, so spintax is
	// resolved once and variables have nothing recipient-specific to fill.
	built, err := compose.Build(job.Template, nil, nil)
	if err != nil {
		if _, e := r.repo.MarkStoryFailed(ctx, p.ID, p.Attempt, job.MaxAttempts,
			job.RetryGapSeconds, "template", err.Error()); e != nil {
			log.Error("mark story failed", "err", e)
		}
		return
	}

	msg, err := wa.StoryMessage(built.Body, prepared, 0)
	if err != nil {
		if _, e := r.repo.MarkStoryFailed(ctx, p.ID, p.Attempt, job.MaxAttempts,
			job.RetryGapSeconds, "pesan", err.Error()); e != nil {
			log.Error("mark story failed", "err", e)
		}
		return
	}

	// A retry reuses the id its previous attempt sent with. WhatsApp
	// deduplicates by message id, so if that attempt did reach the phone — and
	// we simply failed to write down that it had — re-sending is ignored rather
	// than posting the same Story a second time. Only a first attempt mints one.
	waID := ""
	if p.WAMessageID != nil {
		waID = strings.TrimSpace(*p.WAMessageID)
	}
	if waID == "" {
		minted, err := r.wa.NewMessageID(p.AccountID)
		if err != nil {
			if _, e := r.repo.MarkStoryFailed(ctx, p.ID, p.Attempt, job.MaxAttempts,
				job.RetryGapSeconds, "perangkat", err.Error()); e != nil {
				log.Error("mark story failed", "err", e)
			}
			return
		}
		waID = minted
		// Written down before it is used, so an interruption between the send and
		// the outcome leaves evidence rather than a blank row that gets retried.
		if err := r.repo.StartStoryPublication(ctx, p.ID, waID); err != nil {
			log.Error("record story attempt", "err", err)
		}
	}

	// Kept alive for as long as the push takes. A Story to twenty thousand
	// contacts outlives a ten-minute lease, and a lease that ran out mid-push
	// let the next pass claim the same publication again while this one was
	// still encrypting it.
	//
	// Bounded as well: whatsmeow waits for the acknowledgement without a
	// deadline, and a push that never answers would hold its slot, and the
	// campaign, for good. Generous, because fifteen minutes of encryption is
	// ordinary here. A retry reuses this same message id, so a Story that did
	// land is not posted twice.
	started := time.Now()
	stopRenew := r.renewPublicationLease(ctx, p.ID)
	sendCtx, cancelSend := context.WithTimeout(ctx, r.cfg.CampaignStoryTimeout)
	res, sendErr := r.wa.PublishStory(sendCtx, p.AccountID, msg, waID)
	cancelSend()
	stopRenew()

	// The outcome is recorded on a context of its own.
	//
	// The Story is already on somebody's phone by this point. If the job context
	// has just been cancelled — a shutdown, a lost lease, a cancel pressed a
	// second ago — writing the outcome through it fails, and the publication is
	// left `processing` with no result. That is what left one campaign reading
	// "Sedang Dipublikasikan" while the phone showed the Story, and what would
	// have posted it again when the lease expired.
	outCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()

	if sendErr != nil {
		retrying, e := r.repo.MarkStoryFailed(outCtx, p.ID, p.Attempt, job.MaxAttempts,
			job.RetryGapSeconds, "terbit", truncate(sendErr.Error(), 400))
		if e != nil {
			log.Error("mark story failed", "err", e)
		}
		log.Warn("story publish failed", "attempt", p.Attempt, "retrying", retrying, "err", sendErr)
		return
	}

	at := res.Timestamp
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := r.repo.MarkStoryPublished(outCtx, p.ID, waID, at); err != nil {
		// Loud, not a warning: the Story is out and the record says otherwise,
		// which is the one combination that leads to publishing it twice.
		log.Error("mark story published", "wa_message_id", waID, "err", err)
	}

	// Our own copy, in our own Status thread, so the chat screen's Status panel
	// shows it like any other. WhatsApp does not echo a message back to the
	// client that sent it, so if this does not write it, nothing does.
	//
	// After the send, not before. A broadcast writes its row first because the
	// customer's thread has to be able to show a failure; a Status has no thread
	// and no failure to show — one that never published simply did not happen,
	// and a row written ahead of time would leave a Story on screen that nobody
	// can see anywhere else. What protects against publishing twice is the
	// message id recorded on the publication above, not this.
	r.recordOwnStory(outCtx, job, p, built.Body, waID, prepared, at)

	log.Info("story published", "wa_message_id", waID, "took", time.Since(started).Round(time.Second))
	r.publish(outCtx, job)
}

// renewPublicationLease keeps one publication's claim current while its push
// is in progress. Same shape as renewLease, one row narrower.
func (r *Runner) renewPublicationLease(ctx context.Context, publicationID uuid.UUID) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(r.cfg.CampaignLease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := r.repo.RenewStoryPublicationLease(c, publicationID, r.cfg.CampaignLease); err != nil {
					r.log.Warn("renew story publication lease", "publication_id", publicationID, "err", err)
				}
				cancel()
			}
		}
	}()
	return func() { close(done) }
}

// recordOwnStory writes the published Story into this number's own Status
// thread, so it appears under Status in the chat screen like any other.
//
// Best effort throughout: the Story itself is what matters, and a missing local
// copy is not a reason to call the publication failed.
func (r *Runner) recordOwnStory(
	ctx context.Context,
	job repository.CampaignJob,
	p repository.QueuedPublication,
	body, waID string,
	prepared *wa.PreparedMedia,
	at time.Time,
) {
	convID, err := r.repo.UpsertConversation(ctx, repository.UpsertConversationInput{
		WorkspaceID: p.WorkspaceID,
		AccountID:   p.AccountID,
		// Every Status lives on one address, which is what the chat screen's
		// Status panel reads.
		ChatJID: types.StatusBroadcastJID.String(),
		Type:    models.ConversationTypeStatus,
	})
	if err != nil {
		r.log.Warn("resolve status thread for story", "account_id", p.AccountID, "err", err)
		return
	}

	msgType := "text"
	var mime *string
	if prepared != nil {
		msgType = string(prepared.Kind)
		m := prepared.MIME
		mime = &m
	}
	senderJID := r.wa.OwnJID(p.AccountID)
	var senderPtr *string
	if senderJID != "" {
		senderPtr = &senderJID
	}
	var bodyPtr, captionPtr *string
	if prepared == nil {
		bodyPtr = &body
	} else if body != "" {
		captionPtr = &body
	}

	msg, _, err := r.repo.InsertMessage(ctx, repository.InsertMessageInput{
		WorkspaceID:    p.WorkspaceID,
		AccountID:      p.AccountID,
		ConversationID: convID,
		WAMessageID:    waID,
		SenderJID:      senderPtr,
		FromMe:         true,
		Type:           msgType,
		Body:           bodyPtr,
		Caption:        captionPtr,
		MediaMime:      mime,
		Status:         models.MessageStatusSent,
		// WhatsApp's own stamp for the publication, so the Status panel and the
		// Story report agree on when it went out rather than differing by
		// whatever the round trip took.
		Timestamp: at,
		// SenderSource stays 'broadcast' and SentBy stays nil for the same reason
		// as a campaign message: nobody typed this into a chat, and crediting it
		// to the creator would put Story volume into their chat performance.
		SenderSource: models.SourceBroadcast,
		CampaignID:   &job.ID,
	})
	if err != nil {
		r.log.Warn("record own story message", "account_id", p.AccountID, "err", err)
		return
	}
	if prepared != nil {
		r.attachLocalCopy(ctx, p.WorkspaceID, p.AccountID, msg.ID, prepared.LocalCopy())
	}

	// Announced on the same event every other message uses, so the Status panel
	// in the chat screen sees it the moment it publishes instead of on its next
	// poll a minute later. Read back first, so the bubble arrives with its
	// attachment already attached.
	if full, err := r.repo.GetMessageByID(ctx, p.WorkspaceID, msg.ID); err == nil {
		msg = full
	}
	r.hub.Broadcast(p.WorkspaceID, realtime.EventMessageNew, map[string]any{
		"account_id": p.AccountID,
		"message":    msg,
	})
}

// --- shared ------------------------------------------------------------------

func (r *Runner) fail(ctx context.Context, job repository.CampaignJob, reason string) {
	if err := r.repo.FailCampaign(ctx, job.ID, reason); err != nil {
		r.log.Error("mark campaign failed", "campaign_id", job.ID, "err", err)
	}
	r.activity(ctx, job, "failed", models.CampaignFailed, reason)
	r.publish(ctx, job)
}

// offlineReason names the numbers that never came back.
//
// "Tidak ada perangkat pengirim yang terhubung" on its own sends the operator
// to look through thirty-five numbers for the ones that are missing. Naming
// them is the difference between a message and an instruction.
func (r *Runner) offlineReason(ctx context.Context, job repository.CampaignJob) string {
	names, err := r.repo.AccountNames(ctx, job.AccountIDs)
	if err != nil || len(names) == 0 {
		return "Tidak ada nomor pengirim yang terhubung ke WhatsApp. Sambungkan kembali nomornya, lalu kirim ulang."
	}
	return fmt.Sprintf(
		"Nomor pengirim tidak terhubung ke WhatsApp: %s. Pindai ulang kode QR-nya, lalu kirim ulang broadcast ini.",
		strings.Join(names, ", "))
}

// activity appends one entry to the audit log.
//
// The event key folds in the campaign, the action and the day, so a campaign
// resumed after a restart does not add a second "started" line to somebody's
// activity count.
func (r *Runner) activity(ctx context.Context, job repository.CampaignJob, action, status, reason string) {
	key := fmt.Sprintf("campaign:%s:%s:%d", job.ID, action, time.Now().UTC().Unix()/60)
	err := r.repo.RecordActivity(ctx, repository.ActivityInput{
		WorkspaceID:   jobWorkspace(job),
		ApplicationID: job.ApplicationID,
		AdminID:       job.CreatedBy,
		EntityType:    "campaign",
		EntityID:      &job.ID,
		EntityName:    job.Name,
		ActivityType:  action,
		Status:        status,
		FailureReason: reason,
		Detail:        map[string]any{"campaign_type": job.CampaignType},
		EventKey:      key,
	})
	if err != nil {
		r.log.Warn("record campaign activity", "campaign_id", job.ID, "err", err)
	}
}

// publish pushes the campaign's current state to any open screen.
//
// Sent over the same WebSocket hub the Dashboard already uses, rather than a
// second realtime mechanism. Only the campaign row travels: the target list can
// run to thousands, and a browser that receives a summary cannot be tempted to
// recompute the totals from a partial set.
func (r *Runner) publish(ctx context.Context, job repository.CampaignJob) {
	c, err := r.repo.GetCampaign(ctx, jobWorkspace(job), job.ID)
	if err != nil {
		return
	}
	r.hub.Broadcast(jobWorkspace(job), realtime.EventCampaignUpdated, map[string]any{
		"campaign_id": job.ID,
		"campaign":    c,
	})
	// The Dashboard counts campaign activity, so its figures moved too.
	r.hub.Broadcast(jobWorkspace(job), realtime.EventMetricsUpdated, map[string]any{
		"campaign_id": job.ID,
	})
}

func jobWorkspace(job repository.CampaignJob) uuid.UUID { return job.WorkspaceID }

// activityFor maps a settled status onto the audit vocabulary.
func activityFor(status string) string {
	switch status {
	case models.CampaignCompleted:
		return "published"
	case models.CampaignPartial:
		return "partially_published"
	case models.CampaignCancelled:
		return "cancelled"
	default:
		return "failed"
	}
}

// jitterBetween draws a pause from the profile's range.
//
// Redrawn for every recipient, as specified — a fixed gap would be its own
// signature. This is queue pacing and nothing more: it does not make an account
// safe from anything, and no part of the interface says it does.
func jitterBetween(rng *rand.Rand, min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rng.Int63n(int64(max-min)))
}

func sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
