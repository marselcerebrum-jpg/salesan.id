package repository

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

const accountColumns = `
	a.id, a.workspace_id, a.application_id, app.code, app.name, app.color,
	a.name, a.label, a.phone_number, a.device_id, a.jid, a.lid,
	a.connection_method::text, a.status::text, a.status_detail,
	a.last_connected_at, a.last_synced_at, a.sync_window_days,
	a.labels_synced_at, a.label_sync_state, a.label_sync_error, a.created_at,
	coalesce(cv.n, 0), coalesce(cv.unread, 0), coalesce(cv.awaiting, 0),
	coalesce(ms.n, 0)`

const accountFrom = `
	from public.whatsapp_accounts a
	left join public.applications app on app.id = a.application_id
	left join lateral (
	     select count(*) as n,
	            coalesce(sum(c.unread_count), 0) as unread,
	            -- Chats where the customer spoke last and nobody has answered.
	            -- Maintained on the row by refresh_conversation_head, so this is
	            -- an index lookup rather than a scan of every message on the
	            -- number. See migration 0053 for why it is not unread_count and
	            -- not last_message_direction.
	            count(*) filter (where c.awaiting_reply) as awaiting
	       from public.conversations c
	      where c.account_id = a.id and c.is_archived = false
	) cv on true
	left join lateral (
	     select count(*) as n from public.messages m where m.account_id = a.id
	) ms on true`

func scanAccount(row interface {
	Scan(dest ...any) error
}) (*models.Account, error) {
	var a models.Account
	err := row.Scan(
		&a.ID, &a.WorkspaceID, &a.ApplicationID, &a.ApplicationCode, &a.ApplicationName, &a.ApplicationColor,
		&a.Name, &a.Label, &a.PhoneNumber, &a.DeviceID, &a.JID, &a.LID,
		&a.ConnectionMethod, &a.Status, &a.StatusDetail,
		&a.LastConnectedAt, &a.LastSyncedAt, &a.SyncWindowDays,
		&a.LabelsSyncedAt, &a.LabelSyncState, &a.LabelSyncError, &a.CreatedAt,
		&a.ConversationCount, &a.UnreadCount, &a.UnansweredCount, &a.MessageCount,
	)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAccounts returns the account grid (reference screen 1).
func (r *Repo) ListAccounts(ctx context.Context, sc Scope, f models.AccountFilter) ([]models.Account, error) {
	args := []any{sc.WorkspaceID}
	where := []string{"a.workspace_id = $1"}

	// The caller's applications, applied before any of their own filters. A
	// number outside them is not hidden later — it is never selected.
	if !sc.All {
		args = append(args, sc.ApplicationIDs)
		where = append(where, fmt.Sprintf("a.application_id = any($%d)", len(args)))
	}

	if f.Unassigned {
		// Only a Leader can ask for unassigned numbers; for anybody else the
		// clause above has already excluded them, and asking again would return
		// an empty list rather than a contradiction.
		where = append(where, "a.application_id is null")
	} else if f.ApplicationID != nil {
		args = append(args, *f.ApplicationID)
		where = append(where, fmt.Sprintf("a.application_id = $%d", len(args)))
	}
	if f.ConnectionMethod != "" {
		args = append(args, f.ConnectionMethod)
		where = append(where, fmt.Sprintf("a.connection_method = $%d::public.connection_method", len(args)))
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		where = append(where, fmt.Sprintf(
			"(a.name ilike $%d or a.phone_number ilike $%d or a.device_id ilike $%d)",
			len(args), len(args), len(args)))
	}

	q := "select " + accountColumns + accountFrom +
		" where " + strings.Join(where, " and ") +
		" order by app.sort_order asc nulls last, a.created_at asc"

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Account{}
	for rows.Next() {
		acc, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *acc)
	}
	return out, rows.Err()
}

// ListAccountsByApplication powers the number picker (reference screen 5).
func (r *Repo) ListAccountsByApplication(ctx context.Context, sc Scope, applicationID uuid.UUID) ([]models.Account, error) {
	return r.ListAccounts(ctx, sc, models.AccountFilter{ApplicationID: &applicationID})
}

