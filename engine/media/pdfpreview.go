package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"sync"
	"time"

	"github.com/gen2brain/webp"
	"github.com/klippa-app/go-pdfium"
	pdferrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// The picture of a PDF's first page shown in a chat's bubble
// (docs/spec/chat-previews-and-editing.md §1.3): 640 pixels wide, a WebP
// of at most 200 KB, drawn by PDFium compiled to WebAssembly and run
// inside the process by wazero, with no system program and no files.
const (
	PDFPreviewWidth = 640
	// PDFPreviewMaxHeight keeps a very tall page's picture to twice its
	// width; the bubble shows its top.
	PDFPreviewMaxHeight = 2 * PDFPreviewWidth
	MaxPDFPreviewBytes  = 200 << 10
	// PDFPreviewTimeout is how long one PDF may take to open and draw.
	PDFPreviewTimeout = 5 * time.Second
	// PDFPreviewMemory caps each PDFium instance's memory (256 MB).
	PDFPreviewMemory = 256 << 20
	// PDFPreviewSlots is how many PDFs one server draws at once; others
	// wait their turn.
	PDFPreviewSlots = 2
)

var (
	// ErrPDFProtected says a PDF needs a password to open: its page count
	// is unknown and no picture is drawn.
	ErrPDFProtected = errors.New("media: the PDF is password-protected")
	// ErrPDFPreview says a PDF's first page could not be drawn: it is
	// damaged, took too long, or needed more memory than allowed.
	ErrPDFPreview = errors.New("media: the PDF's first page could not be drawn")
)

// PDFPreview is the picture of a PDF's first page, and its page count.
type PDFPreview struct {
	WebP  []byte
	W, H  int
	Pages int
}

// PDFRenderer draws PDFs' first pages. Its zero value uses the limits
// above; one is shared by the whole server, so PDFium is compiled once.
type PDFRenderer struct {
	Timeout time.Duration
	Slots   int
	// MemoryBytes caps each instance's memory, rounded down to 64 KB
	// pages.
	MemoryBytes int
	// inSlot runs while a PDF holds a slot (tests).
	inSlot func()

	once    sync.Once
	pool    pdfium.Pool
	initErr error
	slots   chan struct{}
}

func (r *PDFRenderer) init() error {
	r.once.Do(func() {
		n := r.Slots
		if n <= 0 {
			n = PDFPreviewSlots
		}
		r.slots = make(chan struct{}, n)
		mem := r.MemoryBytes
		if mem <= 0 {
			mem = PDFPreviewMemory
		}
		rc := wazero.NewRuntimeConfig().
			WithCoreFeatures(api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).
			WithMemoryLimitPages(uint32(mem >> 16)). //nolint:gosec // a few thousand pages
			WithCloseOnContextDone(true)
		r.pool, r.initErr = webassembly.Init(webassembly.Config{
			MinIdle: 0, MaxIdle: n, MaxTotal: n,
			// No directory of the server is visible inside: the PDF is
			// handed over as bytes.
			FSConfig:      wazero.NewFSConfig(),
			RuntimeConfig: rc,
			Stdout:        io.Discard,
			Stderr:        io.Discard,
		})
	})
	return r.initErr
}

// Render draws the first page of a PDF that has passed the malware scan
// and checkPDF. It answers ErrPDFProtected for one that needs a password,
// and ErrPDFPreview for one that cannot be drawn in time or in memory.
func (r *PDFRenderer) Render(ctx context.Context, data []byte) (PDFPreview, error) {
	if err := r.init(); err != nil {
		return PDFPreview{}, fmt.Errorf("%w: %w", ErrPDFPreview, err)
	}
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return PDFPreview{}, ctx.Err()
	}
	defer func() { <-r.slots }()
	if r.inSlot != nil {
		r.inSlot()
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = PDFPreviewTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	inst, err := r.pool.GetInstanceWithContext(ctx)
	if err != nil {
		return PDFPreview{}, fmt.Errorf("%w: %w", ErrPDFPreview, err)
	}
	type result struct {
		p   PDFPreview
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- result{err: fmt.Errorf("%w: %v", ErrPDFPreview, v)}
			}
		}()
		p, err := drawFirstPage(inst, data)
		done <- result{p, err}
	}()
	select {
	case res := <-done:
		_ = inst.Close()
		return res.p, res.err
	case <-ctx.Done():
		// Stops the instance mid-draw; the pool makes a fresh one.
		_ = inst.Kill()
		return PDFPreview{}, fmt.Errorf("%w: %w", ErrPDFPreview, ctx.Err())
	}
}

func drawFirstPage(inst pdfium.Pdfium, data []byte) (PDFPreview, error) {
	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		if errors.Is(err, pdferrors.ErrPassword) {
			return PDFPreview{}, ErrPDFProtected
		}
		return PDFPreview{}, fmt.Errorf("%w: %w", ErrPDFPreview, err)
	}
	defer func() { _, _ = inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document}) }()
	count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil || count.PageCount < 1 {
		return PDFPreview{}, fmt.Errorf("%w: no pages", ErrPDFPreview)
	}
	page, err := inst.RenderPageInPixels(&requests.RenderPageInPixels{
		Page:  requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: 0}},
		Width: PDFPreviewWidth, Height: PDFPreviewMaxHeight,
	})
	if err != nil {
		return PDFPreview{}, fmt.Errorf("%w: %w", ErrPDFPreview, err)
	}
	defer page.Cleanup()
	img := page.Result.RenderedImage
	if img == nil || img.Bounds().Empty() {
		return PDFPreview{}, fmt.Errorf("%w: an empty page", ErrPDFPreview)
	}
	out, err := encodePreview(img)
	if err != nil {
		return PDFPreview{}, err
	}
	b := img.Bounds()
	return PDFPreview{WebP: out, W: b.Dx(), H: b.Dy(), Pages: count.PageCount}, nil
}

// encodePreview writes a page as a WebP, lowering its quality until it
// fits MaxPDFPreviewBytes. A page is drawn on white, so it is opaque.
func encodePreview(img image.Image) ([]byte, error) {
	for _, q := range []int{75, 55, 35, 15} {
		var b bytes.Buffer
		if err := webp.Encode(&b, flatten(img), webp.Options{Quality: q}); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrPDFPreview, err)
		}
		if b.Len() <= MaxPDFPreviewBytes {
			return b.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("%w: the picture is too large", ErrPDFPreview)
}

// flatten copies the page onto white, so a page PDFium left
// transparent is not drawn black.
func flatten(img image.Image) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for i := range out.Pix {
		out.Pix[i] = 0xFF
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			cr, cg, cb, ca := img.At(x, y).RGBA()
			a := ca >> 8
			o := out.PixOffset(x-b.Min.X, y-b.Min.Y)
			out.Pix[o] = uint8(cr>>8 + (255 - a))   //nolint:gosec // premultiplied, at most 255
			out.Pix[o+1] = uint8(cg>>8 + (255 - a)) //nolint:gosec // as above
			out.Pix[o+2] = uint8(cb>>8 + (255 - a)) //nolint:gosec // as above
			out.Pix[o+3] = 0xFF
		}
	}
	return out
}
