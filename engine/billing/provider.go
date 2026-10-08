package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Provider collects the platform's naira payments (Paystack, Flutterwave).
// Its credentials are the operator's (environment), never a tenant's
// connection: tenants pay the platform, not themselves.
type Provider interface {
	Name() string
	// StartCheckout starts a hosted checkout and returns where to send the
	// payer.
	StartCheckout(ctx context.Context, c Checkout) (CheckoutSession, error)
	// Verify asks the provider for a payment's state: the truth a webhook
	// is checked against.
	Verify(ctx context.Context, reference string) (Transaction, error)
	// ChargeSaved charges a saved card authorization (renewals). Providers
	// that cannot return ErrUnsupported.
	ChargeSaved(ctx context.Context, c Charge) (Transaction, error)
	// ParseWebhook checks a delivery's signature and reads it. A bad
	// signature is ErrBadSignature.
	ParseWebhook(h http.Header, body []byte) (WebhookEvent, error)
}

// Checkout is a hosted payment of an invoice.
type Checkout struct {
	Reference   string
	AmountKobo  int64
	Email       string
	CallbackURL string
	// Channels offered: card, bank_transfer.
	Channels []string
	Metadata map[string]string
}

// CheckoutSession is where the payer completes the payment.
type CheckoutSession struct {
	URL       string
	AccessKey string // provider's access code, if any
}

// Charge charges a saved authorization.
type Charge struct {
	Reference     string
	AmountKobo    int64
	Email         string
	Authorization string
}

// Transaction states, the same across providers.
const (
	TxSuccess   = "success"
	TxFailed    = "failed"
	TxAbandoned = "abandoned"
	TxPending   = "pending"
)

// Transaction is a provider's view of a payment.
type Transaction struct {
	Reference     string
	Status        string // TxSuccess, TxFailed, TxAbandoned, TxPending
	AmountKobo    int64
	Currency      string
	Channel       string
	ProviderID    string
	Message       string
	Authorization *Authorization // a reusable card, when the payment was by card
}

// Authorization is a saved card.
type Authorization struct {
	Code     string
	Brand    string
	Last4    string
	Exp      string // MM/YYYY
	Reusable bool
}

// WebhookEvent is a verified delivery.
type WebhookEvent struct {
	Type      string // the provider's event name
	Reference string // our payment reference
	// Payment is true for events that settle a payment (Paystack
	// charge.success, Flutterwave charge.completed); others are ignored.
	Payment  bool
	DedupKey string
}

var (
	// ErrBadSignature: a webhook not signed with the platform's key.
	ErrBadSignature = errors.New("billing: bad webhook signature")
	// ErrUnsupported: the provider cannot do this.
	ErrUnsupported = errors.New("billing: not supported by this provider")
)

// httpClient calls providers: bounded time, no redirects to elsewhere.
var httpClient = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// callJSON sends body (nil: none) and decodes the response into out. A
// non-2xx status is an error carrying the provider's message.
func callJSON(ctx context.Context, method, url string, hdr map[string]string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode/100 != 2 {
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &msg)
		return resp.StatusCode, fmt.Errorf("%s %s: status %d: %s", method, url, resp.StatusCode, msg.Message)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: %w", method, url, err)
		}
	}
	return resp.StatusCode, nil
}