// GetAccount loads one account scoped to the workspace.
func (r *Repo) GetAccount(ctx context.Context, workspaceID, id uuid.UUID) (*models.Account, error) {
	q := "select " + accountColumns + accountFrom + " where a.workspace_id = $1 and a.id = $2"
	acc, err := scanAccount(r.pool.QueryRow(ctx, q, workspaceID, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return acc, nil
}

// AccountRef is the minimal shape the WhatsApp manager needs at boot.
type AccountRef struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	JID         *string
	Status      string
}

// ListLinkedAccounts returns every account across all workspaces that has been
// paired at least once, so the manager can restore their sessions on startup.
func (r *Repo) ListLinkedAccounts(ctx context.Context) ([]AccountRef, error) {
	rows, err := r.pool.Query(ctx, `
		select id, workspace_id, jid, status::text
		  from public.whatsapp_accounts
		 where jid is not null
		   and connection_method = 'qr'
		   and status <> 'logged_out'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AccountRef{}
	for rows.Next() {
		var a AccountRef
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.JID, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAccountRef resolves an account without the aggregate joins.
func (r *Repo) GetAccountRef(ctx context.Context, id uuid.UUID) (*AccountRef, error) {
	var a AccountRef
	err := r.pool.QueryRow(ctx, `
		select id, workspace_id, jid, status::text
		  from public.whatsapp_accounts where id = $1`, id,
	).Scan(&a.ID, &a.WorkspaceID, &a.JID, &a.Status)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

// CreateAccountInput is what the "Tambah Akun" modal sends.
type CreateAccountInput struct {
	Name             string
	Label            *string
	ApplicationID    *uuid.UUID
	ConnectionMethod string
}

// CreateAccount inserts an account in the `disconnected` state, retrying on the
// (astronomically unlikely) device-id collision.
func (r *Repo) CreateAccount(ctx context.Context, workspaceID uuid.UUID, in CreateAccountInput) (*models.Account, error) {
	method := in.ConnectionMethod
	if method == "" {
		method = "qr"
	}

	var id uuid.UUID
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		deviceID, err := newDeviceID()
		if err != nil {
			return nil, err
		}
		err = r.pool.QueryRow(ctx, `
			insert into public.whatsapp_accounts
				(workspace_id, application_id, name, label, device_id, connection_method, status)
			values ($1, $2, $3, $4, $5, $6::public.connection_method, 'disconnected')
			returning id`,
			workspaceID, in.ApplicationID, in.Name, in.Label, deviceID, method,
		).Scan(&id)
		if err == nil {
			return r.GetAccount(ctx, workspaceID, id)
		}
		lastErr = err
		if !strings.Contains(err.Error(), "uq_whatsapp_accounts_device_id") {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not allocate a unique device id: %w", lastErr)
}

// UpdateAccountInput carries partial edits from the detail page.
type UpdateAccountInput struct {
	Name          *string
	Label         *string
	ApplicationID *uuid.UUID
	ClearApp      bool
}

// UpdateAccount applies a partial update.
func (r *Repo) UpdateAccount(ctx context.Context, workspaceID, id uuid.UUID, in UpdateAccountInput) (*models.Account, error) {
	tag, err := r.pool.Exec(ctx, `
		update public.whatsapp_accounts
		   set name           = coalesce($3, name),
		       label          = coalesce($4, label),
		       application_id = case when $5 then null else coalesce($6, application_id) end
		 where workspace_id = $1 and id = $2`,
		workspaceID, id, in.Name, in.Label, in.ClearApp, in.ApplicationID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.GetAccount(ctx, workspaceID, id)
}

// SetAccountStatus records a connection-state transition. It is called from
// whatsmeow event handlers, so it is deliberately workspace-agnostic: the
// account id already came from a workspace-scoped lookup.
func (r *Repo) SetAccountStatus(ctx context.Context, id uuid.UUID, status string, detail *string) error {
	_, err := r.pool.Exec(ctx, `
		update public.whatsapp_accounts
		   set status            = $2::public.account_status,
		       status_detail     = $3,
		       last_connected_at = case when $2 = 'connected' then now() else last_connected_at end
		 where id = $1`, id, status, detail)
	return err
}

// SetAccountIdentity stores the JID and phone number learned during pairing.
func (r *Repo) SetAccountIdentity(ctx context.Context, id uuid.UUID, jid, phone string) error {
	_, err := r.pool.Exec(ctx, `
		update public.whatsapp_accounts
		   set jid = $2, phone_number = $3
		 where id = $1`, id, jid, phone)
	return err
}

// ClearAccountIdentity forgets the paired device, used on logout.
func (r *Repo) ClearAccountIdentity(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		update public.whatsapp_accounts
		   set jid = null, status = 'logged_out'
		 where id = $1`, id)
	return err
}

// MarkAccountSynced stamps the end of a successful sync run.
func (r *Repo) MarkAccountSynced(ctx context.Context, id uuid.UUID, windowDays int) error {
	_, err := r.pool.Exec(ctx, `
		update public.whatsapp_accounts
		   set last_synced_at   = now(),
		       sync_window_days = $2
		 where id = $1`, id, windowDays)
	return err
}

// DeleteAccount removes the account and, by cascade, its contacts,
// conversations and messages.
func (r *Repo) DeleteAccount(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`delete from public.whatsapp_accounts where workspace_id = $1 and id = $2`, workspaceID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AccountStats fills the header counters ("29 akun terdaftar", "29/10 perangkat").
func (r *Repo) AccountStats(ctx context.Context, workspaceID uuid.UUID) (*models.AccountStats, error) {
	var s models.AccountStats
	err := r.pool.QueryRow(ctx, `
		select count(*),
		       count(*) filter (where status = 'connected'),
		       count(*) filter (where connection_method = 'qr'),
		       count(*) filter (where connection_method = 'waba'),
		       count(*) filter (where application_id is null)
		  from public.whatsapp_accounts
		 where workspace_id = $1`, workspaceID,
	).Scan(&s.Total, &s.Connected, &s.QRAccounts, &s.WABAAccounts, &s.Unassigned)
	if err != nil {
		return nil, err
	}
	// Plan limits are static in the MVP; move them onto the workspace row when
	// billing lands.
	s.DeviceLimit = 10
	s.WABALimit = 5
	return &s, nil
}

// UpsertSession records which device is bound to an account.
func (r *Repo) UpsertSession(ctx context.Context, accountID uuid.UUID, deviceJID, pushName, platform, businessName string) error {
	_, err := r.pool.Exec(ctx, `
		insert into public.whatsapp_sessions
			(account_id, device_jid, push_name, platform, business_name, is_active, connected_at)
		values ($1, $2, nullif($3, ''), nullif($4, ''), nullif($5, ''), true, now())
		on conflict (account_id, device_jid) do update
		   set push_name       = coalesce(nullif(excluded.push_name, ''), public.whatsapp_sessions.push_name),
		       platform        = coalesce(nullif(excluded.platform, ''), public.whatsapp_sessions.platform),
		       business_name   = coalesce(nullif(excluded.business_name, ''), public.whatsapp_sessions.business_name),
		       is_active       = true,
		       connected_at    = now(),
		       disconnected_at = null`,
		accountID, deviceJID, pushName, platform, businessName)
	return err
}

// CloseSessions marks every session for an account as inactive.
func (r *Repo) CloseSessions(ctx context.Context, accountID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		update public.whatsapp_sessions
		   set is_active = false, disconnected_at = coalesce(disconnected_at, now())
		 where account_id = $1 and is_active`, accountID)
	return err
}

// deviceIDAlphabet omits I/O/0/1 so the code stays unambiguous when read aloud.
const deviceIDAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newDeviceID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("D-")
	for _, b := range buf {
		sb.WriteByte(deviceIDAlphabet[int(b)%len(deviceIDAlphabet)])
	}
	return sb.String(), nil
}

// Touch is a tiny helper for tests that need a deterministic timestamp.
func Touch() time.Time { return time.Now().UTC() }
