package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// The font travels inside the message, so a name this build cannot resolve must
// leave the field out rather than send the enum's zero value. Zero means SYSTEM
// to every viewer, which is a choice the sender never made.
func TestUnknownFontIsLeftOutRatherThanSentAsZero(t *testing.T) {
	msg, err := StoryMessage("halo", nil, 0, "tidak-ada-font-ini")
	if err != nil {
		t.Fatal(err)
	}
	if msg.ExtendedTextMessage.Font != nil {
		t.Errorf("font = %v, want it absent", msg.ExtendedTextMessage.GetFont())
	}
}

func TestChosenFontAndColourReachTheMessage(t *testing.T) {
	const teal = 0xFF075E54
	msg, err := StoryMessage("halo", nil, teal, StoryFontTypewriter)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ExtendedTextMessage.Font == nil {
		t.Fatal("a font that was chosen must be sent")
	}
	if got := msg.ExtendedTextMessage.GetBackgroundArgb(); got != teal {
		t.Errorf("background = %#x, want %#x", got, teal)
	}
}

// No choice at all still produces a sendable Story: WhatsApp's own colour and
// no font field.
func TestNoChoiceFallsBackToTheDefaultColour(t *testing.T) {
	msg, err := StoryMessage("halo", nil, 0, StoryFontDefault)
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.ExtendedTextMessage.GetBackgroundArgb(); got != defaultStoryBackground {
		t.Errorf("background = %#x, want the default %#x", got, defaultStoryBackground)
	}
	if msg.ExtendedTextMessage.Font != nil {
		t.Error("no font was chosen, so none should be sent")
	}
}

// What the composer may offer and what the sender accepts have to agree, or a
// colour picked on screen is refused on submit.
func TestValidationAcceptsEveryFontTheComposerOffers(t *testing.T) {
	if !ValidStoryFont("") {
		t.Error("no choice must be valid; it means the default")
	}
	for _, name := range StoryFontNames() {
		if !ValidStoryFont(name) {
			t.Errorf("%q is offered but refused by validation", name)
		}
	}
	if ValidStoryFont("comic sans") {
		t.Error("a font WhatsApp does not have must be refused")
	}

	for _, argb := range []int64{0, 0xFF075E54, 0xFFFFFFFF} {
		if !ValidStoryBackground(argb) {
			t.Errorf("%#x fits the field but was refused", argb)
		}
	}
	for _, argb := range []int64{-1, 0x1_0000_0000} {
		if ValidStoryBackground(argb) {
			t.Errorf("%#x does not fit the field and must be refused", argb)
		}
	}
}

// The composer claims to offer every font WhatsApp has. That claim is only true
// as long as this list matches the protocol's own, so the protocol is asked
// rather than trusted: if WhatsApp adds a font, this fails and says which one,
// instead of the choice quietly never reaching the screen.
func TestEveryFontWhatsAppDefinesIsOffered(t *testing.T) {
	sent := make(map[waE2E.ExtendedTextMessage_FontType]StoryFont, len(storyFonts))
	for name, font := range storyFonts {
		if other, clash := sent[font]; clash {
			t.Errorf("%q and %q both send %v; one of them is unreachable", name, other, font)
		}
		sent[font] = name
	}
	for value, label := range waE2E.ExtendedTextMessage_FontType_name {
		if _, offered := sent[waE2E.ExtendedTextMessage_FontType(value)]; !offered {
			t.Errorf("WhatsApp has %s but the composer cannot choose it", label)
		}
	}
}
