package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/campaign"
	"github.com/salesan/omnichannel/backend/internal/compose"
	"github.com/salesan/omnichannel/backend/internal/mediafetch"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// Broadcast and WA Story over HTTP.
//
// Two rules run through every handler here.
//
// The first is that authorisation is decided on this side. The RLS policies say
// the same thing for the browser's direct Supabase path, but the API connects as
// the service role, for which RLS does not run — so a check omitted here is a
// check that does not exist. Every device, application and campaign named in a
// request is resolved against the caller's scope before anything is written.
//
// The second is that this layer never sends a message. It validates, it writes,
// and it nudges the scheduler. A request that returned only after a campaign
// finished would have to hold a connection open for hours, and a request that
// started sending inline would lose the queue the moment it timed out.

// campaignRequest is the composer's payload for creating a campaign.
type campaignRequest struct {
	CampaignType string `json:"campaign_type"`
	Name         string `json:"name"`
	// Body is the message template: spintax and variables unresolved.
	Body          string   `json:"body"`
	ComposeMode   string   `json:"compose_mode"`
	ApplicationID *string  `json:"application_id"`
	AccountIDs    []string `json:"account_ids"`

	// MediaURL is a link, never an upload. The file is fetched to a temporary
	// path at send time and deleted; the database keeps the address only.
	MediaURL *string `json:"media_url"`
	// MediaStoragePath and MediaFileName come back from the document upload.
	// Set instead of MediaURL when the operator attached a file rather than a
	// link; the file is sent from the private bucket and deleted afterwards.
	MediaStoragePath *string `json:"media_storage_path"`
	MediaFileName    *string `json:"media_file_name"`
	Caption          *string `json:"caption"`

	DelayProfile string `json:"delay_profile"`
	// DelayMinSeconds and DelayMaxSeconds override the profile. Both or neither.
	DelayMinSeconds *int `json:"delay_min_seconds"`
	DelayMaxSeconds *int `json:"delay_max_seconds"`
	// AutoRetryOnDisconnect defaults to true when the composer does not say.
	AutoRetryOnDisconnect *bool `json:"auto_retry_on_disconnect"`
	// Recurrence is daily, weekly or monthly. Empty means one run.
	Recurrence      string  `json:"recurrence"`
	RecurrenceUntil *string `json:"recurrence_until"`
	// RecurrenceTime is the WIB wall clock, "HH:MM". Required when recurring.
	RecurrenceTime string `json:"recurrence_time"`
	// RecurrenceWeekday is 0 (Sunday) to 6, for weekly. RecurrenceDay is 1-31,
	// for monthly.
	RecurrenceWeekday *int `json:"recurrence_weekday"`
	RecurrenceDay     *int `json:"recurrence_day"`

	TargetSource string   `json:"target_source"`
	Numbers      []string `json:"numbers"`
	GroupIDs     []string `json:"group_ids"`
	ContactIDs   []string `json:"contact_ids"`
	// CustomValues fills the workspace's own placeholders for this campaign.
	CustomValues map[string]string `json:"custom_values"`
	LabelIDs     []string          `json:"label_ids"`

	// ScheduledAt is RFC3339. Absent means a draft, unless RunNow is set.
	ScheduledAt *string `json:"scheduled_at"`
	RunNow      bool    `json:"run_now"`
}

// builtCampaign is a validated request ready to write, plus the review that was
// shown for it.
type builtCampaign struct {
	input repository.SaveBroadcastInput
	plan  *models.TargetPlan
}

