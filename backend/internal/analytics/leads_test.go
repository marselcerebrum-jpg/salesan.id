package analytics

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func ptr[T any](v T) *T { return &v }

var trackingStart = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func TestLeadVerifiedNew(t *testing.T) {
	status, reason := ClassifyLead(LeadEvidence{
		ContactCreatedAt:  trackingStart.Add(48 * time.Hour),
		FirstInboundAt:    ptr(trackingStart.Add(48 * time.Hour)),
		TrackingStartedAt: &trackingStart,
	})
	if status != LeadVerifiedNew {
		t.Fatalf("status = %q (%s), want verified_new", status, reason)
	}
}

// The seven-day history sync is the single biggest source of false "new leads",
// and the reason this classification exists at all.
func TestLeadFromImportIsHistorical(t *testing.T) {
	batch := uuid.New()
	status, _ := ClassifyLead(LeadEvidence{
		ContactCreatedAt:  trackingStart.Add(time.Hour),
		FirstInboundAt:    ptr(trackingStart.Add(time.Hour)),
		TrackingStartedAt: &trackingStart,
		ImportBatchID:     &batch,
	})
	if status != LeadHistorical {
		t.Fatalf("status = %q, want historical", status)
	}
}

func TestLeadWithEarlierHistoryIsHistorical(t *testing.T) {
	status, _ := ClassifyLead(LeadEvidence{
		ContactCreatedAt:         trackingStart.Add(time.Hour),
		FirstInboundAt:           ptr(trackingStart.Add(time.Hour)),
		TrackingStartedAt:        &trackingStart,
		HasHistoryBeforeTracking: true,
	})
	if status != LeadHistorical {
		t.Fatalf("status = %q, want historical", status)
	}
}

// A label can only have come from work done earlier, so the contact is not new
// however recent its first message looks.
func TestLeadWithPriorLabelIsHistorical(t *testing.T) {
	status, _ := ClassifyLead(LeadEvidence{
		ContactCreatedAt:  trackingStart.Add(time.Hour),
		FirstInboundAt:    ptr(trackingStart.Add(time.Hour)),
		TrackingStartedAt: &trackingStart,
		HadLabelBefore:    true,
	})
	if status != LeadHistorical {
		t.Fatalf("status = %q, want historical", status)
	}
}

func TestLeadKnownBeforeTrackingIsHistorical(t *testing.T) {
	// Already in the address book, wrote for the first time later. Not new.
	status, _ := ClassifyLead(LeadEvidence{
		ContactCreatedAt:  trackingStart.Add(-72 * time.Hour),
		FirstInboundAt:    ptr(trackingStart.Add(time.Hour)),
		TrackingStartedAt: &trackingStart,
	})
	if status != LeadHistorical {
		t.Fatalf("status = %q, want historical", status)
	}
}

func TestLeadWithoutTrackingStartIsUnknown(t *testing.T) {
	status, _ := ClassifyLead(LeadEvidence{
		ContactCreatedAt: trackingStart,
		FirstInboundAt:   ptr(trackingStart),
	})
	if status != LeadUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}

// Appeared after tracking began but has never written: genuinely undecidable.
func TestLeadWithoutInboundIsUnknown(t *testing.T) {
	status, _ := ClassifyLead(LeadEvidence{
		ContactCreatedAt:  trackingStart.Add(time.Hour),
		TrackingStartedAt: &trackingStart,
	})
	if status != LeadUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}

// Already in the address book before tracking and has never written. Not
// undecidable — old. This is most of a synced phone book, and calling it
// "unknown" would bury the handful of contacts that really are undecidable.
func TestLeadKnownBeforeTrackingWithoutInboundIsHistorical(t *testing.T) {
	status, reason := ClassifyLead(LeadEvidence{
		ContactCreatedAt:  trackingStart.Add(-48 * time.Hour),
		TrackingStartedAt: &trackingStart,
	})
	if status != LeadHistorical {
		t.Fatalf("status = %q (%s), want historical", status, reason)
	}
}

func TestEveryClassificationCarriesAReason(t *testing.T) {
	cases := []LeadEvidence{
		{ContactCreatedAt: trackingStart.Add(time.Hour), FirstInboundAt: ptr(trackingStart.Add(time.Hour)), TrackingStartedAt: &trackingStart},
		{TrackingStartedAt: &trackingStart},
		{},
	}
	for i, e := range cases {
		if _, reason := ClassifyLead(e); reason == "" {
			t.Errorf("case %d produced no reason", i)
		}
	}
}

func TestQualifiedDateOnlyForVerifiedNew(t *testing.T) {
	e := LeadEvidence{FirstInboundAt: ptr(trackingStart.Add(20 * time.Hour))}
	if got := QualifiedDate(LeadHistorical, e); !got.IsZero() {
		t.Error("historical leads must not carry a qualified date")
	}
	if got := QualifiedDate(LeadVerifiedNew, e); got.IsZero() {
		t.Error("a verified lead must carry the WIB date it arrived on")
	}
}
