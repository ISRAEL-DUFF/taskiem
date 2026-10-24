package pdf

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// A TrueType font, read for what embedding it in a PDF needs: the glyph
// each character maps to (cmap), each glyph's advance width (hhea, hmtx),
// the design units and bounding box (head), the ascent and descent (hhea)
// and the cap height (OS/2). The whole file is embedded as FontFile2;
// nothing is re-encoded.

// Font is a parsed TrueType font.
type Font struct {
	Data       []byte
	UnitsPerEm int
	BBox       [4]int // xMin, yMin, xMax, yMax, in design units
	Ascent     int
	Descent    int
	CapHeight  int
	NumGlyphs  int
	advances   []uint16
	cmap       map[rune]uint16
}

// ParseFont reads a TrueType (glyf-based) font.
func ParseFont(data []byte) (*Font, error) {
	if len(data) < 12 {
		return nil, errors.New("pdf: font too short")
	}
	switch binary.BigEndian.Uint32(data) {
	case 0x00010000, 0x74727565: // 1.0, 'true'
	default:
		return nil, errors.New("pdf: not a TrueType font")
	}
	n := int(binary.BigEndian.Uint16(data[4:]))
	tables := map[string][]byte{}
	for i := range n {
		off := 12 + 16*i
		if off+16 > len(data) {
			return nil, errors.New("pdf: font table directory cut short")
		}
		tag := string(data[off : off+4])
		start, length := binary.BigEndian.Uint32(data[off+8:]), binary.BigEndian.Uint32(data[off+12:])
		if uint64(start)+uint64(length) > uint64(len(data)) {
			return nil, fmt.Errorf("pdf: font table %s out of range", tag)
		}
		tables[tag] = data[start : start+length]
	}
	f := &Font{Data: data, cmap: map[rune]uint16{}}
	head, ok := tables["head"]
	if !ok || len(head) < 54 {
		return nil, errors.New("pdf: font has no head table")
	}
	f.UnitsPerEm = int(binary.BigEndian.Uint16(head[18:]))
	if f.UnitsPerEm == 0 {
		f.UnitsPerEm = 1000
	}
	for i := range 4 {
		f.BBox[i] = int(int16(binary.BigEndian.Uint16(head[36+2*i:])))
	}
	hhea, ok := tables["hhea"]
	if !ok || len(hhea) < 36 {
		return nil, errors.New("pdf: font has no hhea table")
	}
	f.Ascent = int(int16(binary.BigEndian.Uint16(hhea[4:])))
	f.Descent = int(int16(binary.BigEndian.Uint16(hhea[6:])))
	metrics := int(binary.BigEndian.Uint16(hhea[34:]))
	maxp, ok := tables["maxp"]
	if !ok || len(maxp) < 6 {
		return nil, errors.New("pdf: font has no maxp table")
	}
	f.NumGlyphs = int(binary.BigEndian.Uint16(maxp[4:]))
	hmtx, ok := tables["hmtx"]
	if !ok || len(hmtx) < 4*metrics {
		return nil, errors.New("pdf: font has no hmtx table")
	}
	f.advances = make([]uint16, metrics)
	for i := range metrics {
		f.advances[i] = binary.BigEndian.Uint16(hmtx[4*i:])
	}
	f.CapHeight = f.Ascent
	if os2, ok := tables["OS/2"]; ok && len(os2) >= 90 && binary.BigEndian.Uint16(os2) >= 2 {
		f.CapHeight = int(int16(binary.BigEndian.Uint16(os2[88:])))
	}
	cmap, ok := tables["cmap"]
	if !ok {
		return nil, errors.New("pdf: font has no cmap table")
	}
	if err := f.readCmap(cmap); err != nil {
		return nil, err
	}
	return f, nil
}

