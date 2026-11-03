package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Reading every message in a period, for the chat export.
//
// One statement, streamed row by row to the caller, rather than pages. A page
// boundary on (timestamp, id) would need the same ordering repeated in every
// page query and gives nothing back: pgx already hands rows over as they come
// off the socket, so memory stays flat whether the period holds a hundred
// messages or a million. One statement also means one snapshot — a message
// arriving halfway through cannot appear in one page and shift the next.
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
	Timestamp        time.Time
	ApplicationName  *string
	AccountName      string
	AccountPhone     *string
	ConversationType string
	ChatJID          string
	ConversationName *string
	// CounterpartPhone is the other side of a personal chat, whichever way the
	// message went. Null when only a LID is known.
	CounterpartPhone *string
	FromMe           bool
	MessageType      string
	Body, Caption    *string
	Revoked          bool
	// SenderJID, SenderPhone and SenderName describe who wrote an incoming
	// message. Null on outgoing ones, where the writer is our own number.
	SenderJID   *string
	SenderPhone *string
	SenderName  *string
}

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
//
// Scope is applied in the WHERE clause, not afterwards: a number outside the
// caller's applications is never selected at all. A Leader also sees numbers
// with no application, the same rule AccountInScope states.
//
// Messages hidden from this inbox ("hapus untuk saya") stay out, as they do in
// the thread itself. Status posts stay out too: they are conversation type
// 'status', and neither a personal chat nor a group.
//
// Returning an error from each stops the query; that error is returned as is.
func (r *Repo) ExportChatMessages(
	ctx context.Context, sc Scope, f ChatExportFilter, each func(ChatExportRow) error,
) error {
	if f.ApplicationID != nil && !sc.CanSeeApplication(*f.ApplicationID) {
		return ErrForbidden
	}

	q, args := chatExportQuery(sc, f)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var row ChatExportRow
		if err := rows.Scan(
			&row.MessageID, &row.Timestamp,
			&row.ApplicationName, &row.AccountName, &row.AccountPhone,
			&row.ConversationType, &row.ChatJID,
			&row.ConversationName, &row.CounterpartPhone,
			&row.FromMe, &row.MessageType, &row.Body, &row.Caption, &row.Revoked,
			&row.SenderJID, &row.SenderPhone, &row.SenderName,
		); err != nil {
			return err
		}
		if err := each(row); err != nil {
			return err
		}
	}
	return rows.Err()
}

// chatExportQuery builds the export statement and its arguments. Kept apart
// from the loop so the statement itself can be inspected and planned on its
// own.
func chatExportQuery(sc Scope, f ChatExportFilter) (string, []any) {
	args := []any{sc.WorkspaceID, f.From, f.To}
	where := []string{
		"m.workspace_id = $1",
		"m.timestamp >= $2::timestamptz",
		"m.timestamp < $3::timestamptz",
		"m.hidden_at is null",
	}

	if f.Type == "personal" || f.Type == "group" {
		args = append(args, f.Type)
		where = append(where, fmt.Sprintf("c.type = $%d::public.conversation_type", len(args)))
	} else {
		where = append(where, "c.type in ('personal', 'group')")
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

	// The counterpart of a personal chat comes from the chat, not from the
	// message: on an outgoing message the sender is us, and the customer is
	// still the person the row is about. pn_jid is the phone form WhatsApp
	// itself mapped (migration 0013); a chat known only by LID gets null.
	//
	// m.created_at and m.id after the timestamp make the order total. Two
	// messages in the same second are common in history sync, and without a
	// tiebreak they would swap places between one export and the next.
	q := `
		select m.id, m.timestamp,
		       app.name, a.name, a.phone_number,
		       c.type::text, c.chat_jid,
		       coalesce(nullif(btrim(c.name), ''), cct.name, cct.push_name, cct.business_name),
		       case when c.type = 'personal' then coalesce(
		         nullif(split_part(c.pn_jid, '@', 1), ''),
		         cct.phone_number,
		         case when c.chat_jid like '%@s.whatsapp.net'
		              then split_part(c.chat_jid, '@', 1) end
		       ) end,
		       m.from_me, m.type::text, m.body, m.caption, m.revoked_at is not null,
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
