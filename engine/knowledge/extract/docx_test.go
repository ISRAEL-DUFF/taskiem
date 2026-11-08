package extract

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// makeZip builds a zip archive from name/content pairs.
func makeZip(t *testing.T, files ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i+1 < len(files); i += 2 {
		w, err := zw.Create(files[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[i+1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makeDOCX wraps body XML in a minimal Word document.
func makeDOCX(t *testing.T, body string) []byte {
	t.Helper()
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><w:body>` + body + `</w:body></w:document>`
	return makeZip(t,
		"[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"word/document.xml", doc,
	)
}

func para(runs string) string { return "<w:p>" + runs + "</w:p>" }
func run(text string) string  { return `<w:r><w:t xml:space="preserve">` + text + `</w:t></w:r>` }

func TestDOCXText(t *testing.T) {
	body := `<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr>` + run("Opening hours") + `</w:p>` +
		para(run("Monday")+`<w:r><w:tab/></w:r>`+run("9am &amp; 5pm")) +
		para(run("Split ")+run("across runs")+`<w:r><w:br/></w:r>`+run("second line")) +
		para(`<w:del><w:r><w:delText>deleted words</w:delText></w:r></w:del>`+run("kept")) +
		para("") +
		`<w:tbl><w:tr><w:tc>` + para(run("Item")) + `</w:tc><w:tc>` + para(run("Price")) + `</w:tc></w:tr>` +
		`<w:tr><w:tc>` + para(run("Haircut")) + para(run("(men)")) + `</w:tc><w:tc>` + para(run("₦3,000")) + `</w:tc></w:tr>` +
		`<w:tr><w:tc>` + para("") + `</w:tc><w:tc>` + para("") + `</w:tc></w:tr></w:tbl>` +
		para(run("After the table")) +
		para(`<a:t>drawing text is not body text</a:t>`)
	got, err := DOCXText(makeDOCX(t, body))
	if err != nil {
		t.Fatal(err)
	}
	want := "Opening hours\nMonday\t9am & 5pm\nSplit across runs\nsecond line\nkept\nItem | Price\nHaircut (men) | ₦3,000\nAfter the table"
	if got != want {
		t.Errorf("text:\n%q\nwant\n%q", got, want)
	}
}

func TestDOCXRefused(t *testing.T) {
	// A document.xml of 30 MB of spaces: over the entry limit.
	big := makeZip(t, "word/document.xml", strings.Repeat(" ", 30<<20))
	if _, err := DOCXText(big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("large entry: err = %v, want ErrTooLarge", err)
	}
	// 8 MB that compresses a thousandfold: a zip bomb by ratio.
	bomb := makeZip(t, "word/document.xml", "<w:document>"+strings.Repeat("a", 8<<20)+"</w:document>")
	if _, err := DOCXText(bomb); !errors.Is(err, ErrTooLarge) {
		t.Errorf("bomb: err = %v, want ErrTooLarge", err)
	}
	for name, b := range map[string][]byte{
		"not a zip":     []byte("PK\x03\x04 nonsense word/document.xml"),
		"no document":   makeZip(t, "word/other.xml", "<x/>"),
		"broken xml":    makeZip(t, "word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p>`),
		"not word xml":  makeZip(t, "word/document.xml", `<document><p>hi</p></document>`),
		"old .doc file": append([]byte("\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"), make([]byte, 512)...),
	} {
		if _, err := DOCXText(b); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: err = %v, want ErrUnreadable", name, err)
		}
	}
}

func TestFileText(t *testing.T) {
	docx := makeDOCX(t, para(run("We are open on Sundays.")))
	if got, err := FileText(docx); err != nil || got != "We are open on Sundays." {
		t.Errorf("docx: %q, %v", got, err)
	}
	// A plain zip, a text file and an image are none of ours.
	for name, b := range map[string][]byte{
		"zip":   makeZip(t, "notes.txt", "hello"),
		"text":  []byte("just some text"),
		"png":   []byte("\x89PNG\r\n\x1a\n...."),
		"empty": nil,
	} {
		if _, err := FileText(b); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: err = %v, want ErrUnreadable", name, err)
		}
	}
	if _, err := FileText(make([]byte, MaxFileBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: err = %v", err)
	}
	// The PDF sniff accepts junk before the header, as readers do.
	pdfDoc := buildPDF([]pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{body: "<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>"},
		{body: "", stream: []byte("BT /F1 12 Tf 10 10 Td (Our shop is on Allen Avenue, Ikeja.) Tj ET")},
	}, "/Root 1 0 R")
	if got, err := FileText(append([]byte("junk\n"), pdfDoc...)); err != nil || got != "Our shop is on Allen Avenue, Ikeja." {
		t.Errorf("pdf: %q, %v", got, err)
	}
}

func TestTruncate(t *testing.T) {
	para := strings.Repeat("word ", 1000) + "\n\n"
	s := strings.Repeat(para, MaxText/len(para)+5)
	got := truncate(s)
	if len(got) > MaxText || !strings.HasSuffix(got, "word") || strings.HasSuffix(got, "\n") {
		t.Errorf("truncate: %d bytes, ends %q", len(got), got[len(got)-10:])
	}
	if (len(got)+3)%len(para) != 0 { // each paragraph ends "word \n\n"; the cut trims the space
		t.Errorf("truncate did not cut at a paragraph: %d", len(got))
	}
	if truncate("short") != "short" {
		t.Error("short text changed")
	}
}
