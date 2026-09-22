package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// UpsertContactInput is what a whatsmeow contact/push-name event carries.
type UpsertContactInput struct {
	WorkspaceID  uuid.UUID
	AccountID    uuid.UUID
	JID          string
	PhoneNumber  string
	Name         string
	PushName     string
	BusinessName string
	IsBusiness   bool
}

// UpsertContact inserts or refreshes a contact and returns its ID.
//
// Find first, then insert, rather than an ON CONFLICT on (account_id, jid).
// That conflict clause only ever recognised a contact by the one address it
// was first stored under, and a person has two: a phone number and a LID.
// Whichever form arrived second was a new row, so the address book filled with
// pairs. `jid` and `lid_jid` hold the two forms and `phone_number` unifies
// them, so this recognises the person however they are addressed.
//
// Blank incoming values never overwrite something we already know: a push name
// arriving without a saved name must not erase the saved name.
func (r *Repo) UpsertContact(ctx context.Context, in UpsertContactInput) (uuid.UUID, error) {
	// Two passes at most. The lookup can miss when a concurrent insert lands
	// between it and ours, in which case the unique index rejects the second
	// write and the retry finds the row the winner created.
	for attempt := 0; attempt < 2; attempt++ {
		id, found, err := r.findContact(ctx, in)
		if err != nil {
			return uuid.Nil, err
		}
		if found {
			err := r.enrichContact(ctx, id, in)
			if err == nil {
				return id, nil
			}
			// The row we found is this person under one address, and the number
			// we are filling in already belongs to another row: the same person
			// under their other address. One of them was created before anyone
			// knew the two were the same. Fold them together and carry on.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
				pgErr.ConstraintName == "uq_contacts_account_phone" {
				return r.mergeByPhone(ctx, id, in)
			}
			return uuid.Nil, err
		}

		id, err = r.insertContact(ctx, in)
		if err == nil {
			return id, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue // someone else created it; look it up again
		}
		return uuid.Nil, err
	}
	return uuid.Nil, fmt.Errorf("repository: could not resolve contact %s", in.JID)
}

// findContact resolves a person by any address they answer to.
func (r *Repo) findContact(ctx context.Context, in UpsertContactInput) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select id from public.contacts
		 where account_id = $1
		   and (jid = $2
		        or ($2 like '%@lid' and lid_jid = $2)
		        or ($3 <> '' and phone_number = $3))
		 order by (jid = $2) desc
		 limit 1`, in.AccountID, in.JID, in.PhoneNumber).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// enrichContact fills in what the row is missing without erasing what it has.
//
// The addresses are filled in too, not just the names. A row first created from
// a LID learns its phone number here, and a row created from a number learns
// its LID, which is what stops the next sync from opening a second row for the
// form this one does not yet carry.
func (r *Repo) enrichContact(ctx context.Context, id uuid.UUID, in UpsertContactInput) error {
	_, err := r.pool.Exec(ctx, `
		update public.contacts
		   set phone_number  = coalesce(phone_number, nullif($2, '')),
		       lid_jid       = coalesce(lid_jid, case when $3 like '%@lid' then $3 end),
		       name          = coalesce(nullif($4, ''), name),
		       push_name     = coalesce(nullif($5, ''), push_name),
		       business_name = coalesce(nullif($6, ''), business_name),
		       is_business   = is_business or $7
		 where id = $1`,
		id, in.PhoneNumber, in.JID, in.Name, in.PushName, in.BusinessName, in.IsBusiness)
	return err
}

// mergeByPhone folds the row we were about to write a number into together
// with the row that already holds that number.
//
// Which one survives is not arbitrary: the row carrying the phone number is the
// one every other part of the system can address, so it is kept and the other
// is merged into it. Everything pointing at the loser is moved first; the
// database function does that in one transaction.
//
// Without this the two rows stay apart forever. Every sync retries the same
// fill, is refused by the same unique index, and skips the contact — which is
// how a phone ended up with two entries for one person and neither of them
// updating.
func (r *Repo) mergeByPhone(
	ctx context.Context, id uuid.UUID, in UpsertContactInput,
) (uuid.UUID, error) {
	var keep uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select id from public.contacts
		 where account_id = $1 and phone_number = $2 and id <> $3
		 limit 1`, in.AccountID, in.PhoneNumber, id).Scan(&keep)
	if errors.Is(err, pgx.ErrNoRows) {
		// It went away between the refusal and now. The next sync settles it.
		return id, nil
	}
	if err != nil {
		return uuid.Nil, err
	}

	if _, err := r.pool.Exec(ctx,
		`select public.merge_contact_rows($1, $2)`, keep, id); err != nil {
		return uuid.Nil, err
	}
	// The survivor still has to learn the name and the LID the caller brought.
	if err := r.enrichContact(ctx, keep, in); err != nil {
		return uuid.Nil, err
	}
	return keep, nil
}

