package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// PollOptionHash is how WhatsApp names an option inside a vote: the SHA-256 of
// the option's text. Storing it alongside the option is what lets an incoming
// vote be mapped back without relying on ordering.
func PollOptionHash(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// NormalizeJID strips the device part from a WhatsApp address.
//
// An account's stored JID carries the linked-device number ("628…:20@s.whatsapp.net")
// while message senders are stored without it. Comparing the two raw forms never
// matches, which is what hid our own poll votes.
func NormalizeJID(jid string) string {
	at := strings.LastIndexByte(jid, '@')
	if at < 0 {
		return jid
	}
	user, server := jid[:at], jid[at:]
	if colon := strings.IndexByte(user, ':'); colon >= 0 {
		user = user[:colon]
	}
	return user + server
}

// InsertPollInput describes a poll to persist.
type InsertPollInput struct {
	WorkspaceID     uuid.UUID
	AccountID       uuid.UUID
	MessageID       uuid.UUID
	Name            string
	SelectableCount int
	Options         []string
}

// InsertPoll writes a poll and its options.
//
// Idempotent on the message id: a poll replayed by a reconnect or a history
// sync leaves the stored one — and the votes hanging off it — untouched.
func (r *Repo) InsertPoll(ctx context.Context, in InsertPollInput) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		insert into public.message_polls
			(message_id, workspace_id, account_id, name, selectable_count)
		values ($1, $2, $3, $4, $5)
		on conflict (message_id) do nothing`,
		in.MessageID, in.WorkspaceID, in.AccountID, in.Name, in.SelectableCount)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // already stored, options and votes are already in place
	}

	for i, name := range in.Options {
		if _, err := tx.Exec(ctx, `
			insert into public.message_poll_options (message_id, idx, name, option_hash)
			values ($1, $2, $3, $4)
			on conflict (message_id, idx) do nothing`,
			in.MessageID, i, name, PollOptionHash(name)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// PollRef identifies a stored poll well enough to build a vote for it.
type PollRef struct {
	MessageID   uuid.UUID
	WAMessageID string
	ChatJID     string
	SenderJID   string
	FromMe      bool
	Options     []string
}

// PollByMessage loads a poll's addressing details and option texts.
func (r *Repo) PollByMessage(ctx context.Context, workspaceID, messageID uuid.UUID) (*PollRef, error) {
	var ref PollRef
	err := r.pool.QueryRow(ctx, `
		select m.id, m.wa_message_id, c.chat_jid, coalesce(m.sender_jid, ''), m.from_me
		  from public.message_polls p
		  join public.messages m      on m.id = p.message_id
		  join public.conversations c on c.id = m.conversation_id
		 where p.message_id = $1 and p.workspace_id = $2`, messageID, workspaceID,
	).Scan(&ref.MessageID, &ref.WAMessageID, &ref.ChatJID, &ref.SenderJID, &ref.FromMe)
	if err != nil {
		return nil, mapErr(err)
	}

	rows, err := r.pool.Query(ctx,
		`select name from public.message_poll_options where message_id = $1 order by idx`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		ref.Options = append(ref.Options, name)
	}
	return &ref, rows.Err()
}

// ApplyPollVote records one voter's complete current selection.
//
// A WhatsApp vote update carries everything the voter has chosen, not a delta —
// so applying it means replacing that voter's rows outright. `at` guards
// against an older update arriving after a newer one, which happens routinely
// on reconnect.
//
// Returns false when the update was ignored as stale.
//
// aliases are other addresses of the same person. Our own number has two — the
// phone number and the LID — and a vote cast on the web is filed under one
// while the phone's copy of a vote can arrive under the other. Without folding
// them together, voting on the phone after voting here counted this account
// twice. Every alias is treated as this voter: its timestamp takes part in the
// staleness guard, and its rows are replaced along with the voter's own.
func (r *Repo) ApplyPollVote(
	ctx context.Context,
	messageID uuid.UUID,
	voterJID string,
	optionHashes []string,
	at time.Time,
	aliases ...string,
) (bool, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	voters := []string{voterJID}
	for _, a := range aliases {
		if a != "" && a != voterJID {
			voters = append(voters, a)
		}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The guard lives in its own table rather than being derived from the vote
	// rows. Clearing a vote deletes every row a voter has, which would take the
	// guard with it — and then a replayed older update would bring the old
	// answer back to life.
	var newest *time.Time
	if err := tx.QueryRow(ctx,
		`select max(voted_at) from public.message_poll_voters
		  where message_id = $1 and voter_jid = any($2)`, messageID, voters).Scan(&newest); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}
	if newest != nil && !at.After(*newest) {
		return false, nil
	}

	// The aliases' own rows go: from here on this person is one voter.
	if len(voters) > 1 {
		if _, err := tx.Exec(ctx,
			`delete from public.message_poll_voters
			  where message_id = $1 and voter_jid = any($2) and voter_jid <> $3`,
			messageID, voters, voterJID); err != nil {
			return false, err
		}
	}

	if _, err := tx.Exec(ctx, `
		insert into public.message_poll_voters (message_id, voter_jid, voted_at)
		values ($1, $2, $3)
		on conflict (message_id, voter_jid) do update set voted_at = excluded.voted_at`,
		messageID, voterJID, at); err != nil {
		return false, err
	}

	if _, err := tx.Exec(ctx,
		`delete from public.message_poll_votes where message_id = $1 and voter_jid = any($2)`,
		messageID, voters); err != nil {
		return false, err
	}

	// Unknown hashes are dropped rather than stored: an option we never saw
	// cannot be displayed, and inventing a row for it would inflate the tally.
	if len(optionHashes) > 0 {
		if _, err := tx.Exec(ctx, `
			insert into public.message_poll_votes (message_id, voter_jid, option_idx, voted_at)
			select o.message_id, $2, o.idx, $4::timestamptz
			  from public.message_poll_options o
			 where o.message_id = $1 and o.option_hash = any($3)
			on conflict do nothing`,
			messageID, voterJID, optionHashes, at); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// PollMessageIDForWAID resolves the poll a vote refers to.
func (r *Repo) PollMessageIDForWAID(ctx context.Context, accountID uuid.UUID, waMessageID string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select p.message_id
		  from public.message_polls p
		  join public.messages m on m.id = p.message_id
		 where m.account_id = $1 and m.wa_message_id = $2`, accountID, waMessageID).Scan(&id)
	if err != nil {
		return uuid.Nil, mapErr(err)
	}
	return id, nil
}

