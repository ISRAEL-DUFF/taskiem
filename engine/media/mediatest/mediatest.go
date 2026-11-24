// Package mediatest makes audio files for tests: an Ogg Opus voice note
// that engine/media accepts and recognises as Opus, of a given length.
package mediatest

import (
	"bytes"
	"encoding/binary"
	"time"
)

var crcTable = func() [256]uint32 {
	var t [256]uint32
	for i := range t {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return t
}()

func crc(b []byte) uint32 {
	var c uint32
	for _, x := range b {
		c = c<<8 ^ crcTable[byte(c>>24)^x]
	}
	return c
}

func page(seq uint32, flags byte, granule uint64, packets ...[]byte) []byte {
	var lacing, body []byte
	for _, p := range packets {
		n := len(p)
		for n >= 255 {
			lacing = append(lacing, 255)
			n -= 255
		}
		lacing = append(lacing, byte(n))
		body = append(body, p...)
	}
	pg := []byte("OggS\x00")
	pg = append(pg, flags)
	pg = binary.LittleEndian.AppendUint64(pg, granule)
	pg = binary.LittleEndian.AppendUint32(pg, 7)
	pg = binary.LittleEndian.AppendUint32(pg, seq)
	pg = append(pg, 0, 0, 0, 0)
	pg = append(pg, byte(len(lacing)))
	pg = append(pg, lacing...)
	pg = append(pg, body...)
	binary.LittleEndian.PutUint32(pg[22:], crc(pg))
	return pg
}

// OggOpus is a valid Ogg Opus voice note lasting d (48 kHz granules).
func OggOpus(d time.Duration) []byte {
	head := []byte("OpusHead\x01\x01\x38\x01\x80\xbb\x00\x00\x00\x00\x00")
	tags := []byte("OpusTags")
	tags = binary.LittleEndian.AppendUint32(tags, 7)
	tags = append(tags, "taskiem"...)
	tags = binary.LittleEndian.AppendUint32(tags, 0)
	audio := bytes.Repeat([]byte{0x7c}, 300)
	return bytes.Join([][]byte{page(0, 2, 0, head), page(1, 0, 0, tags), page(2, 4, uint64(d.Seconds()*48000)+312, audio)}, nil)
}

// OggVorbis is an Ogg file whose first packet is Vorbis: audio, but not a
// voice note WhatsApp accepts.
func OggVorbis() []byte {
	head := append([]byte("\x01vorbis"), make([]byte, 23)...)
	tags := []byte("\x03vorbis")
	tags = binary.LittleEndian.AppendUint32(tags, 7)
	tags = append(tags, "taskiem"...)
	tags = binary.LittleEndian.AppendUint32(tags, 0)
	tags = append(tags, 1)
	return bytes.Join([][]byte{page(0, 2, 0, head), page(1, 0, 0, tags), page(2, 4, 48000, bytes.Repeat([]byte{1}, 100))}, nil)
}
