package repository

import "testing"

// The four shapes the same Indonesian number is written in, plus the inputs
// that must not be guessed at. This is the function the whole duplicate merge
// rests on: if it reads two spellings of one number differently, the merge
// cannot see them as the same person.
func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already canonical", "6282113791936", "6282113791936"},
		{"local trunk zero", "082113791936", "6282113791936"},
		{"plus prefix", "+6282113791936", "6282113791936"},
		{"spaces and dashes", "+62 821-1379-1936", "6282113791936"},
		{"dots", "62.821.1379.1936", "6282113791936"},
		{"parentheses", "(0821) 1379-1936", "6282113791936"},
		{"country code then trunk zero", "62082113791936", "6282113791936"},
		{"leading zeros collapse", "00082113791936", "6282113791936"},

		// Refused rather than guessed. Storing a wrong number is worse than
		// telling the operator which line could not be read.
		{"empty", "", ""},
		{"letters only", "tidak ada", ""},
		{"too short", "12345", ""},
		{"too long", "6281234567890123456", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizePhone(tt.in); got != tt.want {
				t.Errorf("NormalizePhone(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Every spelling of one number must land on the same string, because that
// string is the key the merge groups by.
func TestNormalizePhoneAgreesAcrossSpellings(t *testing.T) {
	spellings := []string{
		"6282113791936",
		"082113791936",
		"+6282113791936",
		"+62 821-1379-1936",
		"0821 1379 1936",
	}

	want := NormalizePhone(spellings[0])
	if want == "" {
		t.Fatal("the canonical spelling itself was refused")
	}
	for _, s := range spellings[1:] {
		if got := NormalizePhone(s); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q so the merge can group them", s, got, want)
		}
	}
}