// PollsForMessages loads polls, options and tallies for a page of messages.
//
// `ownJIDs` are every address this account answers to — its phone-number JID
// and its LID. Both are needed: WhatsApp addresses the same account one way in
// some chats and the other way in others, so a vote we cast can come back under
// either, and matching only one would leave our own answer looking unchosen.
func (r *Repo) PollsForMessages(
	ctx context.Context,
	messageIDs []uuid.UUID,
	ownJIDs []string,
) (map[uuid.UUID]*models.Poll, error) {
	out := map[uuid.UUID]*models.Poll{}
	if len(messageIDs) == 0 {
		return out, nil
	}

	rows, err := r.pool.Query(ctx, `
		select message_id, name, selectable_count
		  from public.message_polls where message_id = any($1)`, messageIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p models.Poll
		if err := rows.Scan(&p.MessageID, &p.Name, &p.SelectableCount); err != nil {
			rows.Close()
			return nil, err
		}
		p.Options = []models.PollOption{}
		p.SelectedIdx = []int{}
		out[p.MessageID] = &p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	ids := make([]uuid.UUID, 0, len(out))
	for id := range out {
		ids = append(ids, id)
	}

	// Options with their tallies in one pass; a left join keeps options that
	// nobody has voted for.
	optRows, err := r.pool.Query(ctx, `
		select o.message_id, o.idx, o.name,
		       count(v.voter_jid) as votes,
		       bool_or(v.voter_jid = any($2)) as mine
		  from public.message_poll_options o
		  left join public.message_poll_votes v
		         on v.message_id = o.message_id and v.option_idx = o.idx
		 where o.message_id = any($1)
		 group by o.message_id, o.idx, o.name
		 order by o.message_id, o.idx`, ids, ownJIDs)
	if err != nil {
		return nil, err
	}
	for optRows.Next() {
		var messageID uuid.UUID
		var opt models.PollOption
		var mine *bool
		if err := optRows.Scan(&messageID, &opt.Index, &opt.Name, &opt.Votes, &mine); err != nil {
			optRows.Close()
			return nil, err
		}
		if poll := out[messageID]; poll != nil {
			poll.Options = append(poll.Options, opt)
			if mine != nil && *mine {
				poll.SelectedIdx = append(poll.SelectedIdx, opt.Index)
			}
		}
	}
	optRows.Close()
	if err := optRows.Err(); err != nil {
		return nil, err
	}

	// Distinct voters, which is not the sum of option counts when a poll allows
	// more than one answer.
	voterRows, err := r.pool.Query(ctx, `
		select message_id, count(distinct voter_jid)
		  from public.message_poll_votes
		 where message_id = any($1)
		 group by message_id`, ids)
	if err != nil {
		return nil, err
	}
	defer voterRows.Close()
	for voterRows.Next() {
		var messageID uuid.UUID
		var n int
		if err := voterRows.Scan(&messageID, &n); err != nil {
			return nil, err
		}
		if poll := out[messageID]; poll != nil {
			poll.TotalVoters = n
		}
	}
	return out, voterRows.Err()
}
