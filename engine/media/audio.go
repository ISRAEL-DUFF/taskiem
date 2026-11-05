package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Voice notes in a conversation between businesses (business links L3,
// §13: "stripped of metadata"). A voice note is told by its container
// and what the container holds, not by its first bytes alone: an Ogg
// page whose first packet is Vorbis or Opus, a Matroska or WebM file
// whose tracks are all audio, an MPEG audio stream of consecutive valid
// frames, or an MP4 whose tracks are all sound. A container with a video
// track is refused. What is kept is the sound: ID3 tags, Vorbis and Opus
// comments, Matroska tags and titles, and MP4 user data are stripped or
// blanked before the file is stored.

// cleanVoice reports data's audio type ("" when it is not a voice note)
// and the bytes to keep, with their metadata stripped. ErrFile says the
// data is audio in a container that is refused (a video track).
func cleanVoice(data []byte) (string, []byte, error) {
	switch {
	case len(data) >= 4 && string(data[:4]) == "OggS":
		return cleanOgg(data)
	case len(data) >= 4 && data[0] == 0x1A && data[1] == 0x45 && data[2] == 0xDF && data[3] == 0xA3:
		return cleanMatroska(data)
	case len(data) >= 3 && string(data[:3]) == "ID3", isMPEGFrame(data):
		return cleanMP3(data)
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		return cleanMP4(data)
	}
	return "", nil, nil
}

// --- MP3 ---

// mpegFrameLen is the length of the MPEG audio frame starting at b, 0
// when b does not start with a valid frame header.
func mpegFrameLen(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return 0
	}
	version := (b[1] >> 3) & 3 // 0: 2.5, 1: reserved, 2: 2, 3: 1
	layer := (b[1] >> 1) & 3   // 0: reserved, 1: III, 2: II, 3: I
	bitrateIdx := b[2] >> 4
	rateIdx := (b[2] >> 2) & 3
	padding := int((b[2] >> 1) & 1)
	if version == 1 || layer == 0 || bitrateIdx == 0 || bitrateIdx == 15 || rateIdx == 3 {
		return 0
	}
	rates := [4][3]int{{11025, 12000, 8000}, {}, {22050, 24000, 16000}, {44100, 48000, 32000}}
	rate := rates[version][rateIdx]
	var bitrate int
	switch {
	case version == 3 && layer == 3: // MPEG 1 layer I
		bitrate = []int{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448}[bitrateIdx]
	case version == 3 && layer == 2:
		bitrate = []int{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384}[bitrateIdx]
	case version == 3:
		bitrate = []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}[bitrateIdx]
	case layer == 3: // MPEG 2 and 2.5 layer I
		bitrate = []int{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256}[bitrateIdx]
	default:
		bitrate = []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}[bitrateIdx]
	}
	bitrate *= 1000
	switch {
	case layer == 3:
		return (12*bitrate/rate + padding) * 4
	case layer == 2 || version == 3:
		return 144*bitrate/rate + padding
	default:
		return 72*bitrate/rate + padding
	}
}

// isMPEGFrame reports whether b starts with two consecutive valid MPEG
// audio frames (one frame alone is too easy to meet by chance).
func isMPEGFrame(b []byte) bool {
	n := mpegFrameLen(b)
	if n == 0 || len(b) < n+4 {
		return false
	}
	return mpegFrameLen(b[n:]) > 0
}

// cleanMP3 drops an ID3v2 tag at the front, an ID3v1 tag at the back and
// an APE tag at the back, and checks what is left starts with audio frames.
func cleanMP3(data []byte) (string, []byte, error) {
	out := data
	if len(out) >= 10 && string(out[:3]) == "ID3" {
		size := 0
		for _, c := range out[6:10] {
			if c&0x80 != 0 {
				return "", nil, fmt.Errorf("%w: not an MP3", ErrFile)
			}
			size = size<<7 | int(c)
		}
		n := 10 + size
		if out[5]&0x10 != 0 {
			n += 10 // a footer
		}
		if n > len(out) {
			return "", nil, fmt.Errorf("%w: not an MP3", ErrFile)
		}
		out = out[n:]
	}
	if len(out) >= 128 && string(out[len(out)-128:len(out)-125]) == "TAG" {
		out = out[:len(out)-128]
	}
	if len(out) >= 32 && string(out[len(out)-32:len(out)-24]) == "APETAGEX" {
		size := int(binary.LittleEndian.Uint32(out[len(out)-20:]))
		flags := binary.LittleEndian.Uint32(out[len(out)-8:])
		if flags&(1<<31) != 0 {
			size += 32 // a header too
		}
		if size <= len(out) {
			out = out[:len(out)-size]
		}
	}
	if !isMPEGFrame(out) {
		return "", nil, fmt.Errorf("%w: not an MP3", ErrFile)
	}
	return "audio/mpeg", out, nil
}

