package wa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types/events"
)

// Reading a label collection WhatsApp's own MACs will not vouch for.
//
// The normal path verifies, and should: a MAC proves the mutations inside a
// snapshot are the ones WhatsApp signed, and a client that skips that check is
// trusting whatever arrives. But verification is a parameter, not a law —
// whatsmeow's DecodePatches takes `validateMACs` and its ordinary callers pass
// true. Passed false, a snapshot that fails its MAC still decodes, and what
// comes out is still WhatsApp's data. What is lost is the proof that nothing
// altered it in between.
//
// That is a trade worth offering exactly once: on a collection that has been
// wedged long enough that the alternative is no labels at all. CEREBRUM UTAMA
// held zero labels across 2,463 conversations for a day, failing at the
// identical patch v1176 on every attempt, including from a freshly paired
// device. Refusing to read it changes nothing about the risk and costs the
// operator the feature.
//
// Three things keep it honest:
//
// Two of the three rails this started with have since been taken down on
// purpose, and saying which, and why, matters more than the rails did.
//
//   - It is no longer manual. Waiting for somebody to notice a red box meant a
//     number could sit unable to sync labels for a day, and the instruction was
//     to have no label sync failures at all. It now runs by itself once a wedge
//     has lasted half an hour, which is long enough to know it is not weather.
//   - It no longer leaves the stored version alone. Reading was only half the
//     feature: writing a label builds a patch on top of the stored version, and
//     WhatsApp refuses anything built below its own. The numbers whose labelling
//     works are exactly the ones whose version climbed past their broken link.
//
//     What this accepts unverified is smaller than it sounds. The chain is
//     verified normally right up to the break — CEREBRUM UTAMA reached v1226
//     against a break at v1227 on its own — so adopting the replayed version
//     takes one patch on trust and returns the collection to the verified path
//     beyond it. If the hash is wrong, writes are refused exactly as they are
//     refused today; nothing is lost that was not already lost.
//
// What remains, and must:
//
//   - Only the labels collection, never the others.
//   - Only a wedge that has outlasted wedgeConfirmAfter, and only once per
//     wedge, so a collection cannot be read this way in a loop.
//   - Recorded in its own column, never in labels_read_at, and the indicator
//     says `partial` rather than `synced` for as long as it stands.
//   - Replaced wholesale by the first verified read that succeeds.

// ErrNotWedged refuses the bypass on a collection that does not need it.
var ErrNotWedged = errors.New("wa: koleksi label tidak tersangkut, gunakan sinkron biasa")

// UnverifiedLabelRead is what one bypass read achieved.
type UnverifiedLabelRead struct {
	// Mutations is how many label changes were decoded out of the snapshot.
	Mutations int `json:"mutations"`
	// Labels is how many label definitions the account holds afterwards.
	Labels int `json:"labels"`
	// Patch is the version WhatsApp's data fails verification at, carried back
	// so the answer names the thing it worked around.
	Patch uint64 `json:"patch"`
}

