package wa

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeReactionAcceptsEmoji(t *testing.T) {
	// Single code points, skin tones, and zero-width-joiner sequences all count
	// as one emoji to a person, so all three have to pass.
	valid := []string{"👍", "❤️", "😂", "🙏🏽", "👨‍👩‍👧‍👦", "🇮🇩", "✅"}
	for _, emoji := range valid {
		got, err := normalizeReaction(emoji)
		if err != nil {
			t.Errorf("normalizeReaction(%q) = %v, want it accepted", emoji, err)
		}
		if got != emoji {
			t.Errorf("normalizeReaction(%q) = %q, want it unchanged", emoji, got)
		}
	}
}

// An empty string is how WhatsApp says "take the reaction back", so it must be
// accepted rather than treated as missing input.
func TestNormalizeReactionEmptyMeansRemove(t *testing.T) {
	got, err := normalizeReaction("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestNormalizeReactionRejectsText(t *testing.T) {
	for _, text := range []string{"halo", "ok 👍", "123", "👍 mantap", strings.Repeat("😀", 20)} {
		if _, err := normalizeReaction(text); !errors.Is(err, ErrInvalidReaction) {
			t.Errorf("normalizeReaction(%q) err = %v, want ErrInvalidReaction", text, err)
		}
	}
}
