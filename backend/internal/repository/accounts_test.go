package repository

import (
	"regexp"
	"strings"
	"testing"
)

// Device IDs are shown on the account card and read aloud in support chats, so
// they must stay short, uppercase and free of ambiguous glyphs.
var deviceIDPattern = regexp.MustCompile(`^D-[A-HJ-NP-Z2-9]{6}$`)

func TestNewDeviceIDFormat(t *testing.T) {
	for i := 0; i < 200; i++ {
		id, err := newDeviceID()
		if err != nil {
			t.Fatalf("newDeviceID() error = %v", err)
		}
		if !deviceIDPattern.MatchString(id) {
			t.Fatalf("device id %q does not match %s", id, deviceIDPattern)
		}
		if strings.ContainsAny(id[2:], "IO01") {
			t.Fatalf("device id %q contains an ambiguous character", id)
		}
	}
}

func TestNewDeviceIDIsNotConstant(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 500; i++ {
		id, err := newDeviceID()
		if err != nil {
			t.Fatalf("newDeviceID() error = %v", err)
		}
		seen[id] = struct{}{}
	}
	// 32^6 possibilities: 500 draws should essentially never collide, but allow
	// a wide margin so the test cannot flake.
	if len(seen) < 480 {
		t.Errorf("only %d unique ids out of 500 draws — entropy looks wrong", len(seen))
	}
}
