package media

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"testing"
)

// A link preview's picture (chat-previews-and-editing.md §2.2): cleaned as
// a chat photo, its longest side at 800 and 320 pixels, never larger than
// it came; refused over 2 MB or when it is not a picture by its bytes.
func TestCleanLinkImage(t *testing.T) {
	enc := func(w, h int) []byte {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, halves(w, h), nil); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	size := func(data []byte) (int, int) {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Width, cfg.Height
	}
	img, err := CleanLinkImage(enc(1200, 630))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(img.Big); w != 800 || h != 420 || img.Width != 800 || img.Height != 420 || img.Type != "image/jpeg" {
		t.Fatalf("big: %dx%d, %+v", w, h, img.Type)
	}
	if w, h := size(img.Small); w != 320 || h != 168 {
		t.Fatalf("small: %dx%d", w, h)
	}
	// Tall: by its longest side.
	img, err = CleanLinkImage(enc(600, 1200))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(img.Big); w != 400 || h != 800 {
		t.Fatalf("tall: %dx%d", w, h)
	}
	// Small already: kept at its size, one picture for both.
	img, err = CleanLinkImage(enc(200, 200))
	if err != nil || img.Width != 200 || !bytes.Equal(img.Big, img.Small) {
		t.Fatalf("small already: %+v %v", img.Width, err)
	}
	if _, err := CleanLinkImage(append(enc(1200, 630), make([]byte, MaxLinkImageBytes)...)); !errors.Is(err, ErrImage) {
		t.Fatalf("over 2 MB: %v", err)
	}
	if _, err := CleanLinkImage([]byte("<html><title>not a picture</title></html>")); !errors.Is(err, ErrImage) {
		t.Fatalf("not a picture: %v", err)
	}
}
