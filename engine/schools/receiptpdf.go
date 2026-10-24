package schools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"rsc.io/qr"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/pdf"
)

// The receipt's PDF (docs/spec/schools.md 9): A5, the embedded font so
// the naira sign prints anywhere, the school, the student and class, the
// amount and date, what it paid (the invoice's number and lines), the
// invoice's balance after it, the receipt number and a QR code of the
// verification link. Made on demand from the record; nothing is kept on
// disk.

// ReceiptPDF renders a receipt, by its id or number, as a PDF.
func (s *Schools) ReceiptPDF(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string) ([]byte, Receipt, error) {
	r, err := s.ReceiptByID(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return nil, Receipt{}, err
	}
	var school string
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&school); err != nil {
		return nil, Receipt{}, err
	}
	var lines []invoicing.Line
	if s.Invoices != nil {
		x, err := s.Invoices.Get(ctx, tx, tenant, r.InvoiceID.String())
		if err != nil {
			return nil, Receipt{}, err
		}
		lines = x.Lines
	}
	out, err := renderReceipt(school, r, lines, s.now())
	return out, r, err
}

// renderReceipt draws the receipt.
func renderReceipt(school string, r Receipt, lines []invoicing.Line, now time.Time) ([]byte, error) {
	d, err := pdf.New("Receipt " + r.Number)
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
	// The school, the receipt's number and its state.
	for _, l := range d.Wrap(school, 16, true, width-120) {
		d.Text(left, y, 16, l, true)
		y += 20
	}
	d.Text(left, y, 10, "Official receipt", false)
	d.TextRight(right, 54, 12, r.Number, true)
	d.TextRight(right, 70, 9, "Issued "+r.IssuedAt.In(lagos).Format("2 Jan 2006, 15:04"), false)
	if r.Voided {
		d.Gray(0.35)
		d.TextRight(right, 88, 14, "VOIDED", true)
		d.Gray(0)
	}
	y += 14
	d.Line(left, y, right, y, 0.8)
	y += 22

	// Who paid, how much, when.
	d.Text(left, y, 9, "Received from the family of", false)
	y += 15
	name := r.Name
	if name == "" || name == Erased {
		name = "(name erased)"
	}
	d.Text(left, y, 13, name, true)
	y += 16
	d.Text(left, y, 10, fmt.Sprintf("Admission no. %s · %s · %s", r.AdmissionNumber, r.Class, r.Term), false)
	y += 26
	d.Text(left, y, 9, "Amount", false)
	d.TextRight(right, y, 9, "Date paid", false)
	y += 20
	d.Text(left, y, 20, invoicing.Naira(r.AmountKobo), true)
	d.TextRight(right, y, 12, r.PaidAt.In(lagos).Format("2 January 2006"), false)
	y += 14
	d.Text(left, y, 9, HowWords(r.How), false)
	y += 18
	d.Line(left, y, right, y, 0.5)
	y += 18

	// What it paid: the invoice and its lines.
	d.Text(left, y, 9, "Towards invoice", false)
	d.TextRight(right, y, 9, "Balance after this payment", false)
	y += 15
	d.Text(left, y, 11, r.InvoiceNumber, true)
	d.TextRight(right, y, 11, invoicing.Naira(r.BalanceAfterKobo), true)
	y += 18
	shown := 0
	for _, l := range lines {
		if y > pdf.A5Height-150 {
			d.Text(left, y, 9, fmt.Sprintf("… and %d more lines", len(lines)-shown), false)
			y += 12
			break
		}
		desc := l.Description
		if l.PassThroughFor != "" {
			desc += " (collected for " + l.PassThroughFor + ")"
		}
		ws := d.Wrap(desc, 9, false, width-110)
		d.Text(left+8, y, 9, ws[0], false)
		d.TextRight(right, y, 9, invoicing.Naira(l.AmountKobo()), false)
		y += 12
		for _, w := range ws[1:] {
			d.Text(left+8, y, 9, w, false)
			y += 12
		}
		shown++
	}
	if r.BalanceAfterKobo == 0 {
		y += 4
		d.Text(left, y, 10, "Paid in full. Thank you.", true)
		y += 12
	}

	// The QR and how to check the receipt.
	qy := pdf.A5Height - 136
	if y+10 > qy {
		qy = y + 10
	}
	if r.Link != "" {
		if code, err := qr.Encode(r.Link, qr.M); err == nil {
			d.QR(right-96, qy, 96, code.Size, code.Black)
		}
		for i, l := range d.Wrap("Scan the code to check this receipt. It answers Valid or Voided, the school, the amount and the date, and nothing else.", 8.5, false, width-110) {
			d.Text(left, qy+14+float64(i)*11, 8.5, l, false)
		}
	}
	d.Gray(0.4)
	d.Text(left, pdf.A5Height-30, 7.5, strings.TrimSpace("Receipt "+r.Number+" · made "+now.In(lagos).Format("2 Jan 2006 15:04")+" · Taskiem"), false)
	d.Gray(0)
	return d.Bytes()
}
