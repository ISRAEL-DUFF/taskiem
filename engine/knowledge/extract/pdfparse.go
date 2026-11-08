package extract

// A small PDF object reader, enough to find a document's pages and their
// content streams. It is written here rather than taken from a library so
// that every loop and allocation is bounded: an uploaded PDF is hostile
// input, and the pure-Go readers available can be made to loop for ever
// (a /Prev chain that cycles) or to allocate without limit (an xref /Size
// of billions) before any recover could help.
//
// Instead of trusting the cross-reference table, the reader scans the file
// for "N G obj" definitions, as PDF viewers do to repair a damaged file;
// a later definition of an object replaces an earlier one, which is what
// an incremental update means. Objects packed in object streams are read
// from those streams. What it does not do: decryption (an encrypted file
// is refused), LZW, and image filters, which never carry text.

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/ascii85"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
)

// Work limits for one PDF.
const (
	maxPDFObjects     = 1 << 20   // indirect objects defined
	maxPDFElements    = 8 << 20   // values parsed, all objects together
	maxPDFDepth       = 64        // nesting of arrays and dictionaries
	maxPDFArray       = 1 << 20   // values in one array
	maxStreamBytes    = 32 << 20  // one decoded stream
	maxDecodedBytes   = 256 << 20 // all decoded streams together
	maxObjStmEntries  = 100000    // objects in one object stream
	maxPDFPages       = 2000      // pages read
	maxContentOps     = 20 << 20  // content-stream operators, all pages
	maxFormDepth      = 8         // form XObjects inside form XObjects
	maxGraphicsStates = 64        // q without Q
)

var errPDFLimit = errors.New("the PDF needs more work to read than allowed")

// PDF values. Integers and reals are int64 and float64, booleans bool and
// null nil.
type (
	pdfName    string
	pdfString  string // the raw bytes of a literal or hex string
	pdfKeyword string // an operator or other bare word
	pdfDict    map[string]any
	pdfArray   []any
	pdfRef     struct{ num, gen int64 }
	pdfStream  struct {
		dict pdfDict
		raw  []byte // still encoded
	}
)

func isPDFSpace(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0
}

func isPDFDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// pdfParser reads values from b starting at pos.
type pdfParser struct {
	b     []byte
	pos   int
	refs  bool // "N G R" is a reference (not in content streams)
	depth int
	elems *int // shared count of values parsed, against maxPDFElements
}

func (p *pdfParser) skipSpace() {
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		if isPDFSpace(c) {
			p.pos++
			continue
		}
		if c == '%' {
			for p.pos < len(p.b) && p.b[p.pos] != '\n' && p.b[p.pos] != '\r' {
				p.pos++
			}
			continue
		}
		return
	}
}

var errPDFEOF = errors.New("unexpected end of data")

// token reads one token: a number, string, name, keyword or one of the
// delimiters [ ] << >> { } (as keywords).
func (p *pdfParser) token() (any, error) {
	p.skipSpace()
	if p.pos >= len(p.b) {
		return nil, errPDFEOF
	}
	if p.elems != nil {
		*p.elems++
		if *p.elems > maxPDFElements {
			return nil, errPDFLimit
		}
	}
	c := p.b[p.pos]
	switch c {
	case '[', ']', '{', '}':
		p.pos++
		return pdfKeyword(c), nil
	case '<':
		if p.pos+1 < len(p.b) && p.b[p.pos+1] == '<' {
			p.pos += 2
			return pdfKeyword("<<"), nil
		}
		return p.hexString(), nil
	case '>':
		if p.pos+1 < len(p.b) && p.b[p.pos+1] == '>' {
			p.pos += 2
			return pdfKeyword(">>"), nil
		}
		p.pos++
		return nil, fmt.Errorf("stray > at %d", p.pos-1)
	case '(':
		return p.literalString(), nil
	case ')':
		p.pos++
		return nil, fmt.Errorf("stray ) at %d", p.pos-1)
	case '/':
		return p.name(), nil
	}
	start := p.pos
	for p.pos < len(p.b) && !isPDFSpace(p.b[p.pos]) && !isPDFDelim(p.b[p.pos]) {
		p.pos++
	}
	word := string(p.b[start:p.pos])
	if c == '+' || c == '-' || c == '.' || c >= '0' && c <= '9' {
		if n, err := strconv.ParseInt(word, 10, 64); err == nil {
			return n, nil
		}
		if f, err := strconv.ParseFloat(word, 64); err == nil {
			return f, nil
		}
		// Producers write oddities such as "--5" or "1.2.3"; a reader
		// takes them as zero rather than failing the file.
		return float64(0), nil
	}
	switch word {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	return pdfKeyword(word), nil
}

func (p *pdfParser) name() pdfName {
	p.pos++ // the slash
	var b []byte
	for p.pos < len(p.b) && !isPDFSpace(p.b[p.pos]) && !isPDFDelim(p.b[p.pos]) {
		c := p.b[p.pos]
		if c == '#' && p.pos+2 < len(p.b) {
			if v, err := strconv.ParseUint(string(p.b[p.pos+1:p.pos+3]), 16, 8); err == nil {
				b = append(b, byte(v))
				p.pos += 3
				continue
			}
		}
		b = append(b, c)
		p.pos++
	}
	return pdfName(b)
}

func (p *pdfParser) hexString() pdfString {
	p.pos++ // <
	var out []byte
	hi, half := byte(0), false
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		p.pos++
		var v byte
		switch {
		case c == '>':
			if half {
				out = append(out, hi<<4)
			}
			return pdfString(out)
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			continue // white space, or junk a reader skips
		}
		if half {
			out = append(out, hi<<4|v)
		} else {
			hi = v
		}
		half = !half
	}
	return pdfString(out)
}

