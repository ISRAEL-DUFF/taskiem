package pgdock

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// Error classes (plan item P1-T6). The management API's error shape is
// {code, message}, with sql_error carrying a SQLSTATE; its codes are
// mapped only where the OpenAPI file names them, a SQLSTATE where one is
// present, and everything else by HTTP status, conservatively: only
// refusals that prove nothing was done (429, 503, and SQLSTATEs that mean
// the statement did not run) are retryable. The data API's errors are
// {"error": {code, message, details, request_id}} and its codes are
// published (Xenibase docs/errors.md, the Data API table, read 17 October
// 2026): mapDataError below follows that table.
const (
	ClassValidation  = "validation"
	ClassAuth        = "auth"
	ClassPermission  = "permission"
	ClassNotFound    = "not_found"
	ClassConflict    = "conflict"
	ClassQuota       = "quota"
	ClassRateLimited = "rate_limited"
	ClassUnavailable = "unavailable"
	ClassTimeout     = "timeout"
	ClassServer      = "server"
)

// Error is a Xenibase refusal, classified. It wraps the effects sentinel
// the engine acts on, and connector.ErrNotFound for a 404.
type Error struct {
	Status     int
	Code       string // Xenibase's error code
	Message    string
	SQLState   string
	Class      string
	RetryAfter time.Duration
	// RequestID is the data API's request_id, for Xenibase's support.
	RequestID string
	kind      error
	// rejected: a 401 invalid_token, the token revoked or expired
	// (Xenibase docs/errors.md); the engine expires an OAuth connection.
	rejected bool
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "pgdock %s (http %d", e.Class, e.Status)
	if e.Code != "" {
		b.WriteString(", " + e.Code)
	}
	if e.SQLState != "" {
		b.WriteString(", SQLSTATE " + e.SQLState)
	}
	b.WriteString(")")
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.RequestID != "" {
		b.WriteString(" (request " + e.RequestID + ")")
	}
	return b.String()
}

func (e *Error) Unwrap() []error {
	out := []error{e.kind}
	if e.Class == ClassNotFound {
		out = append(out, connector.ErrNotFound)
	}
	if e.rejected {
		out = append(out, connector.ErrCredentialsRejected)
	}
	return out
}

// RetryAfterDelay is Xenibase's Retry-After, honoured by the engine.
func (e *Error) RetryAfterDelay() time.Duration { return e.RetryAfter }

