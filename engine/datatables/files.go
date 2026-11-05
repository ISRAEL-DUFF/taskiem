package datatables

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Files brought into a table (docs/spec/no-sheet-needed.md §5.6): a CSV or
// an Excel workbook's first sheet, read as rows of text cells, the first
// row the header. An .xlsx is a zip of XML parts; it is read with the
// standard library alone, with caps on the number of entries and on what
// each expands to, so a small file cannot unpack into a large one. Cells
// are values only: a formula's last computed value is read as Excel saved
// it and nothing is ever evaluated, and a cell that starts like a formula
// is kept as the text it is.

// FileKind is what a file is read as: csv, xlsx, or "" for neither.
func FileKind(name, mime string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".csv"), mime == "text/csv", mime == "text/comma-separated-values", mime == "application/csv":
		return "csv"
	case strings.HasSuffix(n, ".xlsx"), mime == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return "xlsx"
	}
	return ""
}

// ErrFile is a file that cannot be read as a table: the reason follows.
var ErrFile = errors.New("the file cannot be read")

const (
	// xlsxMaxEntries bounds the parts of a workbook.
	xlsxMaxEntries = 500
	// xlsxMaxPart bounds what one part expands to, and xlsxMaxTotal what
	// the parts read expand to together.
	xlsxMaxPart  = 32 << 20
	xlsxMaxTotal = 64 << 20
	// fileMaxColumns bounds a row's cells, and fileMaxHeading a heading in
	// bytes, as a column's title is bounded.
	fileMaxColumns = 200
	fileMaxHeading = 120
)

// ReadFile reads a CSV or a workbook's first sheet into its header and
// rows, refusing more than maxRows rows (blank rows are left out). Cells
// are trimmed of the spaces around them; a header's byte order mark goes.
func ReadFile(kind string, data []byte, maxRows int) (header []string, rows [][]string, err error) {
	var recs [][]string
	switch kind {
	case "csv":
		recs, err = readCSVRecords(data, maxRows)
	case "xlsx":
		recs, err = readXLSX(data, maxRows)
	default:
		return nil, nil, fmt.Errorf("%w: only CSV and Excel (.xlsx) files are read", ErrFile)
	}
	if err != nil {
		return nil, nil, err
	}
	return ReadRecords(recs)
}

// ReadRecords reads records, the first the header, as ReadFile does: a
// Google Sheet's values read once, or a file's.
func ReadRecords(recs [][]string) (header []string, rows [][]string, err error) {
	if len(recs) == 0 {
		return nil, nil, fmt.Errorf("%w: it has no header row", ErrFile)
	}
	header = recs[0]
	for i := range header {
		header[i] = strings.TrimSpace(strings.TrimPrefix(header[i], "\uFEFF"))
	}
	for len(header) > 0 && header[len(header)-1] == "" {
		header = header[:len(header)-1]
	}
	if len(header) == 0 {
		return nil, nil, fmt.Errorf("%w: it has no header row", ErrFile)
	}
	for i, h := range header {
		if h == "" {
			return nil, nil, fmt.Errorf("%w: column %d has no heading", ErrFile, i+1)
		}
		if len(h) > fileMaxHeading {
			return nil, nil, fmt.Errorf("%w: the heading of column %d is too long", ErrFile, i+1)
		}
	}
	for _, r := range recs[1:] {
		blank := true
		for i := range r {
			r[i] = strings.TrimSpace(r[i])
			blank = blank && r[i] == ""
		}
		if blank {
			continue
		}
		if len(r) > len(header) {
			for _, extra := range r[len(header):] {
				if extra != "" {
					return nil, nil, fmt.Errorf("%w: row %d has more cells than the header", ErrFile, len(rows)+2)
				}
			}
			r = r[:len(header)]
		}
		for len(r) < len(header) {
			r = append(r, "")
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("%w: it has no rows under the header", ErrFile)
	}
	return header, rows, nil
}

// TooManyRows reports a file refused for having more rows than Limit.
type TooManyRows struct{ Limit int }

func (e TooManyRows) Error() string {
	return fmt.Sprintf("the file has more than %d rows", e.Limit)
}

func readCSVRecords(data []byte, maxRows int) ([][]string, error) {
	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	var out [][]string
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrFile, err)
		}
		if len(rec) > fileMaxColumns {
			return nil, fmt.Errorf("%w: more than %d columns", ErrFile, fileMaxColumns)
		}
		if len(out) > maxRows {
			return nil, TooManyRows{Limit: maxRows}
		}
		out = append(out, rec)
	}
}

