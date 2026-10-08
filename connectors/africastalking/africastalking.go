// Package africastalking is the Africa's Talking connector: bulk SMS, the
// SMS inbox, airtime, the application balance, and callbacks for delivery
// reports, incoming SMS and airtime status. Built from Africa's Talking's
// public documentation (docs/integrations/africastalking.md).
//
// Every request carries the API key in an apiKey header and the
// application username (in the body of a POST, the query of a GET).
//
// Environments: the connection's environment "sandbox" uses
// api.sandbox.africastalking.com. Live SMS uses the JSON bulk endpoint
// (/version1/messaging/bulk); its sandbox is documented as "coming soon",
// so the sandbox sends through the form-encoded /version1/messaging
// endpoint, which both environments document.
//
// Sends: SMS has no idempotency key and airtime's Idempotency-Key only
// guards five minutes, so both are unsafe writes. A response Africa's
// Talking explains per recipient is reported, not retried.
package africastalking

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/connectors/internal/money"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// SandboxURL is used for connections whose environment is "sandbox".
const SandboxURL = "https://api.sandbox.africastalking.com"

// Options configure the connector; BaseURL replaces both environments
// (tests).
type Options struct {
	BaseURL string
}

// New returns the Africa's Talking connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{live: strings.TrimRight(m.BaseURL, "/"), sandbox: SandboxURL}
	if o.BaseURL != "" {
		c.live, c.sandbox = strings.TrimRight(o.BaseURL, "/"), strings.TrimRight(o.BaseURL, "/")
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"send_sms":           connector.ActionFunc(c.sendSMS),
		"fetch_messages":     connector.ActionFunc(c.fetchMessages),
		"send_airtime":       connector.ActionFunc(c.sendAirtime),
		"get_airtime_status": connector.ActionFunc(c.airtimeStatus),
		"get_balance":        connector.ActionFunc(c.balance),
	}}
}

type client struct{ live, sandbox string }

func sandbox(req connector.Request) bool {
	return strings.EqualFold(strings.TrimSpace(req.Credentials["environment"]), "sandbox")
}

func (c *client) base(req connector.Request) string {
	if sandbox(req) {
		return c.sandbox
	}
	return c.live
}

// apiError is a non-2xx answer; Africa's Talking often explains in plain text.
type apiError struct {
	status int
	text   string
}

func (e *apiError) Error() string { return fmt.Sprintf("africastalking %d: %s", e.status, e.text) }

func credentials(req connector.Request) (user, key string, err error) {
	user, key = strings.TrimSpace(req.Credentials["username"]), strings.TrimSpace(req.Credentials["api_key"])
	if user == "" || key == "" {
		return "", "", fmt.Errorf("africastalking: the connection needs username and api_key: %w", effects.ErrFatal)
	}
	return user, key, nil
}

// do sends one request. body is JSON (map) or form (url.Values); out is
// decoded from JSON. Failures are classified as connector.DoJSON does:
// transport errors by whether anything was sent, 429 refused (nothing
// sent), 503 retryable, other 5xx unknown outcome, other 4xx fatal.
func (c *client) do(ctx context.Context, req connector.Request, method, u string, headers map[string]string, body any, out any) error {
	_, key, err := credentials(req)
	if err != nil {
		return err
	}
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case url.Values:
		rd, ctype = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return fmt.Errorf("africastalking: encode request: %w: %w", err, effects.ErrFatal)
		}
		rd, ctype = bytes.NewReader(raw), "application/json"
	}
	hr, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return fmt.Errorf("africastalking: %w: %w", err, effects.ErrFatal)
	}
	if ctype != "" {
		hr.Header.Set("Content-Type", ctype)
	}
	hr.Header.Set("Accept", "application/json")
	hr.Header.Set("apiKey", key)
	for k, v := range headers {
		hr.Header.Set(k, v)
	}
	resp, err := req.HTTP.Do(hr)
	if err != nil {
		return connector.ClassifyTransport(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("africastalking: read response: %w: %w", err, effects.ErrUnknownOutcome)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &apiError{status: resp.StatusCode, text: explain(raw)}
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			return fmt.Errorf("%w: %w: %w", ae, effects.ErrRetryable, effects.ErrNotSent)
		case resp.StatusCode == http.StatusServiceUnavailable:
			return fmt.Errorf("%w: %w", ae, effects.ErrRetryable)
		case resp.StatusCode >= 500:
			return fmt.Errorf("%w: %w", ae, effects.ErrUnknownOutcome)
		}
		return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
	}
	if out != nil {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(out); err != nil {
			return fmt.Errorf("africastalking: unreadable response %q: %w: %w", truncate(raw), err, effects.ErrUnknownOutcome)
		}
	}
	return nil
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// explain pulls a message out of an error body (JSON or text).
func explain(raw []byte) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		for _, k := range []string{"errorMessage", "message", "error", "description"} {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
		if d, ok := m["SMSMessageData"].(map[string]any); ok {
			if s, ok := d["Message"].(string); ok && s != "" {
				return s
			}
		}
	}
	return truncate(raw)
}

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func intOf(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case float64:
		return int64(n), n == float64(int64(n))
	case int:
		return int64(n), true
	case int64:
		return n, true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	}
	return 0, false
}

