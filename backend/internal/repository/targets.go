package repository

import (
	"context"

	"github.com/google/uuid"
)

// Candidate recipients for a Broadcast.
//
// Every one of these is scoped to a set of WhatsApp accounts rather than to a
// workspace, and that is not a detail: contacts, groups and group membership all
// belong to a *device*. The same customer may be saved on one number and unknown
// on another, and a group one number is in is unreachable from the next. Picking
// recipients workspace-wide would produce a list half of which no selected
// device can actually address.

// TargetCandidate is one possible recipient, already tied to the device that can
// reach it.
type TargetCandidate struct {
	AccountID      uuid.UUID
	ChatJID        string
	PhoneNumber    string
	Name           string
	ContactID      *uuid.UUID
	ConversationID *uuid.UUID
	// Kind is "personal" or "group".
	Kind string
	// AdminsOnly is set on a group where only admins may post and the device
	// is not one. The device is a member, so the group is listed, but a send
	// would be refused by WhatsApp; the resolver reports it instead of trying.
	AdminsOnly bool
	// Community is set when the row is a community itself rather than one of
	// its groups. Not a chat; nothing can be sent to it.
	Community bool
}

// AccountInfo is a sending device with what the review screen needs to name it.
type AccountInfo struct {
	ID            uuid.UUID
	Name          string
	PhoneNumber   *string
	ApplicationID *uuid.UUID
	Status        string
}

