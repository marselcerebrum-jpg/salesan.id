package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// maxReactionNames bounds how many people are named per emoji. A tooltip
// listing two hundred names is not more informative than one listing five.
const maxReactionNames = 8

// ApplyReaction records one person's reaction to a message, replacing whatever
// they had before.
//
// An empty emoji removes it — that is how WhatsApp expresses "un-react", and
// mirroring it here means the same call handles both without the caller having
// to decide which operation this is.
//
// `at` guards against an older reaction arriving after a newer one, which
// happens on reconnect. Returns whether anything actually changed.
func (r *Repo) ApplyReaction(
	ctx context.Context,
	messageID uuid.UUID,
	reactorJID, emoji string,
	fromMe bool,
	at time.Time,
) (bool, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}

	if emoji == "" {
		tag, err := r.pool.Exec(ctx, `
			delete from public.message_reactions
			 where message_id = $1 and reactor_jid = $2 and reacted_at <= $3`,
			messageID, reactorJID, at)
		if err != nil {
			return false, err
		}
		return tag.RowsAffected() > 0, nil
	}

	tag, err := r.pool.Exec(ctx, `
		insert into public.message_reactions (message_id, reactor_jid, emoji, from_me, reacted_at)
		values ($1, $2, $3, $4, $5)
		on conflict (message_id, reactor_jid) do update
		   set emoji      = excluded.emoji,
		       from_me    = excluded.from_me,
		       reacted_at = excluded.reacted_at
		 where public.message_reactions.reacted_at < excluded.reacted_at`,
		messageID, reactorJID, emoji, fromMe, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ReactionsForMessages loads reactions for a page of messages, grouped by emoji.
//
// One query for the whole page. Names are resolved through the contact book the
// same way sender names are, so a reaction and the bubble above it never
// disagree about what someone is called.
func (r *Repo) ReactionsForMessages(
	ctx context.Context,
	messageIDs []uuid.UUID,
) (map[uuid.UUID][]models.Reaction, error) {
	out := map[uuid.UUID][]models.Reaction{}
	if len(messageIDs) == 0 {
		return out, nil
	}

	rows, err := r.pool.Query(ctx, `
		select rc.message_id,
		       rc.emoji,
		       count(*)                         as total,
		       bool_or(rc.from_me)              as mine,
		       (array_agg(
		          coalesce(
		            nullif(btrim(ct.name), ''),
		            nullif(btrim(ct.push_name), ''),
		            nullif(ct.phone_number, ''),
		            split_part(rc.reactor_jid, '@', 1)
		          )
		          order by rc.reacted_at
		        ))[1:$2]                        as names
		  from public.message_reactions rc
		  join public.messages m on m.id = rc.message_id
		  left join public.contacts ct
		         on ct.account_id = m.account_id and ct.jid = rc.reactor_jid
		 where rc.message_id = any($1)
		 group by rc.message_id, rc.emoji
		 order by rc.message_id, total desc, rc.emoji`, messageIDs, maxReactionNames)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var messageID uuid.UUID
		var re models.Reaction
		if err := rows.Scan(&messageID, &re.Emoji, &re.Count, &re.Mine, &re.Names); err != nil {
			return nil, err
		}
		if re.Names == nil {
			re.Names = []string{}
		}
		out[messageID] = append(out[messageID], re)
	}
	return out, rows.Err()
}

// ReactionTargetByWAID resolves the message a reaction points at.
func (r *Repo) ReactionTargetByWAID(ctx context.Context, accountID uuid.UUID, waMessageID string) (uuid.UUID, uuid.UUID, error) {
	var messageID, conversationID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select id, conversation_id from public.messages
		 where account_id = $1 and wa_message_id = $2`, accountID, waMessageID).
		Scan(&messageID, &conversationID)
	if err != nil {
		return uuid.Nil, uuid.Nil, mapErr(err)
	}
	return messageID, conversationID, nil
}
