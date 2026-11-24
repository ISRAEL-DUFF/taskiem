package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Regression tests for hostile inputs found in review. Each must finish
// well within a second.

// timeScale stretches quick's limits where the code runs slower (under
// the race detector).
var timeScale time.Duration = 1

// quick fails t if f takes longer than limit, without waiting for it.
func quick(t *testing.T, limit time.Duration, f func()) {
	t.Helper()
	limit *= timeScale
	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		if d := time.Since(start); d > limit {
			t.Errorf("took %v, want under %v", d, limit)
		}
	case <-time.After(10 * limit):
		t.Fatalf("still running after %v", 10*limit)
	}
}

func allocatedMB() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.TotalAlloc >> 20
}

// A /W range whose first code is 2^63 as a float: converting it to int
// wrapped to MinInt64 and the loop ran 2^64 times.
func TestHostileCIDWidths(t *testing.T) {
	d := &pdfDoc{objs: map[int64]pdfEntry{}}
	for _, w := range []pdfArray{
		{float64(9223372036854775808.0), int64(9223372036854774784), int64(500)},
		{float64(-1), int64(10), int64(500)},
		{int64(0xFFFFFFFF - 2), pdfArray{int64(1), int64(2), int64(3), int64(4), int64(5)}},
		{int64(0), int64(maxCMapChars), int64(500), int64(maxCMapChars + 1), int64(2 * maxCMapChars), int64(500)},
	} {
		quick(t, time.Second, func() {
			if n := len(cidWidths(d, w)); n > maxCMapChars {
				t.Errorf("%v: %d widths", w[:2], n)
			}
		})
	}
	if got := cidWidths(d, pdfArray{int64(0xFFFFFFFF - 2), pdfArray{int64(1), int64(2), int64(3), int64(4)}}); len(got) != 3 {
		t.Errorf("widths past the last code: %v", got)
	}
}

// Floats a PDF cannot hold are read as 0.
func TestHostileNumbers(t *testing.T) {
	p := &pdfParser{b: []byte("+Inf 1e999 -1e999 1.5")}
	var got []any
	for {
		v, err := p.token()
		if err != nil {
			break
		}
		got = append(got, v)
	}
	if fmt.Sprint(got) != "[0 0 0 1.5]" {
		t.Errorf("got %v", got)
	}
}

// "N 0 obj [ <" repeated: every object ran to the end of the file, and
// the scan resumed just after it, so reading was quadratic.
func TestHostileQuadraticScan(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	for i := range 20000 {
		fmt.Fprintf(&b, "%d 0 obj [ <\n", i+1)
	}
	quick(t, time.Second, func() {
		if _, err := PDFText(b.Bytes()); err == nil {
			t.Error("no error")
		}
	})
}

// One unfiltered 1 MB stream named 300 times in /Contents.
func TestHostileContentsRepeated(t *testing.T) {
	big := bytes.Repeat([]byte("0 "), 512<<10)
	objs := []pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{body: "<< /Type /Page /Parent 2 0 R /Contents [" + strings.Repeat("4 0 R ", 300) + "] >>"},
		{body: "", stream: big},
	}
	pdf := buildPDF(objs, "/Root 1 0 R")
	before := allocatedMB()
	quick(t, time.Second, func() {
		if _, err := PDFText(pdf); !errors.Is(err, ErrUnreadable) {
			t.Errorf("err = %v", err)
		}
	})
	if a := allocatedMB() - before; a > 64 {
		t.Errorf("allocated %d MB", a)
	}
}

// An unfiltered 1 MB form XObject drawn 300 times.
func TestHostileFormRepeated(t *testing.T) {
	form := bytes.Repeat([]byte("0 "), 512<<10)
	objs := []pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{body: "<< /Type /Page /Parent 2 0 R /Resources << /XObject << /F 5 0 R >> >> /Contents 4 0 R >>"},
		{body: "", stream: []byte(strings.Repeat("/F Do\n", 300)), flate: true},
		{body: "/Type /XObject /Subtype /Form", stream: form},
	}
	pdf := buildPDF(objs, "/Root 1 0 R")
	quick(t, time.Second, func() { _, _ = PDFText(pdf) })
}

