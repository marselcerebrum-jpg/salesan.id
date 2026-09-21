package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
	"github.com/salesan/omnichannel/backend/internal/wa"
)

// firstQRWait bounds how long POST /pair blocks for the first QR code before
// falling back to delivering it over the WebSocket.
const firstQRWait = 8 * time.Second

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}

	filter := models.AccountFilter{
		ApplicationID:    queryUUID(r, "application_id"),
		Unassigned:       queryBool(r, "unassigned"),
		ConnectionMethod: oneOf(r.URL.Query().Get("method"), "qr", "waba"),
		Search:           strings.TrimSpace(r.URL.Query().Get("search")),
	}

	accounts, err := s.repo.ListAccounts(r.Context(), sc, filter)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

func (s *Server) handleListApplicationAccounts(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	appID, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "application_id")
	if !ok {
		return
	}
	accounts, err := s.repo.ListAccountsByApplication(r.Context(), sc, appID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

func (s *Server) handleAccountStats(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	stats, err := s.repo.AccountStats(r.Context(), user.WorkspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	account, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

type createAccountRequest struct {
	Name             string  `json:"name"`
	Label            *string `json:"label"`
	ApplicationID    *string `json:"application_id"`
	ConnectionMethod string  `json:"connection_method"`
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	var req createAccountRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		// The modal marks the name optional; fall back to a readable default.
		req.Name = "Akun WhatsApp"
	}

	var appID *uuid.UUID
	if req.ApplicationID != nil && strings.TrimSpace(*req.ApplicationID) != "" {
		parsed, err := uuid.Parse(*req.ApplicationID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_id", "application_id tidak valid")
			return
		}
		if _, err := s.repo.GetApplication(r.Context(), user.WorkspaceID, parsed); err != nil {
			writeAppError(w, err)
			return
		}
		appID = &parsed
	}

	method := oneOf(req.ConnectionMethod, "qr", "waba")
	if method == "" {
		method = "qr"
	}

	account, err := s.repo.CreateAccount(r.Context(), user.WorkspaceID, repository.CreateAccountInput{
		Name:             req.Name,
		Label:            req.Label,
		ApplicationID:    appID,
		ConnectionMethod: method,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

type updateAccountRequest struct {
	Name          *string `json:"name"`
	Label         *string `json:"label"`
	ApplicationID *string `json:"application_id"`
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}

	var req updateAccountRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	in := repository.UpdateAccountInput{
		Name:  req.Name,
		Label: req.Label,
	}
	// An explicit null clears the application; a UUID reassigns it.
	if req.ApplicationID != nil {
		if strings.TrimSpace(*req.ApplicationID) == "" {
			in.ClearApp = true
		} else {
			parsed, err := uuid.Parse(*req.ApplicationID)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_id", "application_id tidak valid")
				return
			}
			in.ApplicationID = &parsed
		}
	}

	account, err := s.repo.UpdateAccount(r.Context(), user.WorkspaceID, id, in)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}

	// Unlink the device before the row (and its cascade) disappears.
	s.manager.Release(r.Context(), id)

	if err := s.repo.DeleteAccount(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	s.hub.Broadcast(user.WorkspaceID, realtime.EventAccountDeleted, map[string]any{"account_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handlePairAccount starts (or restarts) a QR pairing attempt. Calling it again
// is how the "Refresh QR" button works.
func (s *Server) handlePairAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	account, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if account.ConnectionMethod != "qr" {
		writeError(w, http.StatusBadRequest, "unsupported_method",
			"Hanya akun QR Scan yang bisa ditautkan lewat QR")
		return
	}

	qr, err := s.manager.StartPairing(r.Context(), id, firstQRWait)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account_id": id,
		"qr":         qr,
		"status":     models.AccountStatusQRPending,
	})
}

func (s *Server) handleAccountQR(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	qr, available := s.manager.CurrentQR(id)
	writeJSON(w, http.StatusOK, map[string]any{
		"account_id": id,
		"qr":         qr,
		"available":  available,
	})
}

func (s *Server) handleConnectAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	account, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	// Never paired yet — the caller needs the QR flow instead.
	if account.JID == nil || *account.JID == "" {
		qr, err := s.manager.StartPairing(r.Context(), id, firstQRWait)
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"account_id":    id,
			"qr":            qr,
			"needs_pairing": true,
			"status":        models.AccountStatusQRPending,
		})
		return
	}

	if err := s.manager.Connect(r.Context(), id); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"account_id": id,
		"status":     models.AccountStatusConnecting,
	})
}

func (s *Server) handleDisconnectAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}

	if err := s.manager.Disconnect(r.Context(), id); err != nil {
		// Nothing running is not a failure from the operator's point of view.
		detail := "Sesi sudah tidak aktif"
		_ = s.repo.SetAccountStatus(r.Context(), id, models.AccountStatusDisconnected, &detail)
	}
	account, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) handleLogoutAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}
	if err := s.manager.Logout(r.Context(), id); err != nil {
		writeAppError(w, err)
		return
	}
	account, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

// handleSyncAccount refreshes contacts, groups, labels, read state and chat
// history for one account.
//
//	POST /accounts/{id}/sync?days=7&prune=true
//
// `days` bounds the history window (1-90, default 7). `prune` additionally
// deletes stored messages older than that window, so the database matches the
// retention the UI promises rather than keeping whatever an earlier sync pulled.
func (s *Server) handleSyncAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "account_id")
	if !ok {
		return
	}
	if _, err := s.repo.GetAccount(r.Context(), user.WorkspaceID, id); err != nil {
		writeAppError(w, err)
		return
	}

	days := queryInt(r, "days", wa.DefaultSyncWindowDays)
	if days < 1 || days > 90 {
		writeError(w, http.StatusBadRequest, "invalid_window",
			"Rentang sinkronisasi harus antara 1 dan 90 hari")
		return
	}

	// `history=force` repairs a gap: it re-requests the recent stretch even
	// when the window already looks covered, which the ordinary check cannot
	// tell apart from a complete history.
	forceHistory := r.URL.Query().Get("history") == "force"

	result, err := s.manager.SyncAccount(r.Context(), id, wa.SyncOptions{
		ForceHistory: forceHistory,
		WindowDays:   days,
		Prune:        queryBool(r, "prune"),
	})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
