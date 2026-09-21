package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// These exercise routeMessage itself — the function handleMessage actually
// calls — rather than a copy of its logic, so the two cannot drift apart.
//
// The regression this exists for: ProtocolMessage_REVOKE is 0, the zero value
// of the enum. Without a nil check, `GetProtocolMessage().GetType()` on an
// ordinary message reads as REVOKE, and every incoming message is routed to
// the deletion handler — where it disappears silently, with no error to notice.
//
// This broke all incoming messages once. It must not do so again.
func TestOrdinaryMessagesAreStoredNotTreatedAsRevokes(t *testing.T) {
	ordinary := map[string]*waE2E.Message{
		"plain text": {Conversation: proto.String("halo")},
		"extended text": {ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String("halo"),
		}},
		"image":    {ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}},
		"video":    {VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4")}},
		"audio":    {AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg")}},
		"document": {DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("a.pdf")}},
		"sticker":  {StickerMessage: &waE2E.StickerMessage{}},
		"location": {LocationMessage: &waE2E.LocationMessage{}},
		"contact":  {ContactMessage: &waE2E.ContactMessage{}},
		"poll":     {PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("q")}},
		"empty":    {},
	}

	for label, msg := range ordinary {
		if got := routeMessage(msg, false); got != routeStore {
			t.Errorf("%s routed to %q, want %q — an ordinary message must never be "+
				"mistaken for a deletion", label, got, routeStore)
		}
	}
}

func TestRealRevokeIsRouted(t *testing.T) {
	msg := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_REVOKE.Enum(),
		Key:  &waCommon.MessageKey{ID: proto.String("ABC123")},
	}}
	if got := routeMessage(msg, false); got != routeRevoke {
		t.Fatalf("revoke routed to %q, want %q", got, routeRevoke)
	}
}

func TestOtherProtocolMessagesAreNotRevokes(t *testing.T) {
	// A history-sync notification is a protocol message too, and routing it to
	// the deletion handler would be the same class of mistake.
	msg := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum(),
	}}
	if got := routeMessage(msg, false); got == routeRevoke {
		t.Fatal("a history-sync notification must not be treated as a deletion")
	}
}

func TestReactionIsRoutedToItsOwnHandler(t *testing.T) {
	msg := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
		Text: proto.String("👍"),
		Key:  &waCommon.MessageKey{ID: proto.String("ABC123")},
	}}
	if got := routeMessage(msg, false); got != routeReaction {
		t.Fatalf("reaction routed to %q, want %q — it attaches to a message, "+
			"it does not join the thread", got, routeReaction)
	}
}

func TestEditAndPollUpdateTakePrecedence(t *testing.T) {
	edit := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
	}}
	if got := routeMessage(edit, true); got != routeEdit {
		t.Errorf("edit routed to %q, want %q", got, routeEdit)
	}

	vote := &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{}}
	if got := routeMessage(vote, false); got != routePollUpdate {
		t.Errorf("poll vote routed to %q, want %q", got, routePollUpdate)
	}
}