func (p *pdfParser) literalString() pdfString {
	p.pos++ // (
	var out []byte
	nest := 1
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		p.pos++
		switch c {
		case '(':
			nest++
		case ')':
			nest--
			if nest == 0 {
				return pdfString(out)
			}
		case '\\':
			if p.pos >= len(p.b) {
				return pdfString(out)
			}
			e := p.b[p.pos]
			p.pos++
			switch e {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case '\r':
				if p.pos < len(p.b) && p.b[p.pos] == '\n' {
					p.pos++
				}
				continue // a line continuation
			case '\n':
				continue
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for i := 0; i < 2 && p.pos < len(p.b) && p.b[p.pos] >= '0' && p.b[p.pos] <= '7'; i++ {
						v = v*8 + int(p.b[p.pos]-'0')
						p.pos++
					}
					c = byte(v) // #nosec G115 -- three octal digits wrap as PDF readers do
				} else {
					c = e
				}
			}
		}
		out = append(out, c)
	}
	return pdfString(out)
}

// object reads one complete value: arrays and dictionaries in full, and
// "N G R" as a reference when refs is set.
func (p *pdfParser) object() (any, error) {
	tok, err := p.token()
	if err != nil {
		return nil, err
	}
	return p.objectFrom(tok)
}

func (p *pdfParser) objectFrom(tok any) (any, error) {
	switch t := tok.(type) {
	case pdfKeyword:
		switch t {
		case "[":
			return p.array()
		case "<<":
			return p.dict()
		}
		return t, nil
	case int64:
		if !p.refs {
			return t, nil
		}
		// Look ahead for "G R".
		save, saveElems := p.pos, p.elemCount()
		if g, err := p.token(); err == nil {
			if gen, ok := g.(int64); ok {
				if r, err := p.token(); err == nil && r == pdfKeyword("R") {
					return pdfRef{t, gen}, nil
				}
			}
		}
		p.pos = save
		p.setElemCount(saveElems)
		return t, nil
	}
	return tok, nil
}

func (p *pdfParser) elemCount() int {
	if p.elems == nil {
		return 0
	}
	return *p.elems
}

func (p *pdfParser) setElemCount(n int) {
	if p.elems != nil {
		*p.elems = n
	}
}

func (p *pdfParser) array() (pdfArray, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxPDFDepth {
		return nil, errPDFLimit
	}
	var a pdfArray
	for {
		tok, err := p.token()
		if err != nil {
			return a, err
		}
		if tok == pdfKeyword("]") {
			return a, nil
		}
		v, err := p.objectFrom(tok)
		if err != nil {
			return a, err
		}
		if len(a) >= maxPDFArray {
			return a, errPDFLimit
		}
		a = append(a, v)
	}
}

