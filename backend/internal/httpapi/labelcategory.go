package httpapi

import (
	"encoding/csv"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/salesan/omnichannel/backend/internal/analytics"
)

// Status Label: Cold, Warm and Hot.
//
// One endpoint per level of the drill-down rather than one that returns
// everything: the card at the top is read on every dashboard load, and the list
// of eight thousand customers behind it is read when somebody asks for it.

// handleLabelCategorySummary answers the card and the first drill-down in one
// response: the three totals, the split by brand, and the day-by-day movement.
//
// Together because the detail screen states all three at once, and two requests
// could disagree about the same moment while one of them was still in flight.
func (s *Server) handleLabelCategorySummary(w http.ResponseWriter, r *http.Request) {
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

	summary, err := s.repo.LabelCategorySummary(r.Context(), sc, f)
	if err != nil {
		writeAppError(w, err)
		return
	}

	body := map[string]any{"summary": summary}

	// The brand split is only meaningful once a category has been chosen: "how
	// is Hot spread across the brands" is a question, "how is everything spread"
	// is the table below it.
	if category := strings.TrimSpace(r.URL.Query().Get("category")); category != "" {
		apps, err := s.repo.LabelCategoryByApplication(r.Context(), sc, f, category)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_category", err.Error())
			return
		}
		body["applications"] = apps
		body["category"] = category
	}

	// The card asks for the three totals and nothing else; the detail page wants
	// the tables too. One flag rather than two endpoints, so the numbers on both
	// screens come from one definition.
	if r.URL.Query().Get("transitions") != "false" {
		moves, err := s.repo.LabelCategoryTransitions(r.Context(), sc, f)
		if err != nil {
			writeAppError(w, err)
			return
		}
		body["transitions"] = moves

		daily, err := s.repo.LabelCategoryDaily(r.Context(), sc, f)
		if err != nil {
			writeAppError(w, err)
			return
		}
		body["daily"] = daily
	}

	writeJSON(w, http.StatusOK, body)
}

// handleLabelCategoryContacts lists the customers in one category.
func (s *Server) handleLabelCategoryContacts(w http.ResponseWriter, r *http.Request) {
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

	category := strings.TrimSpace(r.URL.Query().Get("category"))
	limit := intParam(r, "limit", 50)
	offset := intParam(r, "offset", 0)

	rows, total, err := s.repo.LabelCategoryContacts(
		r.Context(), sc, f, category, r.URL.Query().Get("q"), limit, offset)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_category", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"contacts": rows, "total": total, "limit": limit, "offset": offset,
	})
}

// handleContactLabelHistory is one customer's whole journey.
func (s *Server) handleContactLabelHistory(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scopeFor(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, chi.URLParam(r, "contactID"), "contact_id")
	if !ok {
		return
	}

	rows, err := s.repo.ContactLabelHistory(r.Context(), sc, id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": rows})
}

// handleExportLabelCategory writes the report as a spreadsheet.
//
// Two shapes, because two questions are being asked. "Summary" is the movement
// between categories day by day — what a manager reads. "Detail" is one row per
// customer — what somebody works through. Offering one and calling it the
// report would leave half the people who asked for it exporting by hand.
func (s *Server) handleExportLabelCategory(w http.ResponseWriter, r *http.Request) {
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

	shape := r.URL.Query().Get("scope")
	switch shape {
	case "detail", "daily":
	default:
		shape = "summary"
	}
	stamp := time.Now().Format("2006-01-02")

	var header []string
	var rows [][]string

	if shape == "daily" {
		days, err := s.repo.LabelCategoryDaily(r.Context(), sc, f)
		if err != nil {
			writeAppError(w, err)
			return
		}
		header = []string{"tanggal", "cold", "warm", "hot", "total"}
		for _, d := range days {
			rows = append(rows, []string{
				d.Day, strconv.Itoa(d.Cold), strconv.Itoa(d.Warm), strconv.Itoa(d.Hot),
				strconv.Itoa(d.Cold + d.Warm + d.Hot),
			})
		}

		// The same closing row the screen shows, and for the same reason: it is
		// not the sum of the column. A customer labelled Cold on Monday and Warm
		// on Thursday appears on both days above, and holds one label today.
		// Leaving it out of the file would let a spreadsheet add the column and
		// arrive at a number of customers that does not exist.
		now, err := s.repo.LabelCategorySummary(r.Context(), sc, f)
		if err != nil {
			writeAppError(w, err)
			return
		}
		rows = append(rows, []string{
			"total (kondisi sekarang)",
			strconv.Itoa(now.Cold), strconv.Itoa(now.Warm), strconv.Itoa(now.Hot),
			strconv.Itoa(now.Cold + now.Warm + now.Hot),
		})
	} else if shape == "summary" {
		moves, err := s.repo.LabelCategoryTransitions(r.Context(), sc, f)
		if err != nil {
			writeAppError(w, err)
			return
		}
		header = []string{"tanggal", "dari", "ke", "perpindahan", "customer"}
		for _, m := range moves {
			rows = append(rows, []string{
				m.Day, labelCategoryName(m.From), labelCategoryName(m.To),
				strconv.Itoa(m.Count), strconv.Itoa(m.Contacts),
			})
		}
	} else {
		category := strings.TrimSpace(r.URL.Query().Get("category"))
		// No paging on an export. Somebody who asked for the report wants the
		// report, and handing them the first fifty rows of it silently is worse
		// than making them wait.
		contacts, _, err := s.repo.LabelCategoryContacts(
			r.Context(), sc, f, category, r.URL.Query().Get("q"), 500, 0)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_category", err.Error())
			return
		}
		header = []string{
			"nomor_whatsapp", "nama", "kategori", "label_terakhir",
			"jumlah_perubahan", "tanggal_perubahan",
		}
		for _, c := range contacts {
			rows = append(rows, []string{
				c.Phone, c.Name, labelCategoryName(c.Category), c.LabelName,
				strconv.Itoa(c.ChangeCount),
				c.ChangedAt.In(analytics.Jakarta).Format("2006-01-02 15:04"),
			})
		}
	}

	base := fmt.Sprintf("status-label-%s-%s", shape, stamp)

	if r.URL.Query().Get("format") == "xlsx" {
		w.Header().Set("Content-Type",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment",
			map[string]string{"filename": base + ".xlsx"}))
		if err := writeXLSX(w, "Status Label", header, rows); err != nil {
			s.log.Error("write xlsx", "err", err)
		}
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment",
		map[string]string{"filename": base + ".csv"}))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write(header)
	for _, row := range rows {
		_ = cw.Write(row)
	}
}

// labelCategoryName is how a category is written for a human.
func labelCategoryName(c string) string {
	switch c {
	case "cold":
		return "Cold"
	case "warm":
		return "Warm"
	case "hot":
		return "Hot"
	}
	return c
}