// buildCampaign validates a composer payload and resolves its recipients.
//
// Shared by the create endpoint and the preview endpoint, so the plan an
// operator approves on the review screen is produced by the same code that then
// writes the rows. Two implementations would eventually disagree, and the place
// it would show is a campaign that sent to more people than the review said.
func (s *Server) buildCampaign(
	w http.ResponseWriter,
	r *http.Request,
	sc repository.Scope,
	req campaignRequest,
) (builtCampaign, bool) {
	if req.CampaignType != models.CampaignStory && req.CampaignType != models.CampaignBroadcast {
		writeError(w, http.StatusBadRequest, "invalid_type", "Jenis campaign harus story atau broadcast")
		return builtCampaign{}, false
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "Nama campaign tidak boleh kosong")
		return builtCampaign{}, false
	}
	hasDocument := req.MediaStoragePath != nil && strings.TrimSpace(*req.MediaStoragePath) != ""
	if strings.TrimSpace(req.Body) == "" &&
		(req.MediaURL == nil || *req.MediaURL == "") && !hasDocument {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Campaign harus punya isi pesan atau media")
		return builtCampaign{}, false
	}

	accountIDs, ok := parseUUIDList(w, req.AccountIDs, "ID perangkat tidak valid")
	if !ok {
		return builtCampaign{}, false
	}
	if len(accountIDs) == 0 {
		writeError(w, http.StatusBadRequest, "no_devices", "Pilih minimal satu nomor pengirim")
		return builtCampaign{}, false
	}

	// The devices are re-read through the caller's scope, so a request naming an
	// account outside it simply finds fewer devices than it asked for — and that
	// mismatch is refused rather than silently honoured for the subset.
	accounts, err := s.repo.AccountsByIDs(r.Context(), sc, accountIDs)
	if err != nil {
		writeAppError(w, err)
		return builtCampaign{}, false
	}
	if len(accounts) != len(accountIDs) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Sebagian nomor pengirim di luar wewenang akun Anda")
		return builtCampaign{}, false
	}

	// The application is chosen first and is not optional.
	//
	// A campaign spanning two applications cannot be reported on ("Broadcast
	// aplikasi TOEFL" is unanswerable when half of it went out from another
	// application's numbers), cannot be retried cleanly, and cannot be
	// authorised — a PIC holds one application, so such a campaign would be half
	// inside their remit and half outside, with no right answer to "may they
	// open it". Migration 0031 enforces the same rule in the database, so this
	// check is the readable refusal rather than the only one.
	if req.ApplicationID == nil || strings.TrimSpace(*req.ApplicationID) == "" {
		writeError(w, http.StatusBadRequest, "missing_application",
			"Pilih aplikasi terlebih dahulu")
		return builtCampaign{}, false
	}
	applicationID, err := uuid.Parse(strings.TrimSpace(*req.ApplicationID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_uuid", "ID aplikasi tidak valid")
		return builtCampaign{}, false
	}
	if !sc.CanSeeApplication(applicationID) {
		writeError(w, http.StatusForbidden, "forbidden", "Aplikasi ini di luar wewenang akun Anda")
		return builtCampaign{}, false
	}

	// Every sending number must belong to it. Checked against the account rows
	// the database returned, never against what the browser claimed.
	if stray := strayDevice(accounts, applicationID); stray != "" {
		writeError(w, http.StatusBadRequest, "mixed_applications",
			"Nomor "+stray+" bukan milik aplikasi yang dipilih. "+
				"Satu campaign hanya boleh memakai nomor dari satu aplikasi.")
		return builtCampaign{}, false
	}

	profile := req.DelayProfile
	if profile == "" {
		profile = models.DelayNormal
	}
	if !validDelayProfile(profile) {
		writeError(w, http.StatusBadRequest, "invalid_profile", "Profil jeda tidak dikenal")
		return builtCampaign{}, false
	}

	mode := req.ComposeMode
	if mode == "" {
		mode = models.ComposePlain
	}

	if problems := compose.Validate(req.Body); len(problems) > 0 {
		writeErrorWithData(w, http.StatusBadRequest, "invalid_template",
			"Template pesan bermasalah: "+strings.Join(problems, "; "),
			map[string]any{"template_problems": problems})
		return builtCampaign{}, false
	}

	// A hand-set range beats the profile, but only as a pair and only if it is a
	// range. Half a range, or a maximum below the minimum, is a typo — and a typo
	// here would either hammer a number or stall a campaign for hours.
	if (req.DelayMinSeconds == nil) != (req.DelayMaxSeconds == nil) {
		writeError(w, http.StatusBadRequest, "invalid_delay",
			"Jeda kustom harus diisi keduanya: minimum dan maksimum")
		return builtCampaign{}, false
	}
	if req.DelayMinSeconds != nil {
		if *req.DelayMinSeconds < 1 || *req.DelayMaxSeconds < *req.DelayMinSeconds ||
			*req.DelayMaxSeconds > 3600 {
			writeError(w, http.StatusBadRequest, "invalid_delay",
				"Jeda kustom harus 1–3600 detik, dan maksimum tidak boleh di bawah minimum")
			return builtCampaign{}, false
		}
	}

	switch req.Recurrence {
	case "", models.RecurrenceDaily, models.RecurrenceWeekly, models.RecurrenceMonthly:
	default:
		writeError(w, http.StatusBadRequest, "invalid_recurrence",
			"Frekuensi berulang harus harian, mingguan, atau bulanan")
		return builtCampaign{}, false
	}

	// A series without a time is a series the scheduler can never compute, and a
	// campaign that looks active while nothing will ever happen is worse than one
	// that refuses to save.
	if req.Recurrence != "" {
		if _, err := time.Parse("15:04", req.RecurrenceTime); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_recurrence",
				"Jam kirim harus diisi dengan format HH:MM")
			return builtCampaign{}, false
		}
		if req.Recurrence == models.RecurrenceWeekly &&
			(req.RecurrenceWeekday == nil || *req.RecurrenceWeekday < 0 || *req.RecurrenceWeekday > 6) {
			writeError(w, http.StatusBadRequest, "invalid_recurrence",
				"Pilih hari kirim untuk jadwal mingguan")
			return builtCampaign{}, false
		}
		if req.Recurrence == models.RecurrenceMonthly &&
			(req.RecurrenceDay == nil || *req.RecurrenceDay < 1 || *req.RecurrenceDay > 31) {
			writeError(w, http.StatusBadRequest, "invalid_recurrence",
				"Pilih tanggal kirim (1-31) untuk jadwal bulanan")
			return builtCampaign{}, false
		}
	}

	autoRetry := true
	if req.AutoRetryOnDisconnect != nil {
		autoRetry = *req.AutoRetryOnDisconnect
	}

	in := repository.SaveBroadcastInput{
		CampaignType:          req.CampaignType,
		Name:                  strings.TrimSpace(req.Name),
		Template:              req.Body,
		ComposeMode:           mode,
		ApplicationID:         &applicationID,
		AccountIDs:            accountIDs,
		Caption:               req.Caption,
		DelayProfile:          profile,
		DelayMinSeconds:       req.DelayMinSeconds,
		DelayMaxSeconds:       req.DelayMaxSeconds,
		AutoRetryOnDisconnect: autoRetry,
		Recurrence:            req.Recurrence,
		TargetSource:          req.TargetSource,
	}

	if req.Recurrence != "" {
		in.RecurrenceTime = req.RecurrenceTime
		if req.Recurrence == models.RecurrenceWeekly {
			in.RecurrenceWeekday = req.RecurrenceWeekday
		}
		if req.Recurrence == models.RecurrenceMonthly {
			in.RecurrenceDay = req.RecurrenceDay
		}
	}

	if req.RecurrenceUntil != nil && *req.RecurrenceUntil != "" {
		until, err := time.Parse(time.RFC3339, *req.RecurrenceUntil)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date",
				"Batas akhir pengulangan harus format RFC3339")
			return builtCampaign{}, false
		}
		in.RecurrenceUntil = &until
	}

	// Media is validated by actually fetching it, right now, so the operator
	// learns at compose time that a link is dead, private, or an executable
	// wearing a .jpg — rather than at send time, from a failed campaign. The
	// bytes are discarded immediately; only the address and what was measured
	// about it are kept.
	if req.MediaURL != nil && strings.TrimSpace(*req.MediaURL) != "" {
		res, err := mediafetch.Fetch(r.Context(), *req.MediaURL, mediaLimitsFor(s), false)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_media", mediaErrorText(err))
			return builtCampaign{}, false
		}
		defer res.Cleanup()

		url := strings.TrimSpace(*req.MediaURL)
		kind := string(res.Info.Kind)
		mime := res.Info.MIME
		size := res.Info.Size
		sha := res.SHA256
		in.MediaURL = &url
		in.MediaKind = &kind
		in.MediaMime = &mime
		in.MediaSizeBytes = &size
		in.MediaSHA256 = &sha
	}

	// WhatsApp has no file status, so a Story carrying a document could only be
	// published as something nobody can see. Refused here rather than left to the
	// composer alone: the interface hides the option, and this is what makes it a
	// rule instead of a suggestion.
	if hasDocument && req.CampaignType == models.CampaignStory {
		writeError(w, http.StatusBadRequest, "invalid_media",
			"WA Story hanya menerima teks, gambar, atau video. Dokumen tidak bisa dijadikan story.")
		return builtCampaign{}, false
	}

	// An uploaded document was already classified and stored when it was
	// uploaded, so there is nothing to fetch or re-validate here — only the key
	// and the name it will arrive under.
	if hasDocument {
		key := strings.TrimSpace(*req.MediaStoragePath)
		raw := ""
		if req.MediaFileName != nil {
			raw = *req.MediaFileName
		}
		name := campaignDocumentName(raw)
		kind := "document"
		in.MediaStoragePath = &key
		in.MediaFileName = &name
		in.MediaKind = &kind
	}

	if labelIDs, ok := parseUUIDList(w, req.LabelIDs, "ID label campaign tidak valid"); ok {
		in.LabelIDs = labelIDs
	} else {
		return builtCampaign{}, false
	}

	if req.ScheduledAt != nil && *req.ScheduledAt != "" {
		at, err := time.Parse(time.RFC3339, *req.ScheduledAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "Jadwal harus format RFC3339")
			return builtCampaign{}, false
		}
		if at.Before(time.Now().Add(-time.Minute)) {
			writeError(w, http.StatusBadRequest, "invalid_date", "Jadwal tidak boleh di masa lalu")
			return builtCampaign{}, false
		}
		in.ScheduledAt = &at
	} else if req.RunNow {
		now := time.Now().UTC()
		in.ScheduledAt = &now
	} else {
		// Saving a campaign with no departure time was how a draft was made, and
		// drafts are gone: a broadcast is either scheduled or run. Refused here
		// rather than only hidden in the composer, so the state cannot be
		// reached by a stale tab or an older client and then sit in the list
		// with no tab that finds it.
		writeError(w, http.StatusBadRequest, "missing_schedule",
			"Broadcast harus dijadwalkan atau dijalankan sekarang")
		return builtCampaign{}, false
	}

	// A Story is published on devices; there is no recipient list to resolve.
	if req.CampaignType == models.CampaignStory {
		// Empty, never nil: the review screen reads .length on all of these, and
		// a nil slice reaches it as `null`.
		plan := &models.TargetPlan{
			Devices:          devicePlans(accounts, s.manager.DeviceOnline),
			DelayProfile:     profile,
			TemplateProblems: []string{},
			Preview:          []string{},
			Problems:         []models.TargetProblem{},
			MissingVariables: []string{},
		}
		if built, err := compose.Build(req.Body, nil, nil); err == nil {
			plan.Preview = []string{built.Body}
		}
		return builtCampaign{input: in, plan: plan}, true
	}

	appCode := ""
	if code, err := s.repo.ApplicationCode(r.Context(), applicationID); err == nil {
		appCode = code
	}

	groupIDs, ok := parseUUIDList(w, req.GroupIDs, "ID grup tidak valid")
	if !ok {
		return builtCampaign{}, false
	}
	contactIDs, ok := parseUUIDList(w, req.ContactIDs, "ID kontak tidak valid")
	if !ok {
		return builtCampaign{}, false
	}

	plan, targets, err := s.resolver.Resolve(r.Context(), campaign.TargetRequest{
		WorkspaceID:     sc.WorkspaceID,
		Scope:           sc,
		AccountIDs:      accountIDs,
		Source:          req.TargetSource,
		Numbers:         req.Numbers,
		GroupIDs:        groupIDs,
		ContactIDs:      contactIDs,
		Template:        req.Body,
		ComposeMode:     mode,
		DelayProfile:    profile,
		ApplicationCode: appCode,
		CustomValues:    req.CustomValues,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_targets", err.Error())
		return builtCampaign{}, false
	}
	in.Targets = targets

	return builtCampaign{input: in, plan: plan}, true
}

