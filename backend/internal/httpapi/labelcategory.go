package httpapi

import (
	"context"
	"encoding/csv"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/salesan/omnichannel/backend/internal/analytics"
	"github.com/salesan/omnichannel/backend/internal/models"
	"github.com/salesan/omnichannel/backend/internal/repository"
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

		// Each brand's own mix, which is a different question from the one
		// above: that splits a chosen category across the brands, this splits
		// one brand across the categories. A brand holding a quarter of all Hot
		// customers can still be mostly Cold inside, and only this says so.
		spread, err := s.repo.LabelSpreadByApplication(r.Context(), sc, f)
		if err != nil {
			writeAppError(w, err)
			return
		}
		body["spread"] = spread
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

	// One group per application, which is what the file is asked to be split
	// by. The screen above it is already narrowed to one brand at a time, so a
	// single flat table was the one shape nobody could use: a report that goes
	// upward is read brand by brand, and a column of mixed brands has to be
	// taken apart by hand before it answers anything.
	apps, err := s.repo.ApplicationsInScope(r.Context(), sc, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	// A reader who has already drilled into one brand gets that brand, not all
	// of them. ApplicationsInScope answers "which may I see", not "which did I
	// ask for", and the two differ exactly here.
	if f.ApplicationID != nil {
		apps = slices.DeleteFunc(apps, func(a models.AppRef) bool { return a.ID != *f.ApplicationID })
	} else if counts, err := s.repo.AccountCountsByApplication(r.Context(), sc.WorkspaceID); err == nil {
		// Applications holding no number are left out, because they cannot hold
		// label data either. Eight of the twenty-two rows here are duplicates —
		// same name, same code, no number on one of each pair — and without
		// this the workbook carries eight empty tabs called "JADIASN (2)" and
		// the like, which is worse than useless: it makes the reader doubt
		// which of the two JADIASN tabs is theirs.
		//
		// Kept only when something would survive, so a workspace that has not
		// linked a number yet still gets a file instead of a 404.
		if kept := slices.DeleteFunc(slices.Clone(apps), func(a models.AppRef) bool {
			return counts[a.ID] == 0
		}); len(kept) > 0 {
			apps = kept
		}
	}
	if len(apps) == 0 {
		writeError(w, http.StatusNotFound, "no_application",
			"Tidak ada aplikasi dalam jangkauan Anda untuk dilaporkan")
		return
	}

	// Checked once, before any brand is queried. Inside the loop it would be
	// the same complaint repeated per application, and the first one would have
	// already written a 400 that the rest could not take back.
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	if shape == "detail" && !slices.Contains([]string{"cold", "warm", "hot"}, category) {
		writeError(w, http.StatusBadRequest, "invalid_category",
			fmt.Sprintf("kategori %q tidak dikenal", category))
		return
	}

	header := labelCategoryHeader(shape)
	groups := make([]xlsxSheet, 0, len(apps))

	for _, app := range apps {
		// The screen's own query, asked once per brand rather than rewritten as
		// one grouped query. Slower by the number of brands, and the only way
		// the file and the screen cannot disagree.
		scoped := f
		id := app.ID
		scoped.ApplicationID = &id

		rows, err := s.labelCategoryRows(r.Context(), sc, scoped, shape, r.URL.Query())
		if err != nil {
			writeAppError(w, err)
			return
		}
		groups = append(groups, xlsxSheet{Name: app.Name, Header: header, Rows: rows})
	}

	base := fmt.Sprintf("status-label-%s-%s", shape, stamp)

	if r.URL.Query().Get("format") == "xlsx" {
		w.Header().Set("Content-Type",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment",
			map[string]string{"filename": base + ".xlsx"}))
		if err := writeXLSXSheets(w, groups); err != nil {
			s.log.Error("write xlsx", "err", err)
		}
		return
	}

	// A CSV has no tabs, so the split becomes a first column instead. Its own
	// column rather than a banner row between blocks: a spreadsheet can filter,
	// group or pivot by a column, and can do nothing at all with a banner.
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment",
		map[string]string{"filename": base + ".csv"}))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write(append([]string{"aplikasi"}, header...))
	for _, g := range groups {
		for _, row := range g.Rows {
			_ = cw.Write(append([]string{g.Name}, row...))
		}
	}
}

// labelCategoryHeader is the column list for one report shape.
//
// Apart from the row builder below so the two cannot drift: a header with one
// more column than its rows produces a file that looks right until somebody
// sorts it.
func labelCategoryHeader(shape string) []string {
	switch shape {
	case "daily":
		return []string{"tanggal", "cold", "warm", "hot", "total"}
	case "detail":
		return []string{
			"nomor_whatsapp", "nama", "kategori", "label_terakhir",
			"jumlah_perubahan", "tanggal_perubahan",
		}
	default:
		return []string{"tanggal", "dari", "ke", "perpindahan", "customer"}
	}
}

// labelCategoryRows builds one application's rows for one report shape.
func (s *Server) labelCategoryRows(
	ctx context.Context,
	sc repository.Scope,
	f models.AnalyticsFilter,
	shape string,
	q url.Values,
) ([][]string, error) {
	var rows [][]string

	switch shape {
	case "daily":
		days, err := s.repo.LabelCategoryDaily(ctx, sc, f)
		if err != nil {
			return nil, err
		}
		for _, d := range days {
			rows = append(rows, []string{
				d.Day, strconv.Itoa(d.Cold), strconv.Itoa(d.Warm), strconv.Itoa(d.Hot),
				strconv.Itoa(d.Cold + d.Warm + d.Hot),
			})
		}

		// The same closing row the screen shows, and for the same reason: it is
		// not the sum of the column. A customer labelled Cold on Monday and
		// Warm on Thursday appears on both days above, and holds one label
		// today. Leaving it out would let a spreadsheet add the column and
		// arrive at a number of customers that does not exist.
		//
		// Per application now, which is the point: a grand total across brands
		// would be the one number in the file nobody could check.
		now, err := s.repo.LabelCategorySummary(ctx, sc, f)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []string{
			"total (kondisi sekarang)",
			strconv.Itoa(now.Cold), strconv.Itoa(now.Warm), strconv.Itoa(now.Hot),
			strconv.Itoa(now.Cold + now.Warm + now.Hot),
		})

	case "detail":
		// No paging on an export. Somebody who asked for the report wants the
		// report, and handing them the first fifty rows of it silently is worse
		// than making them wait. The cap is per application rather than across
		// all of them, so a busy brand cannot crowd out a quiet one.
		contacts, _, err := s.repo.LabelCategoryContacts(
			ctx, sc, f, strings.TrimSpace(q.Get("category")), q.Get("q"), 500, 0)
		if err != nil {
			return nil, err
		}
		for _, c := range contacts {
			rows = append(rows, []string{
				c.Phone, c.Name, labelCategoryName(c.Category), c.LabelName,
				strconv.Itoa(c.ChangeCount),
				c.ChangedAt.In(analytics.Jakarta).Format("2006-01-02 15:04"),
			})
		}

	default:
		moves, err := s.repo.LabelCategoryTransitions(ctx, sc, f)
		if err != nil {
			return nil, err
		}
		for _, m := range moves {
			rows = append(rows, []string{
				m.Day, labelCategoryName(m.From), labelCategoryName(m.To),
				strconv.Itoa(m.Count), strconv.Itoa(m.Contacts),
			})
		}
	}

	return rows, nil
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
