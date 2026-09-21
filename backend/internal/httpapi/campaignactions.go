package httpapi

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/campaign"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// The broadcast list's chips, and the three actions on a broadcast's own page.

// handleCampaignFacets counts campaigns per application for the chip row.
func (s *Server) handleCampaignFacets(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := repository.CampaignFilter{
		CampaignType: q.Get("type"),
		Status:       q.Get("status"),
		Search:       strings.TrimSpace(q.Get("search")),
		Recurring:    recurringParam(q.Get("recurring")),
	}
	// Read for the status counts only: the application chips are counted with
	// this filter removed, which is what lets every chip show a real number.
	if raw := q.Get("application_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_application", "Aplikasi tidak dikenal")
			return
		}
		f.ApplicationID = &id
	}

	total, facets, statuses, err := s.repo.CampaignFacets(r.Context(), sc, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":        total,
		"applications": facets,
		"statuses":     statuses,
	})
}

// recurringParam reads the Broadcast/Berulang tab. Absent means both.
func recurringParam(raw string) *bool {
	switch raw {
	case "true", "1":
		v := true
		return &v
	case "false", "0":
		v := false
		return &v
	}
	return nil
}

// handleArchiveCampaign puts a finished campaign away, or brings it back.
//
// The action for everything that is not a draft. Deleting would take the send
// history with it, and a campaign that reached two hundred people is a record of
// something that happened — wanting it off the screen is not a reason to lose
// it.
func (s *Server) handleArchiveCampaign(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "campaign_id")
	if !ok {
		return
	}
	c, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !canManageCampaign(sc, c, user.ID) {
		writeError(w, http.StatusForbidden, "forbidden", "Campaign ini di luar wewenang akun Anda")
		return
	}

	var req struct {
		Archived *bool `json:"archived"`
	}
	// An empty body means archive; the un-archive path sends {"archived": false}.
	// Decoded directly rather than through decodeJSON, which treats an absent
	// body as an error — here it is the ordinary case.
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req)
	archived := true
	if req.Archived != nil {
		archived = *req.Archived
	}

	// A campaign still being worked on is stopped first. Archiving one mid-send
	// would hide it from the list while it carried on messaging people.
	if archived && (c.Status == "running" || c.Status == "scheduled") {
		if _, err := s.repo.RequestCancel(r.Context(), user.WorkspaceID, id, user.ID); err != nil {
			writeAppError(w, err)
			return
		}
	}

	if err := s.repo.ArchiveCampaign(r.Context(), user.WorkspaceID, id, archived); err != nil {
		writeAppError(w, err)
		return
	}
	fresh, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaign": fresh})
}

// handleResendCampaign queues every recipient again, including the ones who
// already received it.
//
// Deliberately separate from retry. Retry picks up what failed; this sends the
// whole list a second time, which is occasionally what somebody wants and is
// never something to do by accident — so it is its own endpoint with its own
// confirmation in front of it, rather than a flag on the retry call.
func (s *Server) handleResendCampaign(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "campaign_id")
	if !ok {
		return
	}
	c, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !canSeeCampaign(sc, c) {
		writeError(w, http.StatusForbidden, "forbidden", "Campaign ini di luar wewenang akun Anda")
		return
	}
	// A campaign still sending must not be reset underneath its own worker: the
	// recipients it is mid-way through would be queued again while the send that
	// owns them is still running.
	if c.Status == "running" {
		writeError(w, http.StatusConflict, "campaign_running",
			"Campaign masih berjalan. Batalkan dulu sebelum mengirim ulang semua.")
		return
	}

	n, err := s.repo.ResetTargetsForResend(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	s.runner.Wake()

	fresh, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaign": fresh, "queued": n})
}

// Duplicating a campaign used to live here, as a server-side copy made the
// moment the button was pressed. It is gone: duplicating now means reading the
// campaign back into the composer (GET /{id}/source) and saving it as a new one,
// so nothing is written until the operator actually saves. The old route wrote a
// draft every time somebody pressed the button, looked at the copy, and changed
// their mind — and those drafts could not be opened in the composer to be
// finished, because there was no way in.

// handleExportCampaignTargets writes the delivery log as CSV or a spreadsheet.
//
// The whole log, not the page the screen is showing: a file called "log
// pengiriman" that silently holds the first hundred rows is worse than no file.
func (s *Server) handleExportCampaignTargets(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "campaign_id")
	if !ok {
		return
	}
	c, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !canSeeCampaign(sc, c) {
		writeError(w, http.StatusForbidden, "forbidden", "Campaign ini di luar wewenang akun Anda")
		return
	}

	targets, err := s.repo.ListTargets(r.Context(), id, r.URL.Query().Get("status"), 100000, 0)
	if err != nil {
		writeAppError(w, err)
		return
	}

	header := []string{"nama", "nomor_atau_id_grup", "status", "catatan", "waktu", "nomor_pengirim"}
	rows := make([][]string, 0, len(targets))
	for _, t := range targets {
		name := ""
		if t.DisplayName != nil {
			name = *t.DisplayName
		}
		addr := t.ChatJID
		if t.PhoneNumber != nil && *t.PhoneNumber != "" && t.TargetType != "group" {
			addr = *t.PhoneNumber
		}
		// The note column carries whichever of the two the row actually has: a
		// failure reason when it failed, the WhatsApp message id when it went.
		note := ""
		if t.FailureReason != nil {
			note = *t.FailureReason
		} else if t.WAMessageID != nil {
			note = *t.WAMessageID
		}
		when := ""
		if t.SentAt != nil {
			// WIB, because the file is read next to a phone that shows WIB.
			when = t.SentAt.In(campaign.Jakarta).Format("2006-01-02 15:04:05")
		}
		sender := ""
		if t.AccountName != nil {
			sender = *t.AccountName
		}
		rows = append(rows, []string{name, addr, t.Status, note, when, sender})
	}

	base := safeFileName(c.Name)
	if base == "" {
		base = "log-pengiriman"
	}

	if r.URL.Query().Get("format") == "xlsx" {
		w.Header().Set("Content-Type",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": base + ".xlsx"}))
		if err := writeXLSX(w, "Log Pengiriman", header, rows); err != nil {
			s.log.Error("write xlsx", "err", err)
		}
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": base + ".csv"}))

	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write(header)
	for _, row := range rows {
		_ = cw.Write(row)
	}
}
