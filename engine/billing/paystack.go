package billing

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
)

// Paystack collects payments through Paystack (written from its public API
// documentation): a hosted checkout (card and bank transfer), card
// authorizations for renewals, and charge.success webhooks signed with
// HMAC-SHA512 of the body under the secret key, checked with the same
// verification code as the Paystack connector's triggers.
type Paystack struct {
	SecretKey string
	BaseURL   string // default https://api.paystack.co
}

// PaystackBaseURL is Paystack's API.
const PaystackBaseURL = "https://api.paystack.co"

func (p *Paystack) Name() string { return "paystack" }

func (p *Paystack) base() string {
	if p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return PaystackBaseURL
}

func (p *Paystack) auth() map[string]string {
	return map[string]string{"Authorization": "Bearer " + p.SecretKey}
}

type paystackTxn struct {
	ID            int64  `json:"id"`
	Status        string `json:"status"`
	Reference     string `json:"reference"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	Channel       string `json:"channel"`
	GatewayMsg    string `json:"gateway_response"`
	Authorization *struct {
		Code     string `json:"authorization_code"`
		Brand    string `json:"brand"`
		CardType string `json:"card_type"`
		Last4    string `json:"last4"`
		ExpMonth string `json:"exp_month"`
		ExpYear  string `json:"exp_year"`
		Channel  string `json:"channel"`
		Reusable bool   `json:"reusable"`
	} `json:"authorization"`
}

func (t paystackTxn) transaction() Transaction {
	out := Transaction{Reference: t.Reference, AmountKobo: t.Amount, Currency: t.Currency, Channel: t.Channel,
		ProviderID: strconv.FormatInt(t.ID, 10), Message: t.GatewayMsg}
	switch t.Status {
	case "success":
		out.Status = TxSuccess
	case "failed", "reversed":
		out.Status = TxFailed
	case "abandoned":
		out.Status = TxAbandoned
	default: // ongoing, pending, processing, queued, send_otp, ...
		out.Status = TxPending
	}
	if a := t.Authorization; a != nil && a.Code != "" && a.Channel == "card" {
		brand := a.Brand
		if brand == "" {
			brand = a.CardType
		}
		out.Authorization = &Authorization{Code: a.Code, Brand: strings.TrimSpace(brand), Last4: a.Last4, Exp: a.ExpMonth + "/" + a.ExpYear, Reusable: a.Reusable}
	}
	return out
}

func (p *Paystack) StartCheckout(ctx context.Context, c Checkout) (CheckoutSession, error) {
	channels := c.Channels
	if len(channels) == 0 {
		channels = []string{"card", "bank_transfer"}
	}
	body := map[string]any{"email": c.Email, "amount": c.AmountKobo, "currency": "NGN", "reference": c.Reference,
		"channels": channels, "metadata": c.Metadata}
	if c.CallbackURL != "" {
		body["callback_url"] = c.CallbackURL
	}
	var env struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			URL    string `json:"authorization_url"`
			Access string `json:"access_code"`
		} `json:"data"`
	}
	if _, err := callJSON(ctx, http.MethodPost, p.base()+"/transaction/initialize", p.auth(), body, &env); err != nil { //nolint:misspell // Paystack's path
		return CheckoutSession{}, fmt.Errorf("paystack: %w", err)
	}
	if !env.Status || env.Data.URL == "" {
		return CheckoutSession{}, fmt.Errorf("paystack: starting a checkout: %s", env.Message)
	}
	return CheckoutSession{URL: env.Data.URL, AccessKey: env.Data.Access}, nil
}

func (p *Paystack) Verify(ctx context.Context, reference string) (Transaction, error) {
	var env struct {
		Status  bool        `json:"status"`
		Message string      `json:"message"`
		Data    paystackTxn `json:"data"`
	}
	st, err := callJSON(ctx, http.MethodGet, p.base()+"/transaction/verify/"+url.PathEscape(reference), p.auth(), nil, &env)
	if st == http.StatusNotFound || st == http.StatusBadRequest {
		// Paystack has no such transaction: the payer never got as far.
		return Transaction{Reference: reference, Status: TxPending, Message: "not found at Paystack"}, nil
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("paystack: %w", err)
	}
	t := env.Data.transaction()
	if t.Reference == "" {
		t.Reference = reference
	}
	return t, nil
}

func (p *Paystack) ChargeSaved(ctx context.Context, c Charge) (Transaction, error) {
	body := map[string]any{"authorization_code": c.Authorization, "email": c.Email, "amount": c.AmountKobo, "currency": "NGN", "reference": c.Reference}
	var env struct {
		Status  bool        `json:"status"`
		Message string      `json:"message"`
		Data    paystackTxn `json:"data"`
	}
	if _, err := callJSON(ctx, http.MethodPost, p.base()+"/transaction/charge_authorization", p.auth(), body, &env); err != nil {
		return Transaction{}, fmt.Errorf("paystack: %w", err)
	}
	t := env.Data.transaction()
	if t.Reference == "" {
		t.Reference = c.Reference
	}
	return t, nil
}

// paystackVerify is the Paystack connector's webhook verification.
var paystackVerify = &connector.VerifySpec{Scheme: "hmac_sha512", Header: "x-paystack-signature"}

func (p *Paystack) ParseWebhook(h http.Header, body []byte) (WebhookEvent, error) {
	if p.SecretKey == "" || connector.VerifyWebhook(paystackVerify, p.SecretKey, h, body) != nil {
		return WebhookEvent{}, ErrBadSignature
	}
	var ev struct {
		Event string `json:"event"`
		Data  struct {
			ID        int64  `json:"id"`
			Reference string `json:"reference"`
		} `json:"data"`
	}
	if err := jsonUnmarshal(body, &ev); err != nil {
		return WebhookEvent{}, fmt.Errorf("paystack webhook: %w", err)
	}
	return WebhookEvent{Type: ev.Event, Reference: ev.Data.Reference, Payment: ev.Event == "charge.success",
		DedupKey: ev.Event + ":" + ev.Data.Reference}, nil
}
