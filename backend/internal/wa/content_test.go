package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Invisible formatting marks, built from code points so this file stays
// readable and reviewable — pasting the characters themselves would leave
// literals nobody can see, and a raw U+FEFF is rejected by the compiler.
var (
	ltrMark      = string(rune(0x200E))
	rtlMark      = string(rune(0x200F))
	zeroWidth    = string(rune(0x200B))
	zeroWidthJn  = string(rune(0x200D))
	bidiIsolate  = string(rune(0x2066))
	bidiPopIso   = string(rune(0x2069))
	bidiOverride = string(rune(0x202E))
	byteOrderMk  = string(rune(0xFEFF))
)

func TestSanitizeDisplayName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// The real case: WhatsApp's built-in labels arrive with a
			// LEFT-TO-RIGHT MARK glued to the front.
			name: "strips a leading LTR mark",
			in:   ltrMark + "Menunggu pembayaran",
			want: "Menunggu pembayaran",
		},
		{
			name: "strips zero-width characters anywhere",
			in:   "Pelanggan" + zeroWidth + " " + zeroWidthJn + "baru",
			want: "Pelanggan baru",
		},
		{
			name: "strips bidi isolates and overrides",
			in:   bidiIsolate + "Lunas" + bidiPopIso + bidiOverride,
			want: "Lunas",
		},
		{
			name: "strips a byte order mark",
			in:   byteOrderMk + "Penting",
			want: "Penting",
		},
		{
			name: "trims surrounding whitespace",
			in:   "  Favorit\t",
			want: "Favorit",
		},
		{
			name: "leaves an ordinary name untouched",
			in:   "dibatalkan",
			want: "dibatalkan",
		},
		{
			name: "keeps non-latin scripts intact",
			in:   ltrMark + "プレミアム",
			want: "プレミアム",
		},
		{
			name: "a name made only of marks collapses to empty",
			in:   ltrMark + rtlMark + byteOrderMk,
			want: "",
		},
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeDisplayName(tt.in); got != tt.want {
				t.Errorf("sanitizeDisplayName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExtractContent(t *testing.T) {
	tests := []struct {
		name        string
		msg         *waE2E.Message
		wantType    string
		wantBody    string
		wantCaption string
		wantQuoted  string
		wantSkip    bool
	}{
		{
			name:     "nil message is skipped",
			msg:      nil,
			wantSkip: true,
		},
		{
			name:     "plain conversation",
			msg:      &waE2E.Message{Conversation: proto.String("halo kak")},
			wantType: "text",
			wantBody: "halo kak",
		},
		{
			name: "extended text carries the quoted stanza id",
			msg: &waE2E.Message{
				ExtendedTextMessage: &waE2E.ExtendedTextMessage{
					Text:        proto.String("balasan"),
					ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("WAMID-123")},
				},
			},
			wantType:   "text",
			wantBody:   "balasan",
			wantQuoted: "WAMID-123",
		},
		{
			name: "image keeps caption and mime",
			msg: &waE2E.Message{
				ImageMessage: &waE2E.ImageMessage{
					Caption:  proto.String("brosur"),
					Mimetype: proto.String("image/jpeg"),
				},
			},
			wantType:    "image",
			wantCaption: "brosur",
		},
		{
			name: "document falls back to the file name",
			msg: &waE2E.Message{
				DocumentMessage: &waE2E.DocumentMessage{
					FileName: proto.String("materi.pdf"),
					Mimetype: proto.String("application/pdf"),
				},
			},
			wantType: "document",
			wantBody: "materi.pdf",
		},
		{
			// A reaction attaches to an existing message and is applied on its
			// own path. Letting it become a row turns every thumbs-up in a busy
			// group into a bubble of its own, which is not what WhatsApp shows
			// and not what anyone reading the thread expects.
			name: "reaction never becomes an inbox row",
			msg: &waE2E.Message{
				ReactionMessage: &waE2E.ReactionMessage{
					Text: proto.String("👍"),
					Key:  &waCommon.MessageKey{ID: proto.String("WAMID-999")},
				},
			},
			wantSkip: true,
		},
		{
			name: "protocol messages never become inbox rows",
			msg: &waE2E.Message{
				ProtocolMessage: &waE2E.ProtocolMessage{
					Type: waE2E.ProtocolMessage_REVOKE.Enum(),
				},
			},
			wantSkip: true,
		},
		{
			// Unknown payloads still become a row so the thread stays complete;
			// the placeholder body is what the inbox renders.
			name:     "unknown payload is recorded as unsupported",
			msg:      &waE2E.Message{},
			wantType: "unsupported",
			wantBody: "[pesan tidak didukung]",
		},
		{
			// The question becomes the body so the conversation preview and
			// search have something to work with; the options live elsewhere.
			name: "poll keeps its question as the body",
			msg: &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{
				Name: proto.String("Jam berapa?"),
				Options: []*waE2E.PollCreationMessage_Option{
					{OptionName: proto.String("Pagi")},
					{OptionName: proto.String("Malam")},
				},
			}},
			wantType: "poll",
			wantBody: "Jam berapa?",
		},
		{
			// Newer clients send V3; the payload is otherwise identical.
			name: "poll v3 is recognised",
			msg: &waE2E.Message{PollCreationMessageV3: &waE2E.PollCreationMessage{
				Name: proto.String("Setuju?"),
			}},
			wantType: "poll",
			wantBody: "Setuju?",
		},
		{
			// A vote changes an existing poll rather than adding to the thread,
			// so it must never become a row of its own.
			name:     "poll vote is skipped",
			msg:      &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{}},
			wantSkip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractContent(tt.msg)

			if got.Skip != tt.wantSkip {
				t.Fatalf("Skip = %v, want %v", got.Skip, tt.wantSkip)
			}
			if tt.wantSkip {
				return
			}
			if got.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tt.wantType)
			}
			if deref(got.Body) != tt.wantBody {
				t.Errorf("Body = %q, want %q", deref(got.Body), tt.wantBody)
			}
			if deref(got.Caption) != tt.wantCaption {
				t.Errorf("Caption = %q, want %q", deref(got.Caption), tt.wantCaption)
			}
			if deref(got.QuotedID) != tt.wantQuoted {
				t.Errorf("QuotedID = %q, want %q", deref(got.QuotedID), tt.wantQuoted)
			}
		})
	}
}