// --- xlsx ---

// xlsxBook is the parts of a workbook read.
type xlsxBook struct {
	files map[string]*zip.File
	read  int64
}

// part reads one part, bounded; "" and no error when it is absent.
func (b *xlsxBook) part(name string) ([]byte, error) {
	f, ok := b.files[name]
	if !ok {
		return nil, nil
	}
	if f.UncompressedSize64 > xlsxMaxPart {
		return nil, fmt.Errorf("%w: a part of the workbook is too large", ErrFile)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFile, err)
	}
	defer func() { _ = rc.Close() }()
	// The sizes in the zip's directory are not trusted: what is read is
	// counted.
	data, err := io.ReadAll(io.LimitReader(rc, xlsxMaxPart+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFile, err)
	}
	b.read += int64(len(data))
	if len(data) > xlsxMaxPart || b.read > xlsxMaxTotal {
		return nil, fmt.Errorf("%w: the workbook expands too far", ErrFile)
	}
	return data, nil
}

func readXLSX(data []byte, maxRows int) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: it is not an Excel workbook (.xlsx)", ErrFile)
	}
	if len(zr.File) > xlsxMaxEntries {
		return nil, fmt.Errorf("%w: the workbook has too many parts", ErrFile)
	}
	b := &xlsxBook{files: map[string]*zip.File{}}
	for _, f := range zr.File {
		b.files[strings.TrimPrefix(f.Name, "/")] = f
	}
	sheet, date1904, err := firstSheet(b)
	if err != nil {
		return nil, err
	}
	shared, err := sharedStrings(b)
	if err != nil {
		return nil, err
	}
	dates, err := dateStyles(b)
	if err != nil {
		return nil, err
	}
	raw, err := b.part(sheet)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: the workbook's first sheet is missing", ErrFile)
	}
	return sheetRows(raw, shared, dates, date1904, maxRows)
}

