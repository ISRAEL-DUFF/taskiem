package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decodes WebP uploads; they are re-encoded as JPEG
)

// Image limits. An upload is decoded only after its header says it is at
// most MaxPixels, so a small file claiming a huge image (a decompression
// bomb) is refused before it can take the memory.
const (
	MaxImageBytes = 10 << 20
	MaxPixels     = 25_000_000
	jpegQuality   = 82
)

// Widths every image is stored at: the full size for a page opened large,
// and two for lists and cards on small screens (storefronts 6.7).
var Widths = []int{2000, 800, 320}

// ErrImage wraps every refusal of an image.
var ErrImage = errors.New("media: not a usable image")

// rendition is one stored size of an image.
type rendition struct {
	width, height int
	data          []byte
	ctype         string
}

// processImage checks data is a JPEG, PNG or WebP within the limits,
// turns it upright as the camera recorded, and re-encodes it at each of
// Widths (never larger than it came). Re-encoding is what removes the
// metadata: location, camera, the owner's name.
func processImage(data []byte) ([]rendition, error) {
	if len(data) > MaxImageBytes {
		return nil, fmt.Errorf("%w: larger than %d MB", ErrImage, MaxImageBytes>>20)
	}
	sniffed := http.DetectContentType(data)
	switch sniffed {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return nil, fmt.Errorf("%w: a %s file; send a JPEG, PNG or WebP photo", ErrImage, sniffed)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrImage, err)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > MaxPixels {
		return nil, fmt.Errorf("%w: %d by %d pixels is more than %d million", ErrImage, cfg.Width, cfg.Height, MaxPixels/1_000_000)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrImage, err)
	}
	if format == "jpeg" {
		img = orient(img, jpegOrientation(data))
	}
	// PNGs keep their transparency (a logo on any background); everything
	// else becomes a JPEG.
	asPNG := format == "png" && !opaque(img)
	var out []rendition
	for _, w := range Widths {
		b := img.Bounds()
		if w > b.Dx() {
			if len(out) > 0 && out[len(out)-1].width == b.Dx() {
				continue // already stored at its own size
			}
			w = b.Dx()
		}
		r, err := render(img, w, max(1, b.Dy()*w/b.Dx()), asPNG)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// render draws img at w by h pixels and encodes it: a PNG keeping its
// transparency, or a JPEG on white.
func render(img image.Image, w, h int, asPNG bool) (rendition, error) {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if !asPNG {
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	}
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	var buf bytes.Buffer
	r := rendition{width: w, height: h}
	var err error
	if asPNG {
		r.ctype = "image/png"
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, dst)
	} else {
		r.ctype = "image/jpeg"
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality})
	}
	r.data = buf.Bytes()
	return r, err
}

// MaxLinkImageBytes caps the picture of a link's page fetched for its
// preview (docs/spec/chat-previews-and-editing.md §2.2).
const MaxLinkImageBytes = 2 << 20

// LinkImage is a link preview's picture, cleaned as a chat photo is.
type LinkImage struct {
	// Big has its largest side at most 800 pixels, Small at most 320
	// (the same bytes as Big when it is no larger).
	Big, Small []byte
	Type       string
	// Width and Height are Big's.
	Width, Height int
}

// CleanLinkImage checks the picture of a link's page (a JPEG, PNG or WebP
// by its own bytes, at most 2 MB and MaxPixels) and cleans it as a chat
// photo is: turned upright, its metadata gone, re-encoded with its
// largest side at 800 and at 320 pixels (never larger than it came).
func CleanLinkImage(data []byte) (LinkImage, error) {
	if len(data) > MaxLinkImageBytes {
		return LinkImage{}, fmt.Errorf("%w: larger than %d MB", ErrImage, MaxLinkImageBytes>>20)
	}
	switch sniffed := http.DetectContentType(data); sniffed {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return LinkImage{}, fmt.Errorf("%w: a %s file is not a picture", ErrImage, sniffed)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return LinkImage{}, fmt.Errorf("%w: %w", ErrImage, err)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > MaxPixels {
		return LinkImage{}, fmt.Errorf("%w: %d by %d pixels", ErrImage, cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return LinkImage{}, fmt.Errorf("%w: %w", ErrImage, err)
	}
	if format == "jpeg" {
		img = orient(img, jpegOrientation(data))
	}
	asPNG := format == "png" && !opaque(img)
	at := func(side int) (rendition, error) {
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		if long := max(w, h); long > side {
			w, h = max(1, w*side/long), max(1, h*side/long)
		}
		return render(img, w, h, asPNG)
	}
	big, err := at(800)
	if err != nil {
		return LinkImage{}, err
	}
	out := LinkImage{Big: big.data, Small: big.data, Type: big.ctype, Width: big.width, Height: big.height}
	if max(big.width, big.height) > 320 {
		small, err := at(320)
		if err != nil {
			return LinkImage{}, err
		}
		out.Small = small.data
	}
	return out, nil
}

func opaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}

// jpegOrientation reads the EXIF orientation (1 to 8) of a JPEG, or 1. It
// reads only the APP1 Exif segment's first IFD, and gives up quietly on
// anything it does not expect.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		if marker == 0xDA || marker == 0xD9 { // start of scan, end of image
			return 1
		}
		size := int(binary.BigEndian.Uint16(data[i+2:]))
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return exifOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	ifd := int(bo.Uint32(tiff[4:]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	n := int(bo.Uint16(tiff[ifd:]))
	for k := range n {
		e := ifd + 2 + 12*k
		if e+12 > len(tiff) {
			return 1
		}
		if bo.Uint16(tiff[e:]) == 0x0112 { // Orientation, a SHORT
			if v := int(bo.Uint16(tiff[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient turns img upright for an EXIF orientation.
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := range h {
		for x := range w {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