// Many pages sharing one content stream: each use is charged.
func TestHostileSharedContent(t *testing.T) {
	content := bytes.Repeat([]byte("0 "), 256<<10)
	kids := make([]string, 0, maxPDFPages)
	objs := []pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{},
		{body: "", stream: content, flate: true},
	}
	for i := range maxPDFPages {
		kids = append(kids, fmt.Sprintf("%d 0 R", i+4))
		objs = append(objs, pdfObj{body: "<< /Type /Page /Parent 2 0 R /Contents 3 0 R >>"})
	}
	objs[1] = pdfObj{body: "<< /Type /Pages /Kids [" + strings.Join(kids, " ") + "] >>"}
	pdf := buildPDF(objs, "/Root 1 0 R")
	quick(t, time.Second, func() { _, _ = PDFText(pdf) })
}

// A stream with no filter counts against the decode budget.
func TestHostileUnfilteredBudget(t *testing.T) {
	d := &pdfDoc{decoded: maxDecodedBytes - 10, streams: map[*pdfStream]decodedStream{}}
	s := &pdfStream{dict: pdfDict{}, raw: make([]byte, 100)}
	if _, err := d.decode(s); !errors.Is(err, errPDFLimit) {
		t.Errorf("err = %v", err)
	}
	d = &pdfDoc{decoded: maxDecodedBytes - 150, streams: map[*pdfStream]decodedStream{}}
	if _, err := d.decode(s); err != nil {
		t.Fatal(err)
	}
	if _, err := d.decode(s); !errors.Is(err, errPDFLimit) {
		t.Errorf("reuse not charged: err = %v", err)
	}
}

// A predictor row of 64 MB over a few bytes of data.
func TestHostilePredictor(t *testing.T) {
	d := &pdfDoc{}
	parms := pdfDict{"Predictor": int64(12), "Columns": int64(1 << 20), "Colors": int64(32), "BitsPerComponent": int64(16)}
	before := allocatedMB()
	out, err := unpredict([]byte{2, 1, 2, 3}, parms, d)
	if err != nil || len(out) != 0 {
		t.Errorf("out %d bytes, err %v", len(out), err)
	}
	if a := allocatedMB() - before; a > 8 {
		t.Errorf("allocated %d MB", a)
	}
	if _, err := unpredict([]byte{0}, pdfDict{"Predictor": int64(12), "Columns": float64(0) / zero()}, d); err == nil {
		t.Error("NaN columns accepted")
	}
}

func zero() float64 { return 0 }

// One ToUnicode entry mapping a code to 32K characters, shown 20000 times.
func TestHostileCMapAmplify(t *testing.T) {
	dest := strings.Repeat("0041", 32<<10)
	cm := "begincmap 1 begincodespacerange <00> <FF> endcodespacerange 1 beginbfchar <01> <" + dest + "> endbfchar endcmap"
	tj := "BT /F1 12 Tf (" + strings.Repeat("\x01", 20000) + ") Tj ET"
	objs := []pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{body: "<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>"},
		{body: "", stream: []byte(tj), flate: true},
		{body: "<< /Type /Font /Subtype /Type1 /ToUnicode 6 0 R >>"},
		{body: "", stream: []byte(cm), flate: true},
	}
	pdf := buildPDF(objs, "/Root 1 0 R")
	before := allocatedMB()
	quick(t, time.Second, func() {
		txt, err := PDFText(pdf)
		if err != nil {
			t.Fatal(err)
		}
		if len(txt) > 20000*maxCMapDest || len(txt) > MaxText {
			t.Errorf("text is %d bytes", len(txt))
		}
	})
	if a := allocatedMB() - before; a > 64 {
		t.Errorf("allocated %d MB", a)
	}
	if got := cmapDest(pdfString(strings.Repeat("\x00A", 100))); got != strings.Repeat("A", maxCMapDest) {
		t.Errorf("cmapDest = %q", got)
	}
}

// invisiblePDF is a one-page PDF with content drawn in Helvetica.
func invisiblePDF(content string) []byte {
	return buildPDF([]pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{body: "<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>"},
		{body: "", stream: []byte(content), flate: true},
		{body: "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"},
	}, "/Root 1 0 R")
}

