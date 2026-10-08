package voice

import "encoding/binary"

// TestOgg builds a minimal Ogg/Opus stream of the given length for tests:
// an identification page (OpusHead, pre-skip 312) and a last page whose
// granule position marks the end, with pad bytes of filler between.
func TestOgg(seconds, pad int) []byte {
	page := func(granule uint64, payload []byte) []byte {
		h := make([]byte, 27)
		copy(h, "OggS")
		binary.LittleEndian.PutUint64(h[6:], granule)
		h[26] = 1
		return append(append(h, byte(min(len(payload), 255))), payload...)
	}
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	head[9] = 1
	binary.LittleEndian.PutUint16(head[10:], 312)
	binary.LittleEndian.PutUint32(head[12:], 48000)
	out := page(0, head)
	out = append(out, make([]byte, pad)...)
	return append(out, page(uint64(seconds)*48000+312, []byte{0xfc, 0xff})...) //nolint:gosec // test lengths are small and positive
}
