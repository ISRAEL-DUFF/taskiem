package extract

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/pdf"
)

// pdfObj is one object for buildPDF: its body, and stream data if any
// (compressed with Flate when flate is set).
type pdfObj struct {
	body   string
	stream []byte
	flate  bool
}

func deflate(b []byte) []byte {
	var zb bytes.Buffer
	z := zlib.NewWriter(&zb)
	_, _ = z.Write(b)
	_ = z.Close()
	return zb.Bytes()
}

// buildPDF writes objects 1..n in order with a classic xref table and a
// trailer; trailer is the trailer dictionary's body.
func buildPDF(objs []pdfObj, trailer string) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.5\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", i+1)
		if o.stream != nil {
			data := o.stream
			filter := ""
			if o.flate {
				data = deflate(data)
				filter = " /Filter /FlateDecode"
			}
			fmt.Fprintf(&out, "<< %s /Length %d%s >>\nstream\n", o.body, len(data), filter)
			out.Write(data)
			out.WriteString("\nendstream")
		} else {
			out.WriteString(o.body)
		}
		out.WriteString("\nendobj\n")
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d %s >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, trailer, xref)
	return out.Bytes()
}

// TestPDFRoundTrip reads back a receipt written by the repository's own
// PDF writer: Type 0 fonts with Identity-H codes, a ToUnicode map and
// Flate streams.
func TestPDFRoundTrip(t *testing.T) {
	d, err := pdf.New("Receipt")
	if err != nil {
		t.Fatal(err)
	}
	d.Text(40, 60, 14, "Receipt for Ada Obi", true)
	d.Text(40, 90, 10, "School fees, first term", false)
	d.TextRight(380, 90, 10, "₦45,000.00", false)
	d.Text(40, 110, 10, "Paid by bank transfer on 3 March 2026.", false)
	d.NewPage()
	d.Text(40, 60, 10, "Thank you. Keep this receipt for your records; it is your proof of payment.", false)
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got, err := PDFText(b)
	if err != nil {
		t.Fatal(err)
	}
	want := "Receipt for Ada Obi\nSchool fees, first term ₦45,000.00\nPaid by bank transfer on 3 March 2026.\n\nThank you. Keep this receipt for your records; it is your proof of payment."
	if got != want {
		t.Errorf("text:\n%q\nwant\n%q", got, want)
	}
	if got2, err := FileText(b); err != nil || got2 != got {
		t.Errorf("FileText = %q, %v", got2, err)
	}
}

// helloCMap maps codes 1 to 5 to "Hello" and 6 to a space, by bfchar and
// bfrange (both destination forms).
const helloCMap = `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CMapName /Test def
1 begincodespacerange
<00> <FF>
endcodespacerange
2 beginbfchar
<01> <0048>
<06> <0020>
endbfchar
2 beginbfrange
<02> <02> <0065>
<03> <05> [<006C> <006C> <006F>]
endbfrange
3 beginbfchar
<07> <0057>
<08> <0072>
<09> <0064>
endbfchar
1 beginbfrange
<0A> <0B> <FB01>
endbfrange
endcmap
CMapName currentdict /CMap defineresource pop
end
end`

// TestPDFHandWritten covers a Flate content stream, a ToUnicode CMap with
// bfchar and bfrange, a WinAnsi font with /Differences, TJ kerning as a
// word space, T* lines and text inside a form XObject.
func TestPDFHandWritten(t *testing.T) {
	content := []byte(`BT /F1 12 Tf 72 700 Td [<0102030405> -400 <0705080409>] TJ ET
BT /F2 12 Tf 72 680 Td 14 TL (Opening hours: 9am to 5pm) Tj T* (Caf\351 open Sundays \(closed holidays\)) Tj
(A) Tj ET
q 1 0 0 1 0 -100 cm /X1 Do Q
BI /W 4 /H 4 /BPC 8 /CS /G ID ` + "\x00EI\xff\x01\x02 junk" + ` EI
BT /F1 12 Tf 72 450 Td <0A0B> Tj ET`)
	form := []byte(`BT /F2 10 Tf 72 600 Td (Delivery is free across Lagos.) Tj ET`)
	objs := []pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 /Resources << /Font << /F1 5 0 R /F2 7 0 R >> /XObject << /X1 8 0 R >> >> >>"},
		{body: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>"},
		{body: "", stream: content, flate: true},
		{body: "<< /Type /Font /Subtype /Type1 /BaseFont /Custom /FirstChar 1 /LastChar 11 /Widths [700 500 250 250 550 300 900 350 550 600 600] /ToUnicode 6 0 R >>"},
		{body: "", stream: []byte(helloCMap), flate: true},
		{body: "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding << /BaseEncoding /WinAnsiEncoding /Differences [65 /quoteright] >> >>"},
		{body: "/Type /XObject /Subtype /Form /BBox [0 0 612 792] /Resources << /Font << /F2 7 0 R >> >>", stream: form, flate: true},
	}
	b := buildPDF(objs, "/Root 1 0 R")
	got, err := PDFText(b)
	if err != nil {
		t.Fatal(err)
	}
	want := "Hello World\nOpening hours: 9am to 5pm\nCafé open Sundays (closed holidays)’\nDelivery is free across Lagos.\nﬁﬂ"
	if got != want {
		t.Errorf("text:\n%q\nwant\n%q", got, want)
	}
}

