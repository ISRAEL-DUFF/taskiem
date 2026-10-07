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
	"net/url"
	"strconv"
	"strings"
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
// not_sent, after sending unknown_outcome; 429 and 503 retryable; other 5xx
// unknown_outcome (the provider may have acted, so reads and idempotent
// writes retry while unsafe writes park); other 4xx fatal (callers may
// inspect *HTTPError, e.g. to map 404 to ErrNotFound).
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
		return fmt.Errorf("%w: %w", RedactURLError(err), effects.ErrFatal)
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
		switch {
		case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusServiceUnavailable:
			// Refused before processing: safe to send again.
			he.kind = effects.ErrRetryable
		case resp.StatusCode >= 500:
			// The provider may have acted before failing (500, 502, 504).
			he.kind = effects.ErrUnknownOutcome
		}
		if !errors.Is(he.kind, effects.ErrFatal) {
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

// RedactURL drops what a URL may carry besides where it goes: user info,
// query and fragment. Credentials passed in a query string (api_key=...)
// would otherwise reach error messages and so run history.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[url]"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// RedactURLError rewrites a *url.Error (what http.Client.Do returns) so its
// message names only scheme, host and path. The underlying error, and so
// errors.Is and errors.As, are unchanged.
func RedactURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) || ue.URL == RedactURL(ue.URL) {
		return err
	}
	clean := &url.Error{Op: ue.Op, URL: RedactURL(ue.URL), Err: ue.Err}
	if err == error(ue) { //nolint:errorlint // identity: err is the *url.Error itself, not wrapping it
		return clean
	}
	// Wrapped further: keep the chain, but the message must not repeat the URL.
	return &redacted{msg: strings.ReplaceAll(err.Error(), ue.URL, clean.URL), err: err}
}

type redacted struct {
	msg string
	err error
}

func (r *redacted) Error() string { return r.msg }
func (r *redacted) Unwrap() error { return r.err }

// ClassifyTransport decides whether a transport error proves nothing was
// sent. URLs in the result carry no query string.
func ClassifyTransport(err error) error {
	err = RedactURLError(err)
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