// smsResponse is what both SMS endpoints return.
type smsResponse struct {
	SMSMessageData struct {
		Message    string `json:"Message"`
		Recipients []struct {
			StatusCode json.Number `json:"statusCode"`
			Number     string      `json:"number"`
			Status     string      `json:"status"`
			Cost       string      `json:"cost"`
			MessageID  string      `json:"messageId"`
		} `json:"Recipients"`
	} `json:"SMSMessageData"`
}

func recipients(in map[string]any) ([]string, error) {
	var out []string
	switch v := in["to"].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			} else {
				return nil, fmt.Errorf("africastalking: every recipient must be a phone number, got %v: %w", x, effects.ErrFatal)
			}
		}
	case []string:
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("africastalking: send_sms needs at least one recipient in to: %w", effects.ErrFatal)
	}
	return out, nil
}

func (c *client) sendSMS(ctx context.Context, req connector.Request) (connector.Response, error) {
	user, _, err := credentials(req)
	if err != nil {
		return connector.Response{}, err
	}
	to, err := recipients(req.Input)
	if err != nil {
		return connector.Response{}, err
	}
	msg := str(req.Input, "message")
	if msg == "" {
		return connector.Response{}, fmt.Errorf("africastalking: message is required: %w", effects.ErrFatal)
	}
	from := strings.TrimSpace(str(req.Input, "sender_id"))
	if from == "" {
		from = strings.TrimSpace(req.Credentials["sender_id"])
	}
	enqueue, _ := req.Input["enqueue"].(bool)
	var r smsResponse
	if sandbox(req) {
		// The form endpoint: "to" is comma separated; "from" is optional.
		f := url.Values{"username": {user}, "to": {strings.Join(to, ",")}, "message": {msg}}
		if from != "" {
			f.Set("from", from)
		}
		if enqueue {
			f.Set("enqueue", "1")
		}
		err = c.do(ctx, req, http.MethodPost, c.base(req)+"/version1/messaging", nil, f, &r)
	} else {
		if from == "" {
			return connector.Response{}, fmt.Errorf("africastalking: live bulk SMS needs a sender_id (a registered sender ID or short code) in the step or the connection: %w", effects.ErrFatal)
		}
		body := map[string]any{"username": user, "phoneNumbers": to, "message": msg, "senderId": from}
		if enqueue {
			body["enqueue"] = 1
		}
		err = c.do(ctx, req, http.MethodPost, c.base(req)+"/version1/messaging/bulk", nil, body, &r)
	}
	if err != nil {
		return connector.Response{}, err
	}
	return smsOutcome(r)
}