func TestPhoneFromJID(t *testing.T) {
	tests := []struct {
		name string
		jid  types.JID
		want string
	}{
		{"user jid yields the number", types.NewJID("6289612715604", types.DefaultUserServer), "6289612715604"},
		{"group jid has no phone number", types.NewJID("120363000000000018", types.GroupServer), ""},
		{"lid jid is not a phone number", types.NewJID("55512345", types.HiddenUserServer), ""},
		{"device suffix is stripped", types.JID{User: "6289612715604:12", Server: types.DefaultUserServer}, "6289612715604"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := phoneFromJID(tt.jid); got != tt.want {
				t.Errorf("phoneFromJID(%s) = %q, want %q", tt.jid, got, tt.want)
			}
		})
	}
}

func TestConversationType(t *testing.T) {
	if got := conversationType(types.NewJID("1203630000", types.GroupServer)); got != "group" {
		t.Errorf("group chat classified as %q", got)
	}
	if got := conversationType(types.NewJID("628123", types.DefaultUserServer)); got != "personal" {
		t.Errorf("direct chat classified as %q", got)
	}
}

func TestStrPtr(t *testing.T) {
	if strPtr("") != nil {
		t.Error("empty string should map to nil so NULL reaches the database")
	}
	if got := strPtr("x"); got == nil || *got != "x" {
		t.Error("non-empty string should round-trip")
	}
}
