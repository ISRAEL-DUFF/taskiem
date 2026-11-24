package invoicing

import (
	"fmt"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/pdf"
)

// An invoice's PDF (build plan H6, g-saved-addr): A5, as the
// school receipt is, the embedded font so the naira sign prints anywhere:
// the business, the invoice's number, its state and dates; who it is to,
// with their address when it has one (from a saved person's or business's
// place); its lines and total, what was paid and what is due. Made on
// demand from the record, as the caller may see it (masked unless
// revealed); nothing is kept.

// PDF renders invoice x of business as a PDF.
func PDF(business string, x Invoice, now time.Time) ([]byte, error) {
	d, err := pdf.New("Invoice " + x.Number)
	if err != nil {
		return nil, err
	}
	const left, right = 36.0, pdf.A5Width - 36.0
	width := right - left
	lagos, _ := time.LoadLocation("Africa/Lagos")
	if lagos == nil {
		lagos = time.UTC
	}
	y := 54.0
	for _, l := range d.Wrap(business, 16, true, width-130) {
		d.Text(left, y, 16, l, true)
		y += 20
	}
	title := "Invoice"
	if x.Kind == "credit" {
		title = "Statement of what is owed"
	}
	d.Text(left, y, 10, title, false)
	d.TextRight(right, 54, 12, x.Number, true)
	when := x.CreatedAt
	if x.SentAt != nil {
		when = *x.SentAt
	}
	d.TextRight(right, 70, 9, "Dated "+when.In(lagos).Format("2 Jan 2006"), false)
	if x.DueOn != "" {
		if due, err := time.Parse(time.DateOnly, x.DueOn); err == nil {
			d.TextRight(right, 84, 9, "Due "+due.Format("2 Jan 2006"), false)
		}
	}
	if word := pdfState(x); word != "" {
		d.Gray(0.35)
		d.TextRight(right, 102, 12, word, true)
		d.Gray(0)
	}
	y += 18
	d.Line(left, y, right, y, 0.8)
	y += 22

	// Who it is to: the name, the address, then how they are reached.
	d.Text(left, y, 9, "Bill to", false)
	y += 15
	for _, l := range d.Wrap(x.Customer.Name, 13, true, width) {
		d.Text(left, y, 13, l, true)
		y += 16
	}
	if a := strings.TrimSpace(x.Customer.Address); a != "" {
		for _, part := range strings.Split(a, "\n") {
			for _, l := range d.Wrap(strings.TrimSpace(part), 10, false, width) {
				d.Text(left, y, 10, l, false)
				y += 13
			}
		}
	}
	for _, c := range []string{x.Customer.Phone, x.Customer.Email} {
		if c != "" {
			d.Text(left, y, 10, c, false)
			y += 13
		}
	}
	y += 12
	d.Line(left, y, right, y, 0.5)
	y += 18

	// Its lines.
	d.Text(left, y, 9, "Item", false)
	d.TextRight(right, y, 9, "Amount", false)
	y += 15
	shown := 0
	for _, l := range x.Lines {
		if y > pdf.A5Height-130 {
			d.Text(left, y, 9, fmt.Sprintf("… and %d more lines", len(x.Lines)-shown), false)
			y += 12
			break
		}
		desc := l.Description
		if l.Quantity > 1 {
			desc = fmt.Sprintf("%s × %d", desc, l.Quantity)
		}
		if l.PassThroughFor != "" {
			desc += " (collected for " + l.PassThroughFor + ")"
		}
		ws := d.Wrap(desc, 9.5, false, width-110)
		d.Text(left, y, 9.5, ws[0], false)
		d.TextRight(right, y, 9.5, Naira(l.AmountKobo()), false)
		y += 13
		for _, w := range ws[1:] {
			d.Text(left, y, 9.5, w, false)
			y += 13
		}
		shown++
	}
	y += 6
	d.Line(left+width/2, y, right, y, 0.5)
	y += 16
	d.Text(left+width/2, y, 10, "Total", true)
	d.TextRight(right, y, 10, Naira(x.TotalKobo), true)
	if x.PaidKobo > 0 {
		y += 15
		d.Text(left+width/2, y, 10, "Paid", false)
		d.TextRight(right, y, 10, Naira(x.PaidKobo), false)
		y += 15
		d.Text(left+width/2, y, 11, "Due", true)
		d.TextRight(right, y, 11, Naira(x.DueKobo), true)
	}
	if n := strings.TrimSpace(x.Note); n != "" {
		y += 26
		for _, l := range d.Wrap(n, 9, false, width) {
			d.Text(left, y, 9, l, false)
			y += 12
		}
	}
	d.Gray(0.4)
	d.Text(left, pdf.A5Height-30, 7.5, "Invoice "+x.Number+" · made "+now.In(lagos).Format("2 Jan 2006 15:04")+" · Taskiem", false)
	d.Gray(0)
	return d.Bytes()
}

// pdfState is an invoice's state as the PDF prints it, or nothing.
func pdfState(x Invoice) string {
	switch x.Status {
	case "paid":
		return "PAID"
	case "void":
		return "VOID"
	case "draft":
		return "DRAFT"
	}
	return ""
}