// smsOutcome reports each recipient. 100, 101 and 102 are accepted. A send
// Africa's Talking refused for every recipient fails the step: for 4xx codes
// (invalid sender, number, balance, blacklist, risk hold) nothing was sent;
// for 5xx codes (its own or the gateway's errors) a person checks.
func smsOutcome(r smsResponse) (connector.Response, error) {
	d := r.SMSMessageData
	out := make([]any, 0, len(d.Recipients))
	accepted, server := 0, false
	var reasons []string
	for _, rc := range d.Recipients {
		code, _ := rc.StatusCode.Int64()
		if code >= 100 && code <= 102 {
			accepted++
		} else {
			reasons = append(reasons, fmt.Sprintf("%s: %d %s", rc.Number, code, rc.Status))
			if code >= 500 {
				server = true
			}
		}
		out = append(out, map[string]any{"number": rc.Number, "status": rc.Status, "status_code": code, "cost": rc.Cost, "message_id": rc.MessageID})
	}
	if accepted == 0 {
		why := strings.Join(reasons, "; ")
		if why == "" {
			why = d.Message
		}
		if server {
			return connector.Response{}, fmt.Errorf("africastalking: no recipient accepted (%s); Africa's Talking reported its own or a gateway error, so check the dashboard before resolving the step: %w", why, effects.ErrIndeterminate)
		}
		return connector.Response{}, fmt.Errorf("africastalking: no recipient accepted: %s: %w", why, effects.ErrFatal)
	}
	return connector.Response{Output: map[string]any{"summary": d.Message, "accepted": int64(accepted), "rejected": int64(len(d.Recipients) - accepted), "recipients": out}}, nil
}

func (c *client) fetchMessages(ctx context.Context, req connector.Request) (connector.Response, error) {
	user, _, err := credentials(req)
	if err != nil {
		return connector.Response{}, err
	}
	last := "0"
	if v, ok := req.Input["last_received_id"]; ok && v != nil && v != "" {
		n, ok := intOf(v)
		if !ok {
			return connector.Response{}, fmt.Errorf("africastalking: last_received_id must be a number: %w", effects.ErrFatal)
		}
		last = strconv.FormatInt(n, 10)
	}
	q := url.Values{"username": {user}, "lastReceivedId": {last}}
	var r struct {
		SMSMessageData struct {
			Messages []map[string]any `json:"Messages"`
		} `json:"SMSMessageData"`
	}
	if err := c.do(ctx, req, http.MethodGet, c.base(req)+"/version1/messaging?"+q.Encode(), nil, nil, &r); err != nil {
		return connector.Response{}, err
	}
	out := make([]any, 0, len(r.SMSMessageData.Messages))
	for _, m := range r.SMSMessageData.Messages {
		out = append(out, map[string]any{"id": str(m, "id"), "from": str(m, "from"), "to": str(m, "to"), "text": str(m, "text"),
			"date": str(m, "date"), "link_id": str(m, "linkId")})
	}
	return connector.Response{Output: map[string]any{"messages": out}}, nil
}

