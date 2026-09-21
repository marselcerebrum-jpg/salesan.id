package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
)

// scopeFor resolves what the caller may see, once per request.
//
// Every analytics handler starts here. The RLS policies say the same thing for
// the browser's direct Supabase path, but the backend connects as the service
// role, for which RLS does not run — so this is the check that actually holds
// on the API path.
func (s *Server) scopeFor(w http.ResponseWriter, r *http.Request) (repository.Scope, bool) {
	sc, err := s.repo.ResolveScope(r.Context(), userFrom(r.Context()))
	if err != nil {
		writeAppError(w, err)
		return sc, false
	}
	return sc, true
}

// parseAnalyticsFilter reads the filter row's query parameters.
//
// Dates arrive as WIB calendar dates and leave as absolute instants, resolved
// once here so no query downstream has to think about timezones. `to` is
// exclusive: a one-day range is [00:00 today, 00:00 tomorrow).
func parseAnalyticsFilter(w http.ResponseWriter, r *http.Request, defaultDays int) (models.AnalyticsFilter, bool) {
	q := r.URL.Query()
	var f models.AnalyticsFilter

	now := time.Now().In(analytics.Jakarta)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, analytics.Jakarta)

	switch {
	case q.Get("month") != "":
		// A whole calendar month, given as YYYY-MM.
		start, err := time.ParseInLocation("2006-01", q.Get("month"), analytics.Jakarta)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_month", "Format bulan harus YYYY-MM")
			return f, false
		}
		f.From = start
		f.To = start.AddDate(0, 1, 0)

	case q.Get("date") != "":
		day, err := time.ParseInLocation("2006-01-02", q.Get("date"), analytics.Jakarta)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "Format tanggal harus YYYY-MM-DD")
			return f, false
		}
		f.From = day
		f.To = day.AddDate(0, 0, 1)

	case q.Get("from") != "" || q.Get("to") != "":
		from, err := time.ParseInLocation("2006-01-02", q.Get("from"), analytics.Jakarta)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "Format tanggal harus YYYY-MM-DD")
			return f, false
		}
		to := from
		if raw := q.Get("to"); raw != "" {
			if to, err = time.ParseInLocation("2006-01-02", raw, analytics.Jakarta); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_date", "Format tanggal harus YYYY-MM-DD")
				return f, false
			}
		}
		if to.Before(from) {
			writeError(w, http.StatusBadRequest, "invalid_range", "Tanggal akhir lebih awal dari tanggal mulai")
			return f, false
		}
		f.From = from
		f.To = to.AddDate(0, 0, 1)

	default:
		f.From = today.AddDate(0, 0, -(defaultDays - 1))
		f.To = today.AddDate(0, 0, 1)
	}

	// A range wider than a year is almost always a mistake in a hand-built URL,
	// and it is expensive enough to be worth refusing rather than serving.
	if f.To.Sub(f.From) > 400*24*time.Hour {
		writeError(w, http.StatusBadRequest, "range_too_wide", "Rentang maksimal satu tahun")
		return f, false
	}

	switch t := q.Get("chat_type"); t {
	case "", "all":
		f.ChatType = ""
	case models.ConversationTypePersonal, models.ConversationTypeGroup:
		f.ChatType = t
	default:
		writeError(w, http.StatusBadRequest, "invalid_chat_type", "Jenis percakapan tidak dikenal")
		return f, false
	}

	for key, dest := range map[string]**uuid.UUID{
		"application_id": &f.ApplicationID,
		"account_id":     &f.AccountID,
		"pic_id":         &f.PICUserID,
		"admin_id":       &f.AdminID,
		"schedule_id":    &f.ScheduleID,
	} {
		raw := q.Get(key)
		if raw == "" {
			continue
		}
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "Nilai "+key+" tidak valid")
			return f, false
		}
		*dest = &parsed
	}
	// "freelance_id" is what the filter row calls it; the report only knows
	// about an admin, because a PIC's own chats are measured the same way.
	if raw := q.Get("freelance_id"); raw != "" && f.AdminID == nil {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "Nilai freelance_id tidak valid")
			return f, false
		}
		f.AdminID = &parsed
	}

	// Schedule compliance as a filter. Only the sources that carry the flag
	// honour it; see the note on AnalyticsFilter.InSchedule.
	switch q.Get("in_schedule") {
	case "":
	case "true", "in":
		yes := true
		f.InSchedule = &yes
	case "false", "out":
		no := false
		f.InSchedule = &no
	default:
		writeError(w, http.StatusBadRequest, "invalid_schedule_filter",
			"Nilai in_schedule harus true atau false")
		return f, false
	}

	return f, true
}

