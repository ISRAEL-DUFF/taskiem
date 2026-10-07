package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
)

// Flutterwave collects payments through Flutterwave's v3 API (written from
// its public documentation): hosted checkout (card, bank transfer), card
// tokens for renewals, and webhooks carrying the operator's secret hash in
// verif-hash (the Flutterwave connector's check). Amounts at Flutterwave
// are naira; Taskiem's are kobo.
type Flutterwave struct {
	SecretKey   string
	WebhookHash string // the secret hash set on the Flutterwave dashboard
	BaseURL     string // default https://api.flutterwave.com/v3
}

// FlutterwaveBaseURL is Flutterwave's v3 API.
const FlutterwaveBaseURL = "https://api.flutterwave.com/v3"

func (f *Flutterwave) Name() string { return "flutterwave" }

func (f *Flutterwave) base() string {
	if f.BaseURL != "" {
		return strings.TrimRight(f.BaseURL, "/")
	}
	return FlutterwaveBaseURL
}

func (f *Flutterwave) auth() map[string]string {
	return map[string]string{"Authorization": "Bearer " + f.SecretKey}
}

// naira renders kobo as a decimal naira amount without float error.
func naira(kobo int64) json.Number {
	return json.Number(fmt.Sprintf("%d.%02d", kobo/100, kobo%100))
}

type flwTxn struct {
	ID          int64       `json:"id"`
	TxRef       string      `json:"tx_ref"`
	Status      string      `json:"status"`
	Amount      json.Number `json:"amount"`
	Currency    string      `json:"currency"`
	PaymentType string      `json:"payment_type"`
	ProcessorRe string      `json:"processor_response"`
	Card        *struct {
		Token    string `json:"token"`
		Type     string `json:"type"`
		Last4    string `json:"last_4digits"`
		Expiry   string `json:"expiry"`
		Issuer   string `json:"issuer"`
		Country  string `json:"country"`
		Reusable *bool  `json:"reusable,omitempty"`
	} `json:"card"`
}

func (t flwTxn) transaction() Transaction {
	out := Transaction{Reference: t.TxRef, Currency: t.Currency, Channel: t.PaymentType, ProviderID: strconv.FormatInt(t.ID, 10), Message: t.ProcessorRe}
	if f, err := t.Amount.Float64(); err == nil {
		out.AmountKobo = int64(math.Round(f * 100))
	}
	switch strings.ToLower(t.Status) {
	case "successful":
		out.Status = TxSuccess
	case "failed":
		out.Status = TxFailed
	case "cancelled":
		out.Status = TxAbandoned
	default:
		out.Status = TxPending
	}
	if c := t.Card; c != nil && c.Token != "" {
		out.Authorization = &Authorization{Code: c.Token, Brand: c.Type, Last4: c.Last4, Exp: c.Expiry, Reusable: true}
	}
	return out
}

func (f *Flutterwave) StartCheckout(ctx context.Context, c Checkout) (CheckoutSession, error) {
	opts := "card,banktransfer"
	if len(c.Channels) > 0 {
		var o []string
		for _, ch := range c.Channels {
			o = append(o, strings.ReplaceAll(ch, "_", ""))
		}
		opts = strings.Join(o, ",")
	}
	body := map[string]any{"tx_ref": c.Reference, "amount": naira(c.AmountKobo), "currency": "NGN", "payment_options": opts,
		"customer": map[string]string{"email": c.Email}, "meta": c.Metadata}
	if c.CallbackURL != "" {
		body["redirect_url"] = c.CallbackURL
	}
	var env struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Link string `json:"link"`
		} `json:"data"`
	}
	if _, err := callJSON(ctx, http.MethodPost, f.base()+"/payments", f.auth(), body, &env); err != nil {
		return CheckoutSession{}, fmt.Errorf("flutterwave: %w", err)
	}
	if env.Status != "success" || env.Data.Link == "" {
		return CheckoutSession{}, fmt.Errorf("flutterwave: payments: %s", env.Message)
	}
	return CheckoutSession{URL: env.Data.Link}, nil
}

func (f *Flutterwave) Verify(ctx context.Context, reference string) (Transaction, error) {
	var env struct {
		Status string `json:"status"`
		Data   flwTxn `json:"data"`
	}
	st, err := callJSON(ctx, http.MethodGet, f.base()+"/transactions/verify_by_reference?tx_ref="+url.QueryEscape(reference), f.auth(), nil, &env)
	if st == http.StatusNotFound || st == http.StatusBadRequest {
		return Transaction{Reference: reference, Status: TxPending, Message: "not found at Flutterwave"}, nil
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("flutterwave: %w", err)
	}
	t := env.Data.transaction()
	if t.Reference == "" {
		t.Reference = reference
	}
	return t, nil
}

func (f *Flutterwave) ChargeSaved(ctx context.Context, c Charge) (Transaction, error) {
	body := map[string]any{"token": c.Authorization, "email": c.Email, "currency": "NGN", "amount": naira(c.AmountKobo), "tx_ref": c.Reference}
	var env struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    flwTxn `json:"data"`
	}
	if _, err := callJSON(ctx, http.MethodPost, f.base()+"/tokenized-charges", f.auth(), body, &env); err != nil {
		return Transaction{}, fmt.Errorf("flutterwave: %w", err)
	}
	t := env.Data.transaction()
	if t.Reference == "" {
		t.Reference = c.Reference
	}
	return t, nil
}

var flutterwaveVerify = &connector.VerifySpec{Scheme: "header_secret", Header: "verif-hash"}

func (f *Flutterwave) ParseWebhook(h http.Header, body []byte) (WebhookEvent, error) {
	if f.WebhookHash == "" || connector.VerifyWebhook(flutterwaveVerify, f.WebhookHash, h, body) != nil {
		return WebhookEvent{}, ErrBadSignature
	}
	var ev struct {
		Event string `json:"event"`
		Data  struct {
			ID    int64  `json:"id"`
			TxRef string `json:"tx_ref"`
		} `json:"data"`
	}
	if err := jsonUnmarshal(body, &ev); err != nil {
		return WebhookEvent{}, fmt.Errorf("flutterwave webhook: %w", err)
	}
	return WebhookEvent{Type: ev.Event, Reference: ev.Data.TxRef, Payment: ev.Event == "charge.completed",
		DedupKey: ev.Event + ":" + ev.Data.TxRef}, nil
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
