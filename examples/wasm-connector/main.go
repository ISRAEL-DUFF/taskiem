// Command wasm-connector is an example tenant connector. Build it with
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o connector.wasm .
//
// and upload connector.wasm with manifest.yaml (docs/connector-sdk.md).
package main

import (
	"errors"
	"net/url"

	tc "github.com/israel-duff/taskiem/sdk/connectorsdk"
)

func init() {
	tc.Handle("get_balance", getBalance)
	tc.Handle("create_payment", createPayment)
	tc.Handle("get_payment", getPayment)
}

func auth(r *tc.Request) map[string]string {
	return map[string]string{"Authorization": "Bearer " + r.Credentials["api_key"]}
}

func getBalance(r *tc.Request) (any, error) {
	var out struct {
		Available int64  `json:"available"`
		Currency  string `json:"currency"`
	}
	if err := tc.DoJSON("GET", r.BaseURL+"/v1/balance", auth(r), nil, &out); err != nil {
		return nil, err
	}
	return map[string]any{"available": out.Available, "currency": out.Currency}, nil
}

type payment struct {
	ID        string `json:"id"`
	Reference string `json:"reference"`
	Status    string `json:"status"`
}

func (p payment) output() map[string]any {
	return map[string]any{"id": p.ID, "reference": p.Reference, "status": p.Status}
}

// createPayment sends the engine's key as the payment reference. The
// ledger refuses a reference it has seen with 409, so a repeat reports the
// payment already made under it.
func createPayment(r *tc.Request) (any, error) {
	ref := r.Str("reference")
	if ref == "" {
		return nil, tc.Fatal(errors.New("create_payment needs the engine's reference"))
	}
	body := map[string]any{"amount": r.Input["amount"], "account_number": r.Input["account_number"], "reference": ref}
	var p payment
	err := tc.DoJSON("POST", r.BaseURL+"/v1/payments", auth(r), body, &p)
	if tc.StatusOf(err) == 409 {
		return getPayment(&tc.Request{Input: map[string]any{"reference": ref}, Credentials: r.Credentials, BaseURL: r.BaseURL})
	}
	if err != nil {
		return nil, err
	}
	return p.output(), nil
}

func getPayment(r *tc.Request) (any, error) {
	var p payment
	err := tc.DoJSON("GET", r.BaseURL+"/v1/payments?reference="+url.QueryEscape(r.Str("reference")), auth(r), nil, &p)
	if tc.StatusOf(err) == 404 {
		return nil, tc.NotFound
	}
	if err != nil {
		return nil, err
	}
	return p.output(), nil
}

func main() {}
