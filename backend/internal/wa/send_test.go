package wa

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.mau.fi/whatsmeow"
)

// The errors are the ones the messages table holds, so this is the line
// between a reply that waits and one that fails drawn on real cases.
func TestTransientSendError(t *testing.T) {
	retry := []error{
		fmt.Errorf("failed to get device list: failed to send usync query: %w", whatsmeow.ErrIQTimedOut),
		whatsmeow.ErrNotConnected,
		&whatsmeow.DisconnectedError{Action: "info query"},
		fmt.Errorf("send: %w", context.DeadlineExceeded),
		context.Canceled,
		errors.New("failed to create sender key distribution message to send X to Y: " +
			"failed to load sender key from Z for Y: context deadline exceeded"),
		errors.New("cipher encryption failed: failed to check if x identity is trusted: context deadline exceeded"),
		errors.New("failed to save cached sessions: failed to store cached sessions: driver: bad connection"),
	}
	for _, err := range retry {
		if !transientSendError(err) {
			t.Errorf("%q should be retried", err)
		}
	}

	refuse := []error{
		errors.New("server returned error 401"),
		errors.New("server returned error 420"),
		errors.New("server returned error 403"),
		errors.New("no LID found for 628123 from server"),
		errors.New("failed to get group members: you're not participating in that group"),
	}
	for _, err := range refuse {
		if transientSendError(err) {
			t.Errorf("%q should fail at once, not be retried", err)
		}
	}
}
