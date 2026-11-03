package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Reading every message in a period, for the chat export.
//
// One read-only transaction at REPEATABLE READ holds everything the file says:
// the summary that sizes the workbook and the rows themselves come from the
// same snapshot, so a message arriving halfway through can neither appear in
// the rows without being counted nor be counted without appearing.
//
// The rows are read in batches from a server-side cursor over one ordered
// statement. One statement means one scan and one sort for the whole period;
// paging by (timestamp, id) instead would rescan and resort the workspace for
// every page, because messages has no index that serves that order. The cursor
// keeps memory flat whatever the size, and the context is checked between
// batches so an abandoned export stops promptly.
//
// Nothing here writes. The export is read-only by construction.

// ChatExportFilter narrows the export. From is inclusive and To exclusive, both
// absolute instants: the handler turns WIB calendar dates into them once, so
// nothing below has to think about timezones.
type ChatExportFilter struct {
	ApplicationID *uuid.UUID
	// AccountIDs empty means every number the caller may see.
	AccountIDs []uuid.UUID
	From, To   time.Time
	// Type is "personal", "group", or empty for both.
	Type string
}

// ChatExportRow is one message with everything a sheet row needs beside it.
type ChatExportRow struct {
	MessageID        uuid.UUID
	WAMessageID      string
	AccountID        uuid.UUID
	ConversationID   uuid.UUID
	Timestamp        time.Time
	CreatedAt        time.Time
	ApplicationName  *string
	AccountName      string
	AccountPhone     *string
	AccountJID       *string
	ConversationType string
	ChatJID          string
	ConversationName *string
	// CounterpartPhone is the other side of a personal chat, whichever way the
	// message went. Null when only a LID is known.
	CounterpartPhone *string
	FromMe           bool
	MessageType      string
	Status           string
	Body, Caption    *string
	Revoked          bool
	// SenderJID, SenderPhone and SenderName describe who wrote an incoming
	// message. Null on outgoing ones, where the writer is our own number.
	SenderJID   *string
	SenderPhone *string
	SenderName  *string
}

// ChatExportSummary describes the rows an export is about to stream, read
// before the first of them from the same snapshot.
type ChatExportSummary struct {
	Rows int
	// First and Last are the earliest and latest message in the rows. Nil
	// when there are none.
	First, Last *time.Time
	// MaxTextBytes bounds the longest message text in the rows, body and
	// caption together, in bytes. A UTF-8 byte is never fewer than one UTF-16
	// unit, so this is an upper bound on what a cell will have to hold.
	MaxTextBytes int
	// Excluded counts the messages that matched every filter but sit in a
	// thread that is neither a personal chat nor a group: channels, bots,
	// broadcast lists, Status, and anything WhatsApp addresses some other way.
	Excluded ExcludedCounts
}

// ExcludedCounts is what the export left out on purpose, by kind.
type ExcludedCounts struct {
	Newsletter, Bot, Broadcast, Status, Other int
}

// Total is every excluded message.
func (e ExcludedCounts) Total() int {
	return e.Newsletter + e.Bot + e.Broadcast + e.Status + e.Other
}

// ErrExportDuplicate means the statement produced one message twice — a join
// that stopped being one-to-one. The export fails rather than ship a file that
// counts a message twice or quietly drops the copy.
var ErrExportDuplicate = errors.New("chat export produced the same message twice")

// chatExportBatch is how many rows one FETCH brings back.
const chatExportBatch = 2000

// What a conversation is, for the export, decided from the address and not
// from conversations.type alone.
//
// The type column cannot answer it on its own: conversationType() in the
// WhatsApp layer files every address that is not a group or Status as
// 'personal', so channels (@newsletter), Meta AI and other bots (@bot), and
// broadcast lists (<id>@broadcast) all arrive typed 'personal'. Reading them
// as customer chats is how a channel's posts ended up in the sheet as
// "Pribadi". The server part of chat_jid is what WhatsApp itself uses to tell
// them apart, so the export reads both and requires them to agree.
//
// A personal chat is a phone number (s.whatsapp.net, or the legacy c.us), a
// LID — how WhatsApp now addresses most people, and still a person — or the
// hosted forms used by businesses on WhatsApp's Cloud API, which are customers
// like any other. Only the type column says 'group' and the server says g.us
// is a group.
const (
	chatJIDServer        = `split_part(c.chat_jid, '@', 2)`
	chatExportIsPersonal = `(c.type = 'personal' and ` + chatJIDServer +
		` in ('s.whatsapp.net', 'c.us', 'lid', 'hosted', 'hosted.lid'))`
	chatExportIsGroup = `(c.type = 'group' and ` + chatJIDServer + ` = 'g.us')`
)