type apiError struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Quota    any    `json:"quota"`
	SQLError *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"sql_error"`
}

// sqlClass maps a SQLSTATE to a class and whether running the statement
// again may succeed.
func sqlClass(state string) (class string, retry bool) {
	switch {
	case state == "40001", state == "40P01": // serialisation failure, deadlock: nothing committed
		return ClassConflict, true
	case state == "57014": // statement timeout, cancelled
		return ClassTimeout, true
	case state == "42501": // insufficient privilege
		return ClassPermission, false
	case strings.HasPrefix(state, "23"): // integrity constraint (23505 unique)
		return ClassConflict, false
	case strings.HasPrefix(state, "08"), strings.HasPrefix(state, "53"), state == "57P01", state == "57P02", state == "57P03":
		return ClassUnavailable, true // connection, resources, shutdown
	case strings.HasPrefix(state, "22"), strings.HasPrefix(state, "42"):
		return ClassValidation, false // data or syntax
	}
	return ClassServer, false
}

// mapError classifies a call's error; transport errors pass through.
func mapError(err error) error {
	var he *connector.HTTPError
	if err == nil || !errors.As(err, &he) {
		return err
	}
	var body apiError
	_ = json.Unmarshal(he.Body, &body)
	e := &Error{Status: he.Status, Code: body.Code, Message: body.Message, RetryAfter: he.RetryAfter}
	kind := effects.ErrFatal
	switch st := he.Status; {
	case body.SQLError != nil && body.SQLError.Code != "":
		e.SQLState = body.SQLError.Code
		if e.Message == "" {
			e.Message = body.SQLError.Message
		}
		var retry bool
		if e.Class, retry = sqlClass(e.SQLState); retry {
			kind = effects.ErrRetryable
		}
	case body.Quota != nil:
		e.Class = ClassQuota
	case body.Code == "reauth_required":
		e.Class = ClassPermission
	case st == http.StatusTooManyRequests:
		e.Class, kind = ClassRateLimited, effects.ErrRetryable
	case st == http.StatusServiceUnavailable:
		e.Class, kind = ClassUnavailable, effects.ErrRetryable
	case st == http.StatusUnauthorized:
		// invalid_token: unknown, revoked or expired (docs/errors.md).
		e.Class, e.rejected = ClassAuth, body.Code == "invalid_token"
	case st == http.StatusForbidden:
		e.Class = ClassPermission
	case st == http.StatusNotFound:
		e.Class = ClassNotFound
	case st == http.StatusConflict:
		e.Class = ClassConflict
	case st == http.StatusGatewayTimeout:
		e.Class, kind = ClassTimeout, effects.ErrUnknownOutcome
	case st >= 500:
		// Xenibase may have acted before failing: reads and idempotent
		// writes retry, unsafe writes park.
		e.Class, kind = ClassServer, effects.ErrUnknownOutcome
	default:
		e.Class = ClassValidation // 400, 413, 422 and other refusals
	}
	if e.Message == "" {
		e.Message = strings.TrimSpace(string(he.Body))
		if len(e.Message) > 300 {
			e.Message = e.Message[:300]
		}
	}
	e.kind = kind
	return e
}

// dataAPIError is the data API's error body.
type dataAPIError struct {
	Error struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Details   map[string]any `json:"details"`
		RequestID string         `json:"request_id"`
	} `json:"error"`
}

// mapDataError classifies a data API refusal by its published code and
// status (docs/errors.md, the Data API table). Every 4xx is fatal: 409
// unique_violation and foreign_key_violation are conflicts, 422
// idempotency_key_reused too (the engine's key was used for another
// request, which must not happen), the rest validation, auth, permission
// or not found. 429 and 503 are retryable, waiting at least Retry-After,
// except plan_limit_reached, which Xenibase marks as not retryable (the
// month's requests are used up: quota). 504 statement_timeout and any
// other 5xx are unknown outcomes: the write may have committed, so an
// idempotent write retries with the same key, which replays or runs it.
func mapDataError(status int, body []byte, retryAfter time.Duration) error {
	var de dataAPIError
	_ = json.Unmarshal(body, &de)
	e := &Error{Status: status, Code: de.Error.Code, Message: de.Error.Message, RequestID: de.Error.RequestID, RetryAfter: retryAfter}
	if pg, ok := de.Error.Details["pg_code"].(string); ok {
		e.SQLState = pg
	}
	kind := effects.ErrFatal
	switch {
	case status == http.StatusTooManyRequests && e.Code == "plan_limit_reached":
		e.Class = ClassQuota
	case status == http.StatusTooManyRequests:
		e.Class, kind = ClassRateLimited, effects.ErrRetryable
	case status == http.StatusServiceUnavailable:
		e.Class, kind = ClassUnavailable, effects.ErrRetryable
	case status == http.StatusGatewayTimeout:
		e.Class, kind = ClassTimeout, effects.ErrUnknownOutcome
	case status >= 500:
		e.Class, kind = ClassServer, effects.ErrUnknownOutcome
	case status == http.StatusUnauthorized:
		e.Class = ClassAuth
	case status == http.StatusForbidden && e.Code == "read_only":
		e.Class = ClassConflict
	case status == http.StatusForbidden:
		e.Class = ClassPermission
	case status == http.StatusNotFound:
		e.Class = ClassNotFound
	case status == http.StatusConflict, e.Code == "idempotency_key_reused":
		e.Class = ClassConflict
	default:
		e.Class = ClassValidation // 400, 405, 413, 422 and other refusals
	}
	if e.Message == "" {
		e.Message = strings.TrimSpace(string(body))
		if len(e.Message) > 300 {
			e.Message = e.Message[:300]
		}
	}
	e.kind = kind
	return e
}
