package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/realtime"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// Story and Broadcast.
//
// The execution engine — actually publishing a status or fanning a broadcast
// out to its recipients through whatsmeow — is NOT part of this. What is here
// is everything around it: the campaign, its recipients, its scheduling
// history, its audit trail, and the permission rules that decide who may touch
// any of it. That is deliberate rather than unfinished: mass sending from a
// real business number is the single fastest way to get it banned, and it is
// not a thing to switch on as a side effect of building a dashboard.
//
// Because the schema and the attribution rules are in place now, the day the
// executor lands the reports are already correct: a broadcast's outgoing
// messages carry sender_source = 'broadcast' and are therefore already excluded
// from SLA, follow-up, and every "balasan admin" figure.

// handleListCampaigns returns Story/Broadcast campaigns in the caller's scope.
func (s *Server) handleListCampaigns(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	f := repository.CampaignFilter{
		CampaignType: q.Get("type"),
		Status:       q.Get("status"),
		Search:       strings.TrimSpace(q.Get("search")),
		Limit:        intParam(r, "limit", 50),
		Recurring:    recurringParam(q.Get("recurring")),
	}
	if raw := q.Get("application_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "ID aplikasi tidak valid")
			return
		}
		if !sc.CanSeeApplication(id) {
			writeError(w, http.StatusForbidden, "forbidden", "Aplikasi ini di luar wewenang akun Anda")
			return
		}
		f.ApplicationID = &id
	}
	if raw := q.Get("account_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "ID perangkat tidak valid")
			return
		}
		allowed, err := s.repo.AccountInScope(r.Context(), sc, id)
		if err != nil {
			writeAppError(w, err)
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "forbidden", "Nomor ini di luar wewenang akun Anda")
			return
		}
		f.AccountID = &id
	}
	if raw := q.Get("created_by"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "ID pembuat tidak valid")
			return
		}
		if !sc.CanSeeAdmin(id) {
			writeError(w, http.StatusForbidden, "forbidden", "Data admin ini di luar wewenang akun Anda")
			return
		}
		f.CreatedBy = &id
	}
	if raw := q.Get("from"); raw != "" {
		at, err := time.Parse("2006-01-02", raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "Format tanggal harus YYYY-MM-DD")
			return
		}
		f.From = at
	}
	if raw := q.Get("to"); raw != "" {
		at, err := time.Parse("2006-01-02", raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "Format tanggal harus YYYY-MM-DD")
			return
		}
		f.To = at.AddDate(0, 0, 1)
	}
	if f.CampaignType != "" &&
		f.CampaignType != models.CampaignStory && f.CampaignType != models.CampaignBroadcast {
		writeError(w, http.StatusBadRequest, "invalid_type", "Jenis campaign harus story atau broadcast")
		return
	}

	campaigns, err := s.repo.ListCampaigns(r.Context(), sc, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"campaigns": campaigns,
		"scope":     scopePayload(sc),
	})
}

// handleCreateCampaign saves a Story or Broadcast and, optionally, starts it.
//
// It writes and returns; it never sends. Delivery belongs to the scheduler, and
// the difference is not architectural neatness: a campaign to four thousand
// people on the Aman profile takes most of a day, and an HTTP request cannot
// hold that. Wake() only shortens the wait for the next poll.
func (s *Server) handleCreateCampaign(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}

	var req campaignRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	built, ok := s.buildCampaign(w, r, sc, req)
	if !ok {
		return
	}

	campaign, err := s.repo.SaveBroadcast(r.Context(), user.WorkspaceID, user.ID, built.input)
	if err != nil {
		writeAppError(w, err)
		return
	}

	// A Story has no recipient list of ours; its work items are the devices.
	if campaign.CampaignType == models.CampaignStory {
		if err := s.repo.SeedStoryPublications(
			r.Context(), user.WorkspaceID, campaign.ID, built.input.AccountIDs); err != nil {
			writeAppError(w, err)
			return
		}
	}

	s.recordCampaignActivity(r, campaign, "draft_created")
	if campaign.ScheduledAt != nil {
		s.recordCampaignActivity(r, campaign, "schedule_created")
		s.runner.Wake()
	}
	s.hub.Broadcast(user.WorkspaceID, realtime.EventCampaignUpdated, campaign)

	writeJSON(w, http.StatusCreated, map[string]any{
		"campaign": campaign,
		"plan":     built.plan,
	})
}

