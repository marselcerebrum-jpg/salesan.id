package repository

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Quick replies: canned messages summoned in a chat room by typing a slash.
//
// Scoped per application exactly like custom variables, and for the same
// reason: a workspace here holds a dozen brands, and a reply written for one
// of them is a mis-send waiting to happen in another. A NULL application means
// the reply belongs to the company rather than to a brand.

// ListQuickReplies returns the replies this reader may use.
//
// `all` skips the application narrowing (a Leader). `applicationIDs` nil with
// all=false means the reader holds nothing, which still returns the
// workspace-wide replies: those are not anybody's to withhold.
func (r *Repo) ListQuickReplies(
	ctx context.Context, workspaceID uuid.UUID, applicationIDs []uuid.UUID, all bool, activeOnly bool,
) ([]models.QuickReply, error) {
	rows, err := r.pool.Query(ctx, `
		select q.id, q.application_id, `+appJoinColumns+`,
		       q.shortcut, q.title, q.body, q.category,
		       q.media_url, q.media_kind, q.media_mime, q.media_size_bytes,
		       q.usage_count, q.last_used_at, q.is_active, q.created_at
		  from public.quick_replies q
		  left join public.applications a on a.id = q.application_id
		 where q.workspace_id = $1
		   and ($2::boolean or q.application_id is null or q.application_id = any ($3::uuid[]))
		   and (not $4::boolean or q.is_active)
		 order by a.code nulls first, lower(q.shortcut)`,
		workspaceID, all, applicationIDs, activeOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.QuickReply{}
	for rows.Next() {
		var q models.QuickReply
		var appID *uuid.UUID
		if err := rows.Scan(&q.ID, &q.ApplicationID, &appID, &q.ApplicationCode,
			&q.ApplicationName, &q.ApplicationColor,
			&q.Shortcut, &q.Title, &q.Body, &q.Category,
			&q.MediaURL, &q.MediaKind, &q.MediaMime, &q.MediaSizeBytes,
			&q.UsageCount, &q.LastUsedAt, &q.IsActive, &q.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// UpsertQuickReply creates or edits one, keyed by its shortcut within its
// application. See UpsertCustomVariable for why there are two conflict
// targets rather than one.
func (r *Repo) UpsertQuickReply(
	ctx context.Context, workspaceID, createdBy uuid.UUID, q models.QuickReply,
) (*models.QuickReply, error) {
	shortcut := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(q.Shortcut, "/")))

	conflict := `(workspace_id, lower(shortcut)) where application_id is null`
	if q.ApplicationID != nil {
		conflict = `(workspace_id, application_id, lower(shortcut)) where application_id is not null`
	}

	var category *string
	if q.Category != nil {
		if trimmed := strings.TrimSpace(*q.Category); trimmed != "" {
			category = &trimmed
		}
	}

	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		insert into public.quick_replies
			(workspace_id, application_id, shortcut, title, body, category,
			 media_url, media_kind, media_mime, media_size_bytes, media_sha256,
			 is_active, created_by)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		on conflict `+conflict+` do update
		   set title = excluded.title,
		       body = excluded.body,
		       category = excluded.category,
		       -- Replaced wholesale, never merged: clearing the address must
		       -- clear what was measured about it, or the row would claim to
		       -- carry an image it no longer has.
		       media_url = excluded.media_url,
		       media_kind = excluded.media_kind,
		       media_mime = excluded.media_mime,
		       media_size_bytes = excluded.media_size_bytes,
		       media_sha256 = excluded.media_sha256,
		       is_active = excluded.is_active
		returning id`,
		workspaceID, q.ApplicationID, shortcut, strings.TrimSpace(q.Title),
		q.Body, category,
		q.MediaURL, q.MediaKind, q.MediaMime, q.MediaSizeBytes, q.MediaSHA256,
		q.IsActive, createdBy).Scan(&id)
	if err != nil {
		return nil, mapErr(err)
	}
	return r.quickReplyByID(ctx, workspaceID, id)
}

func (r *Repo) quickReplyByID(
	ctx context.Context, workspaceID, id uuid.UUID,
) (*models.QuickReply, error) {
	var q models.QuickReply
	var appID *uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select q.id, q.application_id, `+appJoinColumns+`,
		       q.shortcut, q.title, q.body, q.category,
		       q.media_url, q.media_kind, q.media_mime, q.media_size_bytes,
		       q.usage_count, q.last_used_at, q.is_active, q.created_at
		  from public.quick_replies q
		  left join public.applications a on a.id = q.application_id
		 where q.id = $1 and q.workspace_id = $2`, id, workspaceID).
		Scan(&q.ID, &q.ApplicationID, &appID, &q.ApplicationCode, &q.ApplicationName,
			&q.ApplicationColor, &q.Shortcut, &q.Title, &q.Body, &q.Category,
			&q.MediaURL, &q.MediaKind, &q.MediaMime, &q.MediaSizeBytes,
			&q.UsageCount, &q.LastUsedAt, &q.IsActive, &q.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &q, nil
}

// QuickReplyApplication reports which application a reply belongs to, so a
// handler can check the caller's reach before letting them change it.
// A nil id with no error means the reply is workspace-wide.
func (r *Repo) QuickReplyApplication(
	ctx context.Context, workspaceID, id uuid.UUID,
) (*uuid.UUID, error) {
	var appID *uuid.UUID
	err := r.pool.QueryRow(ctx,
		`select application_id from public.quick_replies where id = $1 and workspace_id = $2`,
		id, workspaceID).Scan(&appID)
	if err != nil {
		return nil, mapErr(err)
	}
	return appID, nil
}

// UpdateQuickReply rewrites one reply by its id.
//
// Separate from UpsertQuickReply, which finds its row by (application,
// shortcut): editing has to be able to CHANGE those two, and an upsert keyed on
// them would leave the original behind under its old name and write a second
// row under the new one.
//
// A shortcut that collides with another reply in the same application is
// refused by the unique index rather than silently merged — two replies are two
// replies, and the caller is told which name is taken.
func (r *Repo) UpdateQuickReply(
	ctx context.Context, workspaceID, id uuid.UUID, q models.QuickReply,
) (*models.QuickReply, error) {
	shortcut := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(q.Shortcut, "/")))

	var category *string
	if q.Category != nil {
		if trimmed := strings.TrimSpace(*q.Category); trimmed != "" {
			category = &trimmed
		}
	}

	tag, err := r.pool.Exec(ctx, `
		update public.quick_replies
		   set application_id = $3, shortcut = $4, title = $5, body = $6,
		       category = $7,
		       media_url = $8, media_kind = $9, media_mime = $10,
		       media_size_bytes = $11, media_sha256 = $12,
		       is_active = $13, updated_at = now()
		 where id = $1 and workspace_id = $2`,
		id, workspaceID, q.ApplicationID, shortcut, strings.TrimSpace(q.Title),
		q.Body, category,
		q.MediaURL, q.MediaKind, q.MediaMime, q.MediaSizeBytes, q.MediaSHA256,
		q.IsActive)
	if err != nil {
		return nil, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.quickReplyByID(ctx, workspaceID, id)
}

// MarkQuickReplyUsed records that one reply was actually sent.
//
// Best effort by design, and the caller ignores its error: the message has
// already gone out, and failing the request now would tell the operator their
// send failed when it did not. A tally that misses one is a smaller wrong than
// an error message that is false.
func (r *Repo) MarkQuickReplyUsed(ctx context.Context, workspaceID, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		update public.quick_replies
		   set usage_count = usage_count + 1, last_used_at = now()
		 where id = $1 and workspace_id = $2`, id, workspaceID)
	return err
}

// DeleteAllQuickReplies removes every reply the caller may delete, and reports
// how many went.
//
// Narrowed in SQL by the same rules the list uses, so this can never reach past
// the caller's own applications. A PIC clearing their brand does not touch the
// workspace-wide replies, which are not theirs: those carry application_id null
// and are excluded unless `all` is set, which only a Leader has.
//
// applicationID narrows further, to the one brand shown on screen. That is what
// makes "hapus semua" mean what the person pressing it is looking at rather
// than everything they own.
func (r *Repo) DeleteAllQuickReplies(
	ctx context.Context,
	workspaceID uuid.UUID,
	applicationIDs []uuid.UUID,
	all bool,
	applicationID *uuid.UUID,
) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		delete from public.quick_replies q
		 where q.workspace_id = $1
		   and ($2::boolean or q.application_id = any ($3::uuid[]))
		   and ($4::uuid is null or q.application_id = $4::uuid)`,
		workspaceID, all, applicationIDs, applicationID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteQuickReply removes one outright.
//
// Safe to delete, unlike a campaign label: a quick reply is a template, and
// the messages it produced are ordinary messages stored on their own rows.
// Removing the template cannot change what anybody received.
func (r *Repo) DeleteQuickReply(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`delete from public.quick_replies where id = $1 and workspace_id = $2`, id, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