// TestPDFObjectStream reads objects packed in an object stream, found
// through an xref stream, with no classic trailer.
func TestPDFObjectStream(t *testing.T) {
	packed := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}
	var hdr, body strings.Builder
	for i, num := range []int{1, 2, 3, 5} {
		fmt.Fprintf(&hdr, "%d %d ", num, body.Len())
		body.WriteString(packed[i] + "\n")
	}
	stm := hdr.String() + body.String()
	content := []byte("BT /F1 11 Tf 50 700 Td (We sell fresh bread every morning from 7am.) Tj ET")
	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n")
	fmt.Fprintf(&out, "4 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", len(deflate(content)))
	out.Write(deflate(content))
	out.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&out, "6 0 obj\n<< /Type /ObjStm /N 4 /First %d /Length %d /Filter /FlateDecode >>\nstream\n", len(hdr.String()), len(deflate([]byte(stm))))
	out.Write(deflate([]byte(stm)))
	out.WriteString("\nendstream\nendobj\n")
	xref := deflate([]byte{0, 0, 0, 0})
	fmt.Fprintf(&out, "7 0 obj\n<< /Type /XRef /Size 8 /W [1 2 1] /Root 1 0 R /Length %d /Filter /FlateDecode >>\nstream\n", len(xref))
	out.Write(xref)
	out.WriteString("\nendstream\nendobj\nstartxref\n0\n%%EOF\n")
	got, err := PDFText(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got != "We sell fresh bread every morning from 7am." {
		t.Errorf("text = %q", got)
	}
}

func TestPDFScanned(t *testing.T) {
	img := bytes.Repeat([]byte{0x80}, 64)
	objs := []pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{body: "<< /Type /Pages /Kids [3 0 R 6 0 R] /Count 2 >>"},
		{body: "<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>"},
		{body: "", stream: []byte("q 612 0 0 792 0 0 cm /Im1 Do Q"), flate: true},
		{body: "/Type /XObject /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceGray /BitsPerComponent 8", stream: img},
		{body: "<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>"},
	}
	_, err := PDFText(buildPDF(objs, "/Root 1 0 R"))
	if !errors.Is(err, ErrScanned) {
		t.Fatalf("err = %v, want ErrScanned", err)
	}
}

func TestPDFUnreadable(t *testing.T) {
	good := buildPDF([]pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{body: "<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>"},
		{body: "", stream: []byte("BT /F1 12 Tf (secret) Tj ET")},
		{body: "<< /Filter /Standard /V 1 /R 2 /O (x) /U (y) /P -4 >>"},
	}, "/Root 1 0 R /Encrypt 5 0 R")
	cases := map[string][]byte{
		"encrypted": good,
		"garbage":   append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("garbage ) ( [ << "), 100)...),
		"not a pdf": []byte("hello"),
		"no pages":  buildPDF([]pdfObj{{body: "<< /Type /Catalog >>"}}, "/Root 1 0 R"),
	}
	for name, b := range cases {
		_, err := PDFText(b)
		if !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: err = %v, want ErrUnreadable", name, err)
		}
	}
	if _, err := PDFText(good); err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("encrypted: err = %v, want a plain message", err)
	}
	// Text drawn with a composite font that has no ToUnicode map.
	noMap := buildPDF([]pdfObj{
		{body: "<< /Type /Catalog /Pages 2 0 R >>"},
		{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{body: "<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>"},
		{body: "", stream: []byte("BT /F1 12 Tf <00010002000300040005000600070008000900100011001200130014001500160017001800190020> Tj ET")},
		{body: "<< /Type /Font /Subtype /Type0 /BaseFont /X /Encoding /Identity-H /DescendantFonts [] >>"},
	}, "/Root 1 0 R")
	if _, err := PDFText(noMap); !errors.Is(err, ErrUnreadable) {
		t.Errorf("no character map: err = %v, want ErrUnreadable", err)
	}
}

