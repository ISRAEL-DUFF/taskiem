package extract

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// minCharsPerPage is the average of non-space characters per page below
// which a PDF is taken to be scanned: a page of real text has hundreds,
// while a scan has none or only a stray page number.
const minCharsPerPage = 20

// PDFText returns a PDF's text, a page at a time with a blank line between
// pages, in the order its content streams draw it.
//
// It reads text drawn with the Tj, TJ, ' and " operators, including text
// in form XObjects, and maps it to Unicode through the font's ToUnicode
// CMap, or for simple fonts through WinAnsi, MacRoman, Standard or a
// /Differences encoding. Lines and word spaces come from where the text is
// placed. It does not read encrypted PDFs (even those with an empty
// password), LZW-compressed streams, text in images (scans need OCR, which
// gives ErrScanned), annotations or form fields, and it does not reorder
// text into columns: most producers draw text in reading order.
func PDFText(data []byte) (text string, err error) {
	if len(data) > MaxFileBytes {
		return "", fmt.Errorf("%w: %d bytes, over the %d MB limit", ErrTooLarge, len(data), MaxFileBytes>>20)
	}
	if !isPDF(data) {
		return "", fmt.Errorf("%w: not a PDF", ErrUnreadable)
	}
	// Malformed input should only ever produce an error, but a bug in the
	// reader must not take the server down with it.
	defer func() {
		if r := recover(); r != nil {
			text, err = "", fmt.Errorf("%w: the PDF is damaged (%v)", ErrUnreadable, r)
		}
	}()
	doc, err := parsePDF(data)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	trailer := doc.trailer()
	if trailer["Encrypt"] != nil {
		return "", fmt.Errorf("%w: the PDF is encrypted or password-protected; save an unprotected copy and upload that", ErrUnreadable)
	}
	pages := doc.pages(trailer)
	if len(pages) == 0 {
		return "", fmt.Errorf("%w: the PDF has no pages", ErrUnreadable)
	}

	var out []string
	var size, chars, shown, decoded, broken int
	for _, pg := range pages {
		if size > MaxText {
			break
		}
		t := &textRun{doc: doc, fonts: map[string]*pdfFont{}}
		t.page(pg)
		shown += t.shown
		decoded += t.decoded
		broken += t.broken
		pageText := cleanLines(t.out.String())
		for _, r := range pageText {
			if !unicode.IsSpace(r) {
				chars++
			}
		}
		if pageText != "" {
			out = append(out, pageText)
			size += len(pageText) + 2
		}
		if doc.ops > maxContentOps {
			break
		}
	}
	if chars < minCharsPerPage*len(pages) {
		if broken > 0 {
			return "", fmt.Errorf("%w: the PDF's pages are damaged or compressed in a way this reader does not support", ErrUnreadable)
		}
		if shown >= minCharsPerPage*len(pages) && decoded < shown/2 {
			// Text is drawn but its fonts give no way back to characters
			// (subset fonts without a ToUnicode map).
			return "", fmt.Errorf("%w: the PDF's text cannot be decoded (its fonts carry no character map)", ErrUnreadable)
		}
		return "", fmt.Errorf("%w (%d pages, %d characters)", ErrScanned, len(pages), chars)
	}
	return truncate(strings.Join(out, "\n\n")), nil
}

// cleanLines collapses white space within lines and drops blank lines.
func cleanLines(s string) string {
	var w lineWriter
	for _, l := range strings.Split(s, "\n") {
		w.text(l)
		w.line()
	}
	w.line()
	return strings.Join(w.lines, "\n")
}

// trailer merges the document's trailers, later ones over earlier ones,
// so that an incremental update's /Root wins.
func (d *pdfDoc) trailer() pdfDict {
	ts := append([]pdfEntry(nil), d.trailers...)
	sortEntries(ts)
	merged := pdfDict{}
	for _, e := range ts {
		for k, v := range e.val.(pdfDict) {
			merged[k] = v
		}
	}
	return merged
}

