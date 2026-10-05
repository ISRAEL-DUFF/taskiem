package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
)

// HTTPError is a non-2xx response; it wraps the matching effects sentinel.
type HTTPError struct {
	Status     int
	Body       []byte
	RetryAfter time.Duration
	kind       error
}

func (e *HTTPError) Error() string {
	b := e.Body
	if len(b) > 300 {
		b = b[:300]
	}
	return fmt.Sprintf("http %d: %s", e.Status, bytes.TrimSpace(b))
}

func (e *HTTPError) Unwrap() error { return e.kind }

// DoJSON sends a JSON request and decodes a JSON response into out. Errors
// are classified for the engine: transport failures before sending are
// not_sent, after sending unknown_outcome; 429 and 5xx retryable; other
// 4xx fatal (callers may inspect *HTTPError, e.g. to map 404 to ErrNotFound).
func DoJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w: %w", err, effects.ErrFatal)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ClassifyTransport(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w: %w", err, effects.ErrUnknownOutcome)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		he := &HTTPError{Status: resp.StatusCode, Body: raw, kind: effects.ErrFatal}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			he.kind = effects.ErrRetryable
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
				he.RetryAfter = time.Duration(s) * time.Second
			}
		}
		return he
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			// The provider acted but we cannot read what it said.
			return fmt.Errorf("decode response: %w: %w", err, effects.ErrUnknownOutcome)
		}
	}
	return nil
}

// ClassifyTransport decides whether a transport error proves nothing was sent.
func ClassifyTransport(err error) error {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.Is(err, effects.ErrNotSent), errors.Is(err, effects.ErrFatal), errors.Is(err, egress.ErrDenied):
		return err
	case errors.As(err, &dnsErr), errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("%w: %w", err, effects.ErrNotSent)
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return fmt.Errorf("%w: %w", err, effects.ErrNotSent)
	}
	return fmt.Errorf("%w: %w", err, effects.ErrUnknownOutcome)
}

// StatusOf returns the HTTP status of an *HTTPError, or 0.
func StatusOf(err error) int {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}