// handlePreviewTargets answers the review screen without writing anything.
func (s *Server) handlePreviewTargets(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	var req campaignRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	built, ok := s.buildCampaign(w, r, sc, req)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plan":          built.plan,
		"delay_profile": built.input.DelayProfile,
		"media_kind":    built.input.MediaKind,
	})
}

// handleListAudiences lists the recipients the chosen devices can reach.
//
// Scoped to those devices rather than to the workspace, because contacts, groups
// and membership all belong to a device: the same customer can be a saved
// contact on one number and a stranger on the next, and offering a list built
// workspace-wide would produce targets half of which cannot be delivered.
func (s *Server) handleListAudiences(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	raw := r.URL.Query().Get("account_ids")
	if raw == "" {
		writeError(w, http.StatusBadRequest, "missing_devices", "Sertakan account_ids")
		return
	}
	ids, ok := parseUUIDList(w, strings.Split(raw, ","), "ID perangkat tidak valid")
	if !ok {
		return
	}
	accounts, err := s.repo.AccountsByIDs(r.Context(), sc, ids)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if len(accounts) != len(ids) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Sebagian nomor pengirim di luar wewenang akun Anda")
		return
	}

	allowed := make([]uuid.UUID, 0, len(accounts))
	for _, a := range accounts {
		allowed = append(allowed, a.ID)
	}

	switch r.URL.Query().Get("kind") {
	case "groups":
		groups, err := s.repo.GroupTargets(r.Context(), sc.WorkspaceID, allowed)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"groups": audiencePayload(groups)})
	default:
		contacts, err := s.repo.ContactTargets(r.Context(), sc.WorkspaceID, allowed,
			intParam(r, "limit", 2000))
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"contacts": audiencePayload(contacts)})
	}
}

