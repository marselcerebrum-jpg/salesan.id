package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// Mentions live in a different place for every kind of message. Reading only
// ExtendedTextMessage — the obvious one — silently misses every mention
// attached to a photo, a video or a document.
func TestMentionedJIDsFoundOnEveryCarrier(t *testing.T) {
	const target = "6285171593270@s.whatsapp.net"
	ctxInfo := func() *waE2E.ContextInfo {
		return &waE2E.ContextInfo{MentionedJID: []string{target}}
	}

	carriers := map[string]*waE2E.Message{
		"text":     {ExtendedTextMessage: &waE2E.ExtendedTextMessage{ContextInfo: ctxInfo()}},
		"image":    {ImageMessage: &waE2E.ImageMessage{ContextInfo: ctxInfo()}},
		"video":    {VideoMessage: &waE2E.VideoMessage{ContextInfo: ctxInfo()}},
		"audio":    {AudioMessage: &waE2E.AudioMessage{ContextInfo: ctxInfo()}},
		"document": {DocumentMessage: &waE2E.DocumentMessage{ContextInfo: ctxInfo()}},
		"sticker":  {StickerMessage: &waE2E.StickerMessage{ContextInfo: ctxInfo()}},
		"poll":     {PollCreationMessage: &waE2E.PollCreationMessage{ContextInfo: ctxInfo()}},
	}

	for label, msg := range carriers {
		got := mentionedJIDs(msg)
		if len(got) != 1 || got[0] != target {
			t.Errorf("%s: mentionedJIDs = %v, want [%s]", label, got, target)
		}
	}
}

// WhatsApp writes the mention in whatever form the sender's client used. If the
// device suffix is not stripped, the comparison against our own address never
// matches and the mention goes unnoticed.
func TestMentionedJIDsStripsDeviceSuffix(t *testing.T) {
	msg := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{
			"6285171593270:20@s.whatsapp.net",
			"86007441547495:3@lid",
		}},
	}}

	got := mentionedJIDs(msg)
	want := []string{"6285171593270@s.whatsapp.net", "86007441547495@lid"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// The same person named twice — once by number, once with a device suffix —
// must count once, or the mention list grows on every re-delivery.
func TestMentionedJIDsDeduplicates(t *testing.T) {
	msg := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{
			"6285171593270@s.whatsapp.net",
			"6285171593270:20@s.whatsapp.net",
			"6285171593270@s.whatsapp.net",
		}},
	}}
	if got := mentionedJIDs(msg); len(got) != 1 {
		t.Fatalf("mentionedJIDs = %v, want one entry", got)
	}
}

func TestMentionedJIDsIgnoresMalformed(t *testing.T) {
	msg := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{
			"", "tidak-valid", "6285171593270@s.whatsapp.net",
		}},
	}}
	got := mentionedJIDs(msg)
	if len(got) != 1 || got[0] != "6285171593270@s.whatsapp.net" {
		t.Fatalf("mentionedJIDs = %v, want only the valid one", got)
	}
}

func TestMentionedJIDsEmptyWithoutContext(t *testing.T) {
	// A plain Conversation string carries no context info at all.
	if got := mentionedJIDs(&waE2E.Message{Conversation: proto.String("halo")}); got != nil {
		t.Fatalf("mentionedJIDs = %v, want nil", got)
	}
	if got := mentionedJIDs(nil); got != nil {
		t.Fatalf("mentionedJIDs(nil) = %v, want nil", got)
	}
}

// A mention detected on a media caption has to survive into the stored row,
// not just be found by the extractor.
func TestExtractContentCarriesMentions(t *testing.T) {
	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption:     proto.String("cek ini @6285171593270"),
		Mimetype:    proto.String("image/jpeg"),
		ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{"6285171593270@s.whatsapp.net"}},
	}}

	got := extractContent(msg)
	if got.Type != "image" {
		t.Fatalf("Type = %q, want image", got.Type)
	}
	if len(got.MentionedJIDs) != 1 {
		t.Fatalf("MentionedJIDs = %v, want one entry", got.MentionedJIDs)
	}
}
