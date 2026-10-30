package datatables_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/datatables"
)

// workbook builds an .xlsx with the given parts over a minimal skeleton.
func workbook(t *testing.T, sheet string, extra map[string]string) []byte {
	t.Helper()
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"xl/workbook.xml": `<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets><sheet name="Owed" sheetId="1" r:id="rId1"/><sheet name="Other" sheetId="2" r:id="rId2"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId2" Target="worksheets/sheet2.xml"/><Relationship Id="rId1" Target="worksheets/first.xml"/></Relationships>`,
		"xl/sharedStrings.xml": `<?xml version="1.0"?><sst><si><t>Name</t></si><si><t>Phone</t></si><si><t>Amount</t></si><si><t>Due</t></si>` +
			`<si><r><t>Ada </t></r><r><t>Obi</t></r></si><si><t>=HYPERLINK("x")</t></si></sst>`,
		"xl/styles.xml": `<?xml version="1.0"?><styleSheet><numFmts><numFmt numFmtId="164" formatCode="dd/mm/yyyy"/><numFmt numFmtId="165" formatCode="&quot;₦&quot;#,##0"/></numFmts>` +
			`<cellXfs><xf numFmtId="0"/><xf numFmtId="164"/><xf numFmtId="165"/><xf numFmtId="14"/></cellXfs></styleSheet>`,
		"xl/worksheets/sheet2.xml": `<worksheet><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>wrong sheet</t></is></c></row></sheetData></worksheet>`,
		"xl/worksheets/first.xml":  sheet,
	}
	for k, v := range extra {
		parts[k] = v
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadXLSX(t *testing.T) {
	sheet := `<worksheet><sheetData>` +
		`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c><c r="D1" t="s"><v>3</v></c></row>` +
		// Rich text; a number shown with a currency format; a date serial;
		// a formula's saved value, never evaluated.
		`<row r="2"><c r="A2" t="s"><v>4</v></c><c r="B2" t="inlineStr"><is><t>08031234567</t></is></c><c r="C2" s="2"><f>SUM(1,2)</f><v>5000</v></c><c r="D2" s="1"><v>46327</v></c></row>` +
		// A skipped row is an empty row, left out; a cell that starts like
		// a formula stays text; a missing cell is empty.
		`<row r="4"><c r="A4" t="s"><v>5</v></c><c r="C4"><v>0.30000000000000004</v></c><c r="D4" s="3"><v>46327.5</v></c></row>` +
		`</sheetData></worksheet>`
	header, rows, err := datatables.ReadFile("xlsx", workbook(t, sheet, nil), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(header, "|") != "Name|Phone|Amount|Due" {
		t.Errorf("header: %q", header)
	}
	want := [][]string{{"Ada Obi", "08031234567", "5000", "2026-11-01"}, {`=HYPERLINK("x")`, "", "0.3", "2026-11-01 12:00:00"}}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Errorf("rows:\n got %q\nwant %q", rows, want)
	}

	// Refused: not a workbook, a zip bomb, too many rows.
	if _, _, err := datatables.ReadFile("xlsx", []byte("name,phone\n"), 5000); !errors.Is(err, datatables.ErrFile) {
		t.Errorf("not a zip: %v", err)
	}
	bomb := workbook(t, `<worksheet><sheetData/></worksheet>`, map[string]string{"xl/sharedStrings.xml": "<sst><si><t>" + strings.Repeat("A", 40<<20) + "</t></si></sst>"})
	if len(bomb) > 1<<20 {
		t.Fatalf("the bomb is not small: %d", len(bomb))
	}
	if _, _, err := datatables.ReadFile("xlsx", bomb, 5000); !errors.Is(err, datatables.ErrFile) || !strings.Contains(err.Error(), "too large") && !strings.Contains(err.Error(), "expands") {
		t.Errorf("bomb: %v", err)
	}
	var big strings.Builder
	big.WriteString(`<worksheet><sheetData><row r="1"><c t="inlineStr"><is><t>n</t></is></c></row>`)
	for i := 2; i <= 6002; i++ {
		fmt.Fprintf(&big, `<row r="%d"><c><v>%d</v></c></row>`, i, i)
	}
	big.WriteString(`</sheetData></worksheet>`)
	var tm datatables.TooManyRows
	if _, _, err := datatables.ReadFile("xlsx", workbook(t, big.String(), nil), 5000); !errors.As(err, &tm) || tm.Limit != 5000 {
		t.Errorf("6,000 rows: %v", err)
	}
}

func TestReadCSVFile(t *testing.T) {
	header, rows, err := datatables.ReadFile("csv", []byte("\uFEFFName, Phone ,Amount\nAda,0803 123 4567,\"5,000\"\n,,\nBola,+2348031234567,300\n"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(header, "|") != "Name|Phone|Amount" || len(rows) != 2 || rows[0][2] != "5,000" || rows[1][0] != "Bola" {
		t.Errorf("csv: %q %q", header, rows)
	}
	for _, bad := range []string{"", "Name,\nAda,1\n", "Name\nAda,extra\n", strings.Repeat("h", 121) + "\nx\n"} {
		if _, _, err := datatables.ReadFile("csv", []byte(bad), 5000); !errors.Is(err, datatables.ErrFile) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	var many strings.Builder
	many.WriteString("n\n")
	for i := range 6000 {
		fmt.Fprintf(&many, "%d\n", i)
	}
	var tm datatables.TooManyRows
	if _, _, err := datatables.ReadFile("csv", []byte(many.String()), 5000); !errors.As(err, &tm) {
		t.Errorf("6,000 rows: %v", err)
	}
	if k := datatables.FileKind("Debtors.XLSX", ""); k != "xlsx" {
		t.Errorf("kind: %q", k)
	}
	if k := datatables.FileKind("old.xls", "application/vnd.ms-excel"); k != "" {
		t.Errorf("old Excel: %q", k)
	}
}

// The web's tableFromCSV case, ported.
func TestTableFromFile(t *testing.T) {
	name, title, cols := datatables.TableFromFile("My customers", []string{"Name", "Phone number", "Amount owed", "Joined", "id", "Account"}, [][]string{
		{"Ada", "0803 123 4567", "5,000", "2026-01-05", "1", "0123456789"},
		{"Bola", "+2348031234567", "₦300.50", "2026-02-01", "2", "0098765432"},
	})
	if name != "my_customers" || title != "My customers" {
		t.Errorf("name: %q %q", name, title)
	}
	got := ""
	for _, c := range cols {
		got += fmt.Sprintf("%s:%s:%s:%v ", c.Name, c.Title, c.Type, c.Personal)
	}
	if got != "name:Name:text:false phone_number:Phone number:phone:true amount_owed:Amount owed:number:false joined:Joined:date:false account:Account:text:false " {
		t.Errorf("columns: %s", got)
	}
	if err := datatables.CheckColumns(cols, 0); err != nil {
		t.Errorf("checked: %v", err)
	}
	if _, title, _ := datatables.TableFromFile(strings.Repeat("é", 100), []string{"a"}, [][]string{{"x"}}); len(title) > 120 || !utf8.ValidString(title) {
		t.Errorf("a long title: %d bytes", len(title))
	}
	if datatables.FileNumber("₦5,000") != "5000" {
		t.Errorf("number: %q", datatables.FileNumber("₦5,000"))
	}
}