// handleCampaignSource hands a campaign back in the shape the composer takes.
//
// The read behind "Duplikat" and "Edit". Neither writes anything: pressing
// either only fills the form, and a row appears — or changes — when the operator
// saves. Making the copy on the way in was the old behaviour, and it left a
// campaign behind every time somebody opened one, looked, and changed their mind.
func (s *Server) handleCampaignSource(w http.ResponseWriter, r *http.Request) {
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

	src, err := s.repo.CampaignSourceFor(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	// Editing needs more than being allowed to look at it.
	if src.Editable && !canManageCampaign(sc, c, user.ID) {
		src.Editable = false
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": src})
}

// handleUpdateCampaign rewrites a campaign that has not gone out yet.
//
// Validated by the same buildCampaign the create endpoint uses, so an edited
// campaign cannot reach a state a new one would have been refused for. The
// recipients are resolved again from the request rather than carried over: the
// numbers may have changed, and a list left over from the previous version is a
// list nobody chose.
func (s *Server) handleUpdateCampaign(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	existing, ok := s.campaignForWrite(w, r)
	if !ok {
		return
	}

	var req campaignRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// The type is not the composer's to change. A Story edited into a broadcast
	// would keep publication rows that mean nothing and lose a recipient list it
	// never had.
	req.CampaignType = existing.CampaignType

	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	built, ok := s.buildCampaign(w, r, sc, req)
	if !ok {
		return
	}

	campaign, err := s.repo.UpdateBroadcast(
		r.Context(), user.WorkspaceID, existing.ID, user.ID, built.input)
	if err != nil {
		if errors.Is(err, repository.ErrCampaignStarted) {
			writeError(w, http.StatusConflict, "already_started",
				"Campaign ini sudah berjalan, jadi isinya tidak bisa diubah lagi. "+
					"Gunakan Duplikat untuk membuat yang baru dari isian ini.")
			return
		}
		writeAppError(w, err)
		return
	}

	if campaign.CampaignType == models.CampaignStory {
		if err := s.repo.SeedStoryPublications(
			r.Context(), user.WorkspaceID, campaign.ID, built.input.AccountIDs); err != nil {
			writeAppError(w, err)
			return
		}
	}

	s.recordCampaignActivity(r, campaign, "draft_updated")
	if campaign.ScheduledAt != nil {
		s.recordCampaignActivity(r, campaign, "schedule_created")
		s.runner.Wake()
	}
	s.hub.Broadcast(user.WorkspaceID, realtime.EventCampaignUpdated, campaign)

	writeJSON(w, http.StatusOK, map[string]any{
		"campaign": campaign,
		"plan":     built.plan,
	})
}

// handleGetCampaign returns one campaign with its scheduling and audit history.
func (s *Server) handleGetCampaign(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "campaign_id")
	if !ok {
		return
	}

	campaign, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !canSeeCampaign(sc, campaign) {
		writeError(w, http.StatusForbidden, "forbidden", "Campaign ini di luar wewenang akun Anda")
		return
	}

	schedules, err := s.repo.CampaignSchedules(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	activity, err := s.repo.EntityActivity(r.Context(), user.WorkspaceID, "campaign", id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"campaign":  campaign,
		"schedules": schedules,
		"activity":  activity,
	})
}

type scheduleCampaignRequest struct {
	ScheduledAt string `json:"scheduled_at"`
}

// handleScheduleCampaign sets or moves a campaign's run time.
//
// Rescheduling never creates a second campaign: the previous publication row is
// superseded and a new one opened, so the detail screen can show the real
// change history rather than a single field that keeps being overwritten.
func (s *Server) handleScheduleCampaign(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "campaign_id")
	if !ok {
		return
	}

	existing, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !canManageCampaign(sc, existing, user.ID) {
		writeError(w, http.StatusForbidden, "forbidden", "Campaign ini di luar wewenang akun Anda")
		return
	}

	var req scheduleCampaignRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	at, err := time.Parse(time.RFC3339, req.ScheduledAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "Jadwal harus format RFC3339")
		return
	}
	if at.Before(time.Now().Add(-time.Minute)) {
		writeError(w, http.StatusBadRequest, "invalid_date", "Jadwal tidak boleh di masa lalu")
		return
	}

	activityType := "schedule_updated"
	if existing.ScheduledAt == nil {
		activityType = "schedule_created"
	}

	campaign, err := s.repo.ScheduleCampaign(r.Context(), user.WorkspaceID, id, user.ID, at)
	if err != nil {
		writeAppError(w, err)
		return
	}

	s.recordCampaignActivity(r, campaign, activityType)
	s.hub.Broadcast(user.WorkspaceID, realtime.EventCampaignUpdated, campaign)
	writeJSON(w, http.StatusOK, map[string]any{"campaign": campaign})
}