// readCmap takes the first Unicode subtable of format 4 or 12.
func (f *Font) readCmap(t []byte) error {
	if len(t) < 4 {
		return errors.New("pdf: cmap cut short")
	}
	n := int(binary.BigEndian.Uint16(t[2:]))
	var best []byte
	bestScore := -1
	for i := range n {
		off := 4 + 8*i
		if off+8 > len(t) {
			break
		}
		platform, encoding := binary.BigEndian.Uint16(t[off:]), binary.BigEndian.Uint16(t[off+2:])
		start := binary.BigEndian.Uint32(t[off+4:])
		if uint64(start)+4 > uint64(len(t)) {
			continue
		}
		format := binary.BigEndian.Uint16(t[start:])
		score := -1
		switch {
		case platform == 3 && encoding == 10 && format == 12:
			score = 3
		case platform == 0 && format == 12:
			score = 3
		case platform == 3 && encoding == 1 && format == 4:
			score = 2
		case platform == 0 && format == 4:
			score = 1
		}
		if score > bestScore {
			best, bestScore = t[start:], score
		}
	}
	if best == nil {
		return errors.New("pdf: font has no Unicode cmap")
	}
	switch binary.BigEndian.Uint16(best) {
	case 4:
		return f.readFormat4(best)
	case 12:
		return f.readFormat12(best)
	}
	return errors.New("pdf: unsupported cmap format")
}

func (f *Font) readFormat4(t []byte) error {
	if len(t) < 16 {
		return errors.New("pdf: cmap format 4 cut short")
	}
	segX2 := int(binary.BigEndian.Uint16(t[6:]))
	seg := segX2 / 2
	ends, starts, deltas, ranges := 14, 16+segX2, 16+2*segX2, 16+3*segX2
	if ranges+segX2 > len(t) {
		return errors.New("pdf: cmap format 4 cut short")
	}
	for i := range seg {
		end := rune(binary.BigEndian.Uint16(t[ends+2*i:]))
		start := rune(binary.BigEndian.Uint16(t[starts+2*i:]))
		delta := binary.BigEndian.Uint16(t[deltas+2*i:])
		ro := int(binary.BigEndian.Uint16(t[ranges+2*i:]))
		if start == 0xFFFF {
			continue
		}
		for c := start; c <= end && c != 0xFFFF; c++ {
			var g uint16
			if ro == 0 {
				g = uint16(c) + delta
			} else {
				idx := ranges + 2*i + ro + 2*int(c-start)
				if idx+2 > len(t) {
					continue
				}
				g = binary.BigEndian.Uint16(t[idx:])
				if g != 0 {
					g += delta
				}
			}
			if g != 0 {
				f.cmap[c] = g
			}
		}
	}
	return nil
}

func (f *Font) readFormat12(t []byte) error {
	if len(t) < 16 {
		return errors.New("pdf: cmap format 12 cut short")
	}
	groups := int(binary.BigEndian.Uint32(t[12:]))
	for i := range groups {
		off := 16 + 12*i
		if off+12 > len(t) {
			break
		}
		start, end, g := binary.BigEndian.Uint32(t[off:]), binary.BigEndian.Uint32(t[off+4:]), binary.BigEndian.Uint32(t[off+8:])
		if end-start > 0x10000 {
			continue
		}
		for c := start; c <= end; c++ {
			if gid := g + (c - start); gid != 0 && gid < 0x10000 {
				f.cmap[rune(c)] = uint16(gid)
			}
		}
	}
	return nil
}

// Glyph is the glyph a character maps to (0, .notdef, when the font has
// none).
func (f *Font) Glyph(r rune) uint16 { return f.cmap[r] }

// Has reports whether the font has a glyph for r.
func (f *Font) Has(r rune) bool { return f.cmap[r] != 0 }

// Advance is a glyph's advance width in design units.
func (f *Font) Advance(g uint16) int {
	if len(f.advances) == 0 {
		return 0
	}
	if int(g) >= len(f.advances) {
		return int(f.advances[len(f.advances)-1])
	}
	return int(f.advances[g])
}

// Width is a string's width at size, in points.
func (f *Font) Width(s string, size float64) float64 {
	units := 0
	for _, r := range s {
		units += f.Advance(f.Glyph(r))
	}
	return float64(units) * size / float64(f.UnitsPerEm)
}