// --- Ogg ---

// oggCRC is Ogg's page checksum: CRC-32 with polynomial 0x04c11db7,
// neither reflected nor inverted.
var oggCRCTable = func() [256]uint32 {
	var t [256]uint32
	for i := range t {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return t
}()

func oggCRC(page []byte) uint32 {
	var crc uint32
	for _, b := range page {
		crc = crc<<8 ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}

// oggPage is one page of an Ogg stream: where it is in the file, and the
// byte ranges of its segments' data.
type oggPage struct {
	off, size int
	serial    uint32
	body      int // the data's offset in the file
	lacing    []byte
}

// parseOgg reads every page; a malformed file answers an error.
func parseOgg(data []byte) ([]oggPage, error) {
	var pages []oggPage
	for off := 0; off < len(data); {
		if off+27 > len(data) || string(data[off:off+4]) != "OggS" || data[off+4] != 0 {
			return nil, fmt.Errorf("%w: not an Ogg file", ErrFile)
		}
		n := int(data[off+26])
		if off+27+n > len(data) {
			return nil, fmt.Errorf("%w: not an Ogg file", ErrFile)
		}
		p := oggPage{off: off, serial: binary.LittleEndian.Uint32(data[off+14:]), body: off + 27 + n, lacing: data[off+27 : off+27+n]}
		size := 0
		for _, l := range p.lacing {
			size += int(l)
		}
		p.size = 27 + n + size
		if off+p.size > len(data) {
			return nil, fmt.Errorf("%w: not an Ogg file", ErrFile)
		}
		pages = append(pages, p)
		off += p.size
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: not an Ogg file", ErrFile)
	}
	return pages, nil
}

// oggPackets are the byte ranges ([start, end) in the file) of the first
// n packets of the stream whose serial the first page has, each packet
// a list of ranges (a packet may span pages).
func oggPackets(data []byte, pages []oggPage, n int) [][][2]int {
	var packets [][][2]int
	var cur [][2]int
	serial := pages[0].serial
	for _, p := range pages {
		if p.serial != serial {
			continue
		}
		at := p.body
		for _, l := range p.lacing {
			if l > 0 {
				cur = append(cur, [2]int{at, at + int(l)})
			}
			at += int(l)
			if l < 255 {
				packets = append(packets, cur)
				cur = nil
				if len(packets) == n {
					return packets
				}
			}
		}
	}
	return packets
}

func oggRead(data []byte, ranges [][2]int) []byte {
	var out []byte
	for _, r := range ranges {
		out = append(out, data[r[0]:r[1]]...)
	}
	return out
}

func oggWrite(data []byte, ranges [][2]int, b []byte) {
	for _, r := range ranges {
		n := copy(data[r[0]:r[1]], b)
		b = b[n:]
	}
}

// cleanOgg accepts an Ogg stream whose first packet is Vorbis or Opus and
// blanks the user comments of its comment header, in place: the packet
// keeps its length (so the pages keep their lacing), says the vendor and
// no comments, and is zero after that; each page it touches gets its
// checksum again.
func cleanOgg(data []byte) (string, []byte, error) {
	pages, err := parseOgg(data)
	if err != nil {
		return "", nil, err
	}
	packets := oggPackets(data, pages, 2)
	if len(packets) < 2 {
		return "", nil, fmt.Errorf("%w: not an Ogg voice note", ErrFile)
	}
	first := oggRead(data, packets[0])
	var magic string
	switch {
	case bytes.HasPrefix(first, []byte("\x01vorbis")):
		magic = "\x03vorbis"
	case bytes.HasPrefix(first, []byte("OpusHead")):
		magic = "OpusTags"
	default:
		return "", nil, fmt.Errorf("%w: an Ogg file that is not a Vorbis or Opus voice note", ErrFile)
	}
	comment := oggRead(data, packets[1])
	if !bytes.HasPrefix(comment, []byte(magic)) || len(comment) < len(magic)+4 {
		return "", nil, fmt.Errorf("%w: not an Ogg voice note", ErrFile)
	}
	vendorLen := int(binary.LittleEndian.Uint32(comment[len(magic):]))
	if len(magic)+4+vendorLen+4 > len(comment) {
		return "", nil, fmt.Errorf("%w: not an Ogg voice note", ErrFile)
	}
	out := bytes.Clone(data)
	fresh := make([]byte, len(comment))
	n := copy(fresh, comment[:len(magic)+4+vendorLen]) // the magic and the vendor
	n += 4                                             // no comments
	if magic == "\x03vorbis" && n < len(fresh) {
		fresh[n] = 1 // the framing bit
	}
	oggWrite(out, packets[1], fresh)
	// The pages the comment header is on, checksummed again.
	for _, p := range pages {
		touched := false
		for _, r := range packets[1] {
			if r[0] >= p.off && r[0] < p.off+p.size {
				touched = true
			}
		}
		if !touched {
			continue
		}
		page := out[p.off : p.off+p.size]
		for i := 22; i < 26; i++ {
			page[i] = 0
		}
		binary.LittleEndian.PutUint32(page[22:], oggCRC(page))
	}
	return "audio/ogg", out, nil
}

// --- Matroska and WebM ---

// ebmlVint reads a variable-length integer at b: its value, its length,
// and whether it is the reserved "unknown" size; length 0 when b is short.
func ebmlVint(b []byte) (uint64, int, bool) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0, false
	}
	n := 1
	for mask := byte(0x80); b[0]&mask == 0; mask >>= 1 {
		n++
	}
	if len(b) < n {
		return 0, 0, false
	}
	v := uint64(b[0] & (0xFF >> n))
	all := v == uint64(0xFF>>n)
	for i := 1; i < n; i++ {
		v = v<<8 | uint64(b[i])
		all = all && b[i] == 0xFF
	}
	return v, n, all
}

