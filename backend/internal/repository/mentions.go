package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// RefreshMentionCount recomputes a conversation's unseen-mention badge from its
// messages.
//
// Always recomputed, never incremented. A counter nudged from three different
// places — a message arriving, a mention being seen, a message being deleted —
// is a counter that drifts, and the whole point of this number is that the
// operator can trust it.
func (r *Repo) RefreshMentionCount(ctx context.Context, conversationID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `select public.refresh_mention_count($1)`, conversationID)
	return err
}

// MarkMentionsSeen stamps every unseen mention in a conversation and returns
// how many it touched.
//
// Called when the operator actually opens the thread. Seeing the badge in the
// list is not the same as having read what was said, so the stamp waits for
// the thread to be opened rather than for the row to be rendered.
func (r *Repo) MarkMentionsSeen(ctx context.Context, workspaceID, conversationID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		update public.messages
		   set mention_seen_at = now()
		 where conversation_id = $1
		   and workspace_id    = $2
		   and mentions_me
		   and mention_seen_at is null`, conversationID, workspaceID)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() > 0 {
		if err := r.RefreshMentionCount(ctx, conversationID); err != nil {
			return tag.RowsAffected(), err
		}
	}
	return tag.RowsAffected(), nil
}

// FirstUnseenMention returns the earliest mention in a thread that has not been
// looked at, so the UI can open the chat at that message rather than at the
// bottom. Nil when there is none.
func (r *Repo) FirstUnseenMention(ctx context.Context, workspaceID, conversationID uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select id from public.messages
		 where conversation_id = $1
		   and workspace_id    = $2
		   and mentions_me
		   and mention_seen_at is null
		   and hidden_at is null
		 order by timestamp asc
		 limit 1`, conversationID, workspaceID).Scan(&id)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &id, nil
}

// ReconcileMentionCounts rebuilds every mention badge for an account.
//
// Run after a reconnect or a restart, for the same reason unread counts are:
// events that arrived while the socket was down cannot be replayed, so the
// counters are rebuilt from what is actually stored rather than trusted.
func (r *Repo) ReconcileMentionCounts(ctx context.Context, accountID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		update public.conversations c
		   set mention_count = sub.n,
		       updated_at    = now()
		  from (
		    select c2.id,
		           count(m.id) filter (
		             where m.mentions_me
		               and m.mention_seen_at is null
		               and m.hidden_at is null
		               and m.revoked_at is null
		           ) as n
		      from public.conversations c2
		      left join public.messages m on m.conversation_id = c2.id
		     where c2.account_id = $1
		     group by c2.id
		  ) sub
		 where c.id = sub.id
		   and c.mention_count is distinct from sub.n`, accountID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GroupMeta is a group's metadata as WhatsApp last reported it.
type GroupMeta struct {
	Name        string
	Description string
	TopicID     string
	OwnerJID    string
	// SelfIsAdmin decides which controls the interface offers. WhatsApp remains
	// the authority; this only avoids asking it once per rendered row.
	SelfIsAdmin bool
	// Announce is WhatsApp's "only admins can send messages" setting, which
	// every community announcement group has. With SelfIsAdmin false it means a
	// broadcast from this number would be refused by the server (error 420).
	Announce bool
	// Community marks a community itself (the parent of its groups). It is
	// listed among joined groups with a @g.us address but is not a chat: a
	// message to it is refused by the server.
	Community bool
}

// SetGroupMeta stores what a group info fetch reported.
//
// Blank values do not overwrite what is already there: a partial read should
// not erase a description WhatsApp simply did not include this time.
func (r *Repo) SetGroupMeta(ctx context.Context, conversationID uuid.UUID, meta GroupMeta) error {
	_, err := r.pool.Exec(ctx, `
		update public.conversations
		   set name              = coalesce(nullif($2, ''), name),
		       group_description = nullif($3, ''),
		       group_topic_id    = coalesce(nullif($4, ''), group_topic_id),
		       group_owner_jid   = coalesce(nullif($5, ''), group_owner_jid),
		       self_is_admin     = $6,
		       group_announce    = $7,
		       group_is_community = $8,
		       updated_at        = now()
		 where id = $1`,
		conversationID, meta.Name, meta.Description, meta.TopicID, meta.OwnerJID,
		meta.SelfIsAdmin, meta.Announce, meta.Community)
	return err
}

// GroupMemberProfile is a participant as shown to the operator.
//
// Distinct from GroupMember, which is what gets written when the member list is
// synced: this one is the read model, carrying the resolved name and the number
// rather than the raw row.
type GroupMemberProfile struct {
	JID         string     `json:"jid"`
	PhoneNumber *string    `json:"phone_number"`
	DisplayName string     `json:"display_name"`
	IsAdmin     bool       `json:"is_admin"`
	LastSeenAt  *time.Time `json:"last_message_at"`
}

// GroupMembers lists a group's participants with their resolved names.
//
// Built from the stored member list, filled out with whatever the contact book
// knows and with the most recent name a message carried — the same resolution
// order used above the bubbles, so the two never disagree.
func (r *Repo) GroupMembers(ctx context.Context, workspaceID, conversationID uuid.UUID) ([]GroupMemberProfile, error) {
	rows, err := r.pool.Query(ctx, `
		select mem.jid,
		       `+memberPhoneExpr+` as phone_number,
		       coalesce(`+memberNameExpr+`, `+memberPhoneExpr+`, '') as display_name,
		       mem.is_admin,
		       (select max(m.timestamp) from public.messages m
		         where m.conversation_id = mem.conversation_id
		           and coalesce(m.participant_jid, m.sender_jid) = mem.jid)
		  from public.conversation_members mem
		  join public.conversations c on c.id = mem.conversation_id`+
		memberContactJoin+`
		 where mem.conversation_id = $1 and c.workspace_id = $2
		 order by mem.is_admin desc, display_name = '', lower(display_name)`,
		conversationID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []GroupMemberProfile{}
	for rows.Next() {
		var m GroupMemberProfile
		if err := rows.Scan(&m.JID, &m.PhoneNumber, &m.DisplayName,
			&m.IsAdmin, &m.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
