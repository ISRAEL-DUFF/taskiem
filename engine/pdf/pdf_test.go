package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The embedded fonts have the naira sign, and a document is well formed:
// a header, objects at the offsets its cross-reference table gives, two
// embedded font files, a content stream that draws the text given and
// a trailer.
func TestDocumentIsWellFormed(t *testing.T) {
	reg, bld, err := Fonts()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []*Font{reg, bld} {
		if !f.Has('₦') || !f.Has('A') || !f.Has('é') || f.UnitsPerEm == 0 || f.Advance(f.Glyph('M')) <= 0 {
			t.Fatalf("font lacks what a receipt needs: naira %v, M %d", f.Has('₦'), f.Advance(f.Glyph('M')))
		}
	}
	d, err := New("Receipt RCT-2026T1-0079")
	if err != nil {
		t.Fatal(err)
	}
	d.Text(40, 60, 16, "Greenfield Court Academy", true)
	d.Text(40, 90, 11, "Receipt RCT-2026T1-0079", false)
	d.TextRight(380, 120, 12, "₦25,000.50", true)
	d.Line(40, 130, 380, 130, 0.5)
	d.Rect(40, 140, 100, 20, false)
	d.QR(40, 200, 120, 21, func(x, y int) bool { return (x+y)%2 == 0 })
	lines := d.Wrap("A long note that wraps over more than one line when the width is small", 10, false, 120)
	if len(lines) < 3 {
		t.Fatalf("wrapped into %d lines: %q", len(lines), lines)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "%PDF-1.4\n") || !strings.HasSuffix(s, "%%EOF\n") {
		t.Fatalf("header or trailer missing: %.20q ... %.20q", s, s[len(s)-20:])
	}
	if !strings.Contains(s, "/Title (Receipt RCT-2026T1-0079)") {
		t.Error("the title is not in the document's information")
	}
	if n := strings.Count(s, "/FontFile2"); n != 2 {
		t.Errorf("%d font files embedded, want 2", n)
	}
	if n := strings.Count(s, "/Encoding /Identity-H"); n != 2 {
		t.Errorf("%d Type 0 fonts, want 2", n)
	}
	// The cross-reference table points at each object.
	m := regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n$`).FindStringSubmatch(s)
	if m == nil {
		t.Fatal("no startxref")
	}
	xref, _ := strconv.Atoi(m[1])
	if !strings.HasPrefix(s[xref:], "xref\n") {
		t.Fatalf("startxref %d does not point at the table", xref)
	}
	entries := regexp.MustCompile(`(?m)^(\d{10}) 00000 n `).FindAllStringSubmatch(s[xref:], -1)
	if len(entries) < 12 {
		t.Fatalf("%d xref entries", len(entries))
	}
	for i, e := range entries {
		off, _ := strconv.Atoi(e[1])
		if want := fmt.Sprintf("%d 0 obj\n", i+1); !strings.HasPrefix(s[off:], want) {
			t.Fatalf("object %d: offset %d holds %.12q", i+1, off, s[off:])
		}
	}
	// The first page's content stream (object 4) draws the text as glyph
	// ids of the regular font.
	content := streamOf(t, out, 4)
	var hex strings.Builder
	for _, r := range "Receipt RCT-2026T1-0079" {
		fmt.Fprintf(&hex, "%04X", reg.Glyph(r))
	}
	if !strings.Contains(content, "<"+hex.String()+"> Tj") {
		t.Errorf("the receipt number is not drawn: %s", content)
	}
	if !strings.Contains(content, "/F2 12 Tf") || !strings.Contains(content, " re f") || !strings.Contains(content, " l S") {
		t.Errorf("bold text, rectangles or lines missing: %s", content)
	}
	// The ToUnicode map gives the naira sign back.
	if !strings.Contains(s, fmt.Sprintf("<%04X> <20A6>", bld.Glyph('₦'))) {
		// It is in a compressed stream: look there.
		found := false
		for id := 5; id <= 20; id++ {
			if strings.Contains(streamOf(t, out, id), fmt.Sprintf("<%04X> <20A6>", bld.Glyph('₦'))) {
				found = true
			}
		}
		if !found {
			t.Error("the naira sign is not in a ToUnicode map")
		}
	}
}

// streamOf inflates object id's stream, or returns "" when it has none.
func streamOf(t *testing.T, pdf []byte, id int) string {
	t.Helper()
	start := bytes.Index(pdf, []byte(fmt.Sprintf("\n%d 0 obj\n", id)))
	if start < 0 {
		return ""
	}
	rest := pdf[start:]
	i := bytes.Index(rest, []byte("stream\n"))
	j := bytes.Index(rest, []byte("\nendstream"))
	if i < 0 || j < 0 || j < i {
		return ""
	}
	r, err := zlib.NewReader(bytes.NewReader(rest[i+len("stream\n") : j]))
	if err != nil {
		t.Fatalf("object %d: %v", id, err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("object %d: %v", id, err)
	}
	return string(b)
}

func TestNumAndStrings(t *testing.T) {
	for v, want := range map[float64]string{0: "0", 1.5: "1.5", 100: "100", -0.004: "0", 12.345: "12.35"} {
		if got := num(v); got != want {
			t.Errorf("num(%v) = %q, want %q", v, got, want)
		}
	}
	if got := pdfString("a(b)\\"); got != `(a\(b\)\\)` {
		t.Errorf("escaped %q", got)
	}
	if got := pdfString("₦5"); got != "<FEFF20A60035>" {
		t.Errorf("utf-16 %q", got)
	}
}
