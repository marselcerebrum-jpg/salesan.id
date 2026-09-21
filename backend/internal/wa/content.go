package wa

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// content is the normalised, storable form of a WhatsApp message payload.
type content struct {
	Type      string
	Body      *string
	Caption   *string
	MediaMime *string
	QuotedID  *string
	// MentionedJIDs is who the message names, taken from WhatsApp's own
	// metadata rather than from scanning the text for "@digits". The text is
	// only what the sender's client chose to render; the metadata is the fact.
	MentionedJIDs []string
	// Skip is true for payloads that carry no user-visible content (protocol
	// messages, revokes, key distribution) and must not become inbox rows.
	Skip bool
}

// contextInfoOf digs out the ContextInfo whichever kind of message carries it.
//
// Every content type has its own copy of the field, so a mention in a photo
// caption lives somewhere different from a mention in a text message. Reading
// only ExtendedTextMessage — the obvious place — would silently miss every
// mention attached to media.
func contextInfoOf(msg *waE2E.Message) *waE2E.ContextInfo {
	if msg == nil {
		return nil
	}
	switch {
	case msg.GetExtendedTextMessage() != nil:
		return msg.GetExtendedTextMessage().GetContextInfo()
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage().GetContextInfo()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage().GetContextInfo()
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage().GetContextInfo()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage().GetContextInfo()
	case msg.GetStickerMessage() != nil:
		return msg.GetStickerMessage().GetContextInfo()
	case msg.GetContactMessage() != nil:
		return msg.GetContactMessage().GetContextInfo()
	case msg.GetLocationMessage() != nil:
		return msg.GetLocationMessage().GetContextInfo()
	case msg.GetPollCreationMessage() != nil:
		return msg.GetPollCreationMessage().GetContextInfo()
	case msg.GetPollCreationMessageV3() != nil:
		return msg.GetPollCreationMessageV3().GetContextInfo()
	}
	return nil
}