// AccountsByIDs loads the given accounts, restricted to the caller's scope.
//
// The scope check happens here rather than in the handler so that no path can
// build a campaign that sends from a number the caller may not use.
func (r *Repo) AccountsByIDs(ctx context.Context, sc Scope, ids []uuid.UUID) ([]AccountInfo, error) {
	q := &queryArgs{}
	where := " where a.workspace_id = " + q.add(sc.WorkspaceID) +
		" and a.id = any(" + q.add(ids) + ")"
	if !sc.All {
		where += " and a.application_id = any(" + q.add(sc.ApplicationIDs) + ")"
	}

	rows, err := r.pool.Query(ctx,
		`select a.id, a.name, a.phone_number, a.application_id, a.status::text
		   from public.whatsapp_accounts a`+where+` order by a.name`, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AccountInfo{}
	for rows.Next() {
		var a AccountInfo
		if err := rows.Scan(&a.ID, &a.Name, &a.PhoneNumber, &a.ApplicationID, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ApplicationCode returns an application's short code, for the {{aplikasi}}
// placeholder.
func (r *Repo) ApplicationCode(ctx context.Context, id uuid.UUID) (string, error) {
	var code string
	err := r.pool.QueryRow(ctx,
		`select code from public.applications where id = $1`, id).Scan(&code)
	if err != nil {
		return "", mapErr(err)
	}
	return code, nil
}

// ContactTargets lists saved contacts reachable from the given devices.
func (r *Repo) ContactTargets(
	ctx context.Context, workspaceID uuid.UUID, accountIDs []uuid.UUID, limit int,
) ([]TargetCandidate, error) {
	if limit <= 0 || limit > 20000 {
		limit = 5000
	}
	rows, err := r.pool.Query(ctx, `
		select ct.account_id, ct.jid, coalesce(ct.phone_number, ''),
		       coalesce(nullif(btrim(ct.name), ''), nullif(btrim(ct.push_name), ''),
		                coalesce(ct.phone_number, split_part(ct.jid, '@', 1))),
		       ct.id, c.id
		  from public.contacts ct
		  left join public.conversations c
		    on c.account_id = ct.account_id and c.contact_id = ct.id and c.type = 'personal'
		 where ct.workspace_id = $1 and ct.account_id = any($2)
		   and ct.jid like '%@s.whatsapp.net'
		 order by 4
		 limit $3`, workspaceID, accountIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCandidates(rows, "personal")
}

// GroupTargets lists groups the given devices belong to.
//
// Archived groups are included. "Diarsipkan" is how the phone tidies its chat
// list, mirrored here from app state; it says nothing about whether the number
// can post there. This used to exclude them, and on a phone whose owner had
// archived every class group the recipient picker offered thirty-one groups of
// which the resolver kept four, so a broadcast to the archived ones was written
// with no recipients and failed a second after it started, with no reason. What
// does decide reachability is membership, so that is the only filter left.
//
// A group in "only admins can send" mode where the device is not an admin is
// still returned, flagged AdminsOnly, so the resolver can say why it was not
// used rather than leaving the operator to find out from a failed send.
func (r *Repo) GroupTargets(
	ctx context.Context, workspaceID uuid.UUID, accountIDs []uuid.UUID,
) ([]TargetCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		select c.account_id, c.chat_jid, '',
		       coalesce(nullif(btrim(c.name), ''), split_part(c.chat_jid, '@', 1)),
		       null::uuid, c.id,
		       (c.group_announce and not c.self_is_admin), c.group_is_community
		  from public.conversations c
		 where c.workspace_id = $1 and c.account_id = any($2)
		   and c.type = 'group' and c.group_is_member is not false
		 order by 4`, workspaceID, accountIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TargetCandidate{}
	for rows.Next() {
		c := TargetCandidate{Kind: "group"}
		if err := rows.Scan(&c.AccountID, &c.ChatJID, &c.PhoneNumber, &c.Name,
			&c.ContactID, &c.ConversationID, &c.AdminsOnly, &c.Community); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GroupMemberTargets lists the individual members of the given groups.
//
// The result addresses each member in their own one-to-one chat, which is what
// "anggota grup, tetapi pesan dikirim ke chat pribadi" means. Members whose JID
// is not an ordinary phone-number address are dropped: a LID-only participant
// cannot be messaged privately by a device that has never spoken to them.
func (r *Repo) GroupMemberTargets(
	ctx context.Context, workspaceID uuid.UUID, conversationIDs []uuid.UUID,
) ([]TargetCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		select c.account_id, mem.jid,
		       coalesce(`+memberPhoneExpr+`, split_part(mem.jid, '@', 1)),
		       coalesce(`+memberNameExpr+`, `+memberPhoneExpr+`,
		                split_part(mem.jid, '@', 1)),
		       cid.id, pc.id
		  from public.conversation_members mem
		  join public.conversations c on c.id = mem.conversation_id`+
		memberContactJoin+`
		  left join lateral (
		    select x.id from public.contacts x
		     where x.workspace_id = c.workspace_id
		       and (x.jid = mem.jid or x.lid_jid = mem.jid)
		     order by (x.account_id = c.account_id) desc
		     limit 1
		  ) cid on true
		  left join public.conversations pc
		    on pc.account_id = c.account_id and pc.chat_jid = mem.jid and pc.type = 'personal'
		 where c.workspace_id = $1 and mem.conversation_id = any($2)
		   and mem.jid like '%@s.whatsapp.net'
		 order by 4`, workspaceID, conversationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCandidates(rows, "group_member")
}

// ConversationIsArchived reports whether a thread sits in the archive folder.
func (r *Repo) ConversationIsArchived(ctx context.Context, id uuid.UUID) (bool, error) {
	var archived bool
	err := r.pool.QueryRow(ctx,
		`select is_archived from public.conversations where id = $1`, id).Scan(&archived)
	if err != nil {
		return false, mapErr(err)
	}
	return archived, nil
}

// SetConversationArchived records what WhatsApp has been told.
//
// Written after the app state patch is accepted, not before: WhatsApp is the
// authority on where a chat lives, and app state sync would overwrite a guess
// made here anyway.
func (r *Repo) SetConversationArchived(ctx context.Context, id uuid.UUID, archived bool) error {
	_, err := r.pool.Exec(ctx,
		`update public.conversations set is_archived = $2, updated_at = now()
		  where id = $1 and is_archived is distinct from $2`, id, archived)
	return err
}

// LookupChats resolves raw JIDs against what each device already knows, so a
// pasted number that is already a saved contact keeps its name and its thread.
//
// Matched on either of a person's addresses. A thread is keyed by whichever
// form WhatsApp used first, and for anyone who first turned up through a group
// that is the LID, with the number in pn_jid. Matching the number against
// chat_jid alone missed those, the campaign then wrote its message into a
// fresh thread keyed by the number, and the inbox showed the same person twice:
// "6281395967940" holding the broadcast, "Thaariq" holding the conversation.
// The candidate keeps the address that was asked for, so the caller's map still
// finds it, and carries the existing thread's id, which is what stops the
// second thread from being created.
func (r *Repo) LookupChats(
	ctx context.Context, workspaceID uuid.UUID, accountIDs []uuid.UUID, jids []string,
) (map[string]TargetCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		select c.account_id,
		       case when c.chat_jid = any($3) then c.chat_jid else c.pn_jid end as asked,
		       coalesce(ct.phone_number, ''),
		       coalesce(nullif(btrim(ct.name), ''), nullif(btrim(ct.push_name), ''),
		                nullif(btrim(c.name), ''),
		                split_part(case when c.chat_jid = any($3) then c.chat_jid else c.pn_jid end, '@', 1)),
		       ct.id, c.id
		  from public.conversations c
		  left join public.contacts ct on ct.id = c.contact_id
		 where c.workspace_id = $1 and c.account_id = any($2)
		   and (c.chat_jid = any($3) or c.pn_jid = any($3))`,
		workspaceID, accountIDs, jids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	found, err := scanCandidates(rows, "manual")
	if err != nil {
		return nil, err
	}
	out := map[string]TargetCandidate{}
	for _, c := range found {
		// First device that knows the chat wins; the distributor may still move
		// the recipient elsewhere, and any connected device can reach a plain
		// phone number.
		if _, seen := out[c.ChatJID]; !seen {
			out[c.ChatJID] = c
		}
	}
	return out, nil
}

func scanCandidates(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}, kind string) ([]TargetCandidate, error) {
	out := []TargetCandidate{}
	for rows.Next() {
		var c TargetCandidate
		if err := rows.Scan(&c.AccountID, &c.ChatJID, &c.PhoneNumber, &c.Name,
			&c.ContactID, &c.ConversationID); err != nil {
			return nil, err
		}
		c.Kind = kind
		out = append(out, c)
	}
	return out, rows.Err()
}