// ebmlID reads an element id at b (its bytes as a number, marker kept).
func ebmlID(b []byte) (uint32, int) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0
	}
	n := 1
	for mask := byte(0x80); b[0]&mask == 0; mask >>= 1 {
		n++
	}
	if n > 4 || len(b) < n {
		return 0, 0
	}
	var id uint32
	for i := range n {
		id = id<<8 | uint32(b[i])
	}
	return id, n
}

// Matroska element ids.
const (
	ebmlHeader     = 0x1A45DFA3
	ebmlDocType    = 0x4282
	mkvSegment     = 0x18538067
	mkvInfo        = 0x1549A966
	mkvTitle       = 0x7BA9
	mkvTracks      = 0x1654AE6B
	mkvTrackEntry  = 0xAE
	mkvTrackType   = 0x83
	mkvTags        = 0x1254C367
	mkvCluster     = 0x1F43B675
	mkvAttachments = 0x1941A469
	mkvVoid        = 0xEC
)

// ebmlWalk calls f for each child element of b, with its id, where it
// starts (its id's first byte, in the file), where its data is and how
// long; an unknown-sized child ends the walk (true from f stops it too).
func ebmlWalk(b []byte, base int, f func(id uint32, start, at, size int, unknown bool) bool) {
	for off := 0; off < len(b); {
		id, n := ebmlID(b[off:])
		if n == 0 {
			return
		}
		size, m, unknown := ebmlVint(b[off+n:])
		if m == 0 {
			return
		}
		at := off + n + m
		end := len(b)
		if !unknown && size <= uint64(len(b)-at) { //nolint:gosec // at <= len(b): the size field was read from b
			end = at + int(size) //nolint:gosec // size <= len(b)-at, checked above
		} else {
			unknown = true
		}
		if f(id, base+off, base+at, end-at, unknown) || unknown {
			return
		}
		off = end
	}
}

// ebmlVoid turns the element starting at start in out, whose data is
// [at, at+size), into a Void element of the same total length, zero
// inside.
func ebmlVoid(out []byte, start, at, size int) {
	total := at + size - start
	if total < 2 {
		return
	}
	for i := start; i < at+size; i++ {
		out[i] = 0
	}
	out[start] = mkvVoid
	n := min(total-1, 8)       // the size field's length
	v := uint64(total - 1 - n) //nolint:gosec // n <= total-1, so not negative
	for i := range n {
		out[start+1+i] = byte(v >> (8 * (n - 1 - i))) //nolint:gosec // the byte wanted, big-endian
	}
	out[start+1] |= 0x80 >> (n - 1)
}

