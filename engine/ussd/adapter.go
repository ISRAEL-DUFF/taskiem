package ussd

import (
	"errors"
	"net/http"
	"regexp"
)

// Request is one aggregator callback, whatever its wire format.
type Request struct {
	SessionID   string
	ServiceCode string
	Phone       string // the caller, in international format (+254...)
	Network     string // the operator's code, when sent
	// Input is everything typed so far joined with "*" (Africa's Talking's
	// text), or for an incremental adapter only the latest input.
	Input string
}

// Adapter speaks one aggregator's callback format. Africa's Talking's is
// in connectors/africastalking; others plug in the same way.
type Adapter interface {
	// Name is the {provider} in /channels/ussd/{tenant}/{provider}.
	Name() string
	// Parse reads a callback (body already read, at most MaxBody bytes).
	Parse(r *http.Request, body []byte) (Request, error)
	// Incremental is true when a callback carries only the latest input,
	// so the edge must keep the path in the session.
	Incremental() bool
	// Reply writes the answer: end false continues the session.
	Reply(w http.ResponseWriter, end bool, text string)
}

// MaxBody bounds a callback.
const MaxBody = 8 << 10

// ErrBadRequest is a callback that is not well formed.
var ErrBadRequest = errors.New("ussd: malformed callback")

var (
	phoneRe   = regexp.MustCompile(`^\+?[1-9][0-9]{6,14}$`)
	sessionRe = regexp.MustCompile(`^[A-Za-z0-9_.:\-]{1,128}$`)
)

// CheckRequest refuses callbacks whose fields are not what an aggregator
// sends: a session id of safe characters, an international number, a
// service code, and an input of at most 400 characters.
func CheckRequest(q Request) error {
	switch {
	case !sessionRe.MatchString(q.SessionID):
		return ErrBadRequest
	case !phoneRe.MatchString(q.Phone):
		return ErrBadRequest
	case !serviceCode.MatchString(q.ServiceCode):
		return ErrBadRequest
	case len(q.Input) > 400 || len(q.Network) > 16:
		return ErrBadRequest
	}
	return nil
}

// NormalPhone puts a number in +<digits> form.
func NormalPhone(p string) string {
	if p != "" && p[0] != '+' {
		return "+" + p
	}
	return p
}

// ValidServiceCode reports whether s looks like a USSD service code.
func ValidServiceCode(s string) bool { return serviceCode.MatchString(s) }
