package invoicing

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/pdf"
)

// An invoice's PDF prints who it is to, with their address (build plan
// H6, g-saved-addr), and its total.
func TestInvoicePDF(t *testing.T) {
	x := Invoice{Number: "INV-0042", Status: "sent", Kind: "invoice", TotalKobo: 25_000_000, DueKobo: 25_000_000, CreatedAt: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC),
		Customer: Customer{Name: "Acme Ltd", Email: "ap@acme.ng", Address: "12 Marina Road, Lagos Island"},
		Lines:    []Line{{Description: "The October job", Quantity: 1, UnitKobo: 25_000_000}}}
	out, err := PDF("Ada Foods", x, time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) || !bytes.Contains(out, []byte("/Title (Invoice INV-0042)")) {
		t.Fatalf("not the invoice's PDF: %.80q", out)
	}
	reg, bld, err := pdf.Fonts()
	if err != nil {
		t.Fatal(err)
	}
	content := pageStream(t, out)
	drawn := func(s string, f *pdf.Font) bool {
		var hex strings.Builder
		for _, r := range s {
			fmt.Fprintf(&hex, "%04X", f.Glyph(r))
		}
		return strings.Contains(content, "<"+hex.String()+"> Tj")
	}
	if !drawn("12 Marina Road, Lagos Island", reg) || !drawn("Acme Ltd", bld) || !drawn("INV-0042", bld) {
		t.Errorf("the customer, their address or the number is not drawn")
	}
	// No address: nothing in its place.
	x.Customer.Address = ""
	if _, err := PDF("Ada Foods", x, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// pageStream is the first page's content stream (object 4), unpacked.
func pageStream(t *testing.T, out []byte) string {
	t.Helper()
	start := bytes.Index(out, []byte("\n4 0 obj\n"))
	if start < 0 {
		t.Fatal("no page content")
	}
	rest := out[start:]
	i, j := bytes.Index(rest, []byte("stream\n")), bytes.Index(rest, []byte("\nendstream"))
	r, err := zlib.NewReader(bytes.NewReader(rest[i+len("stream\n") : j]))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
