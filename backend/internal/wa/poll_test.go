package wa

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizePollTrimsAndKeepsOrder(t *testing.T) {
	name, options, err := normalizePoll("  Jam berapa?  ", []string{" Pagi ", "Siang", "  ", "Malam"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "Jam berapa?" {
		t.Errorf("name = %q", name)
	}
	// Blank entries are dropped, the rest keep the order they were typed in —
	// which is the order they will be shown and voted on.
	want := []string{"Pagi", "Siang", "Malam"}
	if len(options) != len(want) {
		t.Fatalf("options = %v, want %v", options, want)
	}
	for i := range want {
		if options[i] != want[i] {
			t.Errorf("options[%d] = %q, want %q", i, options[i], want[i])
		}
	}
}

func TestNormalizePollRejectsTooFewOptions(t *testing.T) {
	for _, opts := range [][]string{nil, {"Ya"}, {"Ya", "   "}} {
		if _, _, err := normalizePoll("Setuju?", opts); !errors.Is(err, ErrInvalidPoll) {
			t.Errorf("normalizePoll(%v) err = %v, want ErrInvalidPoll", opts, err)
		}
	}
}

func TestNormalizePollRejectsTooManyOptions(t *testing.T) {
	opts := make([]string, MaxPollOptions+1)
	for i := range opts {
		opts[i] = string(rune('a' + i))
	}
	if _, _, err := normalizePoll("Pilih", opts); !errors.Is(err, ErrInvalidPoll) {
		t.Fatalf("err = %v, want ErrInvalidPoll", err)
	}
}

// Options are identified by the hash of their text, so two identical options
// would be indistinguishable in every vote that followed.
func TestNormalizePollRejectsDuplicateOptions(t *testing.T) {
	_, _, err := normalizePoll("Pilih", []string{"Ya", "Tidak", " Ya "})
	if !errors.Is(err, ErrInvalidPoll) {
		t.Fatalf("err = %v, want ErrInvalidPoll", err)
	}
	if !strings.Contains(err.Error(), "dua kali") {
		t.Errorf("error should say which option repeated, got %q", err)
	}
}

func TestNormalizePollRejectsEmptyQuestion(t *testing.T) {
	if _, _, err := normalizePoll("   ", []string{"Ya", "Tidak"}); !errors.Is(err, ErrInvalidPoll) {
		t.Fatalf("err = %v, want ErrInvalidPoll", err)
	}
}

func TestNormalizePollRejectsOverlongText(t *testing.T) {
	long := strings.Repeat("a", MaxPollName+1)
	if _, _, err := normalizePoll(long, []string{"Ya", "Tidak"}); !errors.Is(err, ErrInvalidPoll) {
		t.Errorf("long question: err = %v, want ErrInvalidPoll", err)
	}

	longOption := strings.Repeat("b", MaxPollOption+1)
	if _, _, err := normalizePoll("Pilih", []string{"Ya", longOption}); !errors.Is(err, ErrInvalidPoll) {
		t.Errorf("long option: err = %v, want ErrInvalidPoll", err)
	}
}