// handleRunCampaign starts a draft immediately.
func (s *Server) handleRunCampaign(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	existing, ok := s.campaignForWrite(w, r)
	if !ok {
		return
	}

	c, err := s.repo.RunNow(r.Context(), user.WorkspaceID, existing.ID)
	if err != nil {
		writeError(w, http.StatusConflict, "not_runnable",
			"Campaign ini tidak dalam keadaan yang bisa dijalankan")
		return
	}
	s.recordCampaignActivity(r, c, "schedule_created")
	s.runner.Wake()
	s.hub.Broadcast(user.WorkspaceID, realtime.EventCampaignUpdated, c)
	writeJSON(w, http.StatusOK, map[string]any{"campaign": c})
}

// handleRetryCampaign requeues only the recipients that failed.
//
// Never the whole list. A target already delivered stays delivered and its
// attempt row is untouched, which is what keeps "coba ulang" from meaning
// "send it to everybody again" — and the unique index behind it would refuse a
// second successful send even if this handler were wrong.
func (s *Server) handleRetryCampaign(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	existing, ok := s.campaignForWrite(w, r)
	if !ok {
		return
	}

	n, err := s.repo.RetryFailed(r.Context(), user.WorkspaceID, existing.ID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"requeued": 0,
			"message":  "Tidak ada target gagal yang bisa dicoba ulang.",
		})
		return
	}

	c, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, existing.ID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	s.recordCampaignActivity(r, c, "retried")
	s.runner.Wake()
	s.hub.Broadcast(user.WorkspaceID, realtime.EventCampaignUpdated, c)
	writeJSON(w, http.StatusOK, map[string]any{"requeued": n, "campaign": c})
}

