// Package pdf writes small PDF documents with an embedded TrueType font,
// for receipts (docs/spec/schools.md 9): text in two weights, lines,
// rectangles and a QR code, on A5 pages. It writes PDF 1.4 by hand (a
// catalog, pages, content streams and two Type 0 fonts with Identity-H
// encoding and a ToUnicode map), so a character such as the naira sign
// prints wherever the file is opened: the fonts travel with it.
//
// The fonts are Taskiem Receipt Sans, a renamed subset of Liberation Sans
// under the SIL Open Font License (fonts/OFL.txt).
package pdf

import (
	"bytes"
	"compress/zlib"
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed fonts/receipt-sans-regular.ttf
var regularTTF []byte

//go:embed fonts/receipt-sans-bold.ttf
var boldTTF []byte

// Page sizes, in points.
const (
	A5Width  = 419.53
	A5Height = 595.28
)

var (
	fontsOnce     sync.Once
	regular, bold *Font
	fontsErr      error
)

// Fonts are the embedded regular and bold fonts.
func Fonts() (reg, bld *Font, err error) {
	fontsOnce.Do(func() {
		regular, fontsErr = ParseFont(regularTTF)
		if fontsErr != nil {
			return
		}
		bold, fontsErr = ParseFont(boldTTF)
	})
	return regular, bold, fontsErr
}

// Doc is a document being written. Coordinates are in points from the
// page's top-left corner; y grows downwards.
type Doc struct {
	PageWidth, PageHeight float64
	Title                 string
	Created               time.Time
	pages                 []*bytes.Buffer
	cur                   *bytes.Buffer
	used                  [2]map[uint16]bool // glyphs used, per font
	reg, bld              *Font
	err                   error
}

// New starts a document of A5 pages, with its first page open.
func New(title string) (*Doc, error) {
	reg, bld, err := Fonts()
	if err != nil {
		return nil, err
	}
	d := &Doc{PageWidth: A5Width, PageHeight: A5Height, Title: title, Created: time.Now(), reg: reg, bld: bld}
	d.used[0], d.used[1] = map[uint16]bool{}, map[uint16]bool{}
	d.NewPage()
	return d, nil
}

// NewPage starts another page.
func (d *Doc) NewPage() {
	d.cur = &bytes.Buffer{}
	d.pages = append(d.pages, d.cur)
}

func (d *Doc) font(bold bool) (*Font, int) {
	if bold {
		return d.bld, 1
	}
	return d.reg, 0
}

// Width is a string's width at size, in points.
func (d *Doc) Width(s string, size float64, bold bool) float64 {
	f, _ := d.font(bold)
	return f.Width(s, size)
}

// Text draws s with its baseline at y, starting at x.
func (d *Doc) Text(x, y, size float64, s string, bold bool) {
	if s == "" {
		return
	}
	f, i := d.font(bold)
	var hex strings.Builder
	for _, r := range s {
		g := f.Glyph(r)
		if g == 0 && r != ' ' {
			// A character the font lacks: shown as a question mark rather
			// than lost.
			g = f.Glyph('?')
		}
		d.used[i][g] = true
		fmt.Fprintf(&hex, "%04X", g)
	}
	fmt.Fprintf(d.cur, "BT /F%d %s Tf %s %s Td <%s> Tj ET\n", i+1, num(size), num(x), num(d.PageHeight-y), hex.String())
}

// TextRight draws s ending at xRight.
func (d *Doc) TextRight(xRight, y, size float64, s string, bold bool) {
	d.Text(xRight-d.Width(s, size, bold), y, size, s, bold)
}

// TextCenter draws s centred on xMid.
func (d *Doc) TextCenter(xMid, y, size float64, s string, bold bool) {
	d.Text(xMid-d.Width(s, size, bold)/2, y, size, s, bold)
}

// Wrap breaks s into lines no wider than width at size.
func (d *Doc) Wrap(s string, size float64, bold bool, width float64) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, w := range strings.Fields(para) {
			try := w
			if line != "" {
				try = line + " " + w
			}
			if line != "" && d.Width(try, size, bold) > width {
				lines = append(lines, line)
				line = w
				continue
			}
			line = try
		}
		lines = append(lines, line)
	}
	return lines
}

