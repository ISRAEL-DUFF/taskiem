package sender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/effects"
)

// How a provider's refusal is treated. Anything else a provider returns is
// ambiguous: the message may have gone, and is never sent again, because an
// SMS has no idempotency key (the USSD notifier's rule too).
var (
	// ErrNotSent: the message certainly did not leave (no connection, rate
	// limited). The next provider is tried, then the message later.
	ErrNotSent = errors.New("sender: not sent")
	// ErrProvider: the provider refused for its own reasons (credentials,
	// balance, sender ID, an outage). The next provider is tried, then the
	// message later; operators should look.
	ErrProvider = errors.New("sender: provider refused")
	// ErrRecipient: the number cannot be reached (invalid, barred, on a
	// do-not-disturb list the route cannot pass). The message is dead.
	ErrRecipient = errors.New("sender: recipient refused")
)

// SMSProvider sends one SMS to an E.164 number and returns its reference.
type SMSProvider interface {
	Name() string
	Send(ctx context.Context, to, text string) (ref string, err error)
}

// Termii sends through Termii's /api/sms/send. Channel "dnd" (the default)
// reaches numbers on Nigeria's do-not-disturb register, which transactional
// messages must, and needs an approved sender ID; "generic" does not.
type Termii struct {
	BaseURL, APIKey, SenderID, Channel string
	HTTP                               *http.Client
}

func (t *Termii) Name() string { return "termii" }

func (t *Termii) Send(ctx context.Context, to, text string) (string, error) {
	channel := t.Channel
	if channel == "" {
		channel = "dnd"
	}
	body, _ := json.Marshal(map[string]string{"api_key": t.APIKey, "to": strings.TrimPrefix(to, "+"), "from": t.SenderID,
		"sms": text, "type": "plain", "channel": channel})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.BaseURL, "/")+"/api/sms/send", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	status, raw, err := do(t.HTTP, req)
	if err != nil {
		return "", err
	}
	var out struct {
		Code      string `json:"code"`
		MessageID string `json:"message_id"`
		Message   string `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)
	switch {
	case status == http.StatusOK && out.MessageID != "":
		return out.MessageID, nil
	case status == http.StatusTooManyRequests:
		return "", fmt.Errorf("termii: %w: rate limited", ErrNotSent)
	case status >= 500, status == http.StatusUnauthorized, status == http.StatusForbidden:
		return "", fmt.Errorf("termii: %w: %d %s", ErrProvider, status, clip(out.Message))
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		// Termii answers 400 for its own problems (balance, sender ID) and
		// for bad numbers alike; only the message tells them apart.
		if m := strings.ToLower(out.Message); strings.Contains(m, "phone") || strings.Contains(m, "number") || strings.Contains(m, "recipient") {
			return "", fmt.Errorf("termii: %w: %s", ErrRecipient, clip(out.Message))
		}
		return "", fmt.Errorf("termii: %w: %d %s", ErrProvider, status, clip(out.Message))
	}
	return "", fmt.Errorf("termii: unexpected answer %d %s", status, clip(string(raw)))
}

// AfricasTalking sends through Africa's Talking's /version1/messaging.
type AfricasTalking struct {
	BaseURL, Username, APIKey, SenderID string
	HTTP                                *http.Client
}

func (a *AfricasTalking) Name() string { return "africastalking" }

func (a *AfricasTalking) Send(ctx context.Context, to, text string) (string, error) {
	form := url.Values{"username": {a.Username}, "to": {to}, "message": {text}}
	if a.SenderID != "" {
		form.Set("from", a.SenderID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.BaseURL, "/")+"/version1/messaging", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("apiKey", a.APIKey)
	status, raw, err := do(a.HTTP, req)
	if err != nil {
		return "", err
	}
	switch {
	case status == http.StatusTooManyRequests:
		return "", fmt.Errorf("africastalking: %w: rate limited", ErrNotSent)
	case status == http.StatusUnauthorized || status == http.StatusForbidden || status >= 500:
		return "", fmt.Errorf("africastalking: %w: %d", ErrProvider, status)
	case status != http.StatusOK && status != http.StatusCreated:
		return "", fmt.Errorf("africastalking: unexpected answer %d %s", status, clip(string(raw)))
	}
	var out struct {
		SMSMessageData struct {
			Recipients []struct {
				StatusCode int    `json:"statusCode"`
				Status     string `json:"status"`
				MessageID  string `json:"messageId"`
			} `json:"Recipients"`
		} `json:"SMSMessageData"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.SMSMessageData.Recipients) != 1 {
		return "", fmt.Errorf("africastalking: unexpected answer %s", clip(string(raw)))
	}
	r := out.SMSMessageData.Recipients[0]
	switch r.StatusCode {
	case 100, 101, 102: // processed, sent, queued
		return r.MessageID, nil
	case 403, 404, 406, 409: // invalid number, unsupported type, blacklisted, DND
		return "", fmt.Errorf("africastalking: %w: %s", ErrRecipient, r.Status)
	case 401, 402, 405, 407, 500, 501, 502: // risk hold, sender ID, balance, routing, gateway
		return "", fmt.Errorf("africastalking: %w: %s", ErrProvider, r.Status)
	}
	return "", fmt.Errorf("africastalking: unexpected status %d %s", r.StatusCode, r.Status)
}

// do sends req and reads up to 64 KiB of the answer. A request that never
// reached the provider is ErrNotSent; one that failed later is ambiguous.
func do(c *http.Client, req *http.Request) (int, []byte, error) {
	resp, err := c.Do(req)
	if err != nil {
		if errors.Is(err, effects.ErrNotSent) {
			return 0, nil, fmt.Errorf("%w: %w", ErrNotSent, err)
		}
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, raw, nil
}

func clip(s string) string {
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.ToValidUTF8(s, "")
}

// gsm7 is the GSM 03.38 default alphabet; gsm7ext are the characters that
// take two septets (an escape and the character).
const (
	gsm7    = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
	gsm7ext = "^{}\\[~]|€\f"
)

// Segments is how many SMS parts text takes: 160 GSM-7 septets in one part
// or 153 in each of several; 70 UCS-2 characters, or 67 each, once any
// character is outside GSM-7 (a naira sign, most emoji).
func Segments(text string) int {
	septets, gsm := 0, true
	for _, r := range text {
		switch {
		case strings.ContainsRune(gsm7, r):
			septets++
		case strings.ContainsRune(gsm7ext, r):
			septets += 2
		default:
			gsm = false
		}
	}
	if gsm {
		if septets <= 160 {
			return 1
		}
		return (septets + 152) / 153
	}
	// UCS-2 counts UTF-16 code units: a character outside the BMP is two.
	units := 0
	for _, r := range text {
		if utf8.RuneLen(r) == 4 {
			units += 2
		} else {
			units++
		}
	}
	if units <= 70 {
		return 1
	}
	return (units + 66) / 67
}
