package httpapi

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/salesan/omnichannel/backend/internal/models"
)

// Spreadsheet columns run A..Z then AA, and every cell reference has to use
// that spelling or the file will not open.
func TestColumnName(t *testing.T) {
	cases := map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA"}
	for index, want := range cases {
		if got := columnName(index); got != want {
			t.Errorf("columnName(%d) = %q, want %q", index, got, want)
		}
	}
}

// The file has to be a real zip holding the five parts a spreadsheet reader
// looks for. Written by hand rather than by a library, so this is the check
// that it is still a workbook and not just bytes.
func TestWriteXLSXProducesAReadableWorkbook(t *testing.T) {
	rec := httptest.NewRecorder()
	header := []string{"nama_grup", "jumlah_anggota"}
	rows := [][]string{
		{"15 JADIBUMN (BUMN)", "1025"},
		// An ampersand and angle brackets must survive as text rather than
		// breaking the XML around them.
		{`Tanya & Jawab <CPNS>`, "12"},
	}

	if err := writeXLSX(rec, "Grup", header, rows); err != nil {
		t.Fatalf("writeXLSX: %v", err)
	}

	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("result is not a zip: %v", err)
	}

	found := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		found[f.Name] = string(content)
	}

	for _, want := range []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"xl/workbook.xml",
		"xl/_rels/workbook.xml.rels",
		"xl/worksheets/sheet1.xml",
	} {
		if _, ok := found[want]; !ok {
			t.Errorf("missing part %s", want)
		}
	}

	sheet := found["xl/worksheets/sheet1.xml"]
	if !strings.Contains(sheet, `r="A1"`) || !strings.Contains(sheet, `r="B3"`) {
		t.Errorf("cell references are wrong:\n%s", sheet)
	}
	if !strings.Contains(sheet, "15 JADIBUMN (BUMN)") {
		t.Error("a data cell did not make it into the sheet")
	}
	if strings.Contains(sheet, "<CPNS>") {
		t.Error("angle brackets were written raw and would break the XML")
	}
	if !strings.Contains(sheet, "&amp;") {
		t.Error("the ampersand was not escaped")
	}
}

// An unfetched group must never export as "0 members": nobody has looked, and
// a zero would be read as a fact about the group.
func TestGroupExportSaysWhenNobodyHasLooked(t *testing.T) {
	_, rows := groupExportRows([]models.GroupRow{
		{Name: "Sudah diambil", MemberCount: 12, Fetched: true, AccountCount: 1},
		{Name: "Belum diambil", MemberCount: 0, Fetched: false, AccountCount: 1},
	})

	if rows[0][4] != "12" {
		t.Errorf("fetched group exported %q, want \"12\"", rows[0][4])
	}
	if rows[1][4] != "belum diambil" {
		t.Errorf("unfetched group exported %q, want it to say so", rows[1][4])
	}
}