// mentionedJIDs normalises the mention list to bare, device-less addresses.
//
// WhatsApp writes them in whatever form the sender's client used — with a
// device suffix, as a LID, as a phone number. Comparing those raw is how a
// mention goes unnoticed, so they are levelled here, once, on the way in.
func mentionedJIDs(msg *waE2E.Message) []string {
	raw := contextInfoOf(msg).GetMentionedJID()
	if len(raw) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, jid := range raw {
		// ParseJID is lenient: a string with no "@" comes back as a user on the
		// default server rather than as an error, so a malformed entry would
		// otherwise become a plausible-looking address that matches nothing.
		if !strings.ContainsRune(jid, '@') {
			continue
		}
		parsed, err := types.ParseJID(jid)
		if err != nil || parsed.User == "" || parsed.Server == "" {
			continue
		}
		normalised := parsed.ToNonAD().String()
		if _, dup := seen[normalised]; dup {
			continue
		}
		seen[normalised] = struct{}{}
		out = append(out, normalised)
	}
	return out
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// sanitizeDisplayName strips invisible Unicode formatting characters and trims
// the result.
//
// WhatsApp prefixes its built-in label names with U+200E (LEFT-TO-RIGHT MARK),
// so "Menunggu pembayaran" arrives with an invisible character in front of it.
// Stored raw, such names render with a phantom leading space, sort oddly, and
// break exact-name matching — which is how labels are deduplicated across
// accounts. Push names and group subjects can carry the same marks.
//
// Code points are written numerically rather than as rune literals: the
// characters are invisible, so literals would be unreadable and unreviewable,
// and a literal U+FEFF is rejected by the Go compiler as a stray byte order
// mark.
func sanitizeDisplayName(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 0x200B && r <= 0x200F, // zero-width space/joiners, LTR & RTL marks
			r >= 0x202A && r <= 0x202E, // bidi embedding and override
			r >= 0x2066 && r <= 0x2069, // bidi isolates
			r == 0xFEFF:                // zero-width no-break space
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(cleaned)
}

// extractContent maps a waE2E.Message onto our message_type enum.
//
// Media bytes are not downloaded in the MVP: the row records the type, caption
// and mime so the inbox can render a placeholder, and media_url stays null.
func extractContent(msg *waE2E.Message) content {
	if msg == nil {
		return content{Skip: true}
	}

	// Protocol traffic (revokes, ephemeral settings, app-state keys) is
	// bookkeeping, not conversation.
	if msg.GetProtocolMessage() != nil ||
		msg.GetSenderKeyDistributionMessage() != nil ||
		msg.GetKeepInChatMessage() != nil {
		return content{Skip: true}
	}

	// A vote changes an existing poll rather than adding to the thread, so it
	// is handled on its own path and must not become a row of its own.
	if msg.GetPollUpdateMessage() != nil {
		return content{Skip: true}
	}

	// The question becomes the body, which is what the conversation preview and
	// search read; the options live in their own table.
	if poll := pollCreation(msg); poll != nil {
		return content{Type: "poll", Body: strPtr(sanitizeDisplayName(poll.GetName()))}
	}

	// Everything below shares one mention list; a plain Conversation string
	// carries no context info and so simply has none.
	mentions := mentionedJIDs(msg)

	if txt := msg.GetConversation(); txt != "" {
		return content{Type: "text", Body: strPtr(txt)}
	}

	if ext := msg.GetExtendedTextMessage(); ext != nil {
		return content{
			Type:          "text",
			Body:          strPtr(ext.GetText()),
			QuotedID:      strPtr(ext.GetContextInfo().GetStanzaID()),
			MentionedJIDs: mentions,
		}
	}

	if img := msg.GetImageMessage(); img != nil {
		return content{
			Type:          "image",
			Caption:       strPtr(img.GetCaption()),
			MediaMime:     strPtr(img.GetMimetype()),
			QuotedID:      strPtr(img.GetContextInfo().GetStanzaID()),
			MentionedJIDs: mentions,
		}
	}

	if vid := msg.GetVideoMessage(); vid != nil {
		return content{
			Type:          "video",
			Caption:       strPtr(vid.GetCaption()),
			MediaMime:     strPtr(vid.GetMimetype()),
			QuotedID:      strPtr(vid.GetContextInfo().GetStanzaID()),
			MentionedJIDs: mentions,
		}
	}

	if aud := msg.GetAudioMessage(); aud != nil {
		return content{
			Type:      "audio",
			MediaMime: strPtr(aud.GetMimetype()),
			QuotedID:  strPtr(aud.GetContextInfo().GetStanzaID()),
		}
	}

	if doc := msg.GetDocumentMessage(); doc != nil {
		return content{
			Type:          "document",
			Body:          strPtr(doc.GetFileName()),
			Caption:       strPtr(doc.GetCaption()),
			MediaMime:     strPtr(doc.GetMimetype()),
			QuotedID:      strPtr(doc.GetContextInfo().GetStanzaID()),
			MentionedJIDs: mentions,
		}
	}

	if st := msg.GetStickerMessage(); st != nil {
		return content{Type: "sticker", MediaMime: strPtr(st.GetMimetype())}
	}

	if loc := msg.GetLocationMessage(); loc != nil {
		label := loc.GetName()
		if label == "" {
			label = "Lokasi dibagikan"
		}
		return content{Type: "location", Body: strPtr(label)}
	}

	if c := msg.GetContactMessage(); c != nil {
		return content{Type: "contact", Body: strPtr(c.GetDisplayName())}
	}
	if c := msg.GetContactsArrayMessage(); c != nil {
		return content{Type: "contact", Body: strPtr(c.GetDisplayName())}
	}

	// A reaction attaches to an existing message and is applied on its own
	// path; it must never become a row in the thread.
	if msg.GetReactionMessage() != nil {
		return content{Skip: true}
	}

	return content{Type: "unsupported", Body: strPtr("[pesan tidak didukung]")}
}

// phoneFromJID returns the bare phone number for a real user JID, or "" for
// group/LID/broadcast addresses where the digits are not a phone number.
func phoneFromJID(jid types.JID) string {
	if jid.Server != types.DefaultUserServer && jid.Server != types.LegacyUserServer {
		return ""
	}
	user := jid.User
	if idx := strings.IndexByte(user, ':'); idx >= 0 {
		user = user[:idx]
	}
	return user
}

// contactPhone resolves the phone number behind any address.
//
// phoneFromJID alone answers only for an address that already is a number, and
// returns nothing for a LID. That is why LID contacts were stored with a null
// phone number and could never be recognised as the same person as their
// numbered twin. whatsmeow's own mapping closes the gap, and it is consulted
// only when the cheap answer fails.
func (s *Session) contactPhone(ctx context.Context, jid types.JID) string {
	if pn := phoneFromJID(jid); pn != "" {
		return pn
	}
	full := s.phoneJID(ctx, jid)
	if full == "" {
		return ""
	}
	parsed, err := types.ParseJID(full)
	if err != nil {
		return ""
	}
	return phoneFromJID(parsed)
}

// conversationType maps a chat JID onto our conversation_type enum.
func conversationType(chat types.JID) string {
	switch {
	case chat.Server == types.GroupServer:
		return "group"
	// status@broadcast is where every contact's Status lands, and it is not a
	// chat with anybody. Left as "personal" it produced one thread called
	// "status@broadcast" holding everyone's Status at once, sitting in the inbox
	// between real customers and counting toward their numbers.
	case chat.ToNonAD() == types.StatusBroadcastJID:
		return models.ConversationTypeStatus
	default:
		return "personal"
	}
}

// phoneJID returns the phone-number address for a chat, resolving a LID
// through whatsmeow's own mapping table when needed.
//
// WhatsApp addresses the same person as a number in some places and as a LID in
// others, and which one arrives is not ours to choose. Both forms are kept on
// the conversation so the second one finds the thread the first one created
// rather than starting a duplicate — which is exactly how the same contact
// ended up in the inbox twice.
//
// Returns "" for groups and for a LID nothing is known about yet.
func (s *Session) phoneJID(ctx context.Context, chat types.JID) string {
	switch chat.Server {
	case types.DefaultUserServer, types.LegacyUserServer:
		return chat.ToNonAD().String()
	case types.HiddenUserServer:
		pn, err := s.client.Store.LIDs.GetPNForLID(ctx, chat.ToNonAD())
		if err != nil || pn.IsEmpty() {
			return ""
		}
		return pn.ToNonAD().String()
	}
	return ""
}
