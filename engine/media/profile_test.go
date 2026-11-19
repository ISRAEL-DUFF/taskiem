package media

import (
	"bytes"
	"errors"
	"testing"
)

// A person's photo: cleaned to at most 800 pixels wide, its metadata gone;
// anything that is not a photo refused.
func TestCheckProfilePhoto(t *testing.T) {
	big := encodeJPEG(t, halves(1600, 1200))
	ctype, clean, err := CheckProfilePhoto(withExif(big, 6))
	if err != nil || ctype != "image/jpeg" {
		t.Fatalf("a photo: %q %v", ctype, err)
	}
	if b := decode(t, clean).Bounds(); b.Dx() != 800 {
		t.Fatalf("width %d, want 800", b.Dx())
	}
	if bytes.Contains(clean, []byte("Exif")) {
		t.Fatal("its metadata kept")
	}
	small := encodeJPEG(t, halves(300, 200))
	if _, clean, err := CheckProfilePhoto(small); err != nil || decode(t, clean).Bounds().Dx() != 300 {
		t.Fatalf("a small photo kept at its size: %v", err)
	}
	for _, bad := range [][]byte{nil, []byte("hello, this is not a photo"), []byte("%PDF-1.4\n")} {
		if _, _, err := CheckProfilePhoto(bad); !errors.Is(err, ErrImage) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
