package analytics

import (
	"time"

	"github.com/google/uuid"
)

// LeadEvidence is what is actually known about how a contact reached us.
//
// Deliberately separated from the verdict: these are facts, recorded once and
// never rewritten, while the rule that reads them may change. Storing only the
// verdict would make a later correction impossible to justify.
type LeadEvidence struct {
	// ContactCreatedAt is when the contact row was first written.
	ContactCreatedAt time.Time
	// FirstInboundAt is the contact's first message to us, if any.
	FirstInboundAt *time.Time
	// TrackingStartedAt is when this system began watching the receiving
	// account. Everything before it is, by definition, not observed.
	TrackingStartedAt *time.Time
	// ImportBatchID is set when the contact's history arrived through a sync.
	ImportBatchID *uuid.UUID
	ImportedAt    *time.Time
	// HasHistoryBeforeTracking is true when messages exist that predate the
	// moment tracking began.
	HasHistoryBeforeTracking bool
	// HadLabelBefore is true when the contact already carried a label when we
	// first saw it — a tag it can only have got from work done earlier.
	HadLabelBefore bool
}

// ClassifyLead decides whether a contact is genuinely new.
//
// The bias is deliberate and one-directional: anything that could be historical
// is called historical, and anything that cannot be decided is called unknown.
// The Dashboard counts only verified_new, so an over-eager rule here would
// inflate a number that people are measured on — a seven-day history sync would
// look like the best sales week of the year.
func ClassifyLead(e LeadEvidence) (status, reason string) {
	switch {
	case e.ImportBatchID != nil || e.ImportedAt != nil:
		return LeadHistorical, "kontak berasal dari sinkronisasi riwayat WhatsApp"

	case e.TrackingStartedAt == nil:
		return LeadUnknown, "waktu mulai pemantauan akun tidak diketahui"

	case e.HasHistoryBeforeTracking:
		return LeadHistorical, "sudah memiliki percakapan sebelum sistem memantau akun ini"

	case e.HadLabelBefore:
		return LeadHistorical, "sudah memiliki label sebelum sistem memantau akun ini"

	// The contact row itself predating tracking means we knew of them from the
	// address book before they ever wrote. Checked before the "no inbound" case
	// on purpose: a contact who has never written but was already in the phone
	// book is not an undecidable case, it is an old one.
	case !e.ContactCreatedAt.IsZero() && e.ContactCreatedAt.Before(*e.TrackingStartedAt):
		return LeadHistorical, "kontak sudah dikenal sebelum pemantauan aktif"

	case e.FirstInboundAt == nil:
		return LeadUnknown, "belum ada pesan masuk dari kontak ini"

	case e.FirstInboundAt.Before(*e.TrackingStartedAt):
		return LeadHistorical, "pesan masuk pertama terjadi sebelum pemantauan aktif"

	default:
		return LeadVerifiedNew, "pesan masuk pertama setelah pemantauan aktif, tanpa riwayat dan tanpa label sebelumnya"
	}
}

// QualifiedDate is the WIB date a verified-new lead should be counted on, or a
// zero time for anything that is not a verified lead.
func QualifiedDate(status string, e LeadEvidence) time.Time {
	if status != LeadVerifiedNew || e.FirstInboundAt == nil {
		return time.Time{}
	}
	return LocalDate(*e.FirstInboundAt)
}