// handleCampaignReport returns the detail screen for either kind.
func (s *Server) handleCampaignReport(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "campaign_id")
	if !ok {
		return
	}
	c, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !canSeeCampaign(sc, c) {
		writeError(w, http.StatusForbidden, "forbidden", "Campaign ini di luar wewenang akun Anda")
		return
	}

	if c.CampaignType == models.CampaignStory {
		report, err := s.repo.StoryReportFor(r.Context(), user.WorkspaceID, id)
		if err != nil {
			writeAppError(w, err)
			return
		}
		for i := range report.Publications {
			report.Publications[i].Connected = s.manager.DeviceOnline(report.Publications[i].AccountID)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"story": report,
			// Sent with every Story report rather than hard-coded in the
			// interface, so the caveat travels with the number it qualifies.
			"views_notice": models.StoryViewNotice,
		})
		return
	}

	devices, err := s.repo.CampaignDevices(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	for i := range devices {
		devices[i].Connected = s.manager.DeviceOnline(devices[i].AccountID)
	}
	totals, err := s.repo.TargetTotals(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	replies, err := s.repo.CampaignReplies(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	duration, err := s.repo.CampaignDuration(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	labels, err := s.repo.CampaignLabelsFor(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"broadcast": models.BroadcastReport{
			Campaign:        *c,
			Devices:         devices,
			Totals:          totals,
			DurationSeconds: duration,
			Replies:         replies,
			Labels:          labels,
		},
	})
}

// handleRevokeStoryPublication takes one Story down from WhatsApp's own Status
// display, the same way its owner would delete it on the phone.
//
// One publication, not the campaign: a Story posted from four numbers is four
// Stories on WhatsApp, and pulling one of them is a real thing to want. The
// campaign stays where it is, and so does its report — see RevokeStory for why
// the viewer tally freezes rather than disappearing.
//
// Two ids have to agree before anything is sent. The campaign is resolved
// against the caller's scope by campaignForWrite, and the publication is loaded
// workspace-scoped and then checked to belong to that campaign. Either check
// alone would let a publication id from one campaign be revoked through
// another's route.
func (s *Server) handleRevokeStoryPublication(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	c, ok := s.campaignForWrite(w, r)
	if !ok {
		return
	}
	if c.CampaignType != models.CampaignStory {
		writeError(w, http.StatusBadRequest, "not_a_story",
			"Hanya WA Story yang bisa dihapus dari tampilan status")
		return
	}
	pubID, ok := parseUUIDParam(w, chi.URLParam(r, "publicationID"), "publication_id")
	if !ok {
		return
	}

	target, err := s.repo.LiveStoryPublication(r.Context(), user.WorkspaceID, pubID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if target.CampaignID != c.ID {
		writeError(w, http.StatusNotFound, "not_found", "Publikasi ini bukan milik Story tersebut")
		return
	}
	// 'deleted' and 'expired' are both already gone from the Status display;
	// asking WhatsApp to delete them again would fail with a message nobody can
	// act on. Say what actually happened instead.
	if target.Status != "published" {
		writeError(w, http.StatusConflict, "not_live",
			"Story ini sudah tidak tayang di status, jadi tidak ada yang bisa dihapus")
		return
	}

	if err := s.manager.RevokeStory(r.Context(), target.AccountID, target.WAMessageID); err != nil {
		writeAppError(w, err)
		return
	}

	s.recordCampaignActivity(r, c, "story_revoked")

	report, err := s.repo.StoryReportFor(r.Context(), user.WorkspaceID, c.ID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	for i := range report.Publications {
		report.Publications[i].Connected = s.manager.DeviceOnline(report.Publications[i].AccountID)
	}
	s.hub.Broadcast(user.WorkspaceID, realtime.EventCampaignUpdated, &report.Campaign)
	writeJSON(w, http.StatusOK, map[string]any{
		"story":        report,
		"views_notice": models.StoryViewNotice,
	})
}

// handleListCampaignTargets pages through the recipients.
func (s *Server) handleListCampaignTargets(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "campaign_id")
	if !ok {
		return
	}
	c, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !canSeeCampaign(sc, c) {
		writeError(w, http.StatusForbidden, "forbidden", "Campaign ini di luar wewenang akun Anda")
		return
	}

	targets, err := s.repo.ListTargets(r.Context(), id,
		r.URL.Query().Get("status"), intParam(r, "limit", 100), intParam(r, "offset", 0))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": targets})
}

// handleSetCampaignLabels replaces a campaign's internal labels.
func (s *Server) handleSetCampaignLabels(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	existing, ok := s.campaignForWrite(w, r)
	if !ok {
		return
	}
	var req struct {
		LabelIDs []string `json:"label_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ids, ok := parseUUIDList(w, req.LabelIDs, "ID label campaign tidak valid")
	if !ok {
		return
	}
	if err := s.repo.SetCampaignLabels(r.Context(), existing.ID, user.ID, ids); err != nil {
		writeAppError(w, err)
		return
	}
	labels, err := s.repo.CampaignLabelsFor(r.Context(), existing.ID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"labels": labels})
}

// handleGenerateDraft asks GPT for a suggestion.
//
// A draft and nothing more: it is returned to the composer for a person to read,
// edit and approve. Nothing here writes a campaign and nothing here sends.
func (s *Server) handleGenerateDraft(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	var req struct {
		Brief       string   `json:"brief"`
		Tone        string   `json:"tone"`
		Language    string   `json:"language"`
		Variables   []string `json:"variables"`
		WithSpintax bool     `json:"with_spintax"`
		// Mode is the composer's choice: "spintax", "variable" or "both".
		// Absent, it falls back to WithSpintax so older callers still work.
		Mode string `json:"mode"`
		// ApplicationID is where any newly invented variable is filed. Null files
		// it against the whole workspace, which is the composer's "Global".
		ApplicationID *uuid.UUID `json:"application_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	withSpintax := req.WithSpintax
	withVariables := false
	switch req.Mode {
	case campaign.DraftModeSpintax:
		withSpintax, withVariables = true, false
	case campaign.DraftModeVariable:
		withSpintax, withVariables = false, true
	case campaign.DraftModeBoth:
		withSpintax, withVariables = true, true
	}

	draft, err := campaign.GenerateDraft(r.Context(), s.cfg, campaign.DraftRequest{
		Brief:         req.Brief,
		Tone:          req.Tone,
		Language:      req.Language,
		Variables:     req.Variables,
		WithSpintax:   withSpintax,
		WithVariables: withVariables,
	})
	if err != nil {
		if errors.Is(err, campaign.ErrGPTUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "gpt_unavailable",
				"Generator GPT belum dikonfigurasi di server.")
			return
		}
		// Logged without the prompt's provider response, and never with a key.
		_ = s.repo.LogGPTGeneration(r.Context(), user.WorkspaceID, &user.ID, nil,
			s.cfg.OpenAIModel, req.Brief, "", 0, 0, "failed", err.Error())
		writeError(w, http.StatusBadGateway, "gpt_failed", err.Error())
		return
	}

	if err := s.repo.LogGPTGeneration(r.Context(), user.WorkspaceID, &user.ID, nil,
		draft.Model, req.Brief, draft.Text,
		draft.PromptTokens, draft.CompletionTokens, "ok", ""); err != nil {
		s.log.Warn("record gpt usage", "err", err)
	}

	// A variable the model invented is only useful if it exists afterwards.
	//
	// Without this the composer would show <<kode_promo>> in the editor, offer no
	// way to give it a value, and the review would refuse to send because the
	// variable has neither value nor fallback. Registering them here is what makes
	// "Variable" mode a working feature rather than a text generator.
	//
	// Only for somebody allowed to define vocabulary, and only names that are not
	// taken: an existing variable keeps its label and its default.
	saved := []models.CustomVariable{}
	if withVariables && canDefineVocabulary(sc) {
		known := map[string]bool{}
		for _, v := range models.BuiltInVariables {
			known[v.Key] = true
		}
		existing, err := s.repo.ListCustomVariables(
			r.Context(), sc.WorkspaceID, sc.ApplicationIDs, sc.All)
		if err != nil {
			s.log.Warn("read variables before saving generated ones", "err", err)
		}
		for _, v := range existing {
			known[v.Key] = true
		}

		for _, key := range compose.Placeholders(draft.Text) {
			if known[key] {
				continue
			}
			known[key] = true
			v, err := s.repo.UpsertCustomVariable(r.Context(), sc.WorkspaceID, user.ID,
				models.CustomVariable{
					Key:           key,
					ApplicationID: req.ApplicationID,
					Label:         key,
					Description:   strPtr("Dibuat otomatis dari draft AI."),
					IsActive:      true,
				})
			if err != nil {
				s.log.Warn("save generated variable", "key", key, "err", err)
				continue
			}
			saved = append(saved, *v)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"draft": draft,
		// What was filed, so the composer can say so instead of leaving somebody
		// to discover it in the Set Variabel menu.
		"saved_variables": saved,
		// Said plainly, because the interface must not let a generated message
		// go out unread.
		"notice": "Hasil GPT adalah draft. Tinjau dan sunting sebelum dikirim.",
	})
}

func strPtr(s string) *string { return &s }

// --- campaign labels ----------------------------------------------------------

func (s *Server) handleListCampaignLabels(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	labels, err := s.repo.ListCampaignLabels(r.Context(), sc.WorkspaceID,
		r.URL.Query().Get("archived") == "true")
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"labels": labels})
}

func (s *Server) handleCreateCampaignLabel(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !canDefineVocabulary(sc) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader dan PIC yang dapat membuat label campaign")
		return
	}
	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "Nama label tidak boleh kosong")
		return
	}
	label, err := s.repo.CreateCampaignLabel(r.Context(), sc.WorkspaceID, user.ID, req.Name, req.Color)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"label": label})
}