// enforceScope refuses a filter that reaches outside what the caller may see.
//
// Refusing rather than silently narrowing: a Freelance who asks for a
// colleague's numbers should be told no, not handed their own figures under
// somebody else's name.
func enforceScope(
	w http.ResponseWriter, sc repository.Scope, f models.AnalyticsFilter,
) (models.AnalyticsFilter, bool) {
	if f.ApplicationID != nil && !sc.CanSeeApplication(*f.ApplicationID) {
		writeError(w, http.StatusForbidden, "forbidden", "Aplikasi ini di luar wewenang akun Anda")
		return f, false
	}
	if f.AdminID != nil && !sc.CanSeeAdmin(*f.AdminID) {
		writeError(w, http.StatusForbidden, "forbidden", "Data admin ini di luar wewenang akun Anda")
		return f, false
	}
	if f.PICUserID != nil && !sc.CanSeeAdmin(*f.PICUserID) && !sc.All {
		writeError(w, http.StatusForbidden, "forbidden", "Data PIC ini di luar wewenang akun Anda")
		return f, false
	}

	// A Freelance sees their own work and nothing else. Pinning the admin here
	// rather than trusting the query parameter is what makes that true for every
	// figure downstream, including the ones whose table has no admin column of
	// its own and would otherwise fall back to "everything in my application".
	if sc.Role == models.RoleFreelance {
		self := sc.UserID
		f.AdminID = &self
		f.PICUserID = nil
	}
	// A PIC filtering by PIC can only mean themselves; anything else was already
	// refused above, and leaving the parameter set would narrow their own view
	// to a team they do not have.
	if sc.Role == models.RolePIC && f.PICUserID != nil && *f.PICUserID != sc.UserID {
		f.PICUserID = nil
	}
	return f, true
}

// handleDashboard returns one day's operational summary, defaulting to today.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, 1)
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}

	summary, err := s.repo.Dashboard(r.Context(), sc, f)
	if err != nil {
		writeAppError(w, err)
		return
	}

	upcoming, err := s.repo.UpcomingCampaigns(r.Context(), sc, 5)
	if err != nil {
		writeAppError(w, err)
		return
	}

	// How things stand right now, beside how the period went.
	//
	// The two answer different questions and one of them has no date range at
	// all — "berapa nomor tersambung" has no yesterday — but they are read by
	// one screen at one moment, so they travel together rather than costing a
	// second round trip.
	stats, err := s.repo.DashboardStatsFor(r.Context(), sc)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"summary":  summary,
		"stats":    stats,
		"upcoming": upcoming,
		"scope":    scopePayload(sc),
	})
}

// handleActivityFeed returns one page of a person's — or a scope's — history.
//
// Paged with a keyset rather than an offset. The feed is a union of four tables
// and new rows land at the top constantly; an offset would show the same
// activity twice, or skip one, every time somebody replied while a page was
// being read.
func (s *Server) handleActivityFeed(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, currentMonthDays())
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}

	var cursor repository.ActivityCursor
	if raw := r.URL.Query().Get("before"); raw != "" {
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "Kursor waktu tidak valid")
			return
		}
		cursor.Before = at
		cursor.BeforeID = r.URL.Query().Get("before_id")
	}

	limit := intParam(r, "limit", 40)
	rows, err := s.repo.ActivityFeed(r.Context(), sc, f, cursor, limit)
	if err != nil {
		writeAppError(w, err)
		return
	}

	// The cursor for the next page travels with the page it continues, so the
	// browser never has to reconstruct it from the last row's shape.
	payload := map[string]any{"activities": rows}
	if len(rows) == limit {
		last := rows[len(rows)-1]
		payload["next_before"] = last.OccurredAt.Format(time.RFC3339Nano)
		payload["next_before_id"] = last.ID
	}
	writeJSON(w, http.StatusOK, payload)
}

// handleConversationLocation resolves the ids a deep link needs to open a room.
//
// Answered by the server because the answer is an authorization decision: the
// caller learns where a conversation lives only if they are allowed to open it,
// and a browser that assembled the route itself would be asserting that rather
// than asking.
func (s *Server) handleConversationLocation(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r.URL.Query().Get("conversation_id"), "conversation_id")
	if !ok {
		return
	}
	loc, err := s.repo.LocateConversation(r.Context(), sc, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, loc)
}

