package storefront

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// PaystackPayments makes payment pages with the tenant's own Paystack
// connection (prod), through the connector and the egress guard.
type PaystackPayments struct {
	Registry *connector.Registry
	Vault    *secrets.Vault
	Guard    *egress.Guard
}

func (p *PaystackPayments) call(ctx context.Context, tenant uuid.UUID, connection, action string, in map[string]any, key string) (map[string]any, error) {
	if connection == "" {
		connection = "main"
	}
	reg, err := p.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, err
	}
	conn, ok := reg.Get("paystack@1")
	if !ok {
		return nil, errors.New("storefront: the Paystack connector is not installed")
	}
	creds, err := p.Vault.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "storefront.payment"}), tenant, "prod", conn.Manifest.ID, connection)
	if errors.Is(err, secrets.ErrNotFound) {
		return nil, fmt.Errorf("%w: the Paystack connection %q is missing in prod", ErrInvalid, connection)
	}
	if err != nil {
		return nil, err
	}
	g := p.Guard
	if g == nil {
		g = &egress.Guard{}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: conn.Manifest.HostsFor(creds), Purpose: "connector:paystack"}, 30*time.Second)
	req := connector.Request{Input: in, Credentials: creds, HTTP: hc, Attempt: 1}
	if key != "" {
		req.IdempotencyKey, req.KeyFirstSent = key, time.Now()
	}
	resp, err := conn.Actions[action].Execute(ctx, req)
	if err != nil {
		return nil, err
	}
	out, _ := resp.Output.(map[string]any)
	return out, nil
}

// Create makes a checkout page under r.Reference.
func (p *PaystackPayments) Create(ctx context.Context, tenant uuid.UUID, connection string, r PaymentRequest) (string, string, error) {
	in := map[string]any{"reference": r.Reference, "amount": r.AmountKobo, "email": r.Email, "currency": "NGN"}
	if r.CallbackURL != "" {
		in["callback_url"] = r.CallbackURL
	}
	if r.Metadata != nil {
		in["metadata"] = r.Metadata
	}
	out, err := p.call(ctx, tenant, connection, "initialize_transaction", in, r.Reference)
	if err != nil {
		return "", "", err
	}
	url, _ := out["authorization_url"].(string)
	if url == "" {
		return "", "", errors.New("storefront: Paystack gave no payment page")
	}
	return url, r.Reference, nil
}

// Verify reports whether the page's charge succeeded.
func (p *PaystackPayments) Verify(ctx context.Context, tenant uuid.UUID, connection, ref string) (string, int64, error) {
	out, err := p.call(ctx, tenant, connection, "verify_charge", map[string]any{"reference": ref}, "")
	if err != nil {
		return "", 0, err
	}
	var amount int64
	switch a := out["amount"].(type) {
	case float64:
		amount = int64(a)
	case int64:
		amount = a
	case int:
		amount = int64(a)
	}
	switch out["status"] {
	case "success":
		return "paid", amount, nil
	case "failed", "abandoned", "reversed":
		return "failed", amount, nil
	}
	return "pending", amount, nil
}

// Refund refunds a payment in full with the tenant's Paystack connection.
func (p *PaystackPayments) Refund(ctx context.Context, tenant uuid.UUID, connection, ref, note string) (string, error) {
	in := map[string]any{"reference": ref}
	if note != "" {
		in["customer_note"] = note
	}
	out, err := p.call(ctx, tenant, connection, "refund_transaction", in, "")
	if err != nil {
		return "", err
	}
	st, _ := out["status"].(string)
	return st, nil
}
