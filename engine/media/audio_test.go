package media

import (
	"time"

	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/israel-duff/taskiem/engine/media/mediatest"
)

// Fixtures built here: each container with a tag that must not survive,
// and the same container with a video track, which is refused.

// oggPageOf builds one Ogg page of packets, checksummed.
func oggPageOf(serial, seq uint32, flags byte, packets ...[]byte) []byte {
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
	page := []byte("OggS\x00")
	page = append(page, flags)
	page = append(page, make([]byte, 8)...) // the granule
	page = binary.LittleEndian.AppendUint32(page, serial)
	page = binary.LittleEndian.AppendUint32(page, seq)
	page = append(page, 0, 0, 0, 0) // the checksum
	page = append(page, byte(len(lacing)))
	page = append(page, lacing...)
	page = append(page, body...)
	binary.LittleEndian.PutUint32(page[22:], oggCRC(page))
	return page
}

// oggOpus is an Ogg Opus file with comments; a Theora first packet when
// theora is set.
func oggOpus(comments []string, theora bool) []byte {
	head := []byte("OpusHead\x01\x01\x38\x01\x80\xbb\x00\x00\x00\x00\x00")
	if theora {
		head = []byte("\x80theora\x03\x02\x01")
	}
	tags := []byte("OpusTags")
	tags = binary.LittleEndian.AppendUint32(tags, 7)
	tags = append(tags, "taskiem"...)
	tags = binary.LittleEndian.AppendUint32(tags, uint32(len(comments)))
	for _, c := range comments {
		tags = binary.LittleEndian.AppendUint32(tags, uint32(len(c)))
		tags = append(tags, c...)
	}
	audio := bytes.Repeat([]byte{0x7c}, 300)
	return bytes.Join([][]byte{oggPageOf(7, 0, 2, head), oggPageOf(7, 1, 0, tags), oggPageOf(7, 2, 0, audio, audio)}, nil)
}

// mp3Of is an MPEG 1 layer III stream of frames (417 bytes each at 128
// kbit/s, 44.1 kHz), with an ID3v2 tag before and an ID3v1 tag after when
// tagged.
func mp3Of(tagged bool) []byte {
	frame := append([]byte{0xFF, 0xFB, 0x90, 0x00}, make([]byte, 413)...)
	var out []byte
	if tagged {
		body := []byte("TIT2\x00\x00\x00\x07\x00\x00\x03secret")
		out = append(out, "ID3\x04\x00\x00"...)
		out = append(out, 0, 0, byte(len(body)>>7), byte(len(body)&0x7f))
		out = append(out, body...)
	}
	for range 3 {
		out = append(out, frame...)
	}
	if tagged {
		tag := append([]byte("TAG"), make([]byte, 125)...)
		copy(tag[3:], "secret song")
		out = append(out, tag...)
	}
	return out
}

func ebml(id uint32, payload []byte) []byte {
	var out []byte
	switch {
	case id > 0xFFFFFF:
		out = binary.BigEndian.AppendUint32(out, id)
	case id > 0xFFFF:
		out = append(out, byte(id>>16), byte(id>>8), byte(id))
	case id > 0xFF:
		out = append(out, byte(id>>8), byte(id))
	default:
		out = append(out, byte(id))
	}
	if len(payload) < 127 {
		out = append(out, 0x80|byte(len(payload)))
	} else {
		out = append(out, 0x01)
		out = append(out, make([]byte, 7)...)
		binary.BigEndian.PutUint32(out[len(out)-4:], uint32(len(payload)))
	}
	return append(out, payload...)
}

// webmOf is a WebM file with a title and tags, with an audio track and,
// when video is set, a video track too.
func webmOf(video bool) []byte {
	header := ebml(ebmlHeader, bytes.Join([][]byte{ebml(0x4286, []byte{1}), ebml(ebmlDocType, []byte("webm"))}, nil))
	info := ebml(mkvInfo, bytes.Join([][]byte{ebml(0x2AD7B1, []byte{0x0F, 0x42, 0x40}), ebml(mkvTitle, []byte("secret title"))}, nil))
	tracks := ebml(mkvTrackEntry, bytes.Join([][]byte{ebml(0xD7, []byte{1}), ebml(mkvTrackType, []byte{2}), ebml(0x86, []byte("A_OPUS"))}, nil))
	if video {
		tracks = append(tracks, ebml(mkvTrackEntry, bytes.Join([][]byte{ebml(0xD7, []byte{2}), ebml(mkvTrackType, []byte{1}), ebml(0x86, []byte("V_VP8"))}, nil))...)
	}
	tags := ebml(mkvTags, ebml(0x7373, ebml(0x67C8, bytes.Join([][]byte{ebml(0x45A3, []byte("ARTIST")), ebml(0x4487, []byte("secret artist"))}, nil))))
	cluster := ebml(mkvCluster, bytes.Join([][]byte{ebml(0xE7, []byte{0}), ebml(0xA3, bytes.Repeat([]byte{0x81, 0, 0, 0x80}, 50))}, nil))
	segment := ebml(mkvSegment, bytes.Join([][]byte{info, ebml(mkvTracks, tracks), tags, cluster}, nil))
	return append(header, segment...)
}