func (s *Server) handleUpdateCampaignLabel(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !canDefineVocabulary(sc) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader dan PIC yang dapat mengubah label campaign")
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "label_id")
	if !ok {
		return
	}
	var req struct {
		Name     *string `json:"name"`
		Color    *string `json:"color"`
		Archived *bool   `json:"archived"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	label, err := s.repo.UpdateCampaignLabel(r.Context(), sc.WorkspaceID, id,
		req.Name, req.Color, req.Archived)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"label": label})
}

// --- custom variables ----------------------------------------------------------

func (s *Server) handleListCustomVariables(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	vars, err := s.repo.ListCustomVariables(r.Context(), sc.WorkspaceID, sc.ApplicationIDs, sc.All)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"variables": vars,
		// The built-ins come from data rather than from this table, and the
		// composer needs to offer both in one list.
		"built_in": models.BuiltInVariables,
	})
}

func (s *Server) handleUpsertCustomVariable(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !canDefineVocabulary(sc) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader dan PIC yang dapat mengelola variabel")
		return
	}
	var req struct {
		Key           string  `json:"key"`
		ApplicationID *string `json:"application_id"`
		Label         string  `json:"label"`
		DefaultValue  *string `json:"default_value"`
		Description   *string `json:"description"`
		IsActive      *bool   `json:"is_active"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	appID, ok := scopedApplication(w, sc, req.ApplicationID)
	if !ok {
		return
	}
	key := strings.ToLower(strings.TrimSpace(req.Key))
	if !validVariableKey(key) {
		writeError(w, http.StatusBadRequest, "invalid_key",
			"Nama variabel hanya boleh huruf kecil, angka dan garis bawah (2–40 karakter)")
		return
	}
	// A custom variable must not shadow a built-in: {{nama}} has to keep meaning
	// the contact's name, or one campaign could greet every customer alike.
	for _, b := range models.BuiltInVariables {
		if b.Key == key {
			writeError(w, http.StatusConflict, "reserved_key",
				"Nama variabel ini sudah dipakai sistem: "+key)
			return
		}
	}

	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = key
	}

	v, err := s.repo.UpsertCustomVariable(r.Context(), sc.WorkspaceID, user.ID, models.CustomVariable{
		Key:           key,
		ApplicationID: appID,
		Label:         label,
		DefaultValue:  req.DefaultValue,
		Description:   req.Description,
		IsActive:      active,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"variable": v})
}

