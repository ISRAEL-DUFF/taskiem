package voice

import "encoding/binary"

// TestOgg builds a well-formed Ogg/Opus stream of the given length for
// tests: an identification page (OpusHead, pre-skip 312), a comment page
// (OpusTags), and one audio page whose granule position marks the end and
// whose packet carries pad bytes of filler, so the stream's size can be
// grown without breaking its pages. Every page has its version byte,
// lacing and checksum, as a recorder would write them, so the stream
// passes the media checks a voice note goes through.
func TestOgg(seconds, pad int) []byte {
	var table [256]uint32
	for i := range table {
		r := uint32(i) << 24 //nolint:gosec // 0..255
		for range 8 {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		table[i] = r
	}
	page := func(seq uint32, flags byte, granule uint64, packet []byte) []byte {
		var lacing []byte
		n := len(packet)
		for n >= 255 {
			lacing = append(lacing, 255)
			n -= 255
		}
		lacing = append(lacing, byte(n)) //nolint:gosec // under 255
		pg := append([]byte("OggS\x00"), flags)
		pg = binary.LittleEndian.AppendUint64(pg, granule)
		pg = binary.LittleEndian.AppendUint32(pg, 7) // serial
		pg = binary.LittleEndian.AppendUint32(pg, seq)
		pg = append(pg, 0, 0, 0, 0, byte(len(lacing))) //nolint:gosec // lacing is short
		pg = append(pg, lacing...)
		pg = append(pg, packet...)
		var crc uint32
		for _, b := range pg {
			crc = crc<<8 ^ table[byte(crc>>24)^b]
		}
		binary.LittleEndian.PutUint32(pg[22:], crc)
		return pg
	}
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1 // version
	head[9] = 1 // one channel
	binary.LittleEndian.PutUint16(head[10:], 312)
	binary.LittleEndian.PutUint32(head[12:], 48000)
	tags := []byte("OpusTags")
	tags = binary.LittleEndian.AppendUint32(tags, 7)
	tags = append(tags, "taskiem"...)
	tags = binary.LittleEndian.AppendUint32(tags, 0)
	audio := append([]byte{0xfc, 0xff}, make([]byte, pad)...)
	out := page(0, 2, 0, head)
	out = append(out, page(1, 0, 0, tags)...)
	return append(out, page(2, 4, uint64(seconds)*48000+312, audio)...) //nolint:gosec // test lengths are small and positive
}
