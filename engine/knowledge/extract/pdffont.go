package extract

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Limits on a ToUnicode CMap, so that one cannot make every lookup slow
// or turn one character code into a page of text.
const (
	maxCMapChars  = 1 << 16
	maxCMapRanges = 10000
	maxCMapDest   = 32         // characters one code maps to; real ligatures are a few
	maxCharCode   = 0xFFFFFFFF // codes are at most 4 bytes
)

// pdfFont maps a font's character codes to text and widths.
type pdfFont struct {
	cid      bool        // a Type 0 (composite) font: codes are usually 2 bytes
	utf16    bool        // a composite font whose codes are UTF-16 (UCS2 CMaps)
	enc      [256]string // simple fonts: code to text
	toUni    *cmap
	first    int       // simple fonts: /FirstChar
	widths   []float64 // simple fonts: /Widths from FirstChar
	cidW     map[int]float64
	dw       float64 // default width, in thousandths of an em
	fontUnit float64 // Type 3 fonts: glyph units to thousandths of an em
}

// glyph is one character code and its text.
type glyph struct {
	code int
	n    int // bytes in the code
	text string
}

func (f *pdfFont) simpleDefaults() {
	f.enc = winAnsi
	f.dw = 500
	f.fontUnit = 1
}

// newFont reads a font dictionary. A missing font still reads text as
// WinAnsi, which is right for most simple fonts.
func newFont(d *pdfDoc, fd pdfDict) *pdfFont {
	f := &pdfFont{}
	f.simpleDefaults()
	if fd == nil {
		return f
	}
	if s, ok := d.resolve(fd["ToUnicode"]).(*pdfStream); ok {
		// Fonts are shared between pages: a CMap is parsed once.
		if m, ok := d.cmaps[s]; ok {
			f.toUni = m
		} else if data, err := d.decode(s); err == nil {
			f.toUni = parseCMap(d, data)
			if d.cmaps != nil {
				d.cmaps[s] = f.toUni
			}
		}
	}
	if d.name(fd["Subtype"]) == "Type0" {
		f.cid = true
		f.dw = 1000
		enc := d.name(fd["Encoding"])
		f.utf16 = strings.Contains(enc, "UCS2") || strings.Contains(enc, "UTF16")
		if desc := d.array(fd["DescendantFonts"]); len(desc) > 0 {
			dd := d.dict(desc[0])
			if v, ok := d.num(dd["DW"]); ok {
				f.dw = v
			}
			f.cidW = cidWidths(d, d.array(dd["W"]))
		}
		return f
	}
	f.enc = simpleEncoding(d, fd)
	if v, ok := d.num(fd["FirstChar"]); ok && v >= 0 && v < 256 {
		f.first = int(v)
	}
	for i, w := range d.array(fd["Widths"]) {
		if i >= 256 {
			break
		}
		v, _ := d.num(w)
		f.widths = append(f.widths, v)
	}
	if d.name(fd["Subtype"]) == "Type3" {
		// Type 3 widths are in glyph space, scaled by /FontMatrix.
		if fm := d.array(fd["FontMatrix"]); len(fm) == 6 {
			if v, ok := d.num(fm[0]); ok {
				f.fontUnit = v * 1000
			}
		}
	}
	return f
}

// charCode reads a character code from a PDF number. It refuses, before
// converting, anything that is not a whole number from 0 to maxCharCode:
// converting a float out of int64's range gives an arbitrary int.
func charCode(d *pdfDoc, v any) (int64, bool) {
	f, ok := d.num(v)
	if !ok || math.IsNaN(f) || f < 0 || f > maxCharCode {
		return 0, false
	}
	return int64(f), true
}

