package httpapi

import (
	"archive/zip"
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// A formatted, streamed worksheet for the chat export.
//
// The same hand-written approach as writeXLSXSheets, for the same reason — no
// dependency for a handful of XML parts — but this one needs what that one
// deliberately left out: a bold header, a frozen first row, a filter, wrapped
// text and real column widths. Those need a styles part and a few worksheet
// elements, and they are written here rather than bolted onto the generic
// writer, which every other export in the codebase still uses unchanged.
//
// Rows go straight into the zip as they are produced, so a sheet of a million
// messages costs no more memory than a sheet of ten. The parts that depend on
// the row count (the filter range and its defined name) are written after the
// sheet, which a zip allows: the order of parts in the archive means nothing.

// excelMaxCellChars is the most a single cell may hold, counted the way Excel
// counts: in UTF-16 units. A longer value makes the file open with a repair
// prompt and the text cut, so it is carried over into the next column instead.
const excelMaxCellChars = 32767

// excelMaxRows is the sheet's row limit, header included.
const excelMaxRows = 1048576

// Style indices into cellXfs, in the order chatExportStyles declares them.
const (
	styleHeader     = 1
	styleDate       = 2
	styleTime       = 3
	styleText       = 4
	styleTextQuoted = 5
	styleWrap       = 6
	styleWrapQuoted = 7
	styleInfoLabel  = 8
	styleInfoValue  = 9
	styleInfoQuoted = 10
)

// chatExportStyles is the styles part.
//
// Every data cell carries number format 49 ("@", text), so a number typed over
// one stays text and a phone number never turns into 6.28E+12. The quoted
// variants set quotePrefix: Excel's own marker for "this begins with = but is
// not a formula", which keeps the value exactly as written while making sure
// editing the cell later cannot turn it into one.
const chatExportStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<numFmts count="2">` +
	`<numFmt numFmtId="164" formatCode="dd/mm/yyyy"/>` +
	`<numFmt numFmtId="165" formatCode="hh:mm:ss"/>` +
	`</numFmts>` +
	`<fonts count="2">` +
	`<font><sz val="11"/><name val="Calibri"/><family val="2"/></font>` +
	`<font><b/><sz val="11"/><name val="Calibri"/><family val="2"/></font>` +
	`</fonts>` +
	`<fills count="3">` +
	`<fill><patternFill patternType="none"/></fill>` +
	`<fill><patternFill patternType="gray125"/></fill>` +
	`<fill><patternFill patternType="solid"><fgColor rgb="FFE6EFEA"/><bgColor indexed="64"/></patternFill></fill>` +
	`</fills>` +
	`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="11">` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
	`<xf numFmtId="49" fontId="1" fillId="2" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1" applyFill="1" applyAlignment="1"><alignment vertical="center"/></xf>` +
	`<xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyAlignment="1"><alignment horizontal="left" vertical="top"/></xf>` +
	`<xf numFmtId="165" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyAlignment="1"><alignment horizontal="left" vertical="top"/></xf>` +
	`<xf numFmtId="49" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyAlignment="1"><alignment vertical="top"/></xf>` +
	`<xf numFmtId="49" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyAlignment="1" quotePrefix="1"><alignment vertical="top"/></xf>` +
	`<xf numFmtId="49" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf>` +
	`<xf numFmtId="49" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyAlignment="1" quotePrefix="1"><alignment vertical="top" wrapText="1"/></xf>` +
	`<xf numFmtId="49" fontId="1" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1" applyAlignment="1"><alignment vertical="top"/></xf>` +
	`<xf numFmtId="49" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf>` +
	`<xf numFmtId="49" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyAlignment="1" quotePrefix="1"><alignment vertical="top" wrapText="1"/></xf>` +
	`</cellXfs>` +
	`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
	`</styleSheet>`

// sheetCell is one cell: text, or a number already formatted for the file.
type sheetCell struct {
	Text  string
	Num   string
	Style int
}

func textCell(s string, style int) sheetCell { return sheetCell{Text: s, Style: style} }

// sheetColumn is a header and the width a reader needs to see its contents.
type sheetColumn struct {
	Header string
	Width  float64
}

// styledSheetWriter writes one workbook: a main sheet streamed row by row, and
// a short key/value sheet describing it.
type styledSheetWriter struct {
	zw      *zip.Writer
	buf     *bufio.Writer
	name    string
	columns []sheetColumn
	rows    int // data rows, header excluded
	closed  bool
}

// newStyledSheetWriter opens the archive and writes the header row.
func newStyledSheetWriter(w io.Writer, name string, columns []sheetColumn) (*styledSheetWriter, error) {
	zw := zip.NewWriter(w)
	part, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		return nil, err
	}
	sw := &styledSheetWriter{
		zw:      zw,
		buf:     bufio.NewWriterSize(part, 64<<10),
		name:    name,
		columns: columns,
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	// The header stays on screen however far down the reader scrolls.
	b.WriteString(`<sheetViews><sheetView workbookViewId="0">` +
		`<pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>` +
		`<selection pane="bottomLeft" activeCell="A2" sqref="A2"/>` +
		`</sheetView></sheetViews>`)
	b.WriteString(`<cols>`)
	for i, c := range columns {
		fmt.Fprintf(&b, `<col min="%d" max="%d" width="%s" customWidth="1"/>`,
			i+1, i+1, strconv.FormatFloat(c.Width, 'f', -1, 64))
	}
	b.WriteString(`</cols><sheetData>`)
	if _, err := sw.buf.WriteString(b.String()); err != nil {
		return nil, err
	}

	header := make([]sheetCell, len(columns))
	for i, c := range columns {
		header[i] = textCell(c.Header, styleHeader)
	}
	if err := sw.writeRow(1, header); err != nil {
		return nil, err
	}
	return sw, nil
}

// Rows is how many data rows have been written so far.
func (sw *styledSheetWriter) Rows() int { return sw.rows }

// WriteRow appends one data row.
func (sw *styledSheetWriter) WriteRow(cells []sheetCell) error {
	if sw.rows+1 >= excelMaxRows {
		return errSheetFull
	}
	sw.rows++
	return sw.writeRow(sw.rows+1, cells)
}

// errSheetFull means the next row would be past what a spreadsheet can hold.
var errSheetFull = fmt.Errorf("sheet is full at %d rows", excelMaxRows)

func (sw *styledSheetWriter) writeRow(index int, cells []sheetCell) error {
	var b strings.Builder
	fmt.Fprintf(&b, `<row r="%d">`, index)
	text := func(col int, s string, style int) {
		if s == "" {
			return
		}
		fmt.Fprintf(&b, `<c r="%s%d" s="%d" t="inlineStr"><is><t xml:space="preserve">`,
			columnName(col), index, style)
		_ = xml.EscapeText(&b, []byte(sheetText(s)))
		b.WriteString(`</t></is></c>`)
	}

	// A value past the cell limit is not cut. Its first piece stays in its own
	// column and the rest continue after the last named column, so the columns
	// to its right keep their place. Continuations are plain text: the quote
	// marker only matters for what a cell begins with.
	var overflow []sheetCell
	for col, c := range cells {
		if c.Num != "" {
			fmt.Fprintf(&b, `<c r="%s%d" s="%d"><v>%s</v></c>`, columnName(col), index, c.Style, c.Num)
			continue
		}
		pieces := splitForCell(c.Text)
		text(col, pieces[0], c.Style)
		for _, p := range pieces[1:] {
			overflow = append(overflow, textCell(p, unquotedStyle(c.Style)))
		}
	}
	for i, c := range overflow {
		text(len(cells)+i, c.Text, c.Style)
	}
	b.WriteString(`</row>`)
	_, err := sw.buf.WriteString(b.String())
	return err
}

// Close finishes the main sheet, then writes the info sheet and the parts that
// tie the workbook together. Nothing is usable until this returns nil.
func (sw *styledSheetWriter) Close(infoName string, info [][2]string) error {
	if sw.closed {
		return nil
	}
	sw.closed = true

	// The filter covers the named columns. Overflow columns, if any, sit to
	// their right without a header and are not part of it.
	lastCol := columnName(len(sw.columns) - 1)
	ref := fmt.Sprintf("A1:%s%d", lastCol, sw.rows+1)
	if _, err := sw.buf.WriteString(`</sheetData><autoFilter ref="` + ref + `"/></worksheet>`); err != nil {
		return err
	}
	if err := sw.buf.Flush(); err != nil {
		return err
	}

	if err := sw.writeInfoSheet(infoName, info); err != nil {
		return err
	}

	names := sheetNames([]xlsxSheet{{Name: sw.name}, {Name: infoName}})
	put := func(name, body string) error {
		f, err := sw.zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, body)
		return err
	}

	if err := put("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`+
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`+
		`<Default Extension="xml" ContentType="application/xml"/>`+
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`+
		`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`+
		`<Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`+
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`+
		`</Types>`); err != nil {
		return err
	}
	if err := put("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>`+
		`</Relationships>`); err != nil {
		return err
	}
	if err := put("xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>`+
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/>`+
		`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`+
		`</Relationships>`); err != nil {
		return err
	}
	if err := put("xl/styles.xml", chatExportStyles); err != nil {
		return err
	}

	// Excel records a sheet's filter range under this hidden name as well, and
	// some versions only show the filter buttons when both agree.
	quoted := "'" + strings.ReplaceAll(names[0], "'", "''") + "'"
	filterRange := fmt.Sprintf("%s!$A$1:$%s$%d", quoted, lastCol, sw.rows+1)
	if err := put("xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" `+
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`+
		`<sheets>`+
		`<sheet name="`+xmlEscape(names[0])+`" sheetId="1" r:id="rId1"/>`+
		`<sheet name="`+xmlEscape(names[1])+`" sheetId="2" r:id="rId2"/>`+
		`</sheets>`+
		`<definedNames><definedName name="_xlnm._FilterDatabase" localSheetId="0" hidden="1">`+
		xmlEscape(filterRange)+`</definedName></definedNames>`+
		`</workbook>`); err != nil {
		return err
	}
	return sw.zw.Close()
}

// writeInfoSheet is the second tab: which filter produced this file, and how
// many rows it holds, so a sheet forwarded a week later still explains itself.
func (sw *styledSheetWriter) writeInfoSheet(name string, info [][2]string) error {
	part, err := sw.zw.Create("xl/worksheets/sheet2.xml")
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<cols><col min="1" max="1" width="24" customWidth="1"/>` +
		`<col min="2" max="2" width="80" customWidth="1"/></cols><sheetData>`)
	for i, kv := range info {
		fmt.Fprintf(&b, `<row r="%d">`, i+1)
		for j, v := range kv {
			style := styleInfoLabel
			if j == 1 {
				style = styleInfoValue
				if needsQuotePrefix(v) {
					style = styleInfoQuoted
				}
			}
			fmt.Fprintf(&b, `<c r="%s%d" s="%d" t="inlineStr"><is><t xml:space="preserve">`,
				columnName(j), i+1, style)
			_ = xml.EscapeText(&b, []byte(sheetText(v)))
			b.WriteString(`</t></is></c>`)
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	_, err = io.WriteString(part, b.String())
	return err
}

// needsQuotePrefix reports whether a spreadsheet would read s as a formula if
// it were ever typed in or pasted as plain text: the classic CSV-injection
// leaders, plus the tab and carriage return that some importers strip first.
//
// The cell is an inline string either way, which Excel never evaluates, so the
// file as written is already safe. The prefix is for what happens next — a
// reader editing the cell, or saving the sheet as CSV and opening that.
func needsQuotePrefix(s string) bool {
	if s == "" {
		return false
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return true
	}
	// The full-width forms are read as formula leaders by some versions too.
	return strings.HasPrefix(s, "＝") || strings.HasPrefix(s, "＋") ||
		strings.HasPrefix(s, "－") || strings.HasPrefix(s, "＠")
}

// quoted picks the quote-prefixed variant of a text style when s needs it.
func quoted(s string, style int) int {
	if !needsQuotePrefix(s) {
		return style
	}
	switch style {
	case styleText:
		return styleTextQuoted
	case styleWrap:
		return styleWrapQuoted
	}
	return style
}

func unquotedStyle(style int) int {
	switch style {
	case styleTextQuoted:
		return styleText
	case styleWrapQuoted:
		return styleWrap
	}
	return style
}

// excelEscapeSequence is Excel's own escape for a character in a string,
// _xHHHH_. Excel decodes it on open, so a message that happens to contain
// "_x0041_" would show as "A" unless its leading underscore is escaped too.
var excelEscapeSequence = regexp.MustCompile(`_(x[0-9A-Fa-f]{4}_)`)

// sheetText prepares a value for a cell: line endings become the single line
// feed a spreadsheet wraps on, and anything Excel would decode is escaped so it
// reads back exactly as it was written. Characters XML cannot carry at all are
// replaced by xml.EscapeText itself.
func sheetText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return excelEscapeSequence.ReplaceAllString(s, "_x005F_$1")
}

// splitForCell cuts s into pieces Excel will accept in one cell each, never in
// the middle of a character.
func splitForCell(s string) []string {
	if len(s) <= excelMaxCellChars { // bytes ≥ UTF-16 units, so this is safe
		return []string{s}
	}
	var out []string
	units := 0
	start := 0
	for i, r := range s {
		n := len(utf16.Encode([]rune{r}))
		if units+n > excelMaxCellChars {
			out = append(out, s[start:i])
			start, units = i, 0
		}
		units += n
	}
	return append(out, s[start:])
}

// excelSerial turns a moment into Excel's date and time serials, read on the
// WIB wall clock: whole days since 1899-12-30, and the fraction of a day.
//
// Real dates rather than "03/11/2026" as text, so the filter offers years and
// months and sorting the column sorts by date instead of by day-of-month.
func excelSerial(t time.Time) (date, clock string) {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	days := int(day.Sub(epoch).Hours() / 24)
	seconds := t.Hour()*3600 + t.Minute()*60 + t.Second()
	return strconv.Itoa(days), strconv.FormatFloat(float64(seconds)/86400, 'f', -1, 64)
}
