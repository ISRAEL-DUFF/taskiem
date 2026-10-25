package invoicing_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

// issuerRails stand for a Paystack connection (needs an email, names the
// account itself) and an iswallet one (needs the wallet, takes a name).
type issuerRails struct {
	asked []map[string]any
	fail  error
}

var (
	paystackRail = invoicing.Rail{Connector: "paystack", Connection: "main", Name: "Paystack", Events: map[string]bool{},
		Caps: map[string]connector.CapabilityMap{"virtual_account": {Requires: []string{"customer.email"}, Ignores: []string{"account_name", "account", "customer.bvn", "customer.nin"}, Unsafe: true}}}
	iswalletRail = invoicing.Rail{Connector: "iswallet", Connection: "main", Name: "iswallet", Events: map[string]bool{},
		Caps: map[string]connector.CapabilityMap{"virtual_account": {Requires: []string{"account"}}, "list_credits": {Requires: []string{"account"}}}}
)

func (f *issuerRails) Capable(context.Context, uuid.UUID, string) ([]invoicing.Rail, error) {
	return []invoicing.Rail{iswalletRail, paystackRail}, nil
}

func (f *issuerRails) ResolveChannel(_ context.Context, _ uuid.UUID, channel, _ string) (invoicing.Rail, error) {
	for _, r := range []invoicing.Rail{iswalletRail, paystackRail} {
		if r.Channel() == channel {
			return r, nil
		}
	}
	return invoicing.Rail{}, invoicing.ErrNoRail
}

func (f *issuerRails) Call(_ context.Context, _ uuid.UUID, r invoicing.Rail, _ string, in map[string]any, key effects.KeyInput) (map[string]any, error) {
	f.asked = append(f.asked, in)
	if f.fail != nil {
		return nil, f.fail
	}
	out := map[string]any{"account_number": fmt.Sprintf("99%08d", len(f.asked)), "bank_name": "Wema Bank", "reference": "ref:" + key.Seed + ":" + key.StepID}
	if r.Connector == "paystack" {
		out["account_name"] = "ADA FOODS / ADA OBI"
	}
	return out, nil
}

// A customer's account issued on the one path: what the rail is asked
// (the customer, the name, the wallet), the registration, once per
// customer and connection, the issuers listed, and what each rail needs.
func TestIssueAccount(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	rails := &issuerRails{}
	w.inv.AccountRails = rails
	svc := invoicing.Service{Invoices: w.inv}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE tenants SET name = 'Ada Foods' WHERE id = $1`, w.env.Tenant); err != nil {
		t.Fatal(err)
	}

	is, err := w.inv.Issuers(ctx, w.env.Tenant)
	if err != nil || len(is) != 2 || !is[0].NeedsAccount || !is[1].NeedsEmail || !is[1].NamesItself || is[0].NamesItself {
		t.Fatalf("issuers %+v %v", is, err)
	}

	// What is refused before any rail is asked.
	for _, in := range []invoicing.AccountRequest{
		{Customer: invoicing.Customer{Name: "Ada"}, Channel: "iswallet/main", Issue: true, Account: "wal_1"},
		{Customer: ada, Channel: "opay/main", Issue: true},
		{Customer: ada, Channel: "iswallet/main", Issue: true},
		{Customer: invoicing.Customer{Name: "Ada Obi", Phone: "+2348031234567"}, Channel: "paystack/main", Issue: true},
		{Customer: ada, Channel: "iswallet/main", Issue: true, Account: "wal_1", BVN: "123"},
	} {
		if _, err := svc.GiveAccount(ctx, w.env.Tenant, in, "user:ada"); !errors.Is(err, invoicing.ErrInvalid) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	if len(rails.asked) != 0 {
		t.Fatalf("the rail was asked: %v", rails.asked)
	}

	a, err := svc.GiveAccount(ctx, w.env.Tenant, invoicing.AccountRequest{Customer: ada, Channel: "iswallet/main", Issue: true, Account: "wal_1"}, "user:ada")
	if err != nil {
		t.Fatal(err)
	}
	if a.Channel != "iswallet/main" || a.AccountNumber != "9900000001" || a.BankName != "Wema Bank" || a.Reference == "" || a.Customer.Name != "Ada Obi" || a.Customer.Phone != ada.Phone {
		t.Fatalf("issued %+v", a)
	}
	asked := rails.asked[0]
	if c := asked["customer"].(map[string]any); c["name"] != "Ada Obi" || c["email"] != ada.Email || c["phone"] != ada.Phone || asked["account_name"] != "ADA FOODS / ADA OBI" || asked["account"] != "wal_1" {
		t.Fatalf("asked %v", asked)
	}
	// The same customer and connection again: the account already issued.
	again, err := svc.GiveAccount(ctx, w.env.Tenant, invoicing.AccountRequest{Customer: ada, Channel: "iswallet/main", Issue: true, Account: "wal_1"}, "user:ada")
	if err != nil || again.ID != a.ID || len(rails.asked) != 1 {
		t.Fatalf("again %+v %v (asked %d)", again, err, len(rails.asked))
	}
	// Its wallet is read back.
	var lookbacks int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM money_lookbacks WHERE tenant_id = $1 AND channel = 'iswallet/main' AND account = 'wal_1'`, w.env.Tenant).Scan(&lookbacks); err != nil || lookbacks != 1 {
		t.Fatalf("lookbacks %d %v", lookbacks, err)
	}
	// On Paystack, which names the account itself: no name asked.
	p, err := svc.GiveAccount(ctx, w.env.Tenant, invoicing.AccountRequest{Customer: ada, Channel: "paystack/main", Issue: true}, "user:ada")
	if err != nil || p.Channel != "paystack/main" || p.ID == a.ID {
		t.Fatalf("paystack %+v %v", p, err)
	}
	if _, ok := rails.asked[1]["account_name"]; ok {
		t.Errorf("Paystack was asked for a name: %v", rails.asked[1])
	}
	// The customer has both, from any of their invoices.
	w.in(func(tx pgx.Tx) error {
		list, err := w.inv.AccountsOf(ctx, tx, w.env.Tenant, a.CustomerID())
		if err != nil || len(list) != 2 {
			t.Fatalf("accounts %v %v", list, err)
		}
		return nil
	})
	// The rail's refusal is answered as it came, and nothing registered.
	rails.fail = fmt.Errorf("the business is not live: %w", effects.ErrFatal)
	chi := invoicing.Customer{Name: "Chioma", Phone: "+2348031234568"}
	if _, err := svc.GiveAccount(ctx, w.env.Tenant, invoicing.AccountRequest{Customer: chi, Channel: "iswallet/main", Issue: true, Account: "wal_1"}, "user:ada"); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("refusal: %v", err)
	}
	// By hand, without issuing: registered as given.
	h, err := svc.GiveAccount(ctx, w.env.Tenant, invoicing.AccountRequest{Customer: chi, Channel: "paystack/main", AccountNumber: "1234567890", Reference: "CUS_chi"}, "user:ada")
	if err != nil || h.AccountNumber != "1234567890" || h.Customer.Name != "Chioma" {
		t.Fatalf("by hand %+v %v", h, err)
	}
	// Without rails, issuing says so.
	w.inv.AccountRails = nil
	if _, err := svc.GiveAccount(ctx, w.env.Tenant, invoicing.AccountRequest{Customer: chi, Channel: "iswallet/main", Issue: true, Account: "wal_1"}, "user:ada"); !errors.Is(err, invoicing.ErrNoRail) {
		t.Fatalf("no rails: %v", err)
	}
}
