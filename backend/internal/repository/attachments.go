package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/salesan/omnichannel/backend/internal/models"
)

const attachmentColumns = `
	a.id, a.message_id, a.idx, a.kind::text, a.file_name, a.mime_type, a.size_bytes,
	a.width, a.height, a.duration_secs, a.thumbnail_b64, a.storage_status::text, a.storage_error`

func scanAttachment(row interface {
	Scan(dest ...any) error
}) (*models.Attachment, error) {
	var a models.Attachment
	err := row.Scan(
		&a.ID, &a.MessageID, &a.Index, &a.Kind, &a.FileName, &a.MimeType, &a.SizeBytes,
		&a.Width, &a.Height, &a.Duration, &a.Thumbnail, &a.Status, &a.StoreError,
	)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// InsertAttachmentInput describes one file attached to a message.
type InsertAttachmentInput struct {
	WorkspaceID uuid.UUID
	AccountID   uuid.UUID
	MessageID   uuid.UUID
	Index       int
	Kind        string
	FileName    *string
	MimeType    *string
	SizeBytes   *int64
	Width       *int
	Height      *int
	Duration    *int
	Thumbnail   *string
	StoragePath *string
	Status      string
}

// InsertAttachment writes an attachment row idempotently.
//
// (message_id, idx) is the key. A message replayed by a reconnect or a history
// sync therefore cannot produce a second copy of the same file, and the stored
// row — which may already have completed its download — is returned untouched.
func (r *Repo) InsertAttachment(ctx context.Context, in InsertAttachmentInput) (*models.Attachment, bool, error) {
	if in.Status == "" {
		in.Status = models.AttachmentPending
	}

	const q = `with a as (
		insert into public.message_attachments
			(workspace_id, account_id, message_id, idx, kind, file_name, mime_type, size_bytes,
			 width, height, duration_secs, thumbnail_b64, storage_path, storage_status)
		values ($1, $2, $3, $4, $5::public.attachment_kind, $6, $7, $8, $9, $10, $11, $12, $13,
		        $14::public.attachment_storage_status)
		on conflict (message_id, idx) do nothing
		returning *
	) select ` + attachmentColumns + " from a"

	att, err := scanAttachment(r.pool.QueryRow(ctx, q,
		in.WorkspaceID, in.AccountID, in.MessageID, in.Index, in.Kind, in.FileName, in.MimeType,
		in.SizeBytes, in.Width, in.Height, in.Duration, in.Thumbnail, in.StoragePath, in.Status))
	if err == nil {
		return att, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	q2 := "select " + attachmentColumns + `
		  from public.message_attachments a
		 where a.message_id = $1 and a.idx = $2`
	existing, err := scanAttachment(r.pool.QueryRow(ctx, q2, in.MessageID, in.Index))
	if err != nil {
		return nil, false, mapErr(err)
	}
	return existing, false, nil
}

// MediaRef is the material needed to fetch an attachment from WhatsApp again.
// It never leaves the server: the media key decrypts the file, so it is a
// credential in every practical sense.
type MediaRef struct {
	DirectPath    string
	MediaKey      []byte
	FileEncSHA256 []byte
	FileSHA256    []byte
	MediaType     string
}

// SaveMediaRef stores the download material for an attachment.
func (r *Repo) SaveMediaRef(ctx context.Context, attachmentID uuid.UUID, ref MediaRef) error {
	_, err := r.pool.Exec(ctx, `
		insert into public.whatsapp_media_refs
			(attachment_id, direct_path, media_key, file_enc_sha256, file_sha256, media_type)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (attachment_id) do update set
			direct_path     = excluded.direct_path,
			media_key       = excluded.media_key,
			file_enc_sha256 = excluded.file_enc_sha256,
			file_sha256     = excluded.file_sha256,
			media_type      = excluded.media_type`,
		attachmentID, ref.DirectPath, ref.MediaKey, ref.FileEncSHA256, ref.FileSHA256, ref.MediaType)
	return err
}

// GetMediaRef loads the download material for an attachment.
func (r *Repo) GetMediaRef(ctx context.Context, attachmentID uuid.UUID) (*MediaRef, error) {
	var ref MediaRef
	err := r.pool.QueryRow(ctx, `
		select coalesce(direct_path, ''), media_key, file_enc_sha256, file_sha256, coalesce(media_type, '')
		  from public.whatsapp_media_refs where attachment_id = $1`, attachmentID,
	).Scan(&ref.DirectPath, &ref.MediaKey, &ref.FileEncSHA256, &ref.FileSHA256, &ref.MediaType)
	if err != nil {
		return nil, mapErr(err)
	}
	return &ref, nil
}

// MarkAttachmentStored records a successful upload to the storage bucket.
func (r *Repo) MarkAttachmentStored(ctx context.Context, id uuid.UUID, path string, size int64) error {
	_, err := r.pool.Exec(ctx, `
		update public.message_attachments
		   set storage_path   = $2,
		       storage_status = 'stored',
		       storage_error  = null,
		       size_bytes     = case when $3::bigint > 0 then $3::bigint else size_bytes end
		 where id = $1`, id, path, size)
	return err
}

// MarkAttachmentStatus moves an attachment between non-terminal states, keeping
// the reason when it failed.
func (r *Repo) MarkAttachmentStatus(ctx context.Context, id uuid.UUID, status string, detail *string) error {
	_, err := r.pool.Exec(ctx, `
		update public.message_attachments
		   set storage_status = $2::public.attachment_storage_status,
		       storage_error  = $3
		 where id = $1`, id, status, detail)
	return err
}

// AttachmentLocation is what the API needs to mint a signed URL: the object key
// plus the workspace that owns it.
type AttachmentLocation struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	AccountID   uuid.UUID
	MessageID   uuid.UUID
	StoragePath *string
	Status      string
	FileName    *string
	MimeType    *string
	Kind        string
}

// GetAttachmentLocation loads one attachment scoped to a workspace.
//
// The workspace predicate is the access check: an attachment belonging to
// another tenant returns ErrNotFound rather than a signed URL, and does so
// without revealing that the id exists.
func (r *Repo) GetAttachmentLocation(ctx context.Context, workspaceID, id uuid.UUID) (*AttachmentLocation, error) {
	var loc AttachmentLocation
	err := r.pool.QueryRow(ctx, `
		select a.id, a.workspace_id, a.account_id, a.message_id, a.storage_path,
		       a.storage_status::text, a.file_name, a.mime_type, a.kind::text
		  from public.message_attachments a
		 where a.id = $1 and a.workspace_id = $2`, id, workspaceID,
	).Scan(&loc.ID, &loc.WorkspaceID, &loc.AccountID, &loc.MessageID, &loc.StoragePath,
		&loc.Status, &loc.FileName, &loc.MimeType, &loc.Kind)
	if err != nil {
		return nil, mapErr(err)
	}
	return &loc, nil
}

// AttachmentsForMessages loads every attachment for a page of messages in one
// query, keyed by message id.
func (r *Repo) AttachmentsForMessages(ctx context.Context, messageIDs []uuid.UUID) (map[uuid.UUID][]models.Attachment, error) {
	if len(messageIDs) == 0 {
		return map[uuid.UUID][]models.Attachment{}, nil
	}
	q := "select " + attachmentColumns + `
		  from public.message_attachments a
		 where a.message_id = any($1)
		 order by a.message_id, a.idx`

	rows, err := r.pool.Query(ctx, q, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID][]models.Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out[a.MessageID] = append(out[a.MessageID], *a)
	}
	return out, rows.Err()
}

