package invoicing_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// A customer asking to pay online from an invoice whose business has no
// payments connection that can do it is no longer met with silence: the
// request leaves an 'unavailable' account the page words, the business is
// told once, and asking again after they connected one works.
func TestPayUnavailableIsSaidAndTold(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.inv.Rails = &fakeRails{paid: map[string]int64{}}
	var told []string
	w.inv.PayUnavailable = func(_ context.Context, q invoicing.Request, cause error) {
		if !errors.Is(cause, invoicing.ErrNoRail) {
			t.Errorf("told for %v", cause)
		}
		told = append(told, q.Kind)
	}
	// An invoice on a connection no payments service answers to.
	x := w.create(invoicing.Draft{Customer: invoicing.Customer{Name: "Ada Obi", Phone: "+2348031234567"}, AmountKobo: 2500000, Connection: "nope"})
	sent, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k1")
	if err != nil {
		t.Fatal(err)
	}
	tok := invoicing.TokenHash(strings.TrimPrefix(sent.Link, "https://forms.test/i/"))
	ask := func(kind string) {
		t.Helper()
		var rid *uuid.UUID
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT taskiem_public_invoice_request($1, $2, 0)`, tok, kind).Scan(&rid); err != nil || rid == nil {
			t.Fatalf("request %v %v", err, rid)
		}
		if err := w.inv.Drain(ctx); err != nil {
			t.Fatal(err)
		}
	}
	page := func(kind string) (status string, waiting bool, issued int) {
		t.Helper()
		var s *string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT p.status, p.waiting, p.issued FROM taskiem_public_invoice_payment($1) p WHERE p.kind = $2`, tok, kind).Scan(&s, &waiting, &issued); err != nil {
			t.Fatal(err)
		}
		if s != nil {
			status = *s
		}
		return
	}

	ask("transfer")
	if st, waiting, issued := page("transfer"); st != "unavailable" || waiting || issued != 1 {
		t.Fatalf("the page sees %q waiting %v issued %d, not the word it needs", st, waiting, issued)
	}
	if st, _, _ := page("link"); st != "" {
		t.Errorf("card: %q", st)
	}
	// Asking again changes nothing and tells no one again.
	ask("transfer")
	if st, _, issued := page("transfer"); st != "unavailable" || issued != 1 {
		t.Fatalf("again: %q issued %d", st, issued)
	}
	ask("link")
	if st, _, _ := page("link"); st != "unavailable" {
		t.Errorf("card after asking: %q", st)
	}
	if strings.Join(told, ",") != "transfer,link" {
		t.Errorf("told %v", told)
	}

	// The business connects one (the invoice's connection now answers): the
	// customer asks again and gets an account number.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_invoices SET connection = 'main' WHERE id = $1`, x.ID); err != nil {
		t.Fatal(err)
	}
	ask("transfer")
	if st, _, issued := page("transfer"); st != "open" || issued != 2 {
		t.Fatalf("after connecting: %q issued %d", st, issued)
	}
}