// handleCancelCampaign stops a scheduled run without erasing its history.
func (s *Server) handleCancelCampaign(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "campaign_id")
	if !ok {
		return
	}

	existing, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !canManageCampaign(sc, existing, user.ID) {
		writeError(w, http.StatusForbidden, "forbidden", "Campaign ini di luar wewenang akun Anda")
		return
	}

	// Cancellation is cooperative. A campaign that is mid-run raises a flag the
	// worker reads between two recipients; everything not yet attempted is
	// marked cancelled immediately, and everything already sent stays sent —
	// there is no unsending a WhatsApp message, and an interface that implied
	// otherwise would be lying to whoever pressed the button.
	campaign, err := s.repo.RequestCancel(r.Context(), user.WorkspaceID, id, user.ID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	s.recordCampaignActivity(r, campaign, "schedule_cancelled")
	s.hub.Broadcast(user.WorkspaceID, realtime.EventCampaignUpdated, campaign)
	writeJSON(w, http.StatusOK, map[string]any{"campaign": campaign})
}

// handleDeleteCampaign removes a draft that was never scheduled.
//
// Only a draft. Once a campaign has been scheduled it is part of somebody's
// activity record, and an audit trail that can be deleted is not one.
func (s *Server) handleDeleteCampaign(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "id"), "campaign_id")
	if !ok {
		return
	}

	existing, err := s.repo.GetCampaign(r.Context(), user.WorkspaceID, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !canManageCampaign(sc, existing, user.ID) {
		writeError(w, http.StatusForbidden, "forbidden", "Campaign ini di luar wewenang akun Anda")
		return
	}
	// A running Story is stopped before it is removed, so the queue is not left
	// holding a lease on a row that has gone.
	if existing.CampaignType == models.CampaignStory && existing.Status == models.CampaignRunning {
		if _, err := s.repo.RequestCancel(r.Context(), user.WorkspaceID, id, user.ID); err != nil {
			writeAppError(w, err)
			return
		}
	}
	if err := s.repo.DeleteCampaign(r.Context(), user.WorkspaceID, id); err != nil {
		writeError(w, http.StatusConflict, "not_deletable",
			"Hanya draft yang belum dijadwalkan yang dapat dihapus")
		return
	}

	s.hub.Broadcast(user.WorkspaceID, realtime.EventCampaignUpdated, map[string]any{
		"id": id, "deleted": true,
	})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// canSeeCampaign mirrors the RLS select policy for the API path.
func canSeeCampaign(sc repository.Scope, c *models.Campaign) bool {
	if sc.All {
		return true
	}
	if c.CreatedBy != nil && *c.CreatedBy == sc.UserID {
		return true
	}
	return c.ApplicationID != nil && sc.CanSeeApplication(*c.ApplicationID)
}

// canManageCampaign mirrors the RLS write policy: Leader and PIC within their
// applications, plus whoever created it.
func canManageCampaign(sc repository.Scope, c *models.Campaign, userID uuid.UUID) bool {
	if c.CreatedBy != nil && *c.CreatedBy == userID {
		return true
	}
	if !sc.All && sc.Role != models.RolePIC {
		return false
	}
	return canSeeCampaign(sc, c)
}

// recordCampaignActivity appends one entry to the campaign's audit trail.
//
// The event key folds in the campaign, the activity and the exact moment, so a
// retried request records one activity rather than inflating somebody's count.
func (s *Server) recordCampaignActivity(r *http.Request, c *models.Campaign, activityType string) {
	user := userFrom(r.Context())
	at := time.Now().UTC()

	detail := map[string]any{"campaign_type": c.CampaignType, "status": c.Status}
	if c.ScheduledAt != nil {
		detail["scheduled_at"] = c.ScheduledAt.In(analytics.Jakarta).Format(time.RFC3339)
	}

	if err := s.repo.RecordActivity(r.Context(), repository.ActivityInput{
		WorkspaceID:   user.WorkspaceID,
		ApplicationID: c.ApplicationID,
		AccountID:     c.AccountID,
		AdminID:       &user.ID,
		EntityType:    "campaign",
		EntityID:      &c.ID,
		EntityName:    c.Name,
		ActivityType:  activityType,
		Status:        c.Status,
		Detail:        detail,
		OccurredAt:    at,
		EventKey:      fmt.Sprintf("campaign:%s:%s:%d", c.ID, activityType, at.UnixMilli()),
	}); err != nil {
		s.log.Warn("record campaign activity", "campaign_id", c.ID, "type", activityType, "err", err)
	}
}