// cidWidths reads a /W array: "c [w1 w2 ...]" or "c1 c2 w" entries. It
// keeps at most maxCMapChars widths.
func cidWidths(d *pdfDoc, w pdfArray) map[int]float64 {
	out := map[int]float64{}
	for i := 0; i < len(w) && len(out) < maxCMapChars; {
		c, ok := charCode(d, w[i])
		if !ok || i+1 >= len(w) {
			break
		}
		if list, ok := d.resolve(w[i+1]).(pdfArray); ok {
			for j, x := range list {
				k := c + int64(j)
				if k > maxCharCode || len(out) >= maxCMapChars {
					break
				}
				if v, ok := d.num(x); ok {
					out[int(k)] = v
				}
			}
			i += 2
			continue
		}
		if i+2 >= len(w) {
			break
		}
		c2, ok1 := charCode(d, w[i+1])
		v, ok2 := d.num(w[i+2])
		if !ok1 || !ok2 || c2-c > maxCMapChars {
			break
		}
		for k := c; k <= c2 && len(out) < maxCMapChars; k++ {
			out[int(k)] = v
		}
		i += 3
	}
	return out
}

// simpleEncoding is a simple font's code-to-text table: its base encoding
// with any /Differences applied.
func simpleEncoding(d *pdfDoc, fd pdfDict) [256]string {
	enc := winAnsi
	base := func(name string) {
		switch name {
		case "MacRomanEncoding":
			enc = macRoman
		case "StandardEncoding":
			enc = standard
		case "WinAnsiEncoding":
			enc = winAnsi
		}
	}
	switch e := d.resolve(fd["Encoding"]).(type) {
	case pdfName:
		base(string(e))
	case pdfDict:
		base(d.name(e["BaseEncoding"]))
		code := 0
		for _, x := range d.array(e["Differences"]) {
			switch d.resolve(x).(type) {
			case int64, float64:
				// Anything outside 0 to 255 names no code; clamping first
				// keeps the conversion and the count after it in range.
				if c, ok := charCode(d, x); ok && c < 256 {
					code = int(c)
				} else {
					code = 256
				}
			case pdfName:
				v := d.name(x)
				if code >= 0 && code < 256 {
					enc[code] = glyphText(v)
					code++
				}
			}
		}
	}
	return enc
}

// next reads the character code at the start of s, which must not be
// empty, and its text; g.n is how many bytes it took. Callers step through
// a string with it rather than splitting the whole string up front, so
// that they can stop as soon as they have enough text.
func (f *pdfFont) next(s string) glyph {
	n := 1
	if f.toUni != nil {
		n = f.toUni.codeLen(s, f.cid)
	} else if f.cid {
		n = 2
	}
	n = max(1, min(n, len(s)))
	code := 0
	for _, b := range []byte(s[:n]) {
		code = code<<8 | int(b)
	}
	g := glyph{code: code, n: n}
	if f.toUni != nil {
		if t, ok := f.toUni.lookup(code, n); ok {
			g.text = t
		}
	}
	if g.text == "" && !f.cid {
		g.text = f.enc[code&0xff]
	}
	if g.text == "" && f.cid && f.utf16 {
		g.text = string(rune(code))
	}
	return g
}

// width is a code's advance in thousandths of an em.
func (f *pdfFont) width(code int) float64 {
	if f.cid {
		if w, ok := f.cidW[code]; ok {
			return w
		}
		return f.dw
	}
	if i := code - f.first; i >= 0 && i < len(f.widths) && f.widths[i] > 0 {
		return f.widths[i] * f.fontUnit
	}
	return f.dw
}

// cmap is a parsed ToUnicode CMap.
type cmap struct {
	spaces []codeSpace
	chars  map[uint64]string // (length << 32 | code) to text
	ranges []cmapRange
}

type codeSpace struct {
	n      int
	lo, hi uint32
}

type cmapRange struct {
	n      int
	lo, hi uint32
	dst    []byte   // UTF-16BE for lo; later codes add to its last unit
	list   []string // or one destination per code
}

func cmapKey(code, n int) uint64 { return uint64(n)<<32 | uint64(uint32(code)) } // #nosec G115 -- codes are at most 4 bytes