// handlePerformance returns a month (or range) with its daily breakdown.
func (s *Server) handlePerformance(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	// Defaults to the current month, which is what the page opens on.
	f, ok := parseAnalyticsFilter(w, r, currentMonthDays())
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}

	report, err := s.repo.Performance(r.Context(), sc, f)
	if err != nil {
		writeAppError(w, err)
		return
	}

	// The preceding window of equal length, so every headline figure can say
	// whether it went up or down. Opt-in: it repeats the whole aggregate pass,
	// and only the Performa summary wants it.
	//
	// `to` is exclusive throughout this package, so the previous window ends
	// exactly where this one begins and the two never share a day.
	if r.URL.Query().Get("compare") == "true" {
		span := f.To.Sub(f.From)
		if span > 0 {
			prev := f
			prev.To = f.From
			prev.From = f.From.Add(-span)

			previous, err := s.repo.Performance(r.Context(), sc, prev)
			if err != nil {
				// A missing comparison is not a reason to fail the page: the
				// figures themselves are already correct without it.
				s.log.Warn("previous period comparison failed", "err", err)
			} else {
				report.Previous = &previous.Summary
				report.PreviousFrom = previous.From
				report.PreviousTo = previous.To
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"report": report,
		"scope":  scopePayload(sc),
	})
}

// handleTraffic returns message volume as a curve.
//
// `bucket=hour` for a single day, `bucket=day` for a longer range. The caller
// chooses, because the right grain depends on the question: which hours of the
// day are busy, or which days of the month were.
func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, 1)
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}

	byHour := false
	switch r.URL.Query().Get("bucket") {
	case "", "day":
	case "hour":
		byHour = true
	default:
		writeError(w, http.StatusBadRequest, "invalid_bucket", "Kelompok waktu harus hour atau day")
		return
	}

	// Only what happened while somebody was on shift, when asked for.
	//
	// Echoed back in the response rather than assumed by the caller: a client
	// that asks an older server for this would otherwise draw every hour of the
	// day under a heading promising only the working ones.
	f.WorkHoursOnly = r.URL.Query().Get("work_hours") == "true"

	points, scheduled, err := s.repo.Traffic(r.Context(), sc, f, byHour)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"points":     points,
		"bucket":     r.URL.Query().Get("bucket"),
		"work_hours": f.WorkHoursOnly,
		// False when the rota has nothing covering this period, which is why an
		// in-hours chart can be empty without anything being wrong.
		"schedule_configured": scheduled,
	})
}

// handlePerformanceByApplication splits the same view into one row per
// application, for the reader who holds several of them.
//
// Kept off /performance rather than folded into it: it costs one aggregate pass
// per application, and the screens that want a single number should not pay for
// a breakdown they never draw. The page fetches it separately, so the cards
// render while this is still running.
func (s *Server) handlePerformanceByApplication(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, currentMonthDays())
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}

	apps, err := s.repo.PerformanceByApplication(r.Context(), sc, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": apps})
}

// handleMemberBreakdown returns one full summary per person, for the tables
// that read people the same way Rincian Per Hari reads days.
//
// Its own request for the same reason the application split has one: it costs an
// aggregate pass per person, and `role` decides how many of them are paid for.
func (s *Server) handleMemberBreakdown(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, currentMonthDays())
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}

	role := r.URL.Query().Get("role")
	switch role {
	case "", models.RoleLeader, models.RolePIC, models.RoleFreelance:
	default:
		writeError(w, http.StatusBadRequest, "invalid_role", "Peran tidak dikenal")
		return
	}

	members, err := s.repo.MemberBreakdown(r.Context(), sc, f, role)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// handleTeamPerformance returns one row per person the caller may read.
//
// This is the hierarchy made visible: a Leader gets every PIC and Freelance, a
// PIC gets themselves and their team, a Freelance gets only themselves. The
// narrowing happens in the query, so asking for somebody else's row returns
// nothing rather than returning data the page then declines to draw.
func (s *Server) handleTeamPerformance(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	// Defaults to today, matching the Dashboard; the Performa page passes a
	// month explicitly.
	f, ok := parseAnalyticsFilter(w, r, 1)
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}

	report, err := s.repo.TeamPerformance(r.Context(), sc, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"report": report,
		"scope":  scopePayload(sc),
	})
}

// currentMonthDays is how many days have elapsed this month, so the default
// range is "this month so far" rather than an arbitrary window.
func currentMonthDays() int {
	return time.Now().In(analytics.Jakarta).Day()
}