// In a PDF with visible text, text drawn in render mode 3 or 7 is
// invisible and left out.
func TestPDFInvisibleText(t *testing.T) {
	got, err := PDFText(invisiblePDF(`BT /F1 12 Tf 72 700 Td (Our opening hours are) Tj 3 Tr ( SECRET-ONE) Tj 0 Tr ( nine to five.) Tj ET
q BT 7 Tr /F1 12 Tf 72 680 Td (SECRET-TWO) Tj ET Q
BT /F1 12 Tf 72 660 Td (Closed on public holidays.) Tj ET`))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Our opening hours are nine to five.\nClosed on public holidays."; got != want {
		t.Errorf("text:\n%q\nwant\n%q", got, want)
	}
}

// A PDF whose only text is invisible is a scan with an OCR layer: that
// text is returned rather than ErrScanned.
func TestPDFOCRLayer(t *testing.T) {
	got, err := PDFText(invisiblePDF(`q 612 0 0 792 0 0 cm /Im0 Do Q
BT 3 Tr /F1 12 Tf 72 700 Td (Scanned invoice number 42) Tj 0 -14 Td (Total due: 5,000 naira) Tj ET`))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Scanned invoice number 42\nTotal due: 5,000 naira"; got != want {
		t.Errorf("text:\n%q\nwant\n%q", got, want)
	}
}

// A context that is done stops the readers with ErrUnreadable.
func TestExtractTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	objs := []pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{body: "<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>"},
		{body: "", stream: []byte("BT /F1 12 Tf (Some text that is long enough to count) Tj ET")},
	}
	pdf := buildPDF(objs, "/Root 1 0 R")
	for name, data := range map[string][]byte{"pdf": pdf, "docx": makeDOCX(t, para(run("Hello there, this is text.")))} {
		if _, err := FileTextContext(ctx, data); !errors.Is(err, ErrUnreadable) || !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// The scan itself notices, not only the check before it.
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	for i := range 2000 {
		fmt.Fprintf(&b, "%d 0 obj << >> endobj\n", i+1)
	}
	if _, err := parsePDF(ctx, b.Bytes()); !errors.Is(err, errPDFTimeout) {
		t.Errorf("parsePDF: err = %v", err)
	}
	var x bytes.Buffer
	for range 3000 {
		x.WriteString(para(run("x")))
	}
	if _, err := documentXMLText(ctx, []byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`+x.String()+`</w:body></w:document>`)); !errors.Is(err, context.Canceled) {
		t.Errorf("documentXMLText: err = %v", err)
	}
}

// Runs marked w:vanish are hidden and left out; a vanished paragraph mark
// is not hidden text.
func TestDOCXHiddenText(t *testing.T) {
	body := para(run("Visible ")+`<w:r><w:rPr><w:vanish/></w:rPr><w:t>SECRET-ONE</w:t><w:tab/></w:r>`+run("words.")) +
		`<w:p><w:pPr><w:rPr><w:vanish/></w:rPr></w:pPr>` + run("Paragraph with a hidden mark.") + `</w:p>` +
		para(`<w:r><w:rPr><w:vanish w:val="false"/></w:rPr><w:t>Not hidden after all.</w:t></w:r>`) +
		para(`<w:r><w:rPr><w:b/><w:vanish w:val="1"/></w:rPr><w:t>SECRET-TWO</w:t></w:r>`)
	got, err := DOCXText(makeDOCX(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Visible words.\nParagraph with a hidden mark.\nNot hidden after all."; got != want {
		t.Errorf("text:\n%q\nwant\n%q", got, want)
	}
}

// A Crawl-delay, Sitemap or unknown line ends a group's agents.
func TestRobotsGroups(t *testing.T) {
	for _, c := range []struct {
		txt, path string
		want      bool
	}{
		{"User-agent: TaskiemBot\nCrawl-delay: 5\nUser-agent: *\nDisallow: /\n", "/a", true},
		{"User-agent: TaskiemBot\nSitemap: https://x/s.xml\nUser-agent: *\nDisallow: /\n", "/a", true},
		{"User-agent: TaskiemBot\nFoo: bar\nUser-agent: *\nDisallow: /\n", "/a", true},
		{"User-agent: OtherBot\nUser-agent: TaskiemBot\nDisallow: /a\n", "/a", false},
		{"User-agent: *\nCrawl-delay: 5\nUser-agent: OtherBot\nDisallow: /\n", "/a", true},
		{"User-agent: *\nCrawl-delay: 5\nDisallow: /a\n", "/a", false},
	} {
		r := parseRobots(strings.NewReader(c.txt))
		if got := r.allowed(mustURL(t, "https://example.com"+c.path)); got != c.want {
			t.Errorf("%q %s: allowed = %v", c.txt, c.path, got)
		}
	}
}

// Rules and paths are compared with one percent-encoding.
func TestRobotsEncoding(t *testing.T) {
	for _, c := range []struct {
		rule, path string
		want       bool // disallowed
	}{
		{"/café", "/caf%C3%A9/menu", true},
		{"/caf%C3%A9", "/caf%c3%a9", true},
		{"/caf%c3%a9", "/café", true},
		{"/%7Euser", "/~user/x", true},
		{"/~user", "/%7euser", true},
		{"/a%2Fb", "/a/b", false},
		{"/a/b", "/a%2Fb", false},
		{"/*.php$", "/x.php", true},
		{"/a$b", "/a$b", true},
		{"/private", "/Private", false},
	} {
		r := parseRobots(strings.NewReader("User-agent: *\nDisallow: " + c.rule + "\n"))
		u := mustURL(t, "https://example.com"+c.path)
		if got := !r.allowed(u); got != c.want {
			t.Errorf("Disallow %q, %s: disallowed = %v (%q vs %q)", c.rule, c.path, got, robotsEscape(c.rule, true), robotsEscape(u.EscapedPath(), false))
		}
	}
}

// handlerTransport serves every request, whatever its host, from h.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.h.ServeHTTP(w, r)
	resp := w.Result()
	resp.Request = r
	return resp, nil
}

// twinSite redirects every origin but serves to serves, and serves pages
// there.
func twinSite(serves string, requested *[]string) *http.Client {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.URL.Scheme + "://" + r.URL.Host
		*requested = append(*requested, origin+r.URL.Path)
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if origin != serves {
			http.Redirect(w, r, serves+r.URL.Path, http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><p>Page `+r.URL.Path+`</p><a href="/b">B</a> <a href="http://example.com/c">C</a></body></html>`)
	})
	return &http.Client{Transport: handlerTransport{h}}
}

