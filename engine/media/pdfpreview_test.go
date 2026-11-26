package media

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gen2brain/webp"
)

// One renderer for the package's tests: PDFium is compiled once.
var testRenderer PDFRenderer

// The picture of a PDF's first page: a WebP 640 pixels wide, at most 200
// KB, with the page count (§1.3, §1.5).
func TestRenderPDF(t *testing.T) {
	p, err := testRenderer.Render(context.Background(), fixture(t, "three.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := webp.DecodeConfig(bytes.NewReader(p.WebP))
	if err != nil || cfg.Width != PDFPreviewWidth || p.W != PDFPreviewWidth || p.H != cfg.Height || p.H < 800 || p.Pages != 3 {
		t.Fatalf("%+v %v: %d pages, %dx%d", cfg, err, p.Pages, p.W, p.H)
	}
	if len(p.WebP) > MaxPDFPreviewBytes {
		t.Fatalf("%d bytes", len(p.WebP))
	}
}

// A protected PDF has no picture and no page count; a damaged one fails.
func TestRenderPDFProtectedOrDamaged(t *testing.T) {
	if _, err := testRenderer.Render(context.Background(), fixture(t, "protected.pdf")); !errors.Is(err, ErrPDFProtected) {
		t.Fatalf("protected: %v", err)
	}
	damaged := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("garbage "), 500)...)
	if _, err := testRenderer.Render(context.Background(), damaged); !errors.Is(err, ErrPDFPreview) {
		t.Fatalf("damaged: %v", err)
	}
	half := fixture(t, "three.pdf")
	if _, err := testRenderer.Render(context.Background(), half[:len(half)/3]); err == nil {
		t.Log("a cut PDF was still drawn (PDFium repairs what it can)")
	}
}

// A PDF that takes longer than the time limit is stopped and fails; the
// renderer goes on drawing others.
func TestRenderPDFTimeLimit(t *testing.T) {
	r := PDFRenderer{Timeout: time.Millisecond}
	if _, err := r.Render(context.Background(), fixture(t, "three.pdf")); !errors.Is(err, ErrPDFPreview) {
		t.Fatalf("over time: %v", err)
	}
	r.Timeout = PDFPreviewTimeout
	if _, err := r.Render(context.Background(), fixture(t, "three.pdf")); err != nil {
		t.Fatalf("after a stopped one: %v", err)
	}
}

// At most two PDFs are drawn at once; the others wait their turn.
func TestRenderPDFSlots(t *testing.T) {
	var now, most atomic.Int32
	r := PDFRenderer{Slots: 2}
	r.inSlot = func() {
		n := now.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		now.Add(-1)
	}
	data := fixture(t, "three.pdf")
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			_, err := r.Render(context.Background(), data)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if most.Load() != 2 {
		t.Fatalf("%d at once", most.Load())
	}
}
