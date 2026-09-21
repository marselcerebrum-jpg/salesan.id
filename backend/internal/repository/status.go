package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Status updates, read out of the status@broadcast thread.
//
// Stored as ordinary messages on a conversation of type 'status', so the whole
// attachment pipeline — private bucket, signed URLs, download on demand —
// applies unchanged. What is different is how they are read back: not as a
// thread, but grouped by who posted them, which is how anybody actually looks at
// Status.

// StatusPost is one Status somebody published.
type StatusPost struct {
	MessageID  uuid.UUID  `json:"message_id"`
	SenderJID  string     `json:"sender_jid"`
	SenderName string     `json:"sender_name"`
	Phone      *string    `json:"phone_number"`
	AvatarURL  *string    `json:"avatar_url"`
	Type       string     `json:"type"`
	Body       *string    `json:"body"`
	Caption    *string    `json:"caption"`
	PostedAt   time.Time  `json:"posted_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	FromMe     bool       `json:"from_me"`
	SeenAt     *time.Time `json:"seen_at"`
	// AttachmentID is what the browser asks for a URL with. Null for text.
	AttachmentID *uuid.UUID `json:"attachment_id"`
	// AttachmentStatus is pending, uploading, stored, failed or expired. The
	// viewer needs it: a file still on its way is not a missing one, and telling
	// the two apart is what stops a photo that arrives a second later from being
	// declared unavailable.
	AttachmentStatus *string `json:"attachment_status"`
	// DurationSecs times the progress bar for a video. Null for anything else,
	// which the viewer reads as "use the fixed interval".
	DurationSecs *int    `json:"duration_secs"`
	Thumbnail    *string `json:"thumbnail_b64"`
	// Viewers is how many distinct people this server saw a read receipt from,
	// and it is meaningful only on our own status. A LOWER BOUND, never a view
	// count: WhatsApp gives a linked device no viewer list, so the only evidence
	// is a receipt, and somebody with read receipts switched off never sends one.
	// Always zero on other people's status — their viewers' receipts go to them,
	// not to us.
	Viewers int `json:"viewers"`
}

// ListStatusPosts returns every live Status one number can see, newest first.
//
// Bounded by age in the query rather than trusting the sweeper to have run: a
// Status that is twenty-five hours old is gone from the phone, and showing it
// here because a cleanup has not fired yet would be showing something that no
// longer exists.
func (r *Repo) ListStatusPosts(
	ctx context.Context, workspaceID, accountID uuid.UUID,
) ([]StatusPost, error) {
	rows, err := r.pool.Query(ctx, `
		select m.id, coalesce(m.participant_jid, m.sender_jid, ''),
		       coalesce(
		         nullif(btrim(ct.name), ''),
		         nullif(btrim(ct.push_name), ''),
		         nullif(btrim(ct.business_name), ''),
		         ct.phone_number,
		         split_part(coalesce(m.participant_jid, m.sender_jid, ''), '@', 1)
		       ) as sender_name,
		       ct.phone_number, ct.avatar_url,
		       m.type, m.body, m.caption, m.timestamp,
		       m.timestamp + interval '24 hours',
		       m.from_me, m.read_at,
		       a.id, a.storage_status::text, a.duration_secs, a.thumbnail_b64,
		       -- Viewers. Not counted here: read from status_view_counts, the one
		       -- place that decides what a view figure is, which the Story report
		       -- reads too. Two screens counting the same thing separately is
		       -- exactly how a status came to read 1 penonton on one page and 0 on
		       -- the other.
		       coalesce(vc.viewers, 0)
		  from public.messages m
		  join public.conversations c on c.id = m.conversation_id
		  left join public.status_view_counts vc
		         on vc.account_id = m.account_id
		        and vc.wa_message_id = m.wa_message_id
		  left join lateral (
		    select x.name, x.push_name, x.business_name, x.phone_number, x.avatar_url
		      from public.contacts x
		     where x.workspace_id = c.workspace_id
		       and (x.jid = coalesce(m.participant_jid, m.sender_jid)
		            or x.lid_jid = coalesce(m.participant_jid, m.sender_jid))
		     limit 1
		  ) ct on true
		  left join public.message_attachments a
		         on a.message_id = m.id and a.idx = 0
		 where c.workspace_id = $1
		   and c.account_id = $2
		   and c.type = 'status'
		   and m.hidden_at is null
		   -- Deleted by whoever posted it. Revoking nulls the text and drops the
		   -- attachment rows, so without this a deleted Status stayed in the
		   -- list as a blank one nobody could explain.
		   and m.revoked_at is null
		   and m.timestamp > now() - interval '24 hours'
		 order by m.timestamp desc`, workspaceID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []StatusPost{}
	for rows.Next() {
		var p StatusPost
		if err := rows.Scan(&p.MessageID, &p.SenderJID, &p.SenderName, &p.Phone, &p.AvatarURL,
			&p.Type, &p.Body, &p.Caption, &p.PostedAt, &p.ExpiresAt, &p.FromMe, &p.SeenAt,
			&p.AttachmentID, &p.AttachmentStatus, &p.DurationSecs, &p.Thumbnail,
			&p.Viewers); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkStatusSeen records that the operator opened one Status.
//
// Local only. Nothing is sent to WhatsApp: telling somebody their Status was
// viewed when a person may only have scrolled past it in a list would be
// manufacturing a read receipt, and the read receipt is theirs to earn.
func (r *Repo) MarkStatusSeen(ctx context.Context, workspaceID, messageID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		update public.messages m
		   set read_at = coalesce(m.read_at, now())
		  from public.conversations c
		 where m.id = $1 and c.id = m.conversation_id
		   and c.workspace_id = $2 and c.type = 'status'`, messageID, workspaceID)
	return err
}

// ExpiredStatusAttachments lists the files of Statuses past their 24 hours, so
// the janitor can empty the bucket before the rows go.
func (r *Repo) ExpiredStatusAttachments(ctx context.Context, limit int) ([]ExpiringAttachment, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
		select a.id, a.storage_path
		  from public.message_attachments a
		  join public.messages m on m.id = a.message_id
		  join public.conversations c on c.id = m.conversation_id
		 where c.type = 'status'
		   and a.storage_path is not null
		   and m.timestamp < now() - interval '24 hours'
		 limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ExpiringAttachment{}
	for rows.Next() {
		var e ExpiringAttachment
		if err := rows.Scan(&e.ID, &e.StoragePath); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteExpiredStatuses removes Status messages once they are past their day.
//
// Deleted rather than marked expired, which is the opposite of what happens to
// chat media. A Status is gone from WhatsApp itself after a day: there is no
// thread to look back at, nothing that refers to it, and no report that has to
// be able to say it existed. Keeping the row would leave a permanent record of
// something the platform treats as temporary.
//
// Twenty-six hours, not twenty-four, and the two extra hours are load-bearing.
// The viewer figure for our own status is frozen an hour after it expires, from
// this row, and the sweep that does it runs every ten minutes. Deleting at the
// 24-hour mark took the row away before the figure was safe, so the number a
// status posted from the phone reached was lost while the same number for a
// scheduled Story survived. Nobody can see a status in this window either way:
// ListStatusPosts stops showing anything older than 24 hours, and the files
// leave the bucket on time, an hour or more before the rows do.
func (r *Repo) DeleteExpiredStatuses(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		delete from public.messages m
		 using public.conversations c
		 where c.id = m.conversation_id
		   and c.type = 'status'
		   and m.timestamp < now() - interval '26 hours'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
