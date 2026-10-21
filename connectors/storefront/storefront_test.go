package storefront_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/lang"
	sf "github.com/israel-duff/taskiem/engine/storefront"
)

type ops struct {
	l    lang.Tag
	sent string
}

func (o *ops) Decide(context.Context, uuid.UUID, string, string, string, string) (map[string]any, error) {
	return nil, nil
}
func (o *ops) PaymentLink(context.Context, uuid.UUID, string) (map[string]any, error) {
	return nil, nil
}
func (o *ops) NotifyCustomer(_ context.Context, _ uuid.UUID, _, text, _, _ string) (map[string]any, error) {
	o.sent = text
	return map[string]any{}, nil
}
func (o *ops) GetBooking(context.Context, uuid.UUID, string) (map[string]any, error) { return nil, nil }
func (o *ops) Refund(context.Context, uuid.UUID, string, string) (sf.Order, error) {
	return sf.Order{}, nil
}
func (o *ops) CustomerLanguage(context.Context, uuid.UUID) lang.Tag { return o.l }

// A customer message by name is sent in the business's language, filled
// from vars (no-technical-steps N6); an unknown name fails plainly.
func TestNotifyCustomerSaysInTheBusinessLanguage(t *testing.T) {
	o := &ops{l: lang.YO}
	c := storefront.New(o)
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "tell_pay"})
	in := map[string]any{"reference": "AK-1", "say": "order_pay", "vars": map[string]any{"reference": "AK-1", "total": "₦1,500", "url": "https://pay.test/x"}}
	if _, err := c.Actions["notify_customer"].Execute(ctx, connector.Request{Input: in}); err != nil {
		t.Fatal(err)
	}
	want := lang.Default().Text(lang.YO, "customer.order_pay", "reference", "AK-1", "total", "₦1,500", "url", "https://pay.test/x")
	if o.sent != want || !strings.Contains(o.sent, "https://pay.test/x") || strings.Contains(o.sent, "{") {
		t.Fatalf("sent %q, want %q", o.sent, want)
	}
	o.l = lang.EN
	if _, err := c.Actions["notify_customer"].Execute(ctx, connector.Request{Input: in}); err != nil || o.sent != "Your order AK-1 is accepted. Pay ₦1,500 here within a day: https://pay.test/x" {
		t.Fatalf("English: %q %v", o.sent, err)
	}
	in["say"] = "made_up"
	if _, err := c.Actions["notify_customer"].Execute(ctx, connector.Request{Input: in}); err == nil {
		t.Error("an unknown message was sent")
	}
}
