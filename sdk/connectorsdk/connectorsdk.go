// Package connectorsdk writes Taskiem connectors in Go that run as
// WebAssembly (ABI taskiem-connector/v1, docs/contracts/connector-wasm-v1.md).
//
// A connector registers a handler per manifest action in init and is built
// as a WASI reactor:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o connector.wasm .
//
// Handlers reach the network only through Do, which the engine sends
// through the tenant's egress guard to the hosts the manifest declares.
// Errors decide what the engine does next, exactly as for built-in
// connectors: wrap them with Retryable, Fatal, UnknownOutcome, NotSent or
// Indeterminate, or return NotFound from a reconcile action. An unwrapped
// error is an unknown outcome.
package connectorsdk

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Request is one action call.
type Request struct {
	Action string `json:"action"`
	// BaseURL is the manifest's base_url, or the deployment's override.
	BaseURL        string            `json:"base_url"`
	Input          map[string]any    `json:"input"`
	Credentials    map[string]string `json:"credentials"`
	IdempotencyKey string            `json:"idempotency_key"`
	Attempt        int               `json:"attempt"`
	// KeyFirstSent is when a request with IdempotencyKey was first about to
	// be sent; zero for reads.
	KeyFirstSent time.Time `json:"key_first_sent"`
}

// Str returns a string input field, or "".
func (r *Request) Str(field string) string {
	s, _ := r.Input[field].(string)
	return s
}

// Handler runs one action and returns its output.
type Handler func(r *Request) (any, error)

var handlers = map[string]Handler{}

// requests counts Do calls in the current Run.
var requests int

// Handle registers the handler for a manifest action. Call it from init.
func Handle(action string, h Handler) { handlers[action] = h }

// Kinds of failure, as the engine classifies them.
const (
	KindRetryable      = "retryable"       // nothing happened; send again
	KindFatal          = "fatal"           // will fail the same way again
	KindUnknownOutcome = "unknown_outcome" // may have happened
	KindNotSent        = "not_sent"        // provably never left
	KindIndeterminate  = "indeterminate"   // only a person can settle it
	KindNotFound       = "not_found"       // a reconcile found no such effect
)

// Error is a classified failure.
type Error struct {
	Kind   string
	Status int // the HTTP status, for errors from StatusError
	Err    error
}

func (e *Error) Error() string { return e.Kind + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func classed(kind string, err error) error {
	if err == nil {
		err = errors.New(kind)
	}
	return &Error{Kind: kind, Err: err}
}

func Retryable(err error) error      { return classed(KindRetryable, err) }
func Fatal(err error) error          { return classed(KindFatal, err) }
func UnknownOutcome(err error) error { return classed(KindUnknownOutcome, err) }
func NotSent(err error) error        { return classed(KindNotSent, err) }
func Indeterminate(err error) error  { return classed(KindIndeterminate, err) }

// NotFound is what a reconcile action returns when the provider has no
// record of the effect, so the engine may send it again under a new key.
var NotFound = &Error{Kind: KindNotFound, Err: errors.New("not found")}

// KindOf is the kind an error carries; unknown_outcome when it has none.
func KindOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindUnknownOutcome
}

// HTTPRequest is an outbound request.
type HTTPRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"-"`
}

// HTTPResponse is the provider's answer.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"-"`
}

type wireRequest struct {
	HTTPRequest
	Body string `json:"body,omitempty"`
}

type wireResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Error   *wireError        `json:"error,omitempty"`
}

type wireError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Do sends a request through the engine. A transport failure comes back
// classified (not_sent when nothing left, unknown_outcome otherwise); any
// HTTP status is a response, for the handler to judge (see StatusError).
func Do(r HTTPRequest) (*HTTPResponse, error) {
	requests++
	raw, err := json.Marshal(wireRequest{HTTPRequest: r, Body: base64.StdEncoding.EncodeToString(r.Body)})
	if err != nil {
		return nil, Fatal(err)
	}
	var w wireResponse
	if err := json.Unmarshal(hostHTTP(raw), &w); err != nil {
		return nil, UnknownOutcome(fmt.Errorf("unreadable host response: %w", err))
	}
	if w.Error != nil {
		return nil, classed(w.Error.Kind, errors.New(w.Error.Message))
	}
	body, err := base64.StdEncoding.DecodeString(w.Body)
	if err != nil {
		return nil, UnknownOutcome(err)
	}
	return &HTTPResponse{Status: w.Status, Headers: w.Headers, Body: body}, nil
}

// DoJSON sends body as JSON and decodes a 2xx response into out; other
// statuses become a StatusError.
func DoJSON(method, url string, headers map[string]string, body, out any) error {
	h := map[string]string{"Accept": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return Fatal(err)
		}
		h["Content-Type"] = "application/json"
	}
	resp, err := Do(HTTPRequest{Method: method, URL: url, Headers: h, Body: raw})
	if err != nil {
		return err
	}
	if resp.Status < 200 || resp.Status > 299 {
		return StatusError(resp)
	}
	if out != nil && len(strings.TrimSpace(string(resp.Body))) > 0 {
		if err := json.Unmarshal(resp.Body, out); err != nil {
			return UnknownOutcome(fmt.Errorf("decode response: %w", err))
		}
	}
	return nil
}

// StatusError classifies a non-2xx response as the engine's built-in
// connectors do: 429 and 503 retryable (refused before processing), other
// 5xx unknown outcome (the provider may have acted), 4xx fatal.
func StatusError(r *HTTPResponse) error {
	b := strings.TrimSpace(string(r.Body))
	if len(b) > 300 {
		b = b[:300]
	}
	e := &Error{Kind: KindFatal, Status: r.Status, Err: fmt.Errorf("http %d: %s", r.Status, b)}
	switch {
	case r.Status == 429 || r.Status == 503:
		e.Kind = KindRetryable
	case r.Status >= 500:
		e.Kind = KindUnknownOutcome
	}
	return e
}

// StatusOf is the HTTP status an error from StatusError carries, or 0.
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// Log writes a line to the step's log; the engine masks personal data.
func Log(format string, args ...any) { hostLog(fmt.Sprintf(format, args...)) }

type wireResult struct {
	Output any        `json:"output,omitempty"`
	Error  *wireError `json:"error,omitempty"`
}

// Run executes one call: a JSON Request in, a JSON result out. The ABI
// export calls it; tests can call it directly.
func Run(in []byte) (out []byte) {
	res := wireResult{}
	requests = 0
	defer func() {
		if p := recover(); p != nil {
			// Before any request nothing happened, and the same input
			// will panic again.
			kind := KindFatal
			if requests > 0 {
				kind = KindUnknownOutcome
			}
			res = wireResult{Error: &wireError{Kind: kind, Message: fmt.Sprintf("connector panicked: %v", p)}}
		}
		out, _ = json.Marshal(res)
	}()
	var r Request
	if err := json.Unmarshal(in, &r); err != nil {
		res.Error = &wireError{Kind: KindFatal, Message: "bad request: " + err.Error()}
		return
	}
	h := handlers[r.Action]
	if h == nil {
		res.Error = &wireError{Kind: KindFatal, Message: fmt.Sprintf("no handler for action %q", r.Action)}
		return
	}
	v, err := h(&r)
	if err != nil {
		res.Error = &wireError{Kind: KindOf(err), Message: err.Error()}
		return
	}
	if v == nil {
		v = map[string]any{}
	}
	res.Output = v
	return
}