func (r *Repo) insertContact(ctx context.Context, in UpsertContactInput) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		insert into public.contacts
			(workspace_id, account_id, jid, lid_jid, phone_number,
			 name, push_name, business_name, is_business)
		values ($1, $2, $3,
		        case when $3 like '%@lid' then $3 end,
		        nullif($4, ''), nullif($5, ''), nullif($6, ''), nullif($7, ''), $8)
		returning id`,
		in.WorkspaceID, in.AccountID, in.JID, in.PhoneNumber, in.Name, in.PushName,
		in.BusinessName, in.IsBusiness,
	).Scan(&id)
	return id, err
}

// --- the address book screen -------------------------------------------------

// contactVisible is the one rule about which rows belong in the address book.
//
// A contact with no number is not an address: nothing can be written to it,
// broadcast to it or dialled from it. Those rows arrive as LID entries that
// WhatsApp lists alongside the numbered entry for the same person, so showing
// them put every such person on the screen twice, the second time with the
// digits of their LID sitting where a phone number should be. Written once and
// reused by the list, the counts and the export, because a total that counts
// rows the list does not show is a total nobody can reconcile.
const contactVisible = `c.phone_number is not null and c.phone_number <> ''`

const contactColumns = `
	c.id, c.account_id, c.jid, c.phone_number, c.name, c.push_name, c.avatar_url,
	c.is_business, a.name, a.phone_number, a.application_id, app.code, app.color,
	coalesce(cls.label_names, '{}')`

const contactFrom = `
	from public.contacts c
	join public.whatsapp_accounts a on a.id = c.account_id
	left join public.applications app on app.id = a.application_id
	left join public.contact_label_state cls on cls.contact_id = c.id`

func scanContact(rows pgx.Rows) (models.Contact, error) {
	var c models.Contact
	err := rows.Scan(&c.ID, &c.AccountID, &c.JID, &c.PhoneNumber, &c.Name, &c.PushName,
		&c.AvatarURL, &c.IsBusiness, &c.AccountName, &c.AccountPhone,
		&c.ApplicationID, &c.ApplicationCode, &c.ApplicationColor, &c.Labels)
	return c, err
}

// contactWhere builds the filter shared by the list, the counts and the export.
func contactWhere(workspaceID uuid.UUID, f models.ContactFilter, q *queryArgs) string {
	where := fmt.Sprintf(" where c.workspace_id = %s and %s", q.add(workspaceID), contactVisible)
	if f.AccountID != nil {
		where += fmt.Sprintf(" and c.account_id = %s", q.add(*f.AccountID))
	}
	if f.ApplicationID != nil {
		where += fmt.Sprintf(" and a.application_id = %s", q.add(*f.ApplicationID))
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		p := q.add("%" + s + "%")
		where += fmt.Sprintf(
			" and (c.name ilike %s or c.push_name ilike %s or c.phone_number ilike %s)", p, p, p)
	}
	return where
}

// ListContacts returns one page of the address book.
//
// Ordering mirrors a phone's: contacts with a real saved name come first,
// alphabetically and case-insensitively; then those known only by their
// WhatsApp push name; then bare numbers. Sorting on the raw coalesce would
// interleave the three, because digits sort before letters and casing would
// split "andi" from "Andi".
func (r *Repo) ListContacts(
	ctx context.Context, workspaceID uuid.UUID, f models.ContactFilter,
) ([]models.Contact, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	q := &queryArgs{}
	where := contactWhere(workspaceID, f, q)
	sql := `select ` + contactColumns + contactFrom + where + `
		 order by case
		            when nullif(btrim(coalesce(c.name, '')), '')      is not null then 0
		            when nullif(btrim(coalesce(c.push_name, '')), '') is not null then 1
		            else 2
		          end,
		          lower(coalesce(nullif(btrim(c.name), ''), nullif(btrim(c.push_name), ''),
		                         c.phone_number, c.jid)) asc,
		          c.jid asc` +
		fmt.Sprintf(" limit %s offset %s", q.add(f.Limit), q.add(f.Offset))

	rows, err := r.pool.Query(ctx, sql, q.args...)
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

// CountContacts is how many rows the current filter matches.
//
// Read separately from the page so the header can say "16.321 kontak" while the
// table holds a hundred. Counting the page would make the number mean something
// different from what it says.
func (r *Repo) CountContacts(
	ctx context.Context, workspaceID uuid.UUID, f models.ContactFilter,
) (int, error) {
	q := &queryArgs{}
	where := contactWhere(workspaceID, f, q)
	var n int
	err := r.pool.QueryRow(ctx, `select count(*)`+contactFrom+where, q.args...).Scan(&n)
	return n, err
}

// ContactFacets counts the address book per application and per WhatsApp number.
//
// The two rows are not siblings. Applications are the primary choice and are
// always counted across the whole workspace, so the top row reads the same
// however the page is narrowed. Numbers are the second step *inside* that
// choice: pick CEREBRUM and the row below should offer CEREBRUM's numbers with
// CEREBRUM's counts, not every number in the workspace. A number belonging to
// another brand is not a narrowing of the current view, it is a different view.
//
// Every count applies contactVisible, the same rule the list and the export
// use, so a chip can never promise rows the table will not show.
func (r *Repo) ContactFacets(
	ctx context.Context, workspaceID uuid.UUID, applicationID *uuid.UUID,
) (*models.ContactFacets, error) {
	out := &models.ContactFacets{
		Applications: []models.ContactFacet{},
		Accounts:     []models.ContactFacet{},
	}

	// Every application in the workspace, including those with no contacts yet:
	// a chip reading zero is a fact, and a missing chip looks like a bug.
	appRows, err := r.pool.Query(ctx, `
		select app.id, app.code, app.name, app.color,
		       count(c.id) filter (where `+contactVisible+`)
		  from public.applications app
		  left join public.whatsapp_accounts a on a.application_id = app.id
		  left join public.contacts c on c.account_id = a.id
		 where app.workspace_id = $1
		 group by app.id, app.code, app.name, app.color, app.sort_order
		 order by app.sort_order, app.code`, workspaceID)
	if err != nil {
		return nil, err
	}
	for appRows.Next() {
		var f models.ContactFacet
		var id uuid.UUID
		var name string
		if err := appRows.Scan(&id, &f.Label, &name, &f.Color, &f.Count); err != nil {
			appRows.Close()
			return nil, err
		}
		f.ID = &id
		f.Hint = name
		out.Applications = append(out.Applications, f)
	}
	appRows.Close()
	if err := appRows.Err(); err != nil {
		return nil, err
	}

	// Numbers of the chosen application, or all of them when none is chosen.
	accRows, err := r.pool.Query(ctx, `
		select a.id, a.name, coalesce(a.phone_number, ''),
		       count(c.id) filter (where `+contactVisible+`)
		  from public.whatsapp_accounts a
		  left join public.contacts c on c.account_id = a.id
		 where a.workspace_id = $1
		   and ($2::uuid is null or a.application_id = $2::uuid)
		 group by a.id, a.name, a.phone_number
		 order by a.name`, workspaceID, applicationID)
	if err != nil {
		return nil, err
	}
	defer accRows.Close()
	for accRows.Next() {
		var f models.ContactFacet
		var id uuid.UUID
		if err := accRows.Scan(&id, &f.Label, &f.Hint, &f.Count); err != nil {
			return nil, err
		}
		f.ID = &id
		out.Accounts = append(out.Accounts, f)
	}
	if err := accRows.Err(); err != nil {
		return nil, err
	}

	// Both totals in one round trip. Counted directly rather than summed from
	// the chips above, because an application with two numbers can hold the
	// same person on both and summing would report them twice.
	if err := r.pool.QueryRow(ctx, `
		select count(*),
		       count(*) filter (where $2::uuid is null or a.application_id = $2::uuid)
		  from public.contacts c
		  join public.whatsapp_accounts a on a.id = c.account_id
		 where c.workspace_id = $1 and `+contactVisible,
		workspaceID, applicationID).Scan(&out.Total, &out.ScopedTotal); err != nil {
		return nil, err
	}
	return out, nil
}