// ReadLabelsUnverified decodes the labels collection without checking its MACs.
func (m *Manager) ReadLabelsUnverified(
	ctx context.Context, workspaceID, accountID uuid.UUID,
) (*UnverifiedLabelRead, error) {
	s, ok := m.Session(accountID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if !s.IsConnected() {
		return nil, ErrNotConnected
	}

	// Only for a collection already proved unreadable the proper way. Offering
	// a verification bypass to a healthy account would quietly make it the
	// normal path, which is the opposite of the point.
	w, wedged := s.wedgedAt(appstate.WAPatchRegular)
	if !wedged || w.patch == 0 {
		return nil, ErrNotWedged
	}

	name := appstate.WAPatchRegular
	internals := s.client.DangerousInternals()

	// From version zero with the snapshot, which is the only form that can
	// carry a whole collection. An incremental fetch would hand back the
	// patches after our stored version, and on this account that version is
	// zero anyway.
	list, err := internals.FetchAppStatePatches(ctx, name, 0, true)
	if err != nil {
		return nil, fmt.Errorf("ambil snapshot label: %w", err)
	}

	// Its own processor over the same device store. DecodePatches reads keys and
	// returns mutations; the writing of versions and MACs lives in a different
	// function that this path never reaches, so nothing here can leave our
	// stored state believing it holds something it cannot verify.
	proc := appstate.NewProcessor(s.client.Store, s.client.Log.Sub("LenientAppState"))
	mutations, state, err := proc.DecodePatches(ctx, list, appstate.HashState{}, false)
	if err != nil {
		return nil, fmt.Errorf("dekode snapshot label tanpa verifikasi: %w", err)
	}

	s.log.Warn("decoded the label collection without verifying WhatsApp's MACs",
		"patch", name, "wedged_at", w.patch, "mutations", len(mutations))

	// Turned into the same events the verified path produces, then fed through
	// the same router. The alternative was a second way to apply a label
	// change, which is a second place for the rules about ordering and
	// deletion to drift.
	var collected []any
	if err := internals.CollectEventsToDispatch(ctx, name, mutations, true, &collected); err != nil {
		return nil, fmt.Errorf("susun peristiwa label: %w", err)
	}

	applied := 0
	for _, evt := range collected {
		switch evt.(type) {
		case *events.LabelEdit, *events.LabelAssociationChat:
			s.handleEvent(evt)
			applied++
		}
		// Everything else the collection carries is deliberately dropped. In
		// particular no AppStateSyncComplete is synthesised: that event marks
		// the collection as properly read, which would clear the wedge and turn
		// the indicator green over data nothing has vouched for.
	}

	// Taken on only after the labels are in, so a decode that produced nothing
	// cannot leave the store claiming to be caught up.
	//
	// This is the half that makes labelling possible again rather than merely
	// visible, and it is the one place in the product where our stored
	// app-state version comes from data nothing vouched for. Logged at warning
	// level with both versions, so it is findable later without reading code.
	if state.Version > 0 {
		before, _, _ := s.client.Store.AppState.GetAppStateVersion(ctx, string(name))
		if err := s.client.Store.AppState.PutAppStateVersion(
			ctx, string(name), state.Version, state.Hash); err != nil {
			s.log.Warn("could not adopt the version from the unverified read", "err", err)
		} else {
			s.log.Warn("adopted an app state version from an unverified read",
				"patch", name, "from", before, "to", state.Version,
				"wedged_at", w.patch, "mutations", applied)
		}
	}

	count, err := m.repo.CountWhatsAppLabels(ctx, accountID)
	if err != nil {
		s.log.Warn("could not count labels after the unverified read", "err", err)
	}

	if err := m.repo.MarkLabelsUnverified(ctx, accountID, applied); err != nil {
		s.log.Warn("could not record the unverified read", "err", err)
	}

	// Said out loud, and not as synced. The labels are usable from here; what
	// is still true is that writing one back to WhatsApp will be refused while
	// the stored version sits at zero, and that nothing proved this data intact.
	detail := unverifiedDetail(w, applied, time.Now())
	_ = m.repo.SetLabelSyncState(ctx, accountID, LabelSyncPartial, detail)
	s.broadcastSyncState(ctx, LabelSyncPartial, detail)
	s.broadcastLabels(ctx, "unverified-read")

	return &UnverifiedLabelRead{Mutations: applied, Labels: count, Patch: w.patch}, nil
}

// unverifiedDetail is the sentence that stands while a number's labels come
// from a read nothing vouched for.
func unverifiedDetail(w wedgeState, mutations int, at time.Time) string {
	return fmt.Sprintf(
		"Label dipulihkan tanpa verifikasi pada %s (%d perubahan), sebab data WhatsApp sendiri "+
			"tidak konsisten di patch v%d. Label bisa dibaca dan diubah seperti biasa; yang "+
			"tidak bisa dibuktikan hanyalah keutuhan satu patch itu. Kalau WhatsApp tetap "+
			"menolaknya, pesannya akan mengatakan begitu, bukan diam.",
		at.Format("2 Jan 15:04"), mutations, w.patch)
}