// Line draws a straight line of the given width.
func (d *Doc) Line(x1, y1, x2, y2, width float64) {
	fmt.Fprintf(d.cur, "%s w %s %s m %s %s l S\n", num(width), num(x1), num(d.PageHeight-y1), num(x2), num(d.PageHeight-y2))
}

// Rect fills (or strokes) a rectangle whose top-left corner is x, y.
func (d *Doc) Rect(x, y, w, h float64, fill bool) {
	op := "S"
	if fill {
		op = "f"
	}
	fmt.Fprintf(d.cur, "%s %s %s %s re %s\n", num(x), num(d.PageHeight-y-h), num(w), num(h), op)
}

// Gray sets the fill and stroke colour to a gray level (0 black, 1
// white).
func (d *Doc) Gray(level float64) {
	fmt.Fprintf(d.cur, "%s g %s G\n", num(level), num(level))
}

// QR draws a QR code of n modules a side, black(x, y) saying which are
// dark, with its top-left corner at x, y and size points a side (a
// quiet zone of four modules included).
func (d *Doc) QR(x, y, size float64, n int, black func(x, y int) bool) {
	if n <= 0 {
		return
	}
	m := size / float64(n+8)
	d.Gray(1)
	d.Rect(x, y, size, size, true)
	d.Gray(0)
	for row := range n {
		for col := range n {
			if black(col, row) {
				d.Rect(x+m*float64(col+4), y+m*float64(row+4), m, m, true)
			}
		}
	}
}

// num formats a number for a content stream.
func num(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" || s == "-0" {
		return "0"
	}
	return s
}

// Bytes writes the document.
func (d *Doc) Bytes() ([]byte, error) {
	if d.err != nil {
		return nil, d.err
	}
	w := &writer{}
	// Objects: 1 catalog, 2 pages, then per page (page, content), then
	// the two fonts (Type0, CIDFont, descriptor, file, ToUnicode each),
	// then info.
	catalog := w.reserve()
	pages := w.reserve()
	var pageIDs []int
	type pageObj struct{ page, content int }
	var objs []pageObj
	for range d.pages {
		objs = append(objs, pageObj{w.reserve(), w.reserve()})
		pageIDs = append(pageIDs, objs[len(objs)-1].page)
	}
	fonts := [2]int{w.reserve(), w.reserve()}
	info := w.reserve()
	w.set(catalog, fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pages))
	var kids []string
	for _, id := range pageIDs {
		kids = append(kids, fmt.Sprintf("%d 0 R", id))
	}
	w.set(pages, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pageIDs)))
	for i, o := range objs {
		w.set(o.page, fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %s %s] /Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> /Contents %d 0 R >>",
			pages, num(d.PageWidth), num(d.PageHeight), fonts[0], fonts[1], o.content))
		w.setStream(o.content, "", d.pages[i].Bytes())
	}
	for i, f := range []*Font{d.reg, d.bld} {
		if err := d.writeFont(w, fonts[i], f, d.used[i], i == 1); err != nil {
			return nil, err
		}
	}
	w.set(info, fmt.Sprintf("<< /Title %s /Producer (Taskiem) /CreationDate (D:%s) >>", pdfString(d.Title), d.Created.UTC().Format("20060102150405")+"Z"))
	return w.finish(catalog, info), nil
}