// firstSheet is the path of the workbook's first sheet, and whether its
// dates count from 1904.
func firstSheet(b *xlsxBook) (string, bool, error) {
	raw, err := b.part("xl/workbook.xml")
	if err != nil {
		return "", false, err
	}
	if raw == nil {
		return "", false, fmt.Errorf("%w: it is not an Excel workbook (.xlsx)", ErrFile)
	}
	var wb struct {
		Pr struct {
			Date1904 string `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets []struct {
			ID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(raw, &wb); err != nil || len(wb.Sheets) == 0 {
		return "", false, fmt.Errorf("%w: the workbook has no sheet", ErrFile)
	}
	date1904 := wb.Pr.Date1904 == "1" || wb.Pr.Date1904 == "true"
	rels, err := b.part("xl/_rels/workbook.xml.rels")
	if err != nil {
		return "", false, err
	}
	var rs struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if rels != nil {
		_ = xml.Unmarshal(rels, &rs)
	}
	for _, r := range rs.Rel {
		if r.ID != wb.Sheets[0].ID {
			continue
		}
		t := r.Target
		if strings.HasPrefix(t, "/") {
			return path.Clean(strings.TrimPrefix(t, "/")), date1904, nil
		}
		return path.Clean(path.Join("xl", t)), date1904, nil
	}
	// Without its relationships, the usual name.
	return "xl/worksheets/sheet1.xml", date1904, nil
}

// xlsxText is a string item: plain text, or runs of rich text.
type xlsxText struct {
	T  string `xml:"t"`
	Rs []struct {
		T string `xml:"t"`
	} `xml:"r"`
}

func (x xlsxText) String() string {
	if len(x.Rs) == 0 {
		return x.T
	}
	var b strings.Builder
	b.WriteString(x.T)
	for _, r := range x.Rs {
		b.WriteString(r.T)
	}
	return b.String()
}

func sharedStrings(b *xlsxBook) ([]string, error) {
	raw, err := b.part("xl/sharedStrings.xml")
	if err != nil || raw == nil {
		return nil, err
	}
	var sst struct {
		SI []xlsxText `xml:"si"`
	}
	if err := xml.Unmarshal(raw, &sst); err != nil {
		return nil, fmt.Errorf("%w: its text cannot be read", ErrFile)
	}
	out := make([]string, len(sst.SI))
	for i, si := range sst.SI {
		out[i] = si.String()
	}
	return out, nil
}

var (
	// dateFormatRe finds a date or time part in a number format, once
	// quoted text, brackets and escapes (formatQuotedRe) are taken out.
	dateFormatRe   = regexp.MustCompile(`[dmyhs]`)
	formatQuotedRe = regexp.MustCompile(`"[^"]*"|\[[^\]]*\]|\\.`)
)

// dateStyles reports, per cell style, whether it shows a number as a date.
func dateStyles(b *xlsxBook) ([]bool, error) {
	raw, err := b.part("xl/styles.xml")
	if err != nil || raw == nil {
		return nil, err
	}
	var st struct {
		NumFmts []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Xfs []struct {
			NumFmtID int `xml:"numFmtId,attr"`
		} `xml:"cellXfs>xf"`
	}
	if err := xml.Unmarshal(raw, &st); err != nil {
		return nil, nil //nolint:nilerr // styles only tell dates apart: without them numbers stay numbers
	}
	custom := map[int]bool{}
	for _, f := range st.NumFmts {
		code := formatQuotedRe.ReplaceAllString(strings.ToLower(f.Code), "")
		custom[f.ID] = dateFormatRe.MatchString(code)
	}
	out := make([]bool, len(st.Xfs))
	for i, x := range st.Xfs {
		id := x.NumFmtID
		out[i] = (id >= 14 && id <= 22) || (id >= 45 && id <= 47) || custom[id]
	}
	return out, nil
}

// columnIndex reads a cell reference's column (A1 → 0, AB7 → 27); -1 when
// it has none.
func columnIndex(ref string) int {
	n := 0
	i := 0
	for ; i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		n = n*26 + int(ref[i]-'A'+1)
		if n > fileMaxColumns {
			return fileMaxColumns
		}
	}
	if i == 0 {
		return -1
	}
	return n - 1
}

// sheetRows reads a sheet's cells row by row, streaming the XML.
func sheetRows(raw []byte, shared []string, dates []bool, date1904 bool, maxRows int) ([][]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var out [][]string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: the sheet cannot be read", ErrFile)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "row" {
			continue
		}
		var row struct {
			R string `xml:"r,attr"`
			C []struct {
				R  string   `xml:"r,attr"`
				T  string   `xml:"t,attr"`
				S  int      `xml:"s,attr"`
				V  string   `xml:"v"`
				Is xlsxText `xml:"is"`
			} `xml:"c"`
		}
		if err := dec.DecodeElement(&row, &se); err != nil {
			return nil, fmt.Errorf("%w: the sheet cannot be read", ErrFile)
		}
		// Rows Excel leaves out are empty rows.
		if n, err := strconv.Atoi(row.R); err == nil && n > len(out)+1 {
			if n > maxRows+2 {
				return nil, TooManyRows{Limit: maxRows}
			}
			for len(out) < n-1 {
				out = append(out, nil)
			}
		}
		if len(out) > maxRows {
			return nil, TooManyRows{Limit: maxRows}
		}
		var cells []string
		for i, c := range row.C {
			col := i
			if c.R != "" {
				col = columnIndex(c.R)
			}
			if col < 0 || col >= fileMaxColumns {
				return nil, fmt.Errorf("%w: more than %d columns", ErrFile, fileMaxColumns)
			}
			for len(cells) <= col {
				cells = append(cells, "")
			}
			cells[col] = cellText(c.T, c.V, c.Is, c.S, shared, dates, date1904)
		}
		out = append(out, cells)
	}
}

