package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Channels a number follows.
//
// Only the directory is stored. A channel's posts are read from WhatsApp when
// the screen opens them: they are somebody else's publication, they are not
// replied to, and copying them into our database would add storage and a duty of
// care without answering a single question the screen asks.

// Newsletter is one channel as this number sees it.
type Newsletter struct {
	ID          uuid.UUID  `json:"id"`
	AccountID   uuid.UUID  `json:"account_id"`
	JID         string     `json:"jid"`
	Name        string     `json:"name"`
	Description *string    `json:"description"`
	Subscribers *int       `json:"subscriber_count"`
	PictureURL  *string    `json:"picture_url"`
	// ViewerRole is what WhatsApp says this number is to the channel: owner,
	// admin, subscriber or guest. It decides whether the screen may offer to
	// manage the channel or only to read it.
	ViewerRole string     `json:"viewer_role"`
	Muted      bool       `json:"muted"`
	Verified   bool       `json:"verified"`
	CreatedOn  *time.Time `json:"created_on"`
	SyncedAt   time.Time  `json:"synced_at"`
}

// UpsertNewsletter records one channel as it was just described by WhatsApp.
func (r *Repo) UpsertNewsletter(ctx context.Context, workspaceID uuid.UUID, n Newsletter) error {
	_, err := r.pool.Exec(ctx, `
		insert into public.newsletters
			(workspace_id, account_id, jid, name, description, subscriber_count,
			 picture_url, viewer_role, muted, verified, created_on, synced_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
		on conflict (account_id, jid) do update set
			name             = excluded.name,
			description      = excluded.description,
			subscriber_count = excluded.subscriber_count,
			picture_url      = coalesce(excluded.picture_url, public.newsletters.picture_url),
			viewer_role      = excluded.viewer_role,
			muted            = excluded.muted,
			verified         = excluded.verified,
			created_on       = coalesce(excluded.created_on, public.newsletters.created_on),
			synced_at        = now()`,
		workspaceID, n.AccountID, n.JID, n.Name, n.Description, n.Subscribers,
		n.PictureURL, n.ViewerRole, n.Muted, n.Verified, n.CreatedOn)
	return err
}

// ReplaceNewsletters makes the stored directory match what WhatsApp just
// returned, dropping channels this number no longer follows.
//
// One transaction, because a half-applied sync is a directory that lists a
// channel the operator already left and hides one they just joined.
func (r *Repo) ReplaceNewsletters(
	ctx context.Context, workspaceID, accountID uuid.UUID, list []Newsletter,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	jids := make([]string, 0, len(list))
	for _, n := range list {
		jids = append(jids, n.JID)
		if _, err := tx.Exec(ctx, `
			insert into public.newsletters
				(workspace_id, account_id, jid, name, description, subscriber_count,
				 picture_url, viewer_role, muted, verified, created_on, synced_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
			on conflict (account_id, jid) do update set
				name             = excluded.name,
				description      = excluded.description,
				subscriber_count = excluded.subscriber_count,
				picture_url      = coalesce(excluded.picture_url, public.newsletters.picture_url),
				viewer_role      = excluded.viewer_role,
				muted            = excluded.muted,
				verified         = excluded.verified,
				created_on       = coalesce(excluded.created_on, public.newsletters.created_on),
				synced_at        = now()`,
			workspaceID, accountID, n.JID, n.Name, n.Description, n.Subscribers,
			n.PictureURL, n.ViewerRole, n.Muted, n.Verified, n.CreatedOn); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx,
		`delete from public.newsletters where account_id = $1 and jid <> all($2)`,
		accountID, jids); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListNewsletters returns one number's channels, by name.
func (r *Repo) ListNewsletters(
	ctx context.Context, workspaceID, accountID uuid.UUID,
) ([]Newsletter, error) {
	rows, err := r.pool.Query(ctx, `
		select id, account_id, jid, name, description, subscriber_count,
		       picture_url, viewer_role, muted, verified, created_on, synced_at
		  from public.newsletters
		 where workspace_id = $1 and account_id = $2
		 order by lower(name), jid`, workspaceID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Newsletter{}
	for rows.Next() {
		var n Newsletter
		if err := rows.Scan(&n.ID, &n.AccountID, &n.JID, &n.Name, &n.Description,
			&n.Subscribers, &n.PictureURL, &n.ViewerRole, &n.Muted, &n.Verified,
			&n.CreatedOn, &n.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetNewsletter loads one channel by its address.
func (r *Repo) GetNewsletter(
	ctx context.Context, workspaceID, accountID uuid.UUID, jid string,
) (*Newsletter, error) {
	var n Newsletter
	err := r.pool.QueryRow(ctx, `
		select id, account_id, jid, name, description, subscriber_count,
		       picture_url, viewer_role, muted, verified, created_on, synced_at
		  from public.newsletters
		 where workspace_id = $1 and account_id = $2 and jid = $3`,
		workspaceID, accountID, jid,
	).Scan(&n.ID, &n.AccountID, &n.JID, &n.Name, &n.Description, &n.Subscribers,
		&n.PictureURL, &n.ViewerRole, &n.Muted, &n.Verified, &n.CreatedOn, &n.SyncedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &n, nil
}

// SetNewsletterMuted records a mute locally after WhatsApp accepted it.
func (r *Repo) SetNewsletterMuted(
	ctx context.Context, accountID uuid.UUID, jid string, muted bool,
) error {
	_, err := r.pool.Exec(ctx,
		`update public.newsletters set muted = $3 where account_id = $1 and jid = $2`,
		accountID, jid, muted)
	return err
}

// DeleteNewsletter drops a channel this number has stopped following.
func (r *Repo) DeleteNewsletter(ctx context.Context, accountID uuid.UUID, jid string) error {
	_, err := r.pool.Exec(ctx,
		`delete from public.newsletters where account_id = $1 and jid = $2`, accountID, jid)
	return err
}