func box(typ string, payload []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(payload)))
	out = append(out, typ...)
	return append(out, payload...)
}

// mp4Of is an MP4 with a sound track (and a video track when video is
// set) and user data naming a title.
func mp4Of(video bool) []byte {
	trak := func(handler string) []byte {
		hdlr := box("hdlr", append(append(make([]byte, 8), handler...), make([]byte, 13)...))
		return box("trak", bytes.Join([][]byte{box("tkhd", make([]byte, 84)), box("mdia", bytes.Join([][]byte{box("mdhd", make([]byte, 24)), hdlr}, nil))}, nil))
	}
	traks := trak("soun")
	if video {
		traks = append(traks, trak("vide")...)
	}
	udta := box("udta", box("meta", append(make([]byte, 4), box("ilst", box("\xa9nam", box("data", append(make([]byte, 8), "secret title"...))))...)))
	moov := box("moov", bytes.Join([][]byte{box("mvhd", make([]byte, 100)), traks, udta}, nil))
	return bytes.Join([][]byte{box("ftyp", []byte("M4A \x00\x00\x02\x00M4A isom")), moov, box("mdat", bytes.Repeat([]byte{0x21}, 200))}, nil)
}

func TestVoiceNotesCleaned(t *testing.T) {
	secret := []byte("secret")
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"ogg opus", oggOpus([]string{"TITLE=secret", "ARTIST=secret too"}, false), "audio/ogg"},
		{"webm", webmOf(false), "audio/webm"},
		{"mp3", mp3Of(true), "audio/mpeg"},
		{"mp3 untagged", mp3Of(false), "audio/mpeg"},
		{"m4a", mp4Of(false), "audio/mp4"},
	} {
		ctype, out, err := cleanVoice(c.data)
		if err != nil || ctype != c.want {
			t.Errorf("%s: %q %v", c.name, ctype, err)
			continue
		}
		if bytes.Contains(out, secret) {
			t.Errorf("%s: the tag stayed", c.name)
		}
		if bytes.Contains(c.data, secret) && bytes.Equal(out, c.data) {
			t.Errorf("%s: nothing stripped", c.name)
		}
		if got := voiceType(c.data); got != c.want {
			t.Errorf("%s: voiceType %q", c.name, got)
		}
	}

	// Ogg: the pages keep their length and lacing, and are checksummed again.
	in := oggOpus([]string{"TITLE=secret"}, false)
	_, out, _ := cleanVoice(in)
	if len(out) != len(in) {
		t.Fatalf("ogg: %d bytes from %d", len(out), len(in))
	}
	pages, err := parseOgg(out)
	if err != nil || len(pages) != 3 {
		t.Fatalf("ogg pages: %d %v", len(pages), err)
	}
	for i, p := range pages {
		page := bytes.Clone(out[p.off : p.off+p.size])
		want := binary.LittleEndian.Uint32(page[22:])
		for j := 22; j < 26; j++ {
			page[j] = 0
		}
		if got := oggCRC(page); got != want {
			t.Errorf("ogg page %d: checksum %x, want %x", i, got, want)
		}
	}
	if !bytes.Equal(out[pages[0].off:pages[0].off+pages[0].size], in[pages[0].off:pages[0].off+pages[0].size]) || !bytes.Equal(out[pages[2].off:], in[pages[2].off:]) {
		t.Error("ogg: the head and the sound changed")
	}
	tags := oggRead(out, oggPackets(out, pages, 2)[1])
	if !bytes.HasPrefix(tags, []byte("OpusTags\x07\x00\x00\x00taskiem\x00\x00\x00\x00")) {
		t.Errorf("ogg: the comment header: %q", tags)
	}

	// MP3: the frames alone are kept.
	_, out, _ = cleanVoice(mp3Of(true))
	if !bytes.Equal(out, mp3Of(false)) {
		t.Errorf("mp3: %d bytes, want %d", len(out), len(mp3Of(false)))
	}
	// MP4: the user data is a free box now, zero inside, the rest as it was.
	in = mp4Of(false)
	_, out, _ = cleanVoice(in)
	if len(out) != len(in) || !bytes.Contains(out, []byte("free")) || bytes.Contains(out, []byte("udta")) || !bytes.Equal(out[len(out)-208:], in[len(in)-208:]) {
		t.Errorf("mp4: %q", out)
	}
	// WebM: the title and the tags are void, the rest as it was.
	in = webmOf(false)
	_, out, _ = cleanVoice(in)
	if len(out) != len(in) || bytes.Contains(out, []byte("ARTIST")) || !bytes.Contains(out, []byte("A_OPUS")) {
		t.Errorf("webm: %q", out)
	}

	// Refused: a video track, a Theora stream, and the loose sniffs of
	// before (any ftyp, any EBML, one frame sync, "OggS" alone).
	for name, data := range map[string][]byte{
		"webm video": webmOf(true), "mp4 video": mp4Of(true), "theora": oggOpus(nil, true),
		"ogg alone": []byte("OggS\x00\x02rest"), "ebml alone": {0x1A, 0x45, 0xDF, 0xA3, 0x01}, "id3 alone": []byte("ID3\x04rest"),
		"ftyp alone": []byte("\x00\x00\x00\x20ftypM4A rest"), "mp4 no sound": bytes.Join([][]byte{box("ftyp", []byte("isom")), box("moov", box("mvhd", make([]byte, 100)))}, nil),
	} {
		ctype, _, err := cleanVoice(data)
		if ctype != "" || !errors.Is(err, ErrFile) {
			t.Errorf("%s: %q %v", name, ctype, err)
		}
		if voiceType(data) != "" {
			t.Errorf("%s: sniffed as audio", name)
		}
	}
	for name, data := range map[string][]byte{"one frame sync": {0xFF, 0xFB, 0x90, 0x00}, "pdf": []byte("%PDF-1.4"), "text": []byte("hello")} {
		if ctype, _, err := cleanVoice(data); ctype != "" || err != nil {
			t.Errorf("%s: %q %v", name, ctype, err)
		}
	}
}

