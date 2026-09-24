package wa

import (
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// A text Story is words on a coloured field, and WhatsApp carries both the
// colour and the shape of the letters inside the message itself. They are not
// decoration applied by the sender's app: every viewer sees what was chosen.
//
// Names are stored rather than numbers. The numbers are protobuf enum values
// belonging to WhatsApp, and an enum can be renumbered between versions; a name
// written into a campaign months ago has to keep meaning the same thing. The
// translation happens here, once, where it can be read.

// StoryFont is the name of one of the fonts WhatsApp offers for a text status.
type StoryFont = string

// Every font WhatsApp's protocol has, in the order the composer shows them.
// There are eight and no more: the enum WhatsApp defines has eight members, so
// this list is complete rather than a selection, and it cannot grow until
// WhatsApp itself adds one.
//
// The stored names are frozen. A campaign written months ago carries its name
// into the future, so "serif" keeps meaning the typewriter face even though the
// face is not a serif in the typographic sense. Renaming one here would quietly
// send an old campaign in the wrong letters.
const (
	StoryFontDefault     StoryFont = ""
	StoryFontSystem      StoryFont = "system"
	StoryFontSystemText  StoryFont = "system-text"
	StoryFontSystemBold  StoryFont = "system-bold"
	StoryFontTypewriter  StoryFont = "serif"
	StoryFontScript      StoryFont = "script"
	StoryFontHandwriting StoryFont = "handwriting"
	StoryFontCondensed   StoryFont = "condensed"
	StoryFontRounded     StoryFont = "heavy"
)

// storyFonts maps a stored name to the enum sent on the wire.
//
// Anything absent falls back to the default, which is what an old campaign
// carrying a name this build no longer knows should do: send, in the ordinary
// font, rather than fail.
var storyFonts = map[StoryFont]waE2E.ExtendedTextMessage_FontType{
	StoryFontSystem:      waE2E.ExtendedTextMessage_SYSTEM,
	StoryFontSystemText:  waE2E.ExtendedTextMessage_SYSTEM_TEXT,
	StoryFontSystemBold:  waE2E.ExtendedTextMessage_SYSTEM_BOLD,
	StoryFontTypewriter:  waE2E.ExtendedTextMessage_COURIERPRIME_BOLD,
	StoryFontScript:      waE2E.ExtendedTextMessage_FB_SCRIPT,
	StoryFontHandwriting: waE2E.ExtendedTextMessage_MORNINGBREEZE_REGULAR,
	StoryFontCondensed:   waE2E.ExtendedTextMessage_EXO2_EXTRABOLD,
	StoryFontRounded:     waE2E.ExtendedTextMessage_CALISTOGA_REGULAR,
}

// StoryFontNames is every font the composer may offer, for validation.
func StoryFontNames() []StoryFont {
	out := make([]StoryFont, 0, len(storyFonts))
	for name := range storyFonts {
		out = append(out, name)
	}
	return out
}

// storyFontType translates a stored name, tolerating case and stray spaces
// because the name travels through JSON written by hand as often as by a form.
func storyFontType(name StoryFont) (waE2E.ExtendedTextMessage_FontType, bool) {
	f, ok := storyFonts[strings.ToLower(strings.TrimSpace(name))]
	return f, ok
}

// ValidStoryFont reports whether a name is one this build can send.
//
// An empty name is valid and means the default, so a Story with no choice made
// is not treated as a Story with a bad one.
func ValidStoryFont(name StoryFont) bool {
	if strings.TrimSpace(name) == "" {
		return true
	}
	_, ok := storyFontType(name)
	return ok
}

// ValidStoryBackground reports whether a colour fits the ARGB field.
//
// Zero means no choice was made. Anything above the width of the field would be
// truncated on the wire into a colour nobody picked, so it is refused here
// instead.
func ValidStoryBackground(argb int64) bool {
	return argb >= 0 && argb <= 0xFFFFFFFF
}
