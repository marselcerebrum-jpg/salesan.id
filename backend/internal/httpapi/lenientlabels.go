package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/salesan/omnichannel/backend/internal/wa"
)

// handleReadLabelsUnverified reads a wedged number's labels without checking
// WhatsApp's MACs.
//
//	POST /accounts/{id}/labels/read-unverified
//
// Deliberately a route of its own, pressed by a person, rather than something
// the reconciler may decide to do. It gives up the proof that a snapshot's
// mutations are the ones WhatsApp signed, which is a trade worth making only
// where the alternative is no labels at all — and only where somebody has
// chosen it for that number, knowing what it buys.
//
// The manager refuses it on a collection that is not wedged, so a healthy
// account cannot be talked into the bypass and have it quietly become the
// normal path.
func (s *Server) handleReadLabelsUnverified(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}

	// Logged with who did it. This is the one place in the product that reads
	// data nothing vouched for, and a record of who asked for it is the least
	// it should leave behind.
	s.log.Warn("reading labels without verifying WhatsApp's MACs, by operator request",
		"account_id", id, "user_id", user.ID)

	result, err := s.manager.ReadLabelsUnverified(r.Context(), user.WorkspaceID, id)
	if err != nil {
		switch {
		case errors.Is(err, wa.ErrNotWedged):
			writeError(w, http.StatusConflict, "not_wedged",
				"Koleksi label nomor ini tidak tersangkut, jadi tidak perlu dibaca tanpa verifikasi. "+
					"Gunakan Sinkron biasa.")
		case errors.Is(err, wa.ErrNotConnected), errors.Is(err, wa.ErrSessionNotFound):
			writeError(w, http.StatusConflict, "not_connected",
				"Nomor ini sedang tidak tersambung ke WhatsApp.")
		default:
			writeAppError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}