func (s *Server) handleDeleteCustomVariable(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	if !canDefineVocabulary(sc) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Hanya Leader dan PIC yang dapat mengelola variabel")
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "variable_id")
	if !ok {
		return
	}
	if err := s.repo.DeleteCustomVariable(r.Context(), sc.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// --- helpers -------------------------------------------------------------------

// campaignForWrite loads a campaign and checks the caller may change it.
func (s *Server) campaignForWrite(w http.ResponseWriter, r *http.Request) (*models.Campaign, bool) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return nil, false
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "campaign_id")
	if !ok {
		return nil, false
	}
	c, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return nil, false
	}
	if !canManageCampaign(sc, c, user.ID) {
		writeError(w, http.StatusForbidden, "forbidden", "Campaign ini di luar wewenang akun Anda")
		return nil, false
	}
	return c, true
}

// strayDevice returns the name of the first number that does not belong to the
// campaign's application, or "" when they all do.
//
// The rule it enforces: one campaign, one application. A campaign spanning two
// cannot be reported on, cannot be retried without ambiguity about which device
// a failed target belongs to, and cannot be authorised — a PIC holds one
// application, so such a campaign would be half inside their remit and half
// outside, with no right answer to "may they open it".
//
// A number with no application at all is stray too. It sits outside the
// hierarchy, so it belongs to no campaign.
func strayDevice(accounts []repository.AccountInfo, applicationID uuid.UUID) string {
	for _, a := range accounts {
		if a.ApplicationID == nil || *a.ApplicationID != applicationID {
			return a.Name
		}
	}
	return ""
}

