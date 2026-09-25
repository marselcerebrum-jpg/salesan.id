package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The store-once layer for media bytes.
//
// An attachment row belongs to one message, and a broadcast to sixty contacts
// makes sixty of them. The file they describe is one file. These functions are
// what keeps the bucket holding one copy of it: the object's identity is its
// content hash, not the message that happened to carry it.
//
// The hash is WhatsApp's own, already stored in whatsapp_media_refs, so nothing
// here recomputes it. Scoped to a workspace throughout: two tenants with the
// same bytes get two objects, because a shared path is a path one tenant could
// reach for the other's file.

// StoredObject is where a file with a given content hash already lives.
type StoredObject struct {
	StoragePath string
	SizeBytes   int64
}

// ClaimStoredObject points an attachment at the object already holding its
// content, and reports nil when nothing holds it yet.
//
// The lookup and the attachment update are one transaction over a locked row,
// which is what makes this safe against the janitor running at the same time.
// Releasing an object takes the same lock and then asks which attachments point
// at the path: either this claim lands first and the janitor sees it and keeps
// the object, or the release lands first and this finds no row and reports the
// content as unstored, so the caller fetches and uploads it again. What cannot
// happen is an attachment left pointing at an object that has been deleted.
func (r *Repo) ClaimStoredObject(
	ctx context.Context,
	workspaceID uuid.UUID,
	sha []byte,
	attachmentID uuid.UUID,
) (*StoredObject, error) {
	if len(sha) == 0 {
		return nil, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var obj StoredObject
	err = tx.QueryRow(ctx, `
		select storage_path, size_bytes
		  from public.media_objects
		 where workspace_id = $1 and file_sha256 = $2
		   for update`, workspaceID, sha,
	).Scan(&obj.StoragePath, &obj.SizeBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Scoped to the workspace as well as the id: an attachment belonging to
	// another tenant must not be pointed at this object even by mistake.
	if _, err := tx.Exec(ctx, `
		update public.message_attachments
		   set storage_path   = $3,
		       storage_status = 'stored',
		       storage_error  = null,
		       size_bytes     = case when $4::bigint > 0 then $4::bigint else size_bytes end
		 where id = $1 and workspace_id = $2`,
		attachmentID, workspaceID, obj.StoragePath, obj.SizeBytes); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &obj, nil
}

// RegisterStoredObject records that this content now lives at this path.
//
// Does nothing when another caller registered the same content first. The path
// is derived from the hash, so the winner's path and ours are the same string
// and the loser's upload wrote the same bytes to the same key: there is no
// orphaned second object to clean up, which is the reason the key is computed
// rather than random.
func (r *Repo) RegisterStoredObject(
	ctx context.Context,
	workspaceID uuid.UUID,
	sha []byte,
	path string,
	size int64,
) error {
	if len(sha) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		insert into public.media_objects (workspace_id, file_sha256, storage_path, size_bytes)
		values ($1, $2, $3, $4)
		on conflict (workspace_id, file_sha256) do nothing`,
		workspaceID, sha, path, size)
	return err
}

// ReleaseStorageObjects reports which of these object keys no longer have an
// attachment pointing at them, and forgets them in the lookup as it does so.
//
// This is the question that has to be asked before anything is deleted from the
// bucket now that one object serves many attachments. Deleting a file because
// one of sixty recipients had their conversation cleared would break it for the
// other fifty-nine.
//
// It is asked of the attachment rows themselves rather than of a counter.
// A counter is a second truth that drifts — a crash between decrementing and
// deleting leaves it wrong forever, and nothing would ever notice. Asking the
// rows can only be out of date, never wrong, and the next sweep asks again.
//
// ignore names attachments that are about to be settled but whose rows still
// point at the path. The retention janitor empties the bucket before it marks
// rows expired, on purpose, so without this it would find every key still
// referenced by the very batch it is expiring and never delete anything.
func (r *Repo) ReleaseStorageObjects(
	ctx context.Context,
	keys []string,
	ignore []uuid.UUID,
) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if ignore == nil {
		ignore = []uuid.UUID{}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Locked in the same order the claim path locks, and before the attachment
	// rows are consulted, so a claim in flight either has already pointed its
	// attachment at the path or will find the row gone.
	// Ordered so two sweeps with overlapping key sets take the rows in the same
	// sequence and wait for each other instead of deadlocking.
	if _, err := tx.Exec(ctx, `
		select 1 from public.media_objects
		 where storage_path = any($1)
		 order by storage_path
		   for update`, keys); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		select k
		  from unnest($1::text[]) as k
		 where not exists (
		       select 1
		         from public.message_attachments a
		        where a.storage_path = k
		          and a.id <> all($2::uuid[]))`, keys, ignore)
	if err != nil {
		return nil, err
	}
	free := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return nil, err
		}
		free = append(free, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(free) > 0 {
		// The lookup entry goes with the object. Leaving it behind would hand
		// the next attachment with this content a path to a file that is no
		// longer there, and it would be marked stored rather than fetched.
		if _, err := tx.Exec(ctx,
			`delete from public.media_objects where storage_path = any($1)`, free); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return free, nil
}
