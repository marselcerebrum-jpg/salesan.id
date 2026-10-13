package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tab names a spreadsheet will refuse, and the one case that actually happens.
//
// Duplicates are the real risk here, and the first two rules are what create
// them: the application names in this workspace are long and share prefixes, so
// two of them truncate to the same thirty-one characters. A workbook with two
// tabs of one name does not open at all.
func TestSheetNamesAreOnesASpreadsheetAccepts(t *testing.T) {
	in := []xlsxSheet{
		{Name: "JADIASN"},
		{Name: "Cold/Warm: 2026 [draft]"},
		{Name: "   "},
		{Name: "JADIBUMN SAVE KONTAK NOMOR SATU"},   // exactly 31
		{Name: "JADIBUMN SAVE KONTAK NOMOR SATU 2"}, // truncates to the same
		{Name: "JADIBUMN SAVE KONTAK NOMOR SATU 3"}, // and again
		{Name: "jadiasn"},                           // same name, different case
	}

	got := sheetNames(in)

	for i, name := range got {
		if name == "" {
			t.Errorf("sheet %d has no name", i)
		}
		if n := len([]rune(name)); n > 31 {
			t.Errorf("sheet %d name is %d characters: %q", i, n, name)
		}
		if strings.ContainsAny(name, forbiddenInSheetName) {
			t.Errorf("sheet %d name keeps a forbidden character: %q", i, name)
		}
	}

	seen := map[string]int{}
	for i, name := range got {
		key := strings.ToLower(name)
		if first, dup := seen[key]; dup {
			t.Errorf("sheets %d and %d share the name %q", first, i, name)
		}
		seen[key] = i
	}

	// The readable part has to survive. A name mangled down to "Sheet4" would
	// pass every rule above and tell the reader nothing.
	if !strings.HasPrefix(got[0], "JADIASN") {
		t.Errorf("a perfectly good name was changed: %q", got[0])
	}
	if !strings.HasPrefix(got[3], "JADIBUMN SAVE KONTAK") {
		t.Errorf("the brand name did not survive: %q", got[3])
	}
}

// The four parts of a workbook have to agree with each other. A sheet listed
// without a relationship, or a worksheet absent from the content types, is a
// file Excel refuses to open rather than one that opens wrong — so this walks
// the archive and checks each part names the others.
func TestAMultiSheetWorkbookIsInternallyConsistent(t *testing.T) {
	sheets := []xlsxSheet{
		{Name: "JADIASN", Header: []string{"tanggal", "cold"}, Rows: [][]string{{"2026-10-01", "4"}}},
		{Name: "JADIBUMN", Header: []string{"tanggal", "cold"}, Rows: [][]string{{"2026-10-01", "7"}, {"2026-10-02", "9"}}},
		{Name: "JAGOTPA", Header: []string{"tanggal", "cold"}},
	}

	rec := httptest.NewRecorder()
	if err := writeXLSXSheets(rec, sheets); err != nil {
		t.Fatalf("writeXLSXSheets: %v", err)
	}
	body := rec.Body.Bytes()

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the workbook is not a readable zip: %v", err)
	}

	parts := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		parts[f.Name] = string(raw)

		// Every part must be well-formed XML. A truncated worksheet is the
		// failure this is most likely to produce and the hardest to spot.
		if strings.HasSuffix(f.Name, ".xml") || strings.HasSuffix(f.Name, ".rels") {
			if err := xml.Unmarshal(raw, new(struct{ XMLName xml.Name })); err != nil {
				t.Errorf("%s is not well-formed XML: %v", f.Name, err)
			}
		}
	}

	for _, want := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels"} {
		if _, ok := parts[want]; !ok {
			t.Fatalf("the workbook is missing %s", want)
		}
	}

	for i, s := range sheets {
		part := fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		if _, ok := parts[part]; !ok {
			t.Fatalf("missing %s", part)
		}
		if !strings.Contains(parts["[Content_Types].xml"], "/"+part) {
			t.Errorf("%s is not declared in [Content_Types].xml", part)
		}
		rid := fmt.Sprintf(`r:id="rId%d"`, i+1)
		if !strings.Contains(parts["xl/workbook.xml"], rid) {
			t.Errorf("workbook.xml does not reference %s", rid)
		}
		if !strings.Contains(parts["xl/_rels/workbook.xml.rels"], fmt.Sprintf("worksheets/sheet%d.xml", i+1)) {
			t.Errorf("workbook.xml.rels does not point at %s", part)
		}
		if !strings.Contains(parts["xl/workbook.xml"], s.Name) {
			t.Errorf("workbook.xml does not name the tab %q", s.Name)
		}
	}

	// The rows actually landed, on the right tab, and the header is row 1.
	if !strings.Contains(parts["xl/worksheets/sheet1.xml"], "2026-10-01") {
		t.Error("the first tab has no data")
	}
	if !strings.Contains(parts["xl/worksheets/sheet2.xml"], "2026-10-02") {
		t.Error("the second tab is missing its second row")
	}
	if strings.Contains(parts["xl/worksheets/sheet1.xml"], "2026-10-02") {
		t.Error("a row from one application leaked onto another's tab")
	}
	if !strings.Contains(parts["xl/worksheets/sheet3.xml"], "tanggal") {
		t.Error("an application with no rows lost its header too")
	}
}

// The header and the rows are built by two different functions, and a header
// one column longer than its rows gives a file that looks right until somebody
// sorts it.
func TestEveryReportShapeHasAHeader(t *testing.T) {
	for _, shape := range []string{"daily", "summary", "detail", "anything-else"} {
		header := labelCategoryHeader(shape)
		if len(header) == 0 {
			t.Errorf("shape %q has no header", shape)
		}
		for i, column := range header {
			if strings.TrimSpace(column) == "" {
				t.Errorf("shape %q column %d is blank", shape, i)
			}
		}
	}

	// An unknown shape falls back to the same one the handler falls back to, so
	// the column count cannot disagree with the rows it will be given.
	if got, want := labelCategoryHeader("anything-else"), labelCategoryHeader("summary"); len(got) != len(want) {
		t.Errorf("the fallback header has %d columns, the fallback rows have %d", len(got), len(want))
	}
}