func (p *pdfParser) dict() (pdfDict, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxPDFDepth {
		return nil, errPDFLimit
	}
	d := pdfDict{}
	for {
		tok, err := p.token()
		if err != nil {
			return d, err
		}
		if tok == pdfKeyword(">>") {
			return d, nil
		}
		key, ok := tok.(pdfName)
		if !ok {
			// A broken key: skip it, as readers do, and carry on.
			continue
		}
		v, err := p.object()
		if err != nil {
			return d, err
		}
		if kw, ok := v.(pdfKeyword); ok && kw == ">>" {
			return d, nil // a key with no value at the end
		}
		d[string(key)] = v
	}
}

// pdfDoc is a parsed PDF: its objects by number, and its trailers.
type pdfDoc struct {
	data     []byte
	objs     map[int64]pdfEntry
	trailers []pdfEntry // trailer dictionaries and xref stream dictionaries
	elems    int
	decoded  int // bytes decoded so far, against maxDecodedBytes
	ops      int // content operators run so far, against maxContentOps
}

type pdfEntry struct {
	pos int // where it was defined, to let later definitions win
	val any
}

// parsePDF scans data for object definitions and trailers.
func parsePDF(data []byte) (*pdfDoc, error) {
	d := &pdfDoc{data: data, objs: map[int64]pdfEntry{}}
	if err := d.scan(); err != nil {
		return nil, err
	}
	d.readObjectStreams()
	return d, nil
}

func (d *pdfDoc) scan() error {
	data := d.data
	defs := 0
	// next finds the next occurrence of word at or after i, remembering the
	// last answer so that the file is searched once per word, not once per
	// object.
	type cursor struct{ at int }
	nextOf := func(c *cursor, word string, i int) int {
		if c.at >= i || c.at == -1 {
			return c.at
		}
		k := bytes.Index(data[i:], []byte(word))
		if k < 0 {
			c.at = -1
		} else {
			c.at = i + k
		}
		return c.at
	}
	objC, trailerC := &cursor{at: -2}, &cursor{at: -2}
	for i := 0; i < len(data); {
		k := nextOf(objC, "obj", i)
		tk := nextOf(trailerC, "trailer", i)
		if k < 0 && tk < 0 {
			break
		}
		if k >= 0 {
			k -= i
		}
		if tk >= 0 {
			tk -= i
		}
		if tk >= 0 && (k < 0 || tk < k) {
			at := i + tk
			i = at + len("trailer")
			p := &pdfParser{b: data, pos: i, refs: true, elems: &d.elems}
			if v, err := p.object(); err == nil {
				if td, ok := v.(pdfDict); ok {
					d.trailers = append(d.trailers, pdfEntry{at, td})
					i = p.pos
				}
			} else if errors.Is(err, errPDFLimit) {
				return err
			}
			continue
		}
		at := i + k
		i = at + 3
		if at+3 < len(data) && !isPDFSpace(data[at+3]) && !isPDFDelim(data[at+3]) {
			continue // "objstm" or some other word
		}
		start, ok := objStart(data, at)
		if !ok {
			continue
		}
		p := &pdfParser{b: data, pos: start, refs: true, elems: &d.elems}
		num, err1 := p.token()
		gen, err2 := p.token()
		kw, err3 := p.token()
		if err1 != nil || err2 != nil || err3 != nil || kw != pdfKeyword("obj") {
			continue
		}
		n, ok1 := num.(int64)
		_, ok2 := gen.(int64)
		if !ok1 || !ok2 || n < 0 {
			continue
		}
		v, err := p.object()
		if errors.Is(err, errPDFLimit) {
			return err
		}
		if err != nil {
			continue
		}
		if dict, ok := v.(pdfDict); ok {
			save := p.pos
			if t, err := p.token(); err == nil && t == pdfKeyword("stream") {
				s := &pdfStream{dict: dict}
				s.raw, p.pos = streamData(data, p.pos, dict)
				v = s
				if dict["Type"] == pdfName("XRef") {
					d.trailers = append(d.trailers, pdfEntry{start, dict})
				}
			} else {
				p.pos = save
			}
		}
		defs++
		if defs > maxPDFObjects {
			return errPDFLimit
		}
		d.objs[n] = pdfEntry{start, v}
		i = max(i, p.pos)
	}
	return nil
}

