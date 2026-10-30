package invoicing_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// A customer's payment link and their balance sent in one message
// (decision 0060, R5): "send Ada her balance" moves her recorded lines to sent
// together and sends one message naming them, their total and her link,
// made once and kept with her; the link's page sees her open lines, asks
// for an account towards the total or a part, and the queue issues it on
// the rail of her oldest line; a customer with an account number of her
// own is sent that instead; no address is refused.
func TestCustomerLinkAndBalance(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tenant := w.env.Tenant
	rails := &fakeRails{paid: map[string]int64{}}
	w.inv.Rails = rails
	var ada invoicing.BookCustomer
	w.in(func(tx pgx.Tx) error {
		var err error
		ada, err = w.inv.NewBookCustomer(ctx, tx, tenant, "Ada", "", "user:owner")
		return err
	})
	record := func(key string, kobo int64) invoicing.Invoice {
		t.Helper()
		var x invoicing.Invoice
		w.in(func(tx pgx.Tx) error {
			var err error
			x, err = w.inv.RecordCredit(ctx, tx, tenant, key, ada, invoicing.CreditDraft{AmountKobo: kobo, Description: "Rice"}, "user:owner")
			return err
		})
		return x
	}
	// A line paid off is not part of her balance.
	first := record("c1", 500000)
	if x := w.pay(first.Number, "cash-1", 500000); x.Status != "paid" {
		t.Fatalf("paid: %+v", x)
	}
	second := record("c2", 300000)
	third := record("c3", 200000)
	if second.Status != "recorded" || third.DueKobo != 200000 {
		t.Fatalf("lines: %+v %+v", second, third)
	}

	// No phone or email: refused, nothing sent.
	w.in(func(tx pgx.Tx) error {
		_, err := w.inv.SendBalance(ctx, tx, tenant, second.CustomerID(), "user:owner", "k0")
		if !errors.Is(err, invoicing.ErrInvalid) {
			t.Errorf("no address: %v", err)
		}
		return nil
	})
	w.in(func(tx pgx.Tx) error {
		var err error
		ada, err = w.inv.SetBookPhone(ctx, tx, tenant, ada, "+2348031234567", "user:owner")
		return err
	})

	// One message, her lines sent together, her link in it.
	var out invoicing.BalanceSent
	w.in(func(tx pgx.Tx) error {
		var err error
		out, err = w.inv.SendBalance(ctx, tx, tenant, second.CustomerID(), "user:owner", "k1")
		return err
	})
	if out.DueKobo != 500000 || strings.Join(out.Numbers, ",") != second.Number+","+third.Number || len(out.Sent) != 2 ||
		!strings.HasPrefix(out.Link, "https://forms.test/c/") || out.Account != "" || strings.Join(out.Queued, ",") != "sms" {
		t.Fatalf("sent: %+v", out)
	}
	w.in(func(tx pgx.Tx) error {
		x, err := w.inv.Get(ctx, tx, tenant, second.Number)
		if err != nil || x.Status != "sent" || x.SentAt == nil || x.Kind != "credit" {
			t.Errorf("the line is sent: %+v %v", x, err)
		}
		return nil
	})
	var body []byte
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT body FROM platform_messages WHERE tenant_id = $1 AND dedup_key = $2`, tenant,
		"balance:"+second.CustomerID()+":k1:sms").Scan(&body); err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	_ = json.Unmarshal(body, &env)
	w.in(func(tx pgx.Tx) error {
		v, err := w.env.Vault.OpenTx(ctx, tx, tenant, env)
		text, _ := v.(map[string]any)["text"].(string)
		want := ": you owe ₦5,000 in all (" + second.Number + " ₦3,000, " + third.Number + " ₦2,000). See them and pay: " + out.Link
		if err != nil || !strings.HasSuffix(text, want) {
			t.Errorf("the message: %q %v, want …%q", text, err, want)
		}
		return nil
	})
	// Again under the same key: the same link, no second message.
	w.in(func(tx pgx.Tx) error {
		again, err := w.inv.SendBalance(ctx, tx, tenant, second.CustomerID(), "user:owner", "k1")
		if err != nil || again.Link != out.Link || len(again.Queued) != 0 || len(again.Sent) != 0 {
			t.Errorf("again: %+v %v", again, err)
		}
		return nil
	})

	// The page sees her lines and asks for a part of the total; the
	// queue issues it.
	tok := invoicing.CustomerTokenHash(strings.TrimPrefix(out.Link, "https://forms.test/c/"))
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM taskiem_public_customer_link($1)`, tok).Scan(&n); err != nil || n != 2 {
		t.Fatalf("page lines: %d %v", n, err)
	}
	for amount, ok := range map[int64]bool{600000: false, 50000: false, 150000: true} {
		var rid *uuid.UUID
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT taskiem_public_customer_link_request($1, 'transfer', $2)`, tok, amount).Scan(&rid); err != nil || (rid != nil) != ok {
			t.Errorf("request %d: %v %v", amount, rid, err)
		}
	}
	if err := w.inv.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var status, number string
	var amount int64
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status, account_number, amount_kobo FROM taskiem_public_customer_link_payment($1) WHERE kind = 'transfer'`, tok).
		Scan(&status, &number, &amount); err != nil || status != "open" || number != "9900112233" || amount != 150000 {
		t.Fatalf("page sees %v %q %q %d", err, status, number, amount)
	}
	if in := rails.ins[len(rails.ins)-1]; in["amount_kobo"] != int64(150000) || !strings.Contains(in["description"].(string), "2 invoice(s)") {
		t.Errorf("rail asked %+v", in)
	}
	// The same amount again is the same account; the whole is another.
	link := linkOf(t, w, second.CustomerID())
	a, err := w.inv.IssueForCustomer(ctx, tenant, link, "transfer", 150000)
	if err != nil || a.Seq != 1 {
		t.Errorf("again: %+v %v", a, err)
	}
	if a, err := w.inv.IssueForCustomer(ctx, tenant, link, "transfer", 0); err != nil || a.Seq != 2 || a.AmountKobo != 500000 {
		t.Errorf("the whole: %+v %v", a, err)
	}

	// A customer with an account number of her own is sent that.
	w.in(func(tx pgx.Tx) error {
		if _, err := w.inv.RegisterAccount(ctx, tx, tenant, invoicing.AccountIn{Customer: invoicing.Customer{Name: "Ada", Phone: "+2348031234567", Key: ada.Key},
			Channel: "paystack/main", AccountNumber: "9912345678", Reference: "ref-ada", BankName: "Wema Bank"}, "user:owner"); err != nil {
			return err
		}
		own, err := w.inv.SendBalance(ctx, tx, tenant, second.CustomerID(), "user:owner", "k2")
		if err != nil || own.Account != "9912345678" || own.Link != "" || len(own.Queued) != 1 {
			t.Errorf("own account: %+v %v", own, err)
		}
		return nil
	})
}

func linkOf(t *testing.T, w *world, customer string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM invoice_customer_links WHERE tenant_id = $1 AND customer_id = $2`,
		w.env.Tenant, customer).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