// writeFont writes a Type 0 font with a CIDFontType2 descendant, the
// font file and a ToUnicode map for the glyphs used.
func (d *Doc) writeFont(w *writer, id int, f *Font, used map[uint16]bool, isBold bool) error {
	cid := w.reserve()
	desc := w.reserve()
	file := w.reserve()
	tounicode := w.reserve()
	name := "TaskiemReceiptSans"
	flags := 32 // nonsymbolic
	if isBold {
		name += "-Bold"
		flags |= 1 << 18
	}
	w.set(id, fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /%s /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>", name, cid, tounicode))
	// Widths of the glyphs used, in 1/1000 em.
	gids := make([]int, 0, len(used))
	for g := range used {
		gids = append(gids, int(g))
	}
	sort.Ints(gids)
	var ws []string
	for _, g := range gids {
		ws = append(ws, fmt.Sprintf("%d [%d]", g, f.Advance(uint16(g))*1000/f.UnitsPerEm)) // #nosec G115 -- a glyph id, under 65536
	}
	w.set(cid, fmt.Sprintf("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /%s /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /FontDescriptor %d 0 R /DW %d /W [%s] /CIDToGIDMap /Identity >>",
		name, desc, f.Advance(0)*1000/f.UnitsPerEm, strings.Join(ws, " ")))
	scale := func(v int) int { return v * 1000 / f.UnitsPerEm }
	stemV := 80
	if isBold {
		stemV = 140
	}
	w.set(desc, fmt.Sprintf("<< /Type /FontDescriptor /FontName /%s /Flags %d /FontBBox [%d %d %d %d] /ItalicAngle 0 /Ascent %d /Descent %d /CapHeight %d /StemV %d /FontFile2 %d 0 R >>",
		name, flags, scale(f.BBox[0]), scale(f.BBox[1]), scale(f.BBox[2]), scale(f.BBox[3]), scale(f.Ascent), scale(f.Descent), scale(f.CapHeight), stemV, file))
	w.setStream(file, fmt.Sprintf("/Length1 %d", len(f.Data)), f.Data)
	// ToUnicode: glyph to character, so text can be copied and searched.
	byGlyph := map[uint16]rune{}
	for r, g := range f.cmap {
		if used[g] {
			if prev, ok := byGlyph[g]; !ok || r < prev {
				byGlyph[g] = r
			}
		}
	}
	var cm bytes.Buffer
	cm.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	for i := 0; i < len(gids); i += 100 {
		end := min(i+100, len(gids))
		fmt.Fprintf(&cm, "%d beginbfchar\n", end-i)
		for _, g := range gids[i:end] {
			r, ok := byGlyph[uint16(g)] // #nosec G115 -- a glyph id, under 65536
			if !ok {
				r = 0xFFFD
			}
			if r > 0xFFFF {
				r -= 0x10000
				fmt.Fprintf(&cm, "<%04X> <%04X%04X>\n", g, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
				continue
			}
			fmt.Fprintf(&cm, "<%04X> <%04X>\n", g, r)
		}
		cm.WriteString("endbfchar\n")
	}
	cm.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	w.setStream(tounicode, "", cm.Bytes())
	return nil
}

// pdfString is a literal string, escaped, as UTF-16BE when it is not
// ASCII.
func pdfString(s string) string {
	ascii := true
	for _, r := range s {
		if r > 126 || r < 32 {
			ascii = false
		}
	}
	if ascii {
		r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
		return "(" + r.Replace(s) + ")"
	}
	var b strings.Builder
	b.WriteString("<FEFF")
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			fmt.Fprintf(&b, "%04X%04X", 0xD800+(r>>10), 0xDC00+(r&0x3FF))
			continue
		}
		fmt.Fprintf(&b, "%04X", r)
	}
	b.WriteString(">")
	return b.String()
}

// writer lays objects out with a cross-reference table.
type writer struct {
	objs [][]byte // by id - 1; nil until set
}

func (w *writer) reserve() int {
	w.objs = append(w.objs, nil)
	return len(w.objs)
}

func (w *writer) set(id int, body string) {
	w.objs[id-1] = []byte(body)
}

func (w *writer) setStream(id int, extra string, data []byte) {
	var zb bytes.Buffer
	z := zlib.NewWriter(&zb)
	_, _ = z.Write(data)
	_ = z.Close()
	var b bytes.Buffer
	fmt.Fprintf(&b, "<< /Length %d /Filter /FlateDecode %s >>\nstream\n", zb.Len(), extra)
	b.Write(zb.Bytes())
	b.WriteString("\nendstream")
	w.objs[id-1] = b.Bytes()
}

func (w *writer) finish(catalog, info int) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(w.objs))
	for i, body := range w.objs {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", i+1)
		out.Write(body)
		out.WriteString("\nendobj\n")
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(w.objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(w.objs)+1, catalog, info, xref)
	return out.Bytes()
}
