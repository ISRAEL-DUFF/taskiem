package invoicing

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A customer's own invoices, by their phone number (decision 0062 V9,
// I1): what a customer assistant tells the person writing from that
// number, who the trigger says they are. Read only.

// PhoneSummary is what one phone number's customer owes the business.
type PhoneSummary struct {
	// Found: the number has invoices here (open or paid).
	Found bool
	// CustomerName is the name on their most recent invoice.
	CustomerName string
	// DueKobo is what is still owed on their open invoices.
	DueKobo int64
	// Open are their open invoices, oldest due first (at most 10).
	Open []Invoice
	// LastPaid is their most recently paid invoice, if any.
	LastPaid *Invoice
}

// PhoneForms are the ways a Nigerian number may have been written on an
// invoice: as given, in E.164 (+234…), as WhatsApp gives it (234…), and
// with a leading 0. Each is a different personal-data subject.
func PhoneForms(phone string) []string {
	phone = strings.TrimSpace(phone)
	var d strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			d.WriteRune(r)
		}
	}
	digits := d.String()
	out := []string{}
	add := func(s string) {
		if s == "" {
			return
		}
		for _, x := range out {
			if x == s {
				return
			}
		}
		out = append(out, s)
	}
	add(phone)
	switch {
	case strings.HasPrefix(digits, "234") && len(digits) == 13:
		add("+" + digits)
		add(digits)
		add("0" + digits[3:])
	case strings.HasPrefix(digits, "0") && len(digits) == 11:
		add("+234" + digits[1:])
		add("234" + digits[1:])
		add(digits)
	case digits != "":
		add("+" + digits)
		add(digits)
	}
	return out
}

// FindByPhone is what the customer with this phone number owes: their open
// invoices and their last paid one. A number with no invoices is not an
// error: Found is false.
func (s Service) FindByPhone(ctx context.Context, tenant uuid.UUID, phone string) (PhoneSummary, error) {
	var out PhoneSummary
	if strings.TrimSpace(phone) == "" {
		return out, invalid("phone is required")
	}
	err := s.in(ctx, tenant, func(tx pgx.Tx) error {
		var subjects []string
		for _, f := range PhoneForms(phone) {
			id, err := s.Vault.SubjectForTx(ctx, tx, tenant, "phone", f)
			if err != nil {
				return err
			}
			subjects = append(subjects, id)
		}
		for _, sub := range subjects {
			open, err := s.OpenInvoicesOf(ctx, tx, tenant, sub)
			if err != nil {
				return err
			}
			for _, x := range open {
				out.DueKobo += x.DueKobo
				if len(out.Open) < 10 {
					out.Open = append(out.Open, x)
				}
			}
		}
		var paid uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM customer_invoices WHERE tenant_id = $1 AND customer_id = ANY($2) AND status = 'paid'
			ORDER BY paid_at DESC NULLS LAST LIMIT 1`, tenant, subjects).Scan(&paid)
		switch {
		case err == nil:
			x, err := s.Get(ctx, tx, tenant, paid.String())
			if err != nil {
				return err
			}
			out.LastPaid = &x
		case err != pgx.ErrNoRows:
			return err
		}
		out.Found = len(out.Open) > 0 || out.LastPaid != nil
		switch {
		case len(out.Open) > 0:
			out.CustomerName = out.Open[len(out.Open)-1].Customer.Name
		case out.LastPaid != nil:
			out.CustomerName = out.LastPaid.Customer.Name
		}
		return nil
	})
	return out, err
}
