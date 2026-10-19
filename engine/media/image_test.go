package media

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// halves is w by h: red on the left half, blue on the right.
func halves(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{R: 220, A: 255}
			if x >= w/2 {
				c = color.RGBA{B: 220, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withExif puts an APP1 Exif segment with an orientation and a GPS-ish
// marker string right after the JPEG's start-of-image.
func withExif(jpg []byte, orientation uint16) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("II*\x00")
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(8))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(1)) // one entry
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0x0112))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(3)) // SHORT
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(1))
	_ = binary.Write(&tiff, binary.LittleEndian, orientation)
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(0))
	tiff.WriteString("GPS 6.4541N 3.3947E owner Ada")
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	out := append([]byte{0xFF, 0xD8, 0xFF, 0xE1}, byte((len(seg)+2)>>8), byte(len(seg)+2))
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func reddish(c color.Color) bool {
	r, _, b, _ := c.RGBA()
	return r > 2*b
}

func TestOrientationAndMetadata(t *testing.T) {
	// Stored sideways (40 wide, 20 high) with orientation 6: shown upright
	// it is 20 wide and 40 high, and the stored left (red) is at the top.
	src := withExif(encodeJPEG(t, halves(40, 20)), 6)
	if jpegOrientation(src) != 6 {
		t.Fatalf("orientation %d", jpegOrientation(src))
	}
	out, err := processImage(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].width != 20 || out[0].height != 40 || out[0].ctype != "image/jpeg" {
		t.Fatalf("%d renditions, first %dx%d %s", len(out), out[0].width, out[0].height, out[0].ctype)
	}
	img := decode(t, out[0].data)
	if !reddish(img.At(10, 5)) || reddish(img.At(10, 35)) {
		t.Fatal("not turned upright: red should be at the top")
	}
	if bytes.Contains(out[0].data, []byte("Exif")) || bytes.Contains(out[0].data, []byte("GPS")) {
		t.Fatal("metadata survived re-encoding")
	}
	for o := 1; o <= 8; o++ {
		got := orient(halves(4, 2), o).Bounds()
		if want := (o >= 5); (got.Dx() == 2) != want {
			t.Errorf("orientation %d: %v", o, got)
		}
	}
}

func TestSizes(t *testing.T) {
	out, err := processImage(encodeJPEG(t, halves(3000, 1500)))
	if err != nil {
		t.Fatal(err)
	}
	var got [][2]int
	for _, r := range out {
		got = append(got, [2]int{r.width, r.height})
	}
	if len(got) != 3 || got[0] != [2]int{2000, 1000} || got[1] != [2]int{800, 400} || got[2] != [2]int{320, 160} {
		t.Fatalf("%v", got)
	}
	// Never enlarged; its own size once.
	out, err = processImage(encodeJPEG(t, halves(500, 500)))
	if err != nil || len(out) != 2 || out[0].width != 500 || out[1].width != 320 {
		t.Fatalf("%v %d", err, len(out))
	}
}

func TestPNGTransparency(t *testing.T) {
	logo := image.NewNRGBA(image.Rect(0, 0, 50, 50))
	logo.Set(25, 25, color.NRGBA{G: 200, A: 255}) // the rest is transparent
	var b bytes.Buffer
	_ = png.Encode(&b, logo)
	out, err := processImage(b.Bytes())
	if err != nil || out[0].ctype != "image/png" {
		t.Fatalf("a transparent logo stays a PNG: %v %v", err, out)
	}
	b.Reset()
	_ = png.Encode(&b, halves(50, 50))
	if out, err := processImage(b.Bytes()); err != nil || out[0].ctype != "image/jpeg" {
		t.Fatalf("an opaque PNG becomes a JPEG: %v", err)
	}
}

func TestWebP(t *testing.T) {
	// A 1-by-1 lossless WebP.
	webp, _ := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	out, err := processImage(webp)
	if err != nil || len(out) != 1 || out[0].ctype != "image/jpeg" || out[0].width != 1 {
		t.Fatalf("%v %v", err, out)
	}
}

// pngHeader is a PNG whose header claims w by h, with nothing after it.
func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 17)
	copy(ihdr, "IHDR")
	binary.BigEndian.PutUint32(ihdr[4:], w)
	binary.BigEndian.PutUint32(ihdr[8:], h)
	ihdr[12], ihdr[13] = 8, 6 // 8-bit RGBA
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, 13)
	out = append(out, ihdr...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(ihdr))
}

func TestRefused(t *testing.T) {
	var g bytes.Buffer
	_ = gif.Encode(&g, halves(10, 10), nil)
	for name, data := range map[string][]byte{
		"a decompression bomb": pngHeader(30000, 30000),
		"html":                 []byte("<html><script>alert(1)</script></html>"),
		"a gif":                g.Bytes(),
		"too big":              append([]byte{0xFF, 0xD8, 0xFF}, make([]byte, MaxImageBytes)...),
		"a broken jpeg":        {0xFF, 0xD8, 0xFF, 0xE0, 0, 0},
	} {
		if _, err := processImage(data); !errors.Is(err, ErrImage) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCheckPDF(t *testing.T) {
	ok := []byte("%PDF-1.7\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n/JSON-ish /JSX\n%%EOF")
	if err := checkPDF(ok); err != nil {
		t.Fatalf("a plain PDF: %v", err)
	}
	for _, k := range []string{"/JavaScript", "/JS ", "/JS(", "/Launch", "/OpenAction", "/EmbeddedFile", "/AA<<"} {
		bad := []byte("%PDF-1.7\n1 0 obj << " + k + " (app.alert(1)) >> endobj\n%%EOF")
		if err := checkPDF(bad); !errors.Is(err, ErrFile) {
			t.Errorf("%s: %v", k, err)
		}
	}
	if err := checkPDF([]byte("not a pdf")); !errors.Is(err, ErrFile) {
		t.Error("not a PDF")
	}
}