// cellText is a cell's value as text.
func cellText(t, v string, is xlsxText, style int, shared []string, dates []bool, date1904 bool) string {
	switch t {
	case "s":
		i, err := strconv.Atoi(v)
		if err != nil || i < 0 || i >= len(shared) {
			return ""
		}
		return shared[i]
	case "inlineStr":
		return is.String()
	case "str", "e":
		return v
	case "b":
		if v == "1" {
			return "true"
		}
		return "false"
	case "d":
		return v
	}
	if v == "" {
		return ""
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return v
	}
	if style >= 0 && style < len(dates) && dates[style] {
		return excelDate(f, date1904)
	}
	// Excel keeps 15 significant digits; what it saved past them is noise.
	f, _ = strconv.ParseFloat(strconv.FormatFloat(f, 'g', 15, 64), 64)
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// excelDate turns a date serial into YYYY-MM-DD, or a date and time when
// it has a time of day.
func excelDate(serial float64, date1904 bool) string {
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	if date1904 {
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	days := math.Floor(serial)
	secs := math.Round((serial - days) * 86400)
	t := epoch.AddDate(0, 0, int(days)).Add(time.Duration(secs) * time.Second)
	if secs == 0 {
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01-02 15:04:05")
}

// --- typing ---

var (
	fileNumberRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	filePhoneRe  = regexp.MustCompile(`^(\+?234|0)[789][01]\d{8}$`)
	fileDateRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// leadingZeroRe is a number written with a leading zero (an account
	// or phone number, not an amount).
	leadingZeroRe = regexp.MustCompile(`^-?0\d`)
)

// FileNumber is a number cell as written in a file, its thousands commas
// and naira sign taken off.
func FileNumber(s string) string {
	return strings.TrimSpace(strings.NewReplacer(",", "", "₦", "", "NGN", "", " ", "").Replace(s))
}

// TableFromFile is a table to make from a file (the web's tableFromCSV,
// ported): named from the file, a column per header, each typed from its
// values: numbers (commas and ₦ allowed, no leading zero), Nigerian phone
// numbers, dates as YYYY-MM-DD, else text. Phone and email columns are
// personal, as on the Tables page.
func TableFromFile(name string, header []string, rows [][]string) (string, string, []Column) {
	seen := map[string]bool{}
	var cols []Column
	for i, h := range header {
		key := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, h)
		if key == "id" || key == "createdat" || key == "updatedat" {
			continue
		}
		n := fileSnake(h)
		if CheckName("column", n) != nil {
			n = strings.TrimRight(("c_" + n)[:min(63, len(n)+2)], "_")
		}
		for base, k := n, 2; seen[n]; k++ {
			n = base + "_" + strconv.Itoa(k)
		}
		seen[n] = true
		var values []string
		for _, r := range rows {
			if i < len(r) && strings.TrimSpace(r[i]) != "" {
				values = append(values, strings.TrimSpace(r[i]))
			}
		}
		every := func(ok func(string) bool) bool {
			if len(values) == 0 {
				return false
			}
			for _, v := range values {
				if !ok(v) {
					return false
				}
			}
			return true
		}
		typ := "text"
		switch {
		case every(func(v string) bool {
			n := FileNumber(v)
			return fileNumberRe.MatchString(n) && !leadingZeroRe.MatchString(n)
		}):
			typ = "number"
		case every(func(v string) bool { return filePhoneRe.MatchString(strings.NewReplacer(" ", "", "-", "").Replace(v)) }):
			typ = "phone"
		case every(func(v string) bool { return fileDateRe.MatchString(v) }):
			typ = "date"
		}
		cols = append(cols, Column{Name: n, Title: strings.TrimSpace(h), Type: typ, Personal: PersonalByDefault(typ)})
	}
	return fileSnake(name), cutTitle(strings.TrimSpace(name)), cols
}

// cutTitle keeps a title within 120 bytes, on a character boundary.
func cutTitle(s string) string {
	for len(s) > 120 {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

// fileSnake is a name in lower snake case, as the web makes one.
func fileSnake(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			if under && b.Len() > 0 {
				b.WriteByte('_')
			}
			under = false
			b.WriteRune(r)
			continue
		}
		under = true
	}
	out := b.String()
	if out != "" && out[0] >= '0' && out[0] <= '9' {
		out = "c_" + out
	}
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "_")
	}
	if out == "" {
		return "column"
	}
	return out
}