// chatExportSender resolves the author of an incoming message.
//
// The same sources memberPhoneExpr reads for a group participant, in the same
// order, applied to the message's own sender address — because WhatsApp now
// addresses most group participants by LID, and the local part of a LID is not
// a number anybody can dial. When nothing resolves it, the phone stays null and
// the sheet says so, rather than printing fifteen digits that look like one.
//
// The address book side is aggregated across the workspace for the reason
// memberContactJoin gives: the same person saved on two of our phones is one
// person. max() also keeps the join one-to-one, so a message can never come out
// of this twice.
//
// Only incoming group messages are looked up. In a personal chat the author of
// every incoming message is the chat's own contact, already joined once per
// row as cct, and repeating the lookup per message would be the bulk of the
// query's cost on a large export for an answer it already has.
const chatExportSender = `
	  cross join lateral (
	    select coalesce(m.participant_jid, m.sender_jid) as jid
	  ) s
	  left join lateral (
	    select max(nullif(btrim(x.name), ''))          as name,
	           max(nullif(btrim(x.push_name), ''))     as push_name,
	           max(nullif(btrim(x.business_name), '')) as business_name,
	           max(nullif(btrim(x.phone_number), ''))  as phone_number
	      from public.contacts x
	     where x.workspace_id = m.workspace_id
	       and (x.jid = s.jid or x.lid_jid = s.jid)
	  ) sct on not m.from_me and c.type = 'group'
	  left join whatsmeow_lid_map lm
	         on not m.from_me
	        and c.type = 'group'
	        and s.jid like '%@lid'
	        and lm.lid = split_part(s.jid, '@', 1)`

