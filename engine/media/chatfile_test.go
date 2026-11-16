package media

import (
	"bytes"
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
		"words":               []byte("just some words"),
		"nothing":             nil,
		"a large photo":       append(append([]byte{}, b.Bytes()...), make([]byte, MaxChatPhotoBytes)...),
		"a large PDF":         append([]byte("%PDF-1.4\n"), make([]byte, MaxChatFileBytes)...),
	} {
		if _, _, err := CheckChatFile(data); !errors.Is(err, ErrFile) && !errors.Is(err, ErrImage) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