// codeLen is the length of the code at the start of s: the codespace
// range it falls in, or the font's usual length.
func (m *cmap) codeLen(s string, cid bool) int {
	for n := 1; n <= 4 && n <= len(s); n++ {
		code := uint32(0)
		for _, b := range []byte(s[:n]) {
			code = code<<8 | uint32(b)
		}
		for _, sp := range m.spaces {
			if sp.n == n && code >= sp.lo && code <= sp.hi {
				return n
			}
		}
	}
	if cid {
		return 2
	}
	return 1
}

func (m *cmap) lookup(code, n int) (string, bool) {
	if t, ok := m.chars[cmapKey(code, n)]; ok {
		return t, true
	}
	c := uint32(code) // #nosec G115 -- codes are at most 4 bytes
	for _, r := range m.ranges {
		if r.n != n || c < r.lo || c > r.hi {
			continue
		}
		off := c - r.lo
		if r.list != nil {
			if int(off) < len(r.list) {
				return r.list[off], true
			}
			return "", false
		}
		dst := append([]byte(nil), r.dst...)
		if len(dst) >= 2 {
			last := uint32(dst[len(dst)-2])<<8 | uint32(dst[len(dst)-1])
			last += off
			dst[len(dst)-2], dst[len(dst)-1] = byte(last>>8), byte(last) // #nosec G115 -- the low 16 bits, as the CMap means
		} else if len(dst) == 1 {
			dst[0] += byte(off) // #nosec G115 -- a one-byte destination wraps
		}
		return utf16BE(dst), true
	}
	return "", false
}

// parseCMap reads the codespace ranges, bfchar and bfrange sections of a
// CMap; everything else in it is PostScript this package does not need.
// Its values count against the document's content limit.
func parseCMap(d *pdfDoc, data []byte) *cmap {
	m := &cmap{chars: map[uint64]string{}}
	p := &pdfParser{b: data, elems: &d.contentElems, limit: maxContentElems}
	var prev []any // operands since the last keyword
	for {
		if d.tick() {
			return m
		}
		v, err := p.object()
		if err != nil {
			if errors.Is(err, errPDFEOF) || errors.Is(err, errPDFLimit) {
				return m
			}
			continue
		}
		kw, ok := v.(pdfKeyword)
		if !ok {
			if len(prev) < 8 {
				prev = append(prev, v)
			}
			continue
		}
		prev = prev[:0]
		switch kw {
		case "begincodespacerange":
			for _, e := range readUntil(p, "endcodespacerange", 2) {
				lo, _ := e[0].(pdfString)
				hi, _ := e[1].(pdfString)
				if len(lo) >= 1 && len(lo) <= 4 && len(hi) == len(lo) {
					m.spaces = append(m.spaces, codeSpace{len(lo), beUint(lo), beUint(hi)})
				}
			}
		case "beginbfchar":
			for _, e := range readUntil(p, "endbfchar", 2) {
				src, ok := e[0].(pdfString)
				if !ok || len(src) < 1 || len(src) > 4 || len(m.chars) >= maxCMapChars {
					continue
				}
				m.chars[cmapKey(int(beUint(src)), len(src))] = cmapDest(e[1])
			}
		case "beginbfrange":
			for _, e := range readUntil(p, "endbfrange", 3) {
				lo, ok1 := e[0].(pdfString)
				hi, ok2 := e[1].(pdfString)
				if !ok1 || !ok2 || len(lo) < 1 || len(lo) > 4 || len(hi) != len(lo) || len(m.ranges) >= maxCMapRanges {
					continue
				}
				r := cmapRange{n: len(lo), lo: beUint(lo), hi: beUint(hi)}
				if r.hi < r.lo {
					continue
				}
				switch dst := e[2].(type) {
				case pdfString:
					r.dst = []byte(dst[:min(len(dst), 2*maxCMapDest)])
				case pdfArray:
					for _, x := range dst {
						if len(r.list) >= maxCMapChars {
							break
						}
						r.list = append(r.list, cmapDest(x))
					}
				default:
					continue
				}
				m.ranges = append(m.ranges, r)
			}
		}
	}
}