// adtsFrame is one ADTS frame of n bytes (header and silence).
func adtsFrame(n int) []byte {
	f := make([]byte, n)
	f[0], f[1] = 0xFF, 0xF1
	f[3] = byte(n >> 11 & 0x03)
	f[4] = byte(n >> 3)
	f[5] = byte(n&0x07)<<5 | 0x1F
	f[6] = 0xFC
	return f
}

func TestAACAndAMRAreKept(t *testing.T) {
	aac := append(append(adtsFrame(40), adtsFrame(52)...), adtsFrame(33)...)
	if ct, out, err := cleanVoice(aac); err != nil || ct != "audio/aac" || !bytes.Equal(out, aac) {
		t.Fatalf("AAC: %q %v", ct, err)
	}
	broken := append(adtsFrame(40), 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07)
	if _, _, err := cleanVoice(broken); !errors.Is(err, ErrFile) {
		t.Fatalf("a stream that only starts like AAC: %v", err)
	}
	amr := append([]byte("#!AMR\n"), 0x3C, 0x48, 0xF5, 0x1F)
	if ct, _, err := cleanVoice(amr); err != nil || ct != "audio/amr" {
		t.Fatalf("AMR: %q %v", ct, err)
	}
	if _, _, err := cleanVoice([]byte("#!AMR\n")); !errors.Is(err, ErrFile) {
		t.Fatalf("an empty AMR: %v", err)
	}
	if AudioType(append([]byte("#!AMR-WB\n"), 1, 2, 3)) != "audio/amr" {
		t.Fatal("AMR-WB")
	}
}

func TestIsOpus(t *testing.T) {
	if !IsOpus(oggOpus(nil, false)) {
		t.Fatal("an Ogg Opus voice note")
	}
	if IsOpus([]byte("OggS not really")) || IsOpus([]byte("ID3 mp3")) {
		t.Fatal("not Opus")
	}
}

func TestMediatestFiles(t *testing.T) {
	if ct, _, err := cleanVoice(mediatest.OggOpus(3 * time.Second)); err != nil || ct != "audio/ogg" || !IsOpus(mediatest.OggOpus(time.Second)) {
		t.Fatalf("mediatest Opus: %q %v", ct, err)
	}
	if ct, _, err := cleanVoice(mediatest.OggVorbis()); err != nil || ct != "audio/ogg" || IsOpus(mediatest.OggVorbis()) {
		t.Fatalf("mediatest Vorbis: %q %v", ct, err)
	}
}
