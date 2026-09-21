package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Address book maintenance: deleting, importing, and merging what the same
// number was stored as twice.
//
// All of it is scoped to a workspace in the query itself rather than checked
// beforehand, so a row belonging to another tenant is simply not found and
// nothing happens to it.

// DeleteContacts removes rows from the address book.
//
// Local only, and only the address book. The conversation, its messages and
// every performance row survive: contact_id is declared `on delete set null`
// on the history tables, so deleting somebody here does not delete what they
// did. It also does not touch the phone, which keeps its own address book.
//
// Returns how many were actually removed, which is what the interface reports.
// Asking for twenty and being told twelve is the useful answer when eight
// belonged to another workspace.
func (r *Repo) DeleteContacts(
	ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID,
) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := r.pool.Exec(ctx,
		`delete from public.contacts where workspace_id = $1 and id = any($2)`,
		workspaceID, ids)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ImportContactRow is one line of an uploaded CSV, already parsed.
type ImportContactRow struct {
	Phone string
	Name  string
}

// ImportResult says what an import actually did, split three ways.
type ImportResult struct {
	Added    int      `json:"added"`
	Updated  int      `json:"updated"`
	Rejected int      `json:"rejected"`
	Reasons  []string `json:"reasons"`
}

// ImportContacts adds a CSV's rows to one WhatsApp account's address book.
//
// An existing number is never duplicated: it is found and its name filled in,
// which is what makes re-importing a corrected list safe. The account is chosen
// in the dialog rather than read from the file, because a contact belongs to
// the number that will message it and a CSV has no way to know which that is.
func (r *Repo) ImportContacts(
	ctx context.Context,
	workspaceID, accountID uuid.UUID,
	rows []ImportContactRow,
) (*ImportResult, error) {
	out := &ImportResult{Reasons: []string{}}

	// The account must belong to this workspace, checked here rather than
	// trusted from the request: the whole import is written against it.
	var owned bool
	if err := r.pool.QueryRow(ctx,
		`select exists (select 1 from public.whatsapp_accounts
		                 where id = $1 and workspace_id = $2)`,
		accountID, workspaceID).Scan(&owned); err != nil {
		return nil, err
	}
	if !owned {
		return nil, ErrNotFound
	}

	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		phone := NormalizePhone(row.Phone)
		if phone == "" {
			out.Rejected++
			if len(out.Reasons) < 10 {
				out.Reasons = append(out.Reasons,
					fmt.Sprintf("%q bukan nomor yang bisa dibaca", truncateText(row.Phone, 24)))
			}
			continue
		}
		// A file that lists the same number twice is one contact, not two, and
		// counting it twice would make the report disagree with the result.
		if _, dup := seen[phone]; dup {
			continue
		}
		seen[phone] = struct{}{}

		var existing uuid.UUID
		err := r.pool.QueryRow(ctx,
			`select id from public.contacts where account_id = $1 and phone_number = $2`,
			accountID, phone).Scan(&existing)
		switch {
		case err == nil:
			if strings.TrimSpace(row.Name) != "" {
				if _, err := r.pool.Exec(ctx,
					`update public.contacts set name = $2 where id = $1`,
					existing, strings.TrimSpace(row.Name)); err != nil {
					return nil, err
				}
			}
			out.Updated++
		case mapErr(err) == ErrNotFound:
			if _, err := r.pool.Exec(ctx, `
				insert into public.contacts
					(workspace_id, account_id, jid, phone_number, name)
				values ($1, $2, $3, $4, nullif($5, ''))`,
				workspaceID, accountID, phone+"@s.whatsapp.net", phone,
				strings.TrimSpace(row.Name)); err != nil {
				return nil, err
			}
			out.Added++
		default:
			return nil, err
		}
	}
	return out, nil
}

// MergeResult reports one merge pass.
type MergeResult struct {
	Normalised int `json:"normalised"`
	Merged     int `json:"merged"`
}

