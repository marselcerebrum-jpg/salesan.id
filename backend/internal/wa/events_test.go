package wa

import (
	"fmt"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// The idempotency key has to stay indexable. WhatsApp sends one receipt
// covering every message a reader caught up on, and the unhashed key grew past
// what Postgres will index, which made the claim fail and dropped the delivered
// and read marks the receipt carried.
func TestReceiptKeyStaysShortAndStaysDistinct(t *testing.T) {
	many := make([]types.MessageID, 4000)
	for i := range many {
		many[i] = types.MessageID(fmt.Sprintf("3EB0%036d", i))
	}
	at := time.Unix(1750000000, 0)

	big := receiptKey("read", "628@s.whatsapp.net", at, many)
	if len(big) > 200 {
		t.Fatalf("key is %d bytes; it must stay well inside the btree limit", len(big))
	}

	same := receiptKey("read", "628@s.whatsapp.net", at, many)
	if big != same {
		t.Error("the same receipt must produce the same key, or replays get applied twice")
	}

	for name, other := range map[string]string{
		"a different message set": receiptKey("read", "628@s.whatsapp.net", at, many[:3999]),
		"a different chat":        receiptKey("read", "629@s.whatsapp.net", at, many),
		"a different type":        receiptKey("delivered", "628@s.whatsapp.net", at, many),
		"a different moment":      receiptKey("read", "628@s.whatsapp.net", at.Add(time.Second), many),
	} {
		if other == big {
			t.Errorf("%s must not collide with the original receipt", name)
		}
	}
}