// TestPDFHostile checks that loops in the file and decompression bombs
// end, quickly, in an error or text and never a panic.
func TestPDFHostile(t *testing.T) {
	bomb := bytes.Repeat([]byte("0 0 m "), 8<<20) // 48 MB of operators
	cases := map[string][]byte{
		"page tree cycle": buildPDF([]pdfObj{
			{body: "<< /Type /Catalog /Pages 2 0 R >>"},
			{body: "<< /Type /Pages /Kids [2 0 R 3 0 R] /Count 1 >>"},
			{body: "<< /Type /Pages /Kids [2 0 R] /Count 1 >>"},
		}, "/Root 1 0 R"),
		"form draws itself": buildPDF([]pdfObj{
			{body: "<< /Type /Catalog /Pages 2 0 R >>"},
			{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
			{body: "<< /Type /Page /Parent 2 0 R /Resources << /XObject << /X 4 0 R >> >> /Contents 4 0 R >>"},
			{body: "/Type /XObject /Subtype /Form /Resources << /XObject << /X 4 0 R >> >>", stream: []byte("/X Do /X Do"), flate: true},
		}, "/Root 1 0 R"),
		"reference cycle": buildPDF([]pdfObj{
			{body: "2 0 R"},
			{body: "1 0 R"},
		}, "/Root 1 0 R"),
		"flate bomb": buildPDF([]pdfObj{
			{body: "<< /Type /Catalog /Pages 2 0 R >>"},
			{body: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
			{body: "<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>"},
			{body: "", stream: bomb, flate: true},
		}, "/Root 1 0 R"),
		"deep nesting": append([]byte("%PDF-1.4\n1 0 obj\n"), bytes.Repeat([]byte("[<<"), 100000)...),
	}
	for name, b := range cases {
		start := time.Now()
		_, err := PDFText(b)
		if err == nil || strings.Contains(err.Error(), "runtime error") {
			t.Errorf("%s: err = %v", name, err)
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("%s took %v", name, d)
		}
	}
}

// TestPDFMutations feeds damaged copies of a real PDF through the reader:
// none may panic (a recovered panic reads "runtime error") or hang.
func TestPDFMutations(t *testing.T) {
	d, err := pdf.New("Menu")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		d.Text(40, float64(40+i*20), 10, fmt.Sprintf("Line %d of the menu: jollof rice, plantain and chicken", i), i%2 == 0)
	}
	good, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 300 {
		b := append([]byte(nil), good...)
		switch i % 3 {
		case 0: // flip bytes
			for range 1 + rng.IntN(20) {
				b[rng.IntN(len(b))] = byte(rng.IntN(256))
			}
		case 1: // truncate
			b = b[:rng.IntN(len(b))]
		case 2: // cut a chunk out
			at := rng.IntN(len(b))
			b = append(b[:at], b[min(len(b), at+rng.IntN(500)):]...)
		}
		if _, err := PDFText(b); err != nil && strings.Contains(err.Error(), "runtime error") {
			t.Fatalf("mutation %d: %v", i, err)
		}
	}
}

func TestEncodingTables(t *testing.T) {
	if len(asciiNames) != 95 || len(latin1Names) != 96 || len(macRomanHigh) != 128 {
		t.Fatalf("table sizes %d %d %d", len(asciiNames), len(latin1Names), len(macRomanHigh))
	}
	for name, want := range map[string]string{
		"eacute": "é", "A": "A", "quoteright": "’", "uni20AC": "€", "u1F600": "😀",
		"f_f_i": "ffi", "a.sc": "a", "Ydieresis": "Ÿ", "germandbls": "ß", "zero": "0",
	} {
		if got := glyphText(name); got != want {
			t.Errorf("glyphText(%s) = %q, want %q", name, got, want)
		}
	}
	if macRoman[0x8E] != "é" || standard[0xAE] != "fi" || winAnsi[0x93] != "“" {
		t.Errorf("encodings: %q %q %q", macRoman[0x8E], standard[0xAE], winAnsi[0x93])
	}
}