// MergeDuplicateContacts folds rows that are the same number written differently.
//
// 6282113791936, 082113791936 and +62 821-1379-1936 are one person. They only
// ever diverge through an import, because everything arriving from WhatsApp is
// already in one shape, so this is the repair for a file somebody typed by hand.
//
// Two steps, in this order. Every number is first rewritten into the one shape
// (country code, digits only), which is what makes the duplicates visible at
// all. Then rows that have become identical within an account are merged, the
// survivor keeping whatever the loser knew and every reference repointed.
//
// Across accounts nothing is merged. The same person on two of our numbers is
// two relationships with two histories and two sets of labels, and collapsing
// them would silently destroy one.
func (r *Repo) MergeDuplicateContacts(
	ctx context.Context, workspaceID uuid.UUID,
) (*MergeResult, error) {
	out := &MergeResult{}

	rows, err := r.pool.Query(ctx, `
		select id, account_id, coalesce(phone_number, '')
		  from public.contacts
		 where workspace_id = $1 and phone_number is not null and phone_number <> ''`,
		workspaceID)
	if err != nil {
		return nil, err
	}

	type row struct {
		id      uuid.UUID
		account uuid.UUID
		phone   string
	}
	var all []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.account, &x.phone); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Pass one: rewrite anything not already in canonical shape. Done one row
	// at a time rather than in bulk because a rewrite can collide with a row
	// that already holds the canonical form, and that collision is exactly the
	// duplicate pass two is here to resolve.
	byKey := map[string][]uuid.UUID{}
	for _, x := range all {
		canonical := NormalizePhone(x.phone)
		if canonical == "" {
			continue
		}
		if canonical != x.phone {
			if _, err := r.pool.Exec(ctx, `
				update public.contacts set phone_number = $2
				 where id = $1
				   and not exists (select 1 from public.contacts o
				                    where o.account_id = $3 and o.phone_number = $2)`,
				x.id, canonical, x.account); err != nil {
				return nil, err
			}
			out.Normalised++
		}
		key := x.account.String() + ":" + canonical
		byKey[key] = append(byKey[key], x.id)
	}

	// Pass two: merge what is now the same number on the same account.
	for _, ids := range byKey {
		if len(ids) < 2 {
			continue
		}
		winner := ids[0]
		for _, loser := range ids[1:] {
			if err := r.mergeContactInto(ctx, winner, loser); err != nil {
				return nil, err
			}
			out.Merged++
		}
	}
	return out, nil
}

// mergeContactInto moves everything from one contact row onto another.
//
// The same shape as migration 0037, kept here because an import can create the
// duplicates a migration already cleaned up once. The three tables keyed by
// contact_id hold one row per contact, so the loser's is moved only when the
// winner has none.
func (r *Repo) mergeContactInto(ctx context.Context, winner, loser uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		update public.contacts w
		   set name          = coalesce(w.name, l.name),
		       push_name     = coalesce(w.push_name, l.push_name),
		       business_name = coalesce(w.business_name, l.business_name),
		       avatar_url    = coalesce(w.avatar_url, l.avatar_url),
		       lid_jid       = coalesce(w.lid_jid, l.lid_jid),
		       is_business   = w.is_business or l.is_business,
		       is_blocked    = w.is_blocked or l.is_blocked
		  from public.contacts l
		 where w.id = $1 and l.id = $2`, winner, loser); err != nil {
		return err
	}

	for _, table := range []string{
		"conversations", "conversation_members", "sla_cycles", "follow_up_events",
		"contact_label_events", "campaign_targets",
	} {
		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`update public.%s set contact_id = $1 where contact_id = $2`, table),
			winner, loser); err != nil {
			return fmt.Errorf("move %s: %w", table, err)
		}
	}

	for _, table := range []string{
		"contact_first_seen", "lead_classifications", "contact_label_state",
	} {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			update public.%[1]s t set contact_id = $1
			 where t.contact_id = $2
			   and not exists (select 1 from public.%[1]s w where w.contact_id = $1)`, table),
			winner, loser); err != nil {
			return fmt.Errorf("move %s: %w", table, err)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`delete from public.%s where contact_id = $1`, table), loser); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}

	if _, err := tx.Exec(ctx, `delete from public.contacts where id = $1`, loser); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ExportContacts streams the address book for a CSV download.
//
// Built from the same filter the screen is showing, so the file matches what
// was on the page. It is deliberately not capped: an export that silently
// stopped at five hundred rows would be worse than no export.
func (r *Repo) ExportContacts(
	ctx context.Context, workspaceID uuid.UUID, f models.ContactFilter,
) ([]models.Contact, error) {
	q := &queryArgs{}
	where := contactWhere(workspaceID, f, q)
	rows, err := r.pool.Query(ctx,
		`select `+contactColumns+contactFrom+where+
			` order by lower(coalesce(nullif(btrim(c.name), ''), nullif(btrim(c.push_name), ''),
			                          c.phone_number)) asc`, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Contact{}
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NormalizePhone rewrites a number into the one shape WhatsApp addresses use:
// country code first, digits only.
//
// Indonesian numbers are written four ways in the same spreadsheet — 0812…,
// +62 812…, 62812…, and 0812-3456-7890 — and all four are one person. A leading
// zero is the local trunk prefix and becomes 62; a leading "+" is punctuation.
// Anything that cannot be read as a number returns empty rather than a guess,
// and the caller reports it as rejected instead of storing something wrong.
func NormalizePhone(raw string) string {
	digits := make([]rune, 0, len(raw))
	for _, ch := range raw {
		if ch >= '0' && ch <= '9' {
			digits = append(digits, ch)
		}
	}
	s := string(digits)
	if s == "" {
		return ""
	}

	switch {
	case strings.HasPrefix(s, "0"):
		s = "62" + strings.TrimLeft(s, "0")
	case strings.HasPrefix(s, "620"):
		// "+62 0812…" typed by hand: one country code, one trunk zero.
		s = "62" + strings.TrimLeft(s[2:], "0")
	}

	// Shorter than this is not a reachable number anywhere, and longer than
	// fifteen is outside what E.164 allows.
	if len(s) < 8 || len(s) > 15 {
		return ""
	}
	return s
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