// AttachmentsForMessage loads one message's attachments.
func (r *Repo) AttachmentsForMessage(ctx context.Context, messageID uuid.UUID) ([]models.Attachment, error) {
	byMessage, err := r.AttachmentsForMessages(ctx, []uuid.UUID{messageID})
	if err != nil {
		return nil, err
	}
	if list := byMessage[messageID]; list != nil {
		return list, nil
	}
	return []models.Attachment{}, nil
}

// PendingDownload names an attachment whose bytes never made it into storage.
type PendingDownload struct {
	AttachmentID uuid.UUID
	AccountID    uuid.UUID
	WorkspaceID  uuid.UUID
	MessageID    uuid.UUID
	WAMessageID  string
	Kind         string
	MimeType     string
	FileName     string
}

// PendingDownloads lists incoming attachments still waiting for their bytes.
//
// Used by the retry sweep after a reconnect: a download that failed because the
// socket was down is retried, without the message itself being re-inserted.
// Only rows that have download material are returned — an outgoing attachment
// whose upload failed is the sender's problem, not something to fetch.
func (r *Repo) PendingDownloads(ctx context.Context, accountID uuid.UUID, limit int) ([]PendingDownload, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		select a.id, a.account_id, a.workspace_id, a.message_id, m.wa_message_id,
		       a.kind::text, coalesce(a.mime_type, ''), coalesce(a.file_name, '')
		  from public.message_attachments a
		  join public.messages m on m.id = a.message_id
		  join public.whatsapp_media_refs r on r.attachment_id = a.id
		 where a.account_id = $1
		   and a.storage_status in ('pending', 'failed')
		   and m.from_me = false
		 order by m.timestamp desc
		 limit $2`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PendingDownload{}
	for rows.Next() {
		var p PendingDownload
		if err := rows.Scan(&p.AttachmentID, &p.AccountID, &p.WorkspaceID, &p.MessageID,
			&p.WAMessageID, &p.Kind, &p.MimeType, &p.FileName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// StoragePathsOlderThan lists object keys for messages that have aged out of
// the sync window, so they can be removed from the bucket before the rows are
// pruned. Without this, pruning would orphan the files and the bucket would
// grow without bound.
func (r *Repo) StoragePathsOlderThan(ctx context.Context, accountID uuid.UUID, cutoff time.Time) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		select a.storage_path
		  from public.message_attachments a
		  join public.messages m on m.id = a.message_id
		 where a.account_id = $1
		   and m.timestamp < $2
		   and a.storage_path is not null`, accountID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RetentionTarget is one account's media-retention setting.
type RetentionTarget struct {
	AccountID  uuid.UUID
	WindowDays int
}

// AccountsWithStoredMedia lists every account that still has files in the
// bucket, along with how long it keeps them.
//
// Deliberately not driven by the live sessions: an account that is disconnected
// — or one whose session failed to restore — would otherwise keep its media
// forever, which is exactly the case where nobody is watching.
func (r *Repo) AccountsWithStoredMedia(ctx context.Context) ([]RetentionTarget, error) {
	rows, err := r.pool.Query(ctx, `
		select a.id, greatest(a.sync_window_days, 1)
		  from public.whatsapp_accounts a
		 where exists (
		   select 1 from public.message_attachments m
		    where m.account_id = a.id and m.storage_status = 'stored')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RetentionTarget{}
	for rows.Next() {
		var t RetentionTarget
		if err := rows.Scan(&t.AccountID, &t.WindowDays); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ExpiringAttachment names one file that has outlived the retention window.
type ExpiringAttachment struct {
	ID          uuid.UUID
	StoragePath string
}

// AttachmentsPastRetention lists stored files whose message is older than the
// cutoff, so the janitor can delete them from the bucket.
func (r *Repo) AttachmentsPastRetention(
	ctx context.Context,
	accountID uuid.UUID,
	cutoff time.Time,
	limit int,
) ([]ExpiringAttachment, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
		select a.id, a.storage_path
		  from public.message_attachments a
		  join public.messages m on m.id = a.message_id
		 where a.account_id = $1
		   and a.storage_status = 'stored'
		   and a.storage_path is not null
		   and m.timestamp < $2
		 order by m.timestamp asc
		 limit $3`, accountID, cutoff, limit)
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

// MarkAttachmentsExpired records that files are gone for good.
//
// Everything that could reproduce the image is cleared in one transaction: the
// storage path, the inline thumbnail, and — crucially — the download material
// in whatsapp_media_refs. Leaving the ref behind would let the next person to
// open the message pull the file down from WhatsApp again, which would make
// the whole retention window meaningless.
//
// The attachment row itself stays so the conversation keeps its shape: the
// bubble says the file expired rather than vanishing and leaving a gap.
func (r *Repo) MarkAttachmentsExpired(ctx context.Context, ids []uuid.UUID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`delete from public.whatsapp_media_refs where attachment_id = any($1)`, ids); err != nil {
		return 0, err
	}

	tag, err := tx.Exec(ctx, `
		update public.message_attachments
		   set storage_status = 'expired',
		       storage_path   = null,
		       thumbnail_b64  = null,
		       storage_error  = null
		 where id = any($1)`, ids)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