// cleanMatroska accepts a WebM or Matroska file whose tracks are all
// audio, with its Tags, Attachments and Title voided.
func cleanMatroska(data []byte) (string, []byte, error) {
	bad := fmt.Errorf("%w: not a WebM voice note", ErrFile)
	id, n := ebmlID(data)
	if id != ebmlHeader || n == 0 {
		return "", nil, bad
	}
	hsize, m, unknown := ebmlVint(data[n:])
	if m == 0 || unknown || hsize > uint64(len(data)-n-m) { //nolint:gosec // n+m <= len(data): the size field was read from data
		return "", nil, bad
	}
	docType := ""
	body := n + m + int(hsize) //nolint:gosec // hsize <= len(data)-n-m, checked above
	ebmlWalk(data[n+m:body], n+m, func(id uint32, _, at, size int, _ bool) bool {
		if id == ebmlDocType {
			docType = string(data[at : at+size])
		}
		return false
	})
	if docType != "webm" && docType != "matroska" {
		return "", nil, bad
	}
	out := bytes.Clone(data)
	audio, video := 0, 0
	ebmlWalk(data[body:], body, func(id uint32, _, at, size int, _ bool) bool {
		if id != mkvSegment {
			return false
		}
		ebmlWalk(data[at:at+size], at, func(id uint32, start, at, size int, unknown bool) bool {
			switch id {
			case mkvTags, mkvAttachments:
				if !unknown {
					ebmlVoid(out, start, at, size)
				}
			case mkvInfo:
				ebmlWalk(data[at:at+size], at, func(id uint32, start, at, size int, unknown bool) bool {
					if id == mkvTitle && !unknown {
						ebmlVoid(out, start, at, size)
					}
					return false
				})
			case mkvTracks:
				ebmlWalk(data[at:at+size], at, func(id uint32, _, at, size int, _ bool) bool {
					if id != mkvTrackEntry {
						return false
					}
					ebmlWalk(data[at:at+size], at, func(id uint32, _, at, size int, _ bool) bool {
						if id == mkvTrackType && size >= 1 {
							switch data[at+size-1] {
							case 1:
								video++
							case 2:
								audio++
							}
						}
						return false
					})
					return false
				})
			case mkvCluster:
				return true
			}
			return false
		})
		return true
	})
	if video > 0 {
		return "", nil, fmt.Errorf("%w: a video, not a voice note", ErrFile)
	}
	if audio == 0 {
		return "", nil, bad
	}
	return "audio/webm", out, nil
}

// --- MP4 ---

// mp4Walk calls f for each box in b (data offsets relative to base, as
// in the file), with where the box starts, where its payload starts and
// the payload's length.
func mp4Walk(b []byte, base int, f func(typ string, start, at, size int) bool) {
	for off := 0; off+8 <= len(b); {
		size := int(binary.BigEndian.Uint32(b[off:]))
		typ := string(b[off+4 : off+8])
		hdr := 8
		switch size {
		case 0:
			size = len(b) - off
		case 1:
			if off+16 > len(b) {
				return
			}
			large := binary.BigEndian.Uint64(b[off+8:])
			if large > uint64(len(b)-off) {
				return
			}
			size, hdr = int(large), 16 //nolint:gosec // large <= len(b)-off, checked above
		}
		if size < hdr || off+size > len(b) {
			return
		}
		if f(typ, base+off, base+off+hdr, size-hdr) {
			return
		}
		off += size
	}
}

// cleanMP4 accepts an MP4 whose tracks are all sound, with its user data
// and metadata boxes made free boxes, zero inside.
func cleanMP4(data []byte) (string, []byte, error) {
	bad := fmt.Errorf("%w: not an MP4 voice note", ErrFile)
	out := bytes.Clone(data)
	free := func(start, at, size int) {
		copy(out[start+4:start+8], "free")
		for i := at; i < at+size; i++ {
			out[i] = 0
		}
	}
	sound, video, moov := 0, 0, false
	mp4Walk(data, 0, func(typ string, start, at, size int) bool {
		switch typ {
		case "meta", "udta":
			free(start, at, size)
		case "moov":
			moov = true
			mp4Walk(data[at:at+size], at, func(typ string, start, at, size int) bool {
				switch typ {
				case "meta", "udta":
					free(start, at, size)
				case "trak":
					mp4Walk(data[at:at+size], at, func(typ string, start, at, size int) bool {
						switch typ {
						case "meta", "udta":
							free(start, at, size)
						case "mdia":
							mp4Walk(data[at:at+size], at, func(typ string, _, at, size int) bool {
								if typ == "hdlr" && size >= 12 {
									switch string(data[at+8 : at+12]) {
									case "soun":
										sound++
									case "vide":
										video++
									}
								}
								return false
							})
						}
						return false
					})
				}
				return false
			})
		}
		return false
	})
	if video > 0 {
		return "", nil, fmt.Errorf("%w: a video, not a voice note", ErrFile)
	}
	if !moov || sound == 0 {
		return "", nil, bad
	}
	return "audio/mp4", out, nil
}
