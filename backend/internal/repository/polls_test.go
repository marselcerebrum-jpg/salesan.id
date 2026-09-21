package repository

import "testing"

// The account's stored JID carries a linked-device number while message senders
// are stored without one. Comparing the raw forms never matched, which is what
// hid the operator's own poll votes.
func TestNormalizeJIDStripsDeviceNumber(t *testing.T) {
	cases := map[string]string{
		"6285171593270:20@s.whatsapp.net": "6285171593270@s.whatsapp.net",
		"6285171593270@s.whatsapp.net":    "6285171593270@s.whatsapp.net",
		"86007441547495@lid":              "86007441547495@lid",
		"86007441547495:3@lid":            "86007441547495@lid",
		"120363000000000000@g.us":         "120363000000000000@g.us",
		"":                                "",
		"nonsense":                        "nonsense",
	}
	for in, want := range cases {
		if got := NormalizeJID(in); got != want {
			t.Errorf("NormalizeJID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPollOptionHashIsStable(t *testing.T) {
	// WhatsApp identifies an option by the SHA-256 of its exact text, so the
	// hash must depend on nothing else — not order, not the poll it belongs to.
	a := PollOptionHash("Pagi")
	b := PollOptionHash("Pagi")
	if a != b {
		t.Fatalf("hash is not stable: %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("hash length = %d, want 64 hex characters", len(a))
	}
	if PollOptionHash("Pagi") == PollOptionHash("pagi") {
		t.Error("hash must be case sensitive; WhatsApp hashes the exact text")
	}
}
