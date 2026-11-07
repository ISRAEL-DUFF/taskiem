package api

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/israel-duff/taskiem/engine/areas"
)

func TestAreaOfPath(t *testing.T) {
	for path, want := range map[string]areas.Area{
		"/v1/storefront":                       areas.Storefront,
		"/v1/storefront/fronts/abc":            areas.Storefront,
		"/v1/storefront/orders":                areas.Orders,
		"/v1/storefront/orders/ref/refund":     areas.Orders,
		"/v1/storefront/orders/ref/deliveries": areas.Orders,
		"/v1/trade-orders/1/accept":            areas.Orders,
		"/v1/riders/owed":                      areas.Storefront,
		"/v1/deliveries":                       areas.Storefront,
		"/v1/stock/suppliers/1/link":           areas.Stock,
		"/v1/price-lists/1/items":              areas.Stock,
		"/v1/invoices/1/send":                  areas.Money,
		"/v1/invoices/":                        areas.Money,
		"/v1/money/summary":                    areas.Money,
		"/v1/bills/1/pay":                      areas.Money,
		"/v1/links/1/trade":                    areas.Links,
		"/v1/link-chats/1/messages":            areas.Links,
		"/v1/link-settings":                    areas.Links,
	} {
		if got, ok := areaOfPath(path); !ok || got != want {
			t.Errorf("%s: %s %v, want %s", path, got, ok, want)
		}
	}
	// Close names and always-on routes belong to no area.
	for _, path := range []string{"/v1/storefronts", "/v1/stocks", "/v1/moneyline", "/v1/workflows", "/v1/me", "/v1/settings/areas", "/v1/settings/schools", "/v1/schools/students", "/v1/audit", "/v1/reports/chain", "/hooks/x"} {
		if got, ok := areaOfPath(path); ok {
			t.Errorf("%s: unexpectedly in %s", path, got)
		}
	}
}

// alwaysOn are routes whose names look like an area's but that stay on
// for every business, and why.
var alwaysOn = map[string]string{}

func init() {
	for _, r := range []string{
		"GET /v1/billing", "GET /v1/billing/invoices/{id}", "GET /v1/billing/status", "POST /v1/billing/cancel",
		"POST /v1/billing/checkout", "POST /v1/billing/plan", "POST /v1/billing/webhooks/{provider}",
	} {
		alwaysOn[r] = "the business's own Taskiem subscription, not its customers' invoices"
	}
	for _, r := range []string{
		"GET /v1/receive-money/balance", "GET /v1/receive-money/banks", "POST /v1/receive-money/withdraw",
		"GET /v1/settings/receive-money", "POST /v1/settings/receive-money", "PUT /v1/settings/receive-money/sweep",
		"PUT /v1/settings/receive-money/withdrawals",
	} {
		alwaysOn[r] = "receiving money with Taskiem is a payment rail any business may use; whether it belongs to Money is open (docs/spec/business-areas.md, phase 3)"
	}
	for _, r := range []string{
		"GET /v1/schools/terms/{id}/invoices", "POST /v1/schools/terms/{id}/invoice", "POST /v1/schools/families/{id}/link",
	} {
		alwaysOn[r] = "the Schools area, which the schools package refuses itself when off"
	}
	alwaysOn["POST /v1/forms/{id}/links"] = "links to a form, not business links"
	alwaysOn["POST /v1/me/chat-link"] = "links a person's own chat account"
	alwaysOn["POST /v1/auth/chat/link"] = "a sign-in link sent in chat (chat_signin.go), not business links"
	alwaysOn["POST /v1/auth/chat/link/peek"] = "a sign-in link sent in chat (chat_signin.go), not business links"
}

// TestAreaRoutesAreMapped walks the router: a route whose path names a
// part of the product that can be switched off must be in areaPaths, so a
// route added later under such a name is refused when its area is off, or
// is listed above with the reason it is not.
func TestAreaRoutesAreMapped(t *testing.T) {
	var unmapped []string
	words := []string{"storefront", "stock", "invoice", "money", "bill", "link", "rider", "deliver", "dispatch", "price", "trade", "supplier", "customer-account"}
	err := chi.Walk((&Server{AllowSignup: true, Ingest: http.NotFoundHandler(), Static: http.NotFoundHandler()}).Handler().(chi.Routes),
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			route = strings.TrimSuffix(route, "/")
			if !strings.HasPrefix(route, "/v1/") || strings.HasPrefix(route, "/v1/embed/") || strings.HasPrefix(route, "/v1/ops/") || strings.HasPrefix(route, "/v1/partner/") {
				return nil
			}
			named := false
			for _, w := range words {
				named = named || strings.Contains(route, w)
			}
			if _, ok := areaOfPath(route); named && !ok && alwaysOn[method+" "+route] == "" {
				unmapped = append(unmapped, method+" "+route)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(unmapped)
	if len(unmapped) > 0 {
		t.Errorf("routes named for an area but in none (add the path to areaPaths in areas.go, or to alwaysOn with the reason):\n%s", strings.Join(unmapped, "\n"))
	}
}
