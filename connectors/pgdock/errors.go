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

// Error classes (plan item P1-T6). PGDock publishes its error shape
// ({code, message}, with sql_error carrying a SQLSTATE) but not yet the
// list of codes (P1-G4), so a code is mapped only where the OpenAPI file
// names it, a SQLSTATE where one is present, and everything else by HTTP
// status, conservatively: only refusals that prove nothing was done
// (429, 503, and SQLSTATEs that mean the statement did not run) are
// retryable.
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

// Error is a PGDock refusal, classified. It wraps the effects sentinel the
// engine acts on, and connector.ErrNotFound for a 404.
type Error struct {
	Status     int
	Code       string // PGDock's error code
	Message    string
	SQLState   string
	Class      string
	RetryAfter time.Duration
	kind       error
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
	return b.String()
}

func (e *Error) Unwrap() []error {
	if e.Class == ClassNotFound {
		return []error{e.kind, connector.ErrNotFound}
	}
	return []error{e.kind}
}

// RetryAfterDelay is PGDock's Retry-After, honoured by the engine.
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
	case state == "40001", state == "40P01": // serialization failure, deadlock: nothing committed
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
		e.Class = ClassAuth
	case st == http.StatusForbidden:
		e.Class = ClassPermission
	case st == http.StatusNotFound:
		e.Class = ClassNotFound
	case st == http.StatusConflict:
		e.Class = ClassConflict
	case st == http.StatusGatewayTimeout:
		e.Class, kind = ClassTimeout, effects.ErrUnknownOutcome
	case st >= 500:
		// PGDock may have acted before failing: reads and idempotent
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