func (c *client) sendAirtime(ctx context.Context, req connector.Request) (connector.Response, error) {
	user, _, err := credentials(req)
	if err != nil {
		return connector.Response{}, err
	}
	list, _ := req.Input["recipients"].([]any)
	if len(list) == 0 {
		return connector.Response{}, fmt.Errorf("africastalking: send_airtime needs recipients: %w", effects.ErrFatal)
	}
	var rcpts []any
	for i, x := range list {
		r, _ := x.(map[string]any)
		phone, cur := strings.TrimSpace(str(r, "phone_number")), strings.ToUpper(strings.TrimSpace(str(r, "currency_code")))
		amount, err := money.Minor(r["amount"])
		if phone == "" || len(cur) != 3 || err != nil || amount < 1 {
			return connector.Response{}, fmt.Errorf("africastalking: recipient %d needs phone_number, a three-letter currency_code and a positive whole amount in hundredths: %w", i, effects.ErrFatal)
		}
		rcpts = append(rcpts, map[string]any{"phoneNumber": phone, "amount": cur + " " + money.Format(amount, 2)})
	}
	body := map[string]any{"username": user, "recipients": rcpts}
	if v, ok := req.Input["max_num_retry"]; ok && v != nil {
		n, ok := intOf(v)
		if !ok || n < 0 {
			return connector.Response{}, fmt.Errorf("africastalking: max_num_retry must be a whole number: %w", effects.ErrFatal)
		}
		body["maxNumRetry"] = n
	}
	var headers map[string]string
	if req.IdempotencyKey != "" {
		headers = map[string]string{"Idempotency-Key": req.IdempotencyKey}
	}
	var r struct {
		ErrorMessage  string           `json:"errorMessage"`
		NumSent       json.Number      `json:"numSent"`
		TotalAmount   string           `json:"totalAmount"`
		TotalDiscount string           `json:"totalDiscount"`
		Responses     []map[string]any `json:"responses"`
	}
	if err := c.do(ctx, req, http.MethodPost, c.base(req)+"/version1/airtime/send", headers, body, &r); err != nil {
		return connector.Response{}, err
	}
	sent, _ := r.NumSent.Int64()
	resps := make([]any, 0, len(r.Responses))
	var reasons []string
	for _, x := range r.Responses {
		resps = append(resps, map[string]any{"phone_number": str(x, "phoneNumber"), "status": str(x, "status"), "request_id": str(x, "requestId"),
			"amount": str(x, "amount"), "discount": str(x, "discount"), "error_message": str(x, "errorMessage")})
		if e := str(x, "errorMessage"); e != "" && e != "None" {
			reasons = append(reasons, str(x, "phoneNumber")+": "+e)
		}
	}
	if sent == 0 {
		// Refused before reaching a telco: nothing was sent and Africa's
		// Talking does not retry.
		why := strings.Join(reasons, "; ")
		if why == "" {
			why = r.ErrorMessage
		}
		return connector.Response{}, fmt.Errorf("africastalking: no airtime sent: %s: %w", why, effects.ErrFatal)
	}
	return connector.Response{Output: map[string]any{"num_sent": sent, "total_amount": r.TotalAmount, "total_discount": r.TotalDiscount,
		"error_message": r.ErrorMessage, "responses": resps}}, nil
}

func (c *client) airtimeStatus(ctx context.Context, req connector.Request) (connector.Response, error) {
	user, _, err := credentials(req)
	if err != nil {
		return connector.Response{}, err
	}
	id := strings.TrimSpace(str(req.Input, "transaction_id"))
	if id == "" {
		return connector.Response{}, fmt.Errorf("africastalking: transaction_id is required: %w", effects.ErrFatal)
	}
	q := url.Values{"username": {user}, "transactionId": {id}}
	var r map[string]any
	if err := c.do(ctx, req, http.MethodGet, c.base(req)+"/query/transaction/find?"+q.Encode(), nil, nil, &r); err != nil {
		return connector.Response{}, err
	}
	status := str(r, "status")
	if status == "" {
		if d, ok := r["data"].(map[string]any); ok {
			status = str(d, "status")
		}
	}
	if status == "" {
		return connector.Response{}, fmt.Errorf("africastalking: the transaction lookup returned no status (%v): %w", r, effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: map[string]any{"status": status}}, nil
}

func (c *client) balance(ctx context.Context, req connector.Request) (connector.Response, error) {
	user, _, err := credentials(req)
	if err != nil {
		return connector.Response{}, err
	}
	var r struct {
		UserData struct {
			Balance string `json:"balance"`
		} `json:"UserData"`
	}
	if err := c.do(ctx, req, http.MethodGet, c.base(req)+"/version1/user?"+url.Values{"username": {user}}.Encode(), nil, nil, &r); err != nil {
		return connector.Response{}, err
	}
	bal := strings.TrimSpace(r.UserData.Balance)
	cur, amt, ok := strings.Cut(bal, " ")
	if !ok {
		return connector.Response{}, fmt.Errorf("africastalking: unreadable balance %q: %w", bal, effects.ErrUnknownOutcome)
	}
	var rd money.Reader
	n := rd.Floor(strings.TrimSpace(amt), 2)
	if rd.Err != nil {
		return connector.Response{}, fmt.Errorf("africastalking: unreadable balance %q: %w: %w", bal, rd.Err, effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: map[string]any{"balance": bal, "currency": cur, "amount": n}}, nil
}