func sortEntries(es []pdfEntry) {
	sort.SliceStable(es, func(i, j int) bool { return es[i].pos < es[j].pos })
}

// pdfPage is a page dictionary with the resources it inherits.
type pdfPage struct {
	dict      pdfDict
	resources pdfDict
}

// pages walks the page tree from the catalog. A file whose tree cannot be
// found falls back to every /Type /Page object in file order, as a viewer
// repairing it would.
func (d *pdfDoc) pages(trailer pdfDict) []pdfPage {
	var out []pdfPage
	seen := map[int64]bool{}
	var walk func(node any, res pdfDict, depth int)
	walk = func(node any, res pdfDict, depth int) {
		if depth > maxPDFDepth || len(out) >= maxPDFPages {
			return
		}
		if r, ok := node.(pdfRef); ok {
			if seen[r.num] {
				return // a cycle in the tree
			}
			seen[r.num] = true
		}
		n := d.dict(node)
		if n == nil {
			return
		}
		if r := d.dict(n["Resources"]); r != nil {
			res = r
		}
		if kids, ok := d.resolve(n["Kids"]).(pdfArray); ok && d.name(n["Type"]) != "Page" {
			for _, k := range kids {
				walk(k, res, depth+1)
			}
			return
		}
		out = append(out, pdfPage{n, res})
	}
	if cat := d.dict(trailer["Root"]); cat != nil {
		walk(cat["Pages"], nil, 0)
	}
	if len(out) > 0 {
		return out
	}
	var all []pdfEntry
	for _, e := range d.objs {
		if dict, ok := e.val.(pdfDict); ok && dict["Type"] == pdfName("Page") {
			all = append(all, e)
		}
	}
	sortEntries(all)
	for _, e := range all {
		if len(out) >= maxPDFPages {
			break
		}
		dict := e.val.(pdfDict)
		out = append(out, pdfPage{dict, d.dict(dict["Resources"])})
	}
	return out
}

// matrix is a PDF transformation matrix [a b c d e f].
type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