// readUntil reads groups of n values up to the keyword end.
func readUntil(p *pdfParser, end string, n int) [][]any {
	var out [][]any
	var cur []any
	for {
		v, err := p.object()
		if err != nil {
			return out
		}
		if kw, ok := v.(pdfKeyword); ok && string(kw) == end {
			return out
		}
		cur = append(cur, v)
		if len(cur) == n {
			out = append(out, cur)
			cur = nil
		}
	}
}

// cmapDest is a bfchar or bfrange destination: UTF-16BE, or a glyph name.
// It keeps at most maxCMapDest characters.
func cmapDest(v any) string {
	var s string
	switch d := v.(type) {
	case pdfString:
		s = utf16BE([]byte(d[:min(len(d), 4*maxCMapDest)]))
	case pdfName:
		s = glyphText(string(d[:min(len(d), 8*maxCMapDest)]))
	}
	n := 0
	for i := range s {
		if n == maxCMapDest {
			return s[:i]
		}
		n++
	}
	return s
}

func beUint(s pdfString) uint32 {
	var v uint32
	for i := 0; i < len(s); i++ {
		v = v<<8 | uint32(s[i])
	}
	return v
}

// utf16BE decodes UTF-16BE; a lone byte is taken as a Latin-1 character.
func utf16BE(b []byte) string {
	if len(b) == 1 {
		return string(rune(b[0]))
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return string(utf16.Decode(u))
}

// glyphText maps an Adobe glyph name to its text: the standard Latin names,
// uniXXXX and uXXXX[XX] names, ligatures written a_b, and names with a
// variant suffix such as a.sc.
func glyphText(name string) string {
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	if t, ok := glyphNames[name]; ok {
		return t
	}
	if strings.Contains(name, "_") {
		var b strings.Builder
		for _, part := range strings.Split(name, "_") {
			b.WriteString(glyphText(part))
		}
		return b.String()
	}
	if strings.HasPrefix(name, "uni") && len(name) >= 7 && (len(name)-3)%4 == 0 {
		var u []uint16
		for i := 3; i < len(name); i += 4 {
			v, err := strconv.ParseUint(name[i:i+4], 16, 16)
			if err != nil {
				return ""
			}
			u = append(u, uint16(v))
		}
		return string(utf16.Decode(u))
	}
	if strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7 {
		if v, err := strconv.ParseUint(name[1:], 16, 32); err == nil && v <= 0x10FFFF {
			return string(rune(v))
		}
	}
	if len(name) == 1 {
		return name
	}
	return ""
}

// Glyph names in code order: printable ASCII (32 to 126) and Latin-1
// (160 to 255), as WinAnsiEncoding lays them out.
var (
	asciiNames = strings.Fields(`space exclam quotedbl numbersign dollar percent ampersand quotesingle
parenleft parenright asterisk plus comma hyphen period slash zero one two three four five six seven
eight nine colon semicolon less equal greater question at A B C D E F G H I J K L M N O P Q R S T U V
W X Y Z bracketleft backslash bracketright asciicircum underscore grave a b c d e f g h i j k l m n o
p q r s t u v w x y z braceleft bar braceright asciitilde`)
	latin1Names = strings.Fields(`nbspace exclamdown cent sterling currency yen brokenbar section
dieresis copyright ordfeminine guillemotleft logicalnot sfthyphen registered macron degree plusminus
twosuperior threesuperior acute mu paragraph periodcentered cedilla onesuperior ordmasculine
guillemotright onequarter onehalf threequarters questiondown Agrave Aacute Acircumflex Atilde
Adieresis Aring AE Ccedilla Egrave Eacute Ecircumflex Edieresis Igrave Iacute Icircumflex Idieresis
Eth Ntilde Ograve Oacute Ocircumflex Otilde Odieresis multiply Oslash Ugrave Uacute Ucircumflex
Udieresis Yacute Thorn germandbls agrave aacute acircumflex atilde adieresis aring ae ccedilla egrave
eacute ecircumflex edieresis igrave iacute icircumflex idieresis eth ntilde ograve oacute ocircumflex
otilde odieresis divide oslash ugrave uacute ucircumflex udieresis yacute thorn ydieresis`)
)

// winAnsiHigh is WinAnsiEncoding's 128 to 159, where it differs from
// Latin-1.
var winAnsiHigh = map[int]rune{
	0x80: 0x20AC, 0x82: 0x201A, 0x83: 0x0192, 0x84: 0x201E, 0x85: 0x2026, 0x86: 0x2020,
	0x87: 0x2021, 0x88: 0x02C6, 0x89: 0x2030, 0x8A: 0x0160, 0x8B: 0x2039, 0x8C: 0x0152,
	0x8E: 0x017D, 0x91: 0x2018, 0x92: 0x2019, 0x93: 0x201C, 0x94: 0x201D, 0x95: 0x2022,
	0x96: 0x2013, 0x97: 0x2014, 0x98: 0x02DC, 0x99: 0x2122, 0x9A: 0x0161, 0x9B: 0x203A,
	0x9C: 0x0153, 0x9E: 0x017E, 0x9F: 0x0178,
}

// winAnsiHighNames are the glyph names for winAnsiHigh, in its code order.
var winAnsiHighNames = map[int]string{
	0x80: "Euro", 0x82: "quotesinglbase", 0x83: "florin", 0x84: "quotedblbase", 0x85: "ellipsis",
	0x86: "dagger", 0x87: "daggerdbl", 0x88: "circumflex", 0x89: "perthousand", 0x8A: "Scaron",
	0x8B: "guilsinglleft", 0x8C: "OE", 0x8E: "Zcaron", 0x91: "quoteleft", 0x92: "quoteright",
	0x93: "quotedblleft", 0x94: "quotedblright", 0x95: "bullet", 0x96: "endash", 0x97: "emdash",
	0x98: "tilde", 0x99: "trademark", 0x9A: "scaron", 0x9B: "guilsinglright", 0x9C: "oe",
	0x9E: "zcaron", 0x9F: "Ydieresis",
}

// standardHigh is StandardEncoding's codes over 160, by glyph name.
var standardHigh = map[int]string{
	0xA1: "exclamdown", 0xA2: "cent", 0xA3: "sterling", 0xA4: "fraction", 0xA5: "yen",
	0xA6: "florin", 0xA7: "section", 0xA8: "currency", 0xA9: "quotesingle", 0xAA: "quotedblleft",
	0xAB: "guillemotleft", 0xAC: "guilsinglleft", 0xAD: "guilsinglright", 0xAE: "fi", 0xAF: "fl",
	0xB1: "endash", 0xB2: "dagger", 0xB3: "daggerdbl", 0xB4: "periodcentered", 0xB6: "paragraph",
	0xB7: "bullet", 0xB8: "quotesinglbase", 0xB9: "quotedblbase", 0xBA: "quotedblright",
	0xBB: "guillemotright", 0xBC: "ellipsis", 0xBD: "perthousand", 0xBF: "questiondown",
	0xC1: "grave", 0xC2: "acute", 0xC3: "circumflex", 0xC4: "tilde", 0xC5: "macron", 0xC8: "dieresis",
	0xCB: "cedilla", 0xD0: "emdash", 0xE1: "AE", 0xE3: "ordfeminine", 0xE8: "Lslash", 0xE9: "Oslash",
	0xEA: "OE", 0xEB: "ordmasculine", 0xF1: "ae", 0xF5: "dotlessi", 0xF8: "lslash", 0xF9: "oslash",
	0xFA: "oe", 0xFB: "germandbls",
}

// macRomanHigh is MacRomanEncoding's 128 to 255.
var macRomanHigh = []rune{
	0xC4, 0xC5, 0xC7, 0xC9, 0xD1, 0xD6, 0xDC, 0xE1, 0xE0, 0xE2, 0xE4, 0xE3, 0xE5, 0xE7, 0xE9, 0xE8,
	0xEA, 0xEB, 0xED, 0xEC, 0xEE, 0xEF, 0xF1, 0xF3, 0xF2, 0xF4, 0xF6, 0xF5, 0xFA, 0xF9, 0xFB, 0xFC,
	0x2020, 0xB0, 0xA2, 0xA3, 0xA7, 0x2022, 0xB6, 0xDF, 0xAE, 0xA9, 0x2122, 0xB4, 0xA8, 0x2260, 0xC6, 0xD8,
	0x221E, 0xB1, 0x2264, 0x2265, 0xA5, 0xB5, 0x2202, 0x2211, 0x220F, 0x3C0, 0x222B, 0xAA, 0xBA, 0x3A9, 0xE6, 0xF8,
	0xBF, 0xA1, 0xAC, 0x221A, 0x192, 0x2248, 0x2206, 0xAB, 0xBB, 0x2026, 0xA0, 0xC0, 0xC3, 0xD5, 0x152, 0x153,
	0x2013, 0x2014, 0x201C, 0x201D, 0x2018, 0x2019, 0xF7, 0x25CA, 0xFF, 0x178, 0x2044, 0x20AC, 0x2039, 0x203A, 0xFB01, 0xFB02,
	0x2021, 0xB7, 0x201A, 0x201E, 0x2030, 0xC2, 0xCA, 0xC1, 0xCB, 0xC8, 0xCD, 0xCE, 0xCF, 0xCC, 0xD3, 0xD4,
	0xF8FF, 0xD2, 0xDA, 0xDB, 0xD9, 0x131, 0x2C6, 0x2DC, 0xAF, 0x2D8, 0x2D9, 0x2DA, 0xB8, 0x2DD, 0x2DB, 0x2C7,
}

var (
	glyphNames = map[string]string{
		"fi": "fi", "fl": "fl", "ff": "ff", "ffi": "ffi", "ffl": "ffl", "minus": "-",
		"fraction": "/", "dotlessi": string(rune(0x131)), "Lslash": string(rune(0x141)),
		"lslash": string(rune(0x142)), "nbspace": " ", "sfthyphen": "-", "middot": string(rune(0xB7)),
		"space": " ", "hyphen": "-", "breve": string(rune(0x2D8)), "dotaccent": string(rune(0x2D9)),
		"ring": string(rune(0x2DA)), "ogonek": string(rune(0x2DB)), "caron": string(rune(0x2C7)),
		"hungarumlaut": string(rune(0x2DD)), "nonbreakingspace": " ",
	}
	winAnsi, standard, macRoman [256]string
)

func init() {
	for i, n := range asciiNames {
		if _, ok := glyphNames[n]; !ok {
			glyphNames[n] = string(rune(32 + i))
		}
		winAnsi[32+i] = string(rune(32 + i))
	}
	for i, n := range latin1Names {
		if _, ok := glyphNames[n]; !ok {
			glyphNames[n] = string(rune(160 + i))
		}
		winAnsi[160+i] = string(rune(160 + i))
	}
	for c, r := range winAnsiHigh {
		glyphNames[winAnsiHighNames[c]] = string(r)
		winAnsi[c] = string(r)
	}
	// StandardEncoding: ASCII but for curly quotes at 39 and 96.
	for i := 32; i < 127; i++ {
		standard[i] = winAnsi[i]
	}
	standard[39], standard[96] = string(rune(0x2019)), string(rune(0x2018))
	for c, n := range standardHigh {
		standard[c] = glyphNames[n]
	}
	// MacRomanEncoding: ASCII, then its own upper half.
	for i := 32; i < 127; i++ {
		macRoman[i] = winAnsi[i]
	}
	for i, r := range macRomanHigh {
		macRoman[128+i] = string(r)
	}
	winAnsi[160] = " "
	macRoman[0xCA] = " "
}