func scopePayload(sc repository.Scope) map[string]any {
	return map[string]any{
		"role":            sc.Role,
		"is_leader":       sc.IsLeader(),
		"can_manage":      sc.CanManageSchedules(),
		"application_ids": sc.ApplicationIDs,
		"admin_ids":       sc.AdminIDs,
	}
}

// handleAnalyticsFilters lists everything the filter row may offer.
func (s *Server) handleAnalyticsFilters(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	// The Freelance list follows the chosen PIC, so a Leader cannot pair a PIC
	// with somebody else's team and get an empty report they then have to
	// explain to themselves.
	var pic *uuid.UUID
	if raw := r.URL.Query().Get("pic_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_uuid", "Nilai pic_id tidak valid")
			return
		}
		if !sc.All && !sc.CanSeeAdmin(parsed) {
			writeError(w, http.StatusForbidden, "forbidden", "Data PIC ini di luar wewenang akun Anda")
			return
		}
		pic = &parsed
	}

	options, err := s.repo.FilterOptions(r.Context(), sc, pic)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

func intParam(r *http.Request, key string, fallback int) int {
	if raw := r.URL.Query().Get(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// handleMessageDrilldown lists the messages behind a chat figure.
//
// The example the specification gives outright: pressing "Pesan Terkirim" has
// to show the messages it counted, with their date, application, device,
// conversation and who sent them.
func (s *Server) handleMessageDrilldown(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, 1)
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}

	direction := r.URL.Query().Get("direction")
	switch direction {
	case "", "in", "out", "device":
	default:
		writeError(w, http.StatusBadRequest, "invalid_direction",
			"Arah pesan harus in, out, atau device")
		return
	}

	chatType := r.URL.Query().Get("chat_type")
	if chatType == "all" {
		chatType = ""
	}

	rows, err := s.repo.MessageActivity(r.Context(), sc, f, direction, chatType, intParam(r, "limit", 100))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": rows})
}

// handleSLADrilldown lists the cycles behind the SLA cards.
func (s *Server) handleSLADrilldown(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, 1)
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}
	rows, err := s.repo.SLACycles(r.Context(), sc, f, r.URL.Query().Get("status"), intParam(r, "limit", 100))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cycles": rows})
}

// handleFollowUpDrilldown lists the follow-up activities.
func (s *Server) handleFollowUpDrilldown(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, 1)
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}
	rows, err := s.repo.FollowUps(r.Context(), sc, f, r.URL.Query().Get("replied"), intParam(r, "limit", 100))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"follow_ups": rows})
}

// handleGroupDrilldown lists group mentions and how they were answered.
func (s *Server) handleGroupDrilldown(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, 1)
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}
	rows, err := s.repo.GroupMentions(r.Context(), sc, f, r.URL.Query().Get("answered"), intParam(r, "limit", 100))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mentions": rows})
}

// handleLabelDrilldown lists the append-only label history and its transitions.
func (s *Server) handleLabelDrilldown(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, 1)
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}
	// The Status Label card reads the breakdown without wanting the event list
	// behind it, so `events=false` skips the expensive half. Same endpoint, same
	// filter, same definitions — a second route for the summary would be a
	// second place for the numbers to drift.
	events := []models.LabelEventRow{}
	if r.URL.Query().Get("events") != "false" {
		rows, err := s.repo.LabelEvents(r.Context(), sc, f,
			r.URL.Query().Get("event_type"), intParam(r, "limit", 100))
		if err != nil {
			writeAppError(w, err)
			return
		}
		events = rows
	}

	usage, err := s.repo.LabelUsage(r.Context(), sc, f, 50)
	if err != nil {
		writeAppError(w, err)
		return
	}
	transitions, err := s.repo.LabelTransitions(r.Context(), sc, f, 50)
	if err != nil {
		writeAppError(w, err)
		return
	}
	// What this filter is hiding, so an empty card can say why it is empty
	// rather than implying nothing happened.
	hidden, err := s.repo.UnattributedLabelEvents(r.Context(), sc, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events":       events,
		"usage":        usage,
		"transitions":  transitions,
		"unattributed": hidden,
	})
}

// handleLeadDrilldown lists classified contacts, with the reason for each.
func (s *Server) handleLeadDrilldown(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := parseAnalyticsFilter(w, r, 1)
	if !ok {
		return
	}
	f, ok = enforceScope(w, sc, f)
	if !ok {
		return
	}
	rows, err := s.repo.Leads(r.Context(), sc, f, r.URL.Query().Get("status"), intParam(r, "limit", 100))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"leads": rows})
}