// mul is m then n.
func (m matrix) mul(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func translate(x, y float64) matrix { return matrix{1, 0, 0, 1, x, y} }

// textRun interprets one page's content, writing its text with line
// breaks and spaces judged from where each piece of text is placed.
type textRun struct {
	doc   *pdfDoc
	out   strings.Builder
	fonts map[string]*pdfFont // by resource name, for the current resources
	res   pdfDict

	ctm      matrix
	stack    []matrix
	tm, tlm  matrix
	font     *pdfFont
	size     float64 // Tf size
	tc, tw   float64 // character and word spacing
	th       float64 // horizontal scaling, 1 = 100%
	tl       float64 // leading
	rise     float64
	newLine  bool // T*, ' or " asked for a new line
	hasLast  bool
	lastX    float64 // where the last text ended, in device space
	lastY    float64
	lastSize float64
	shown    int // character codes drawn
	decoded  int // codes that gave text
	broken   int // content streams that could not be decoded
	forms    []int64
}

func (t *textRun) page(p pdfPage) {
	t.ctm, t.th = identity, 1
	t.res = p.resources
	data := t.contents(p.dict["Contents"])
	t.run(data, 0)
}

// contents returns a page's content: one stream, or several joined.
func (t *textRun) contents(v any) []byte {
	decode := func(s *pdfStream) []byte {
		b, err := t.doc.decode(s)
		if err != nil {
			t.broken++
		}
		return b
	}
	switch c := t.doc.resolve(v).(type) {
	case *pdfStream:
		return decode(c)
	case pdfArray:
		var all [][]byte
		for _, x := range c {
			if s, ok := t.doc.resolve(x).(*pdfStream); ok {
				all = append(all, decode(s))
			}
		}
		return bytes.Join(all, []byte("\n"))
	}
	return nil
}

// run interprets a content stream.
func (t *textRun) run(data []byte, depth int) {
	p := &pdfParser{b: data}
	var args []any
	for {
		v, err := p.object()
		if errors.Is(err, errPDFEOF) || errors.Is(err, errPDFLimit) {
			return
		}
		if err != nil {
			args = args[:0] // a stray delimiter: drop what came before it
			continue
		}
		op, ok := v.(pdfKeyword)
		if !ok {
			if len(args) < 64 {
				args = append(args, v)
			}
			continue
		}
		t.doc.ops++
		if t.doc.ops > maxContentOps || t.out.Len() > MaxText+1024 {
			return
		}
		if op == "ID" {
			skipInlineImage(p)
		} else {
			t.op(string(op), args, depth)
		}
		args = args[:0]
	}
}

// skipInlineImage moves past an inline image's data, which ends at "EI"
// between white space.
func skipInlineImage(p *pdfParser) {
	b := p.b
	for i := p.pos + 1; i+2 <= len(b); i++ {
		if b[i] == 'E' && b[i+1] == 'I' && isPDFSpace(b[i-1]) && (i+2 == len(b) || isPDFSpace(b[i+2]) || isPDFDelim(b[i+2])) {
			p.pos = i + 2
			return
		}
	}
	p.pos = len(b)
}

func nums(args []any, n int) ([]float64, bool) {
	if len(args) < n {
		return nil, false
	}
	out := make([]float64, n)
	for i, a := range args[len(args)-n:] {
		switch v := a.(type) {
		case int64:
			out[i] = float64(v)
		case float64:
			out[i] = v
		default:
			return nil, false
		}
	}
	return out, true
}

func (t *textRun) op(op string, args []any, depth int) {
	switch op {
	case "q":
		if len(t.stack) < maxGraphicsStates {
			t.stack = append(t.stack, t.ctm)
		}
	case "Q":
		if n := len(t.stack); n > 0 {
			t.ctm, t.stack = t.stack[n-1], t.stack[:n-1]
		}
	case "cm":
		if v, ok := nums(args, 6); ok {
			t.ctm = matrix(v).mul(t.ctm)
		}
	case "BT":
		t.tm, t.tlm = identity, identity
	case "Tf":
		if len(args) >= 2 {
			if name, ok := args[len(args)-2].(pdfName); ok {
				t.font = t.loadFont(string(name))
			}
			if v, ok := nums(args, 1); ok {
				t.size = v[0]
			}
		}
	case "Tc":
		if v, ok := nums(args, 1); ok {
			t.tc = v[0]
		}
	case "Tw":
		if v, ok := nums(args, 1); ok {
			t.tw = v[0]
		}
	case "Tz":
		if v, ok := nums(args, 1); ok {
			t.th = v[0] / 100
		}
	case "TL":
		if v, ok := nums(args, 1); ok {
			t.tl = v[0]
		}
	case "Ts":
		if v, ok := nums(args, 1); ok {
			t.rise = v[0]
		}
	case "Td", "TD":
		if v, ok := nums(args, 2); ok {
			if op == "TD" {
				t.tl = -v[1]
			}
			t.tlm = translate(v[0], v[1]).mul(t.tlm)
			t.tm = t.tlm
		}
	case "Tm":
		if v, ok := nums(args, 6); ok {
			t.tlm = matrix(v)
			t.tm = t.tlm
		}
	case "T*":
		t.nextLine()
	case "Tj":
		if len(args) >= 1 {
			if s, ok := args[len(args)-1].(pdfString); ok {
				t.show(s)
			}
		}
	case "'", "\"":
		if op == "\"" {
			if v, ok := nums(args[:max(0, len(args)-1)], 2); ok {
				t.tw, t.tc = v[0], v[1]
			}
		}
		t.nextLine()
		if len(args) >= 1 {
			if s, ok := args[len(args)-1].(pdfString); ok {
				t.show(s)
			}
		}
	case "TJ":
		if len(args) >= 1 {
			if a, ok := args[len(args)-1].(pdfArray); ok {
				for _, x := range a {
					switch v := x.(type) {
					case pdfString:
						t.show(v)
					case int64:
						t.kern(float64(v))
					case float64:
						t.kern(v)
					}
				}
			}
		}
	case "Do":
		if len(args) >= 1 {
			if name, ok := args[len(args)-1].(pdfName); ok {
				t.form(string(name), depth)
			}
		}
	}
}

func (t *textRun) nextLine() {
	t.tlm = translate(0, -t.tl).mul(t.tlm)
	t.tm = t.tlm
	t.newLine = true
}

// kern applies a TJ adjustment, in thousandths of an em; a large one is
// how many producers write a word space.
func (t *textRun) kern(n float64) {
	t.tm = translate(-n/1000*t.size*t.th, 0).mul(t.tm)
}

// show writes a string's text, after a line break or space if where it
// is drawn calls for one, and advances the text matrix past it.
func (t *textRun) show(s pdfString) {
	if t.font == nil {
		t.font = &pdfFont{}
		t.font.simpleDefaults()
	}
	m := t.tm.mul(t.ctm)
	x, y := m[4], m[5]
	vs := math.Abs(t.size * math.Hypot(m[2], m[3]))
	if vs == 0 {
		vs = 1
	}
	if t.hasLast {
		ref := max(vs, t.lastSize)
		switch gap := x - t.lastX; {
		case t.newLine || math.Abs(y-t.lastY) > 0.5*ref:
			t.out.WriteByte('\n')
		case gap > 0.18*ref || gap < -0.5*ref:
			t.out.WriteByte(' ')
		}
	}
	t.newLine = false
	for _, c := range t.font.codes(string(s)) {
		t.shown++
		if c.text != "" {
			t.decoded++
		}
		t.out.WriteString(c.text)
		w := t.font.width(c.code)/1000*t.size + t.tc
		if c.code == 32 && c.n == 1 {
			w += t.tw
		}
		t.tm = translate(w*t.th, 0).mul(t.tm)
	}
	m = t.tm.mul(t.ctm)
	t.lastX, t.lastY, t.lastSize, t.hasLast = m[4], m[5], vs, true
}

// form runs a form XObject's content, with its own resources and matrix.
func (t *textRun) form(name string, depth int) {
	xobjs := t.doc.dict(t.res["XObject"])
	if xobjs == nil || depth >= maxFormDepth {
		return
	}
	raw := xobjs[name]
	if r, ok := raw.(pdfRef); ok {
		for _, f := range t.forms {
			if f == r.num {
				return // a form that draws itself
			}
		}
		t.forms = append(t.forms, r.num)
		defer func() { t.forms = t.forms[:len(t.forms)-1] }()
	}
	s, ok := t.doc.resolve(raw).(*pdfStream)
	if !ok || t.doc.name(s.dict["Subtype"]) != "Form" {
		return
	}
	data, err := t.doc.decode(s)
	if err != nil {
		return
	}
	saveCTM, saveRes, saveFonts := t.ctm, t.res, t.fonts
	saveStack := len(t.stack)
	if v, ok := t.doc.resolve(s.dict["Matrix"]).(pdfArray); ok {
		if mv, ok := nums(t.resolveAll(v), 6); ok {
			t.ctm = matrix(mv).mul(t.ctm)
		}
	}
	if r := t.doc.dict(s.dict["Resources"]); r != nil {
		t.res, t.fonts = r, map[string]*pdfFont{}
	}
	t.run(data, depth+1)
	t.ctm, t.res, t.fonts = saveCTM, saveRes, saveFonts
	t.stack = t.stack[:min(saveStack, len(t.stack))]
}

func (t *textRun) resolveAll(a pdfArray) []any {
	out := make([]any, len(a))
	for i, v := range a {
		out[i] = t.doc.resolve(v)
	}
	return out
}

func (t *textRun) loadFont(name string) *pdfFont {
	if f, ok := t.fonts[name]; ok {
		return f
	}
	f := newFont(t.doc, t.doc.dict(t.doc.dict(t.res["Font"])[name]))
	t.fonts[name] = f
	return f
}