// objStart finds where "N G obj" begins, given the offset of "obj": two
// unsigned integers before it, separated by white space.
func objStart(data []byte, at int) (int, bool) {
	j := at - 1
	digits := func() bool {
		end := j
		for j >= 0 && data[j] >= '0' && data[j] <= '9' {
			j--
		}
		return j < end
	}
	spaces := func() bool {
		end := j
		for j >= 0 && isPDFSpace(data[j]) {
			j--
		}
		return j < end
	}
	spaces()
	if !digits() || !spaces() || !digits() {
		return 0, false
	}
	if j >= 0 && !isPDFSpace(data[j]) && !isPDFDelim(data[j]) {
		return 0, false
	}
	return j + 1, true
}

// streamData finds a stream's bytes, starting just after the "stream"
// keyword, and the offset after "endstream". It trusts a direct /Length
// only when "endstream" follows it, and otherwise searches for
// "endstream", since /Length is often wrong or an indirect reference.
func streamData(data []byte, pos int, dict pdfDict) ([]byte, int) {
	if pos < len(data) && data[pos] == '\r' {
		pos++
	}
	if pos < len(data) && data[pos] == '\n' {
		pos++
	}
	if n, ok := dict["Length"].(int64); ok && n >= 0 && n <= int64(len(data)-pos) {
		end := pos + int(n)
		q := end
		for q < len(data) && isPDFSpace(data[q]) {
			q++
		}
		if bytes.HasPrefix(data[q:], []byte("endstream")) {
			return data[pos:end], q + len("endstream")
		}
	}
	k := bytes.Index(data[pos:], []byte("endstream"))
	if k < 0 {
		return data[pos:], len(data)
	}
	raw := data[pos : pos+k]
	raw = bytes.TrimSuffix(raw, []byte("\n"))
	raw = bytes.TrimSuffix(raw, []byte("\r"))
	return raw, pos + k + len("endstream")
}

// readObjectStreams adds the objects packed in object streams. An object
// in a stream is placed at the stream's position, so a later direct
// definition still replaces it and an earlier one does not.
func (d *pdfDoc) readObjectStreams() {
	type objstm struct {
		pos int
		s   *pdfStream
	}
	var streams []objstm
	for _, e := range d.objs {
		if s, ok := e.val.(*pdfStream); ok && s.dict["Type"] == pdfName("ObjStm") {
			streams = append(streams, objstm{e.pos, s})
		}
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].pos < streams[j].pos })
	for _, os := range streams {
		data, err := d.decode(os.s)
		if err != nil {
			continue
		}
		n, _ := d.resolve(os.s.dict["N"]).(int64)
		first, _ := d.resolve(os.s.dict["First"]).(int64)
		if n <= 0 || n > maxObjStmEntries || first <= 0 || first > int64(len(data)) {
			continue
		}
		hdr := &pdfParser{b: data[:first], elems: &d.elems}
		for range n {
			num, err1 := hdr.token()
			off, err2 := hdr.token()
			on, ok1 := num.(int64)
			oo, ok2 := off.(int64)
			if err1 != nil || err2 != nil || !ok1 || !ok2 || oo < 0 || first+oo >= int64(len(data)) {
				break
			}
			if prev, ok := d.objs[on]; ok && prev.pos > os.pos {
				continue
			}
			p := &pdfParser{b: data, pos: int(first + oo), refs: true, elems: &d.elems}
			v, err := p.object()
			if err != nil {
				continue
			}
			d.objs[on] = pdfEntry{os.pos, v}
		}
	}
}

// resolve follows references to the value they name; a missing object is
// null.
func (d *pdfDoc) resolve(v any) any {
	for range 16 {
		r, ok := v.(pdfRef)
		if !ok {
			return v
		}
		e, ok := d.objs[r.num]
		if !ok {
			return nil
		}
		v = e.val
	}
	return nil
}

func (d *pdfDoc) dict(v any) pdfDict {
	switch t := d.resolve(v).(type) {
	case pdfDict:
		return t
	case *pdfStream:
		return t.dict
	}
	return nil
}

func (d *pdfDoc) array(v any) pdfArray {
	a, _ := d.resolve(v).(pdfArray)
	return a
}

func (d *pdfDoc) num(v any) (float64, bool) {
	switch t := d.resolve(v).(type) {
	case int64:
		return float64(t), true
	case float64:
		return t, true
	}
	return 0, false
}

func (d *pdfDoc) name(v any) string {
	n, _ := d.resolve(v).(pdfName)
	return string(n)
}