// canDefineVocabulary reports whether the caller may define labels and
// variables. A Freelance uses both and defines neither: they are workspace-wide
// settings, and a name chosen by one person shows up in everybody's composer.
func canDefineVocabulary(sc repository.Scope) bool {
	return sc.All || sc.Role == models.RolePIC
}

func validDelayProfile(name string) bool {
	for _, p := range models.DelayProfileNames() {
		if p == name {
			return true
		}
	}
	return false
}

func validVariableKey(key string) bool {
	if len(key) < 2 || len(key) > 40 {
		return false
	}
	for _, r := range key {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func parseUUIDList(w http.ResponseWriter, raw []string, message string) ([]uuid.UUID, bool) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, err := uuid.Parse(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", message)
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

func devicePlans(accounts []repository.AccountInfo, online func(uuid.UUID) bool) []models.DevicePlan {
	out := make([]models.DevicePlan, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, models.DevicePlan{
			AccountID:   a.ID,
			AccountName: a.Name,
			PhoneNumber: a.PhoneNumber,
			Connected:   online(a.ID),
		})
	}
	return out
}

func audiencePayload(list []repository.TargetCandidate) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"account_id":      c.AccountID,
			"chat_jid":        c.ChatJID,
			"phone_number":    c.PhoneNumber,
			"name":            c.Name,
			"contact_id":      c.ContactID,
			"conversation_id": c.ConversationID,
			"kind":            c.Kind,
		})
	}
	return out
}

func mediaLimitsFor(s *Server) mediafetch.Limits {
	return mediafetch.Limits{
		Timeout:      s.cfg.CampaignMediaTimeout,
		MaxBytes:     s.cfg.CampaignMediaMaxBytes,
		MaxRedirects: 3,
		TempDir:      s.cfg.CampaignTempDir,
	}
}

// mediaErrorText turns a fetch failure into something an operator can act on.
func mediaErrorText(err error) string {
	switch {
	case errors.Is(err, mediafetch.ErrScheme):
		return "Media harus berupa tautan https."
	case errors.Is(err, mediafetch.ErrBlocked):
		return "Alamat media tidak diizinkan: tautan mengarah ke jaringan internal."
	case errors.Is(err, mediafetch.ErrTooLarge):
		return "Berkas media melebihi batas ukuran WhatsApp."
	case errors.Is(err, mediafetch.ErrRedirects):
		return "Tautan media terlalu banyak dialihkan."
	default:
		return "Media tidak dapat diambil: " + err.Error()
	}
}
