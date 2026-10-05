package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

// maxHTTPBody bounds a response kept as step output (spec 4.9: 256 KB inline).
const maxHTTPBody = 256 << 10

// doHTTP executes an http step. Outbound traffic will go through the egress
// proxy (spec 14.2) once it exists; the client is injected for that.
func (w *Worker) doHTTP(ctx context.Context, p *plan, in any) (any, error) {
	cfg, _ := in.(map[string]any)
	method, _ := cfg["method"].(string)
	rawURL, _ := cfg["url"].(string)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("invalid url %q: %w", rawURL, effects.ErrFatal)
	}
	if q, ok := cfg["query"].(map[string]any); ok {
		vals := u.Query()
		for k, v := range q {
			vals.Set(k, fmt.Sprint(v))
		}
		u.RawQuery = vals.Encode()
	}
	var body io.Reader
	if b, ok := cfg["body"]; ok && b != nil {
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("encode body: %w", effects.ErrFatal)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h, ok := cfg["headers"].(map[string]any); ok {
		for k, v := range h {
			req.Header.Set(k, fmt.Sprint(v))
		}
	}
	if strings.HasPrefix(p.field, "header:") && p.key != "" {
		req.Header.Set(strings.TrimPrefix(p.field, "header:"), p.key)
	}
	hc, err := w.client(ctx, p, w.CallTimeout)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, connector.ClassifyTransport(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBody+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w: %w", err, effects.ErrUnknownOutcome)
	}
	if len(raw) > maxHTTPBody {
		return nil, fmt.Errorf("response larger than %d bytes: %w", maxHTTPBody, effects.ErrFatal)
	}
	var parsed any = string(raw)
	if v, err := expr.DecodeJSON(raw); err == nil && len(raw) > 0 {
		parsed = v
	}
	out := map[string]any{"status": int64(resp.StatusCode), "body": parsed}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("http %d: %w", resp.StatusCode, effects.ErrRetryable)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("http %d: %w", resp.StatusCode, effects.ErrFatal)
	}
	return out, nil
}
