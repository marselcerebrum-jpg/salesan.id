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
func (r *Repo) GroupTargets(
	ctx context.Context, workspaceID uuid.UUID, accountIDs []uuid.UUID,
) ([]TargetCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		select c.account_id, c.chat_jid, '',
		       coalesce(nullif(btrim(c.name), ''), split_part(c.chat_jid, '@', 1)),
		       null::uuid, c.id
		  from public.conversations c
		 where c.workspace_id = $1 and c.account_id = any($2)
		   and c.type = 'group' and c.is_archived = false
		 order by 4`, workspaceID, accountIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCandidates(rows, "group")
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

// LookupChats resolves raw JIDs against what each device already knows, so a
// pasted number that is already a saved contact keeps its name and its thread.
func (r *Repo) LookupChats(
	ctx context.Context, workspaceID uuid.UUID, accountIDs []uuid.UUID, jids []string,
) (map[string]TargetCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		select c.account_id, c.chat_jid, coalesce(ct.phone_number, ''),
		       coalesce(nullif(btrim(ct.name), ''), nullif(btrim(ct.push_name), ''),
		                nullif(btrim(c.name), ''), split_part(c.chat_jid, '@', 1)),
		       ct.id, c.id
		  from public.conversations c
		  left join public.contacts ct on ct.id = c.contact_id
		 where c.workspace_id = $1 and c.account_id = any($2) and c.chat_jid = any($3)`,
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
