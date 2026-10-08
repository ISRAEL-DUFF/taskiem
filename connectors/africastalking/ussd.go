package africastalking

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/engine/ussd"
)

// USSD is Africa's Talking's USSD callback format, for the edge's fast
// path (docs/ussd.md). Built from Africa's Talking's public USSD
// documentation and help centre (links in docs/integrations/africastalking.md):
//
//   - each step of a session is an HTTP POST to the service code's callback
//     URL, form-encoded, with sessionId, serviceCode, phoneNumber,
//     networkCode and text, where text is everything the caller typed in
//     this session joined with "*" ("" on the first request);
//   - the answer is plain text starting "CON " (show this and wait for the
//     next input) or "END " (show this and close the session);
//   - callbacks carry no signature, so the edge authenticates them with a
//     per-tenant token in the URL and, optionally, an address allow-list.
type USSD struct{}

// USSDAdapter is the adapter the edge registers as "africastalking".
var USSDAdapter ussd.Adapter = USSD{}

// Name implements ussd.Adapter.
func (USSD) Name() string { return "africastalking" }

// Incremental implements ussd.Adapter: text carries the whole path.
func (USSD) Incremental() bool { return false }

// Parse implements ussd.Adapter.
func (USSD) Parse(r *http.Request, body []byte) (ussd.Request, error) {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if ct != "" && ct != "application/x-www-form-urlencoded" {
		return ussd.Request{}, fmt.Errorf("%w: content type %q", ussd.ErrBadRequest, ct)
	}
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return ussd.Request{}, fmt.Errorf("%w: %w", ussd.ErrBadRequest, err)
	}
	return ussd.Request{
		SessionID:   vals.Get("sessionId"),
		ServiceCode: strings.TrimSpace(vals.Get("serviceCode")),
		Phone:       ussd.NormalPhone(strings.TrimSpace(vals.Get("phoneNumber"))),
		Network:     strings.TrimSpace(vals.Get("networkCode")),
		Input:       vals.Get("text"),
	}, nil
}

// Reply implements ussd.Adapter: "CON <text>" or "END <text>", plain text.
func (USSD) Reply(w http.ResponseWriter, end bool, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	prefix := "CON "
	if end {
		prefix = "END "
	}
	_, _ = w.Write([]byte(prefix + text))
}