func TestCrawlTwinRedirect(t *testing.T) {
	ctx := context.Background()
	var req []string

	// Crawl itself never follows the start page to a twin.
	pages, errs := Crawl(ctx, twinSite("https://www.example.com", &req), "http://example.com/", 5)
	if len(pages) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "off the site") {
		t.Errorf("Crawl: %d pages, errors %v", len(pages), errs)
	}
	for _, r := range req {
		if strings.HasPrefix(r, "https://www.example.com") {
			t.Errorf("Crawl requested %s", r)
		}
	}

	// CrawlWith follows it to an allowed twin, and stays there.
	req = nil
	pages, errs = CrawlWith(ctx, twinSite("https://www.example.com", &req), "http://example.com/", 5,
		CrawlOptions{AllowOrigins: []string{"https://www.example.com"}})
	var got []string
	for _, p := range pages {
		got = append(got, p.URL)
	}
	if strings.Join(got, " ") != "https://www.example.com/ https://www.example.com/b" || len(errs) != 0 {
		t.Errorf("CrawlWith: pages %v, errors %v", got, errs)
	}
	for _, r := range req {
		if r == "http://example.com/c" {
			t.Error("followed a link back to the start origin")
		}
	}

	// Not an allowed origin, not a twin, or a step down to http: refused.
	for _, c := range []struct{ start, serves, allow string }{
		{"http://example.com/", "https://www.example.com", "https://example.com"},
		{"http://example.com/", "https://evil.example", "https://evil.example"},
		{"https://example.com/", "http://www.example.com", "http://www.example.com"},
		{"https://example.com/", "http://example.com", "http://example.com"},
	} {
		req = nil
		pages, errs := CrawlWith(ctx, twinSite(c.serves, &req), c.start, 5, CrawlOptions{AllowOrigins: []string{c.allow}})
		if len(pages) != 0 || len(errs) != 1 {
			t.Errorf("%s to %s allowing %s: %d pages, errors %v", c.start, c.serves, c.allow, len(pages), errs)
		}
		for _, r := range req {
			if strings.HasPrefix(r, c.serves) {
				t.Errorf("%s to %s: requested %s", c.start, c.serves, r)
			}
		}
	}
}