// decode returns a stream's data with its filters undone, up to
// maxStreamBytes and within the document's budget.
func (d *pdfDoc) decode(s *pdfStream) ([]byte, error) {
	var filters []string
	var parms []pdfDict
	switch f := d.resolve(s.dict["Filter"]).(type) {
	case pdfName:
		filters = []string{string(f)}
		parms = []pdfDict{d.dict(s.dict["DecodeParms"])}
	case pdfArray:
		pa := d.array(s.dict["DecodeParms"])
		for i, x := range f {
			filters = append(filters, d.name(x))
			var pd pdfDict
			if i < len(pa) {
				pd = d.dict(pa[i])
			}
			parms = append(parms, pd)
		}
	}
	data := s.raw
	for i, f := range filters {
		limit := min(maxStreamBytes, maxDecodedBytes-d.decoded)
		if limit <= 0 {
			return nil, errPDFLimit
		}
		var err error
		switch f {
		case "FlateDecode", "Fl":
			data, err = inflate(data, limit)
			if err == nil {
				data, err = unpredict(data, parms[i], d)
			}
		case "ASCIIHexDecode", "AHx":
			p := &pdfParser{b: append(append([]byte{'<'}, data...), '>')}
			data = []byte(p.hexString())
		case "ASCII85Decode", "A85":
			data, err = unascii85(data, limit)
		default:
			// LZW, run-length and image filters: nothing this package
			// needs is stored that way in a modern PDF.
			return nil, fmt.Errorf("unsupported filter %s", f)
		}
		if err != nil {
			return nil, err
		}
		d.decoded += len(data)
	}
	return data, nil
}

// inflate undoes FlateDecode. A stream that is cut short or fails its
// checksum still gives what it decoded, as viewers show it; one with a
// broken zlib header is tried as raw deflate.
func inflate(data []byte, limit int) ([]byte, error) {
	var r io.Reader
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		r = flate.NewReader(bytes.NewReader(data))
	} else {
		r = zr
	}
	out, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if len(out) > limit {
		return nil, errPDFLimit
	}
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return out, nil
}

func unascii85(data []byte, limit int) ([]byte, error) {
	data = bytes.TrimSpace(data)
	data = bytes.TrimPrefix(data, []byte("<~"))
	if i := bytes.Index(data, []byte("~>")); i >= 0 {
		data = data[:i]
	}
	out, err := io.ReadAll(io.LimitReader(ascii85.NewDecoder(bytes.NewReader(data)), int64(limit)+1))
	if len(out) > limit {
		return nil, errPDFLimit
	}
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return out, nil
}

// unpredict undoes the PNG predictors (Predictor 10 to 15) that some
// producers apply before Flate. TIFF prediction is not undone; nothing
// textual uses it.
func unpredict(data []byte, parms pdfDict, d *pdfDoc) ([]byte, error) {
	pred, _ := d.num(parms["Predictor"])
	if pred < 10 {
		return data, nil
	}
	cols, ok := d.num(parms["Columns"])
	if !ok || cols < 1 {
		cols = 1
	}
	colours, ok := d.num(parms["Colors"]) //nolint:misspell // the PDF key
	if !ok || colours < 1 {
		colours = 1
	}
	bpc, ok := d.num(parms["BitsPerComponent"])
	if !ok || bpc < 1 {
		bpc = 8
	}
	if cols > 1<<20 || colours > 32 || bpc > 16 {
		return nil, errors.New("bad predictor parameters")
	}
	bpp := max(1, int(colours*bpc+7)/8)
	row := int(cols*colours*bpc+7) / 8
	var out []byte
	prev := make([]byte, row)
	for len(data) >= row+1 {
		kind, cur := data[0], append([]byte(nil), data[1:row+1]...)
		data = data[row+1:]
		for i := range cur {
			var left, up, upLeft byte
			if i >= bpp {
				left, upLeft = cur[i-bpp], prev[i-bpp]
			}
			up = prev[i]
			switch kind {
			case 1:
				cur[i] += left
			case 2:
				cur[i] += up
			case 3:
				cur[i] += byte((uint(left) + uint(up)) / 2) // #nosec G115 -- the mean of two bytes fits a byte
			case 4:
				cur[i] += paeth(left, up, upLeft)
			}
		}
		out = append(out, cur...)
		prev = cur
	}
	return out, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