// ExportChatMessages streams every message matching f, oldest first, to each.
// begin is called once beforehand with the summary of what follows; returning
// an error from it stops the export before any row is read.
//
// Scope is applied in the WHERE clause, not afterwards: a number outside the
// caller's applications is never selected at all. A Leader also sees numbers
// with no application, the same rule AccountInScope states.
//
// Messages hidden from this inbox ("hapus untuk saya") stay out, as they do in
// the thread itself. So does every thread that is not a personal chat or a
// group; the summary counts those by kind so the file can say what it left
// out.
//
// Returning an error from each stops the query; that error is returned as is.
func (r *Repo) ExportChatMessages(
	ctx context.Context, sc Scope, f ChatExportFilter,
	begin func(ChatExportSummary) error, each func(ChatExportRow) error,
) error {
	if f.ApplicationID != nil && !sc.CanSeeApplication(*f.ApplicationID) {
		return ErrForbidden
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	// Read-only: there is nothing to commit, and rolling back is the same.
	defer func() { _ = tx.Rollback(context.Background()) }()

	sq, sargs := chatExportSummaryQuery(sc, f)
	var sum ChatExportSummary
	var ex ExcludedCounts
	if err := tx.QueryRow(ctx, sq, sargs...).Scan(
		&sum.Rows, &sum.First, &sum.Last, &sum.MaxTextBytes,
		&ex.Newsletter, &ex.Bot, &ex.Broadcast, &ex.Status, &ex.Other,
	); err != nil {
		return err
	}
	sum.Excluded = ex
	if err := begin(sum); err != nil {
		return err
	}
	if sum.Rows == 0 {
		return nil
	}

	q, args := chatExportQuery(sc, f)
	if _, err := tx.Exec(ctx, "declare chat_export no scroll cursor for "+q, args...); err != nil {
		return err
	}

	var prev uuid.UUID
	fetch := fmt.Sprintf("fetch forward %d from chat_export", chatExportBatch)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, fetch)
		if err != nil {
			return err
		}
		n := 0
		for rows.Next() {
			n++
			var row ChatExportRow
			if err := rows.Scan(
				&row.MessageID, &row.WAMessageID, &row.AccountID, &row.ConversationID,
				&row.Timestamp, &row.CreatedAt,
				&row.ApplicationName, &row.AccountName, &row.AccountPhone, &row.AccountJID,
				&row.ConversationType, &row.ChatJID,
				&row.ConversationName, &row.CounterpartPhone,
				&row.FromMe, &row.MessageType, &row.Status,
				&row.Body, &row.Caption, &row.Revoked,
				&row.SenderJID, &row.SenderPhone, &row.SenderName,
			); err != nil {
				rows.Close()
				return err
			}
			// The order ends in m.id, so a message the joins produced twice
			// comes out twice in a row. Comparing with the previous row is
			// enough to catch it, without holding every id in memory.
			if row.MessageID == prev {
				rows.Close()
				return ErrExportDuplicate
			}
			prev = row.MessageID
			if err := each(row); err != nil {
				rows.Close()
				return err
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if n < chatExportBatch {
			return nil
		}
	}
}

// chatExportWhere is the filter both statements share: workspace, period,
// visibility, scope, application and numbers — everything except what kind of
// conversation the row is in.
func chatExportWhere(sc Scope, f ChatExportFilter) ([]string, []any) {
	args := []any{sc.WorkspaceID, f.From, f.To}
	where := []string{
		"m.workspace_id = $1",
		"m.timestamp >= $2::timestamptz",
		"m.timestamp < $3::timestamptz",
		"m.hidden_at is null",
	}
	if !sc.All {
		args = append(args, sc.ApplicationIDs)
		where = append(where, fmt.Sprintf("a.application_id = any($%d::uuid[])", len(args)))
	}
	if f.ApplicationID != nil {
		args = append(args, *f.ApplicationID)
		where = append(where, fmt.Sprintf("a.application_id = $%d::uuid", len(args)))
	}
	if len(f.AccountIDs) > 0 {
		args = append(args, f.AccountIDs)
		where = append(where, fmt.Sprintf("m.account_id = any($%d::uuid[])", len(args)))
	}
	return where, args
}

// chatExportKind is the predicate for the rows the file holds.
func chatExportKind(f ChatExportFilter) string {
	switch f.Type {
	case "personal":
		return chatExportIsPersonal
	case "group":
		return chatExportIsGroup
	}
	return "(" + chatExportIsPersonal + " or " + chatExportIsGroup + ")"
}

// chatExportSummaryQuery counts what the export will hold and what it leaves
// out, in one pass over the same rows.
//
// The excluded counts cover only threads that are out of scope whatever the
// filter — not groups left out because the reader asked for personal chats.
func chatExportSummaryQuery(sc Scope, f ChatExportFilter) (string, []any) {
	where, args := chatExportWhere(sc, f)
	in := chatExportKind(f)
	out := "not (" + chatExportIsPersonal + " or " + chatExportIsGroup + ")"
	q := `
		select count(*) filter (where ` + in + `)::int,
		       min(m.timestamp) filter (where ` + in + `),
		       max(m.timestamp) filter (where ` + in + `),
		       coalesce(max(coalesce(octet_length(m.body), 0) + coalesce(octet_length(m.caption), 0))
		         filter (where ` + in + `), 0)::int,
		       count(*) filter (where ` + out + ` and ` + chatJIDServer + ` = 'newsletter')::int,
		       count(*) filter (where ` + out + ` and ` + chatJIDServer + ` = 'bot')::int,
		       count(*) filter (where ` + out + ` and ` + chatJIDServer + ` = 'broadcast'
		                          and c.type <> 'status')::int,
		       count(*) filter (where ` + out + ` and c.type = 'status')::int,
		       count(*) filter (where ` + out + ` and c.type <> 'status'
		                          and ` + chatJIDServer + ` not in ('newsletter', 'bot', 'broadcast'))::int
		  from public.messages m
		  join public.conversations c
		    on c.id = m.conversation_id and c.workspace_id = m.workspace_id
		  join public.whatsapp_accounts a
		    on a.id = m.account_id and a.workspace_id = m.workspace_id
		 where ` + strings.Join(where, " and ")
	return q, args
}

// chatExportQuery builds the export statement and its arguments. Kept apart
// from the loop so the statement itself can be inspected and planned on its
// own.
func chatExportQuery(sc Scope, f ChatExportFilter) (string, []any) {
	where, args := chatExportWhere(sc, f)
	where = append(where, chatExportKind(f))

	// The counterpart of a personal chat comes from the chat, not from the
	// message: on an outgoing message the sender is us, and the customer is
	// still the person the row is about. pn_jid is the phone form WhatsApp
	// itself mapped (migration 0013); a chat known only by LID gets null.
	//
	// m.created_at and m.id after the timestamp make the order total. Two
	// messages in the same second are common in history sync, and without a
	// tiebreak they would swap places between one export and the next.
	q := `
		select m.id, m.wa_message_id, m.account_id, m.conversation_id,
		       m.timestamp, m.created_at,
		       app.name, a.name, a.phone_number, a.jid,
		       c.type::text, c.chat_jid,
		       coalesce(nullif(btrim(c.name), ''), cct.name, cct.push_name, cct.business_name),
		       case when c.type = 'personal' then coalesce(
		         nullif(split_part(c.pn_jid, '@', 1), ''),
		         cct.phone_number,
		         case when c.chat_jid like '%@s.whatsapp.net'
		              then split_part(split_part(c.chat_jid, '@', 1), ':', 1) end
		       ) end,
		       m.from_me, m.type::text, m.status::text,
		       m.body, m.caption, m.revoked_at is not null,
		       case when not m.from_me then s.jid end,
		       case when not m.from_me then coalesce(
		         nullif(btrim(m.sender_phone), ''),
		         sct.phone_number,
		         nullif(btrim(lm.pn), ''),
		         case when s.jid like '%@s.whatsapp.net'
		              then split_part(split_part(s.jid, '@', 1), ':', 1) end
		       ) end,
		       case when not m.from_me then coalesce(
		         sct.name, sct.push_name, sct.business_name, nullif(btrim(m.sender_name), '')
		       ) end
		  from public.messages m
		  join public.conversations c
		    on c.id = m.conversation_id and c.workspace_id = m.workspace_id
		  join public.whatsapp_accounts a
		    on a.id = m.account_id and a.workspace_id = m.workspace_id
		  left join public.applications app on app.id = a.application_id
		  left join lateral (
		    select nullif(btrim(x.name), '')          as name,
		           nullif(btrim(x.push_name), '')     as push_name,
		           nullif(btrim(x.business_name), '') as business_name,
		           nullif(btrim(x.phone_number), '')  as phone_number
		      from public.contacts x
		     where x.id = c.contact_id
		  ) cct on true` + chatExportSender + `
		 where ` + strings.Join(where, " and ") + `
		 order by m.timestamp asc, m.created_at asc, m.id asc`
	return q, args
}

// ExportAccountOption is one number the export dialog can offer.
type ExportAccountOption struct {
	ID            uuid.UUID
	ApplicationID *uuid.UUID
	Name          string
}

// AccountsForExport returns the requested numbers that exist in the workspace
// and fall inside the caller's scope.
//
// The handler compares the answer against what was asked for, so an id that
// belongs to another workspace, or to an application the caller does not hold,
// is refused by name instead of quietly contributing no rows — an export that
// silently left out a number would look complete and not be.
func (r *Repo) AccountsForExport(
	ctx context.Context, sc Scope, ids []uuid.UUID,
) ([]ExportAccountOption, error) {
	args := []any{sc.WorkspaceID, ids}
	q := `select id, application_id, name from public.whatsapp_accounts
	       where workspace_id = $1 and id = any($2::uuid[])`
	if !sc.All {
		args = append(args, sc.ApplicationIDs)
		q += " and application_id = any($3::uuid[])"
	}

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ExportAccountOption{}
	for rows.Next() {
		var o ExportAccountOption
		if err := rows.Scan(&o.ID, &o.ApplicationID, &o.Name); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
