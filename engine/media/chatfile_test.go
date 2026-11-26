package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// The files of an app conversation (taskiem-for-everyone §8.5): a photo is
// cleaned (re-encoded, so its metadata goes) and held to 10 MB, a PDF to
// 25 MB and refused with scripts, anything else is refused.
func TestCheckChatFile(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 9, A: 255}) //nolint:gosec // bounded
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	ctype, clean, err := CheckChatFile(b.Bytes())
	if err != nil || (ctype != "image/png" && ctype != "image/jpeg") || len(clean) == 0 || bytes.Equal(clean, b.Bytes()) {
		t.Fatalf("a photo: %s %d %v", ctype, len(clean), err)
	}
	if ctype, _, err := CheckChatFile([]byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\n%%EOF")); err != nil || ctype != "application/pdf" {
		t.Fatalf("a PDF: %s %v", ctype, err)
	}
	for name, data := range map[string][]byte{
		"a PDF with a script": []byte("%PDF-1.4\n1 0 obj << /OpenAction << /JS (app.alert(1)) >> >> endobj"),
		"a program":           append([]byte("\x7fELF\x02\x01\x01"), make([]byte, 64)...),
		"nothing":             nil,
		"a large photo":       append(append([]byte{}, b.Bytes()...), make([]byte, MaxChatPhotoBytes)...),
		"a large PDF":         append([]byte("%PDF-1.4\n"), make([]byte, MaxChatFileBytes)...),
	} {
		if _, _, err := CheckChatFile(data); !errors.Is(err, ErrFile) && !errors.Is(err, ErrImage) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// An Ogg voice note whose length cannot be read from it (no page carries
// a granule) is held to the size of five minutes of speech, as other
// voice notes are; a short one passes.
func TestCheckChatFileOggLengthUnknown(t *testing.T) {
	unknown := func(pages int) []byte {
		ogg := oggOpus(nil, false)
		audio := bytes.Repeat([]byte{0x7c}, 250)
		packets := make([][]byte, 200)
		for i := range packets {
			packets[i] = audio
		}
		for i := range pages {
			ogg = append(ogg, oggPageOf(7, uint32(3+i), 0, packets...)...)
		}
		// Every page's granule "none ends here", checksummed again.
		for i := 0; i+27 <= len(ogg); {
			n := int(ogg[i+26])
			size := 27 + n
			for _, l := range ogg[i+27 : i+27+n] {
				size += int(l)
			}
			page := ogg[i : i+size]
			for j := 6; j < 14; j++ {
				page[j] = 0xFF
			}
			binary.LittleEndian.PutUint32(page[22:], 0)
			binary.LittleEndian.PutUint32(page[22:], oggCRC(page))
			i += size
		}
		return ogg
	}
	if ctype, _, err := CheckChatFile(unknown(2)); err != nil || ctype != "audio/ogg" {
		t.Fatalf("a short Ogg of unknown length: %s %v", ctype, err)
	}
	big := unknown(MaxChatVoiceBytes/(200*250) + 2)
	if _, _, err := CheckChatFile(big); !errors.Is(err, ErrFile) {
		t.Fatalf("a long Ogg of unknown length passed: %d bytes, %v", len(big), err)
	}
}
