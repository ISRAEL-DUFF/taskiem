package media

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// zipOf builds a ZIP of the given parts, in order.
func zipOf(t *testing.T, parts ...[2]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, p := range parts {
		f, err := w.Create(p[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(p[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func contentTypes(main string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="` + main + `"/></Types>`
}

// Each new type is accepted and recognised from its bytes, whatever its
// name says (§1.2, §1.5).
func TestCheckChatUploadDocuments(t *testing.T) {
	for _, c := range []struct {
		file, name, ctype, doc, kept string
	}{
		{"note.docx", "Order.docx", TypeDOCX, "word", "Order.docx"},
		{"note.doc", "order.doc", TypeDOC, "word", "order.doc"},
		{"sheet.xlsx", "prices.xlsx", TypeXLSX, "excel", "prices.xlsx"},
		{"sheet.xls", "prices.xls", TypeXLS, "excel", "prices.xls"},
		{"slides.pptx", "pitch.pptx", TypePPTX, "powerpoint", "pitch.pptx"},
		{"slides.ppt", "pitch.ppt", TypePPT, "powerpoint", "pitch.ppt"},
		{"note.odt", "letter.odt", TypeODT, "opendocument", "letter.odt"},
		{"sheet.ods", "stock.ods", TypeODS, "opendocument", "stock.ods"},
		{"slides.odp", "talk.odp", TypeODP, "opendocument", "talk.odp"},
		// Renamed: the real extension is put back.
		{"sheet.xlsx", "prices.pdf", TypeXLSX, "excel", "prices.xlsx"},
		{"note.docx", "report", TypeDOCX, "word", "report.docx"},
		{"three.pdf", "invoice.pdf.exe", "application/pdf", "", "invoice.pdf"},
	} {
		u, err := CheckChatUpload(fixture(t, c.file), c.name)
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		if u.Type != c.ctype || u.Doc != c.doc || u.Name != c.kept || ChatKind(u.Type) != "file" {
			t.Errorf("%s as %s: %s %q %q", c.file, c.name, u.Type, u.Doc, u.Name)
		}
	}
	text := []byte("item,qty\nrice,2\n")
	for name, want := range map[string]string{"list.csv": TypeCSV, "notes.txt": TypeText, "notes.csv.txt": TypeText} {
		u, err := CheckChatUpload(text, name)
		if err != nil || u.Type != want {
			t.Errorf("%s: %s %v", name, u.Type, err)
		}
	}
	if u, _ := CheckChatUpload(text, "list.csv"); u.Doc != "csv" || u.Name != "list.csv" {
		t.Errorf("a CSV: %+v", u.Facts)
	}
	if u, _ := CheckChatUpload(text, "run.sh"); u.Name != "run.txt" || u.Doc != "text" {
		t.Errorf("a script as text: %q", u.Name)
	}
}

// These are refused: macros, password-protected Office files, a zip bomb,
// a renamed executable, wrong UTF-8 in a CSV, and a ZIP that is no
// document (§1.5).
func TestCheckChatUploadRefused(t *testing.T) {
	many := make([][2]string, 0, MaxDocumentEntries+1)
	many = append(many, [2]string{"[Content_Types].xml", contentTypes("application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml")})
	for i := range MaxDocumentEntries {
		many = append(many, [2]string{fmt.Sprintf("word/media/%d.xml", i), "x"})
	}
	var big bytes.Buffer
	w := zip.NewWriter(&big)
	f, _ := w.Create("[Content_Types].xml")
	_, _ = f.Write([]byte(contentTypes("application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml")))
	f, _ = w.Create("word/document.xml")
	_, _ = f.Write(make([]byte, MaxDocumentUnpacked+1))
	_ = w.Close()
	exe := append([]byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00\xff\xff\x00\x00"), make([]byte, 200)...)
	for name, c := range map[string]struct {
		data []byte
		want error
	}{
		"a .docm": {zipOf(t, [2]string{"[Content_Types].xml", contentTypes("application/vnd.ms-word.document.macroEnabled.main+xml")}), ErrMacros},
		"a .docx with a vbaProject.bin": {zipOf(t,
			[2]string{"[Content_Types].xml", contentTypes("application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml")},
			[2]string{"word/vbaProject.bin", "Attribute VB_Name"}), ErrMacros},
		"an .odt with Basic": {zipOf(t, [2]string{"mimetype", TypeODT}, [2]string{"Basic/Standard/Module1.xml", "Sub Main"}), ErrMacros},
		"a template":         {zipOf(t, [2]string{"[Content_Types].xml", contentTypes("application/vnd.openxmlformats-officedocument.wordprocessingml.template.main+xml")}), ErrFile},
		"a protected .docx":  {fixture(t, "protected.docx"), ErrProtected},
		"a protected .doc":   {fixture(t, "protected.doc"), ErrProtected},
		"a protected .xls":   {fixture(t, "protected.xls"), ErrProtected},
		"too many entries":   {zipOf(t, many...), ErrFile},
		"too large unpacked": {big.Bytes(), ErrFile},
		"a plain ZIP":        {zipOf(t, [2]string{"a.txt", "hello"}), ErrFile},
		"a renamed .exe":     {exe, ErrFile},
		"a CSV in Latin-1":   {[]byte("name,town\nRen\xe9,Ibadan\n"), ErrFile},
		"text with a NUL":    {[]byte("one\x00two three four five six"), ErrFile},
		"HTML":               {[]byte("<html><script>alert(1)</script></html>"), ErrFile},
	} {
		if _, err := CheckChatUpload(c.data, "invoice.docx"); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The name is cleaned: no path parts, control characters or right-to-left
// marks, at most 120 characters, and the real extension (§1.2).
func TestChatFileName(t *testing.T) {
	long := string(bytes.Repeat([]byte("a"), 300))
	for _, c := range []struct{ in, ctype, want string }{
		{`C:\Users\ade\Invoice.pdf`, "application/pdf", "Invoice.pdf"},
		{"../../etc/passwd", TypeText, "passwd.txt"},
		{"invoice\u202Efdp.exe", "application/pdf", "invoicefdp.pdf"},
		{"bad\x00\x07name.docx", TypeDOCX, "badname.docx"},
		{"photo.JPEG", "image/jpeg", "photo.JPEG"},
		{"photo.png", "image/jpeg", "photo.jpg"},
		{"", TypeXLSX, "file.xlsx"},
		{".xlsx", TypeXLSX, "file.xlsx"},
		{long + ".pdf", "application/pdf", long[:116] + ".pdf"},
	} {
		if got := ChatFileName(c.in, c.ctype); got != c.want {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
		}
	}
}
