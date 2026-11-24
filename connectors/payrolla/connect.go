package payrolla

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// Client is Payrolla's Connect API as Taskiem's own screens use it (P4b;
// decision 0074): typed calls on one connection, a key or the tokens of a
// connection made in one step, refreshed under the connection's lock.
// Answers are unwrapped from {"data": …}; refusals are *Error with
// Payrolla's code. Every write carries an Idempotency-Key and the person
// acting (Write).
//
// P4b-1 uses the company and the approvers (list, link, confirm, unlink).
// The later slices add employees, runs, confirmations, bank changes,
// payslips, funding, settings and remittances here, the same way.
type Client struct {
	c   *client
	req connector.Request
	// wait sleeps between a write's attempts (tests: none).
	wait func(context.Context, time.Duration) error
}

// NewClient is a Client for one connection of conn (the payrolla@1
// connector), with its credentials as the vault hands them out (with
// their mode), an egress-guarded HTTP client and how to store refreshed
// credentials (the vault's RotateCredentials; nil for a key connection).
func NewClient(conn *connector.Connector, creds map[string]string, hc *http.Client, rotate func(context.Context, connector.RotateFunc) (map[string]string, error)) *Client {
	return &Client{c: &client{m: conn.Manifest, now: time.Now}, req: connector.Request{Credentials: creds, HTTP: hc, Rotate: rotate}, wait: sleep}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// OAuth says whether the connection was made in one step.
func (cl *Client) OAuth() bool { return strings.TrimSpace(cl.req.Credentials["api_key"]) == "" }

// ActingUser is X-Acting-User for a Taskiem person: their own account's
// id, the same in every business and never reused (decision 0068;
// payrolla-reference/08-taskiem-answers.md, section 5).
func ActingUser(user uuid.UUID) string { return "tsk_user_" + user.String() }

// Write is who makes a change and the key that makes it once: an
// Idempotency-Key fixed for one submission (reused only to retry it; each
// attempt at a code gets a new one), the person's ActingUser and their
// name as the business knows them.
type Write struct {
	Key  string
	User uuid.UUID
	Name string
}

func (w Write) headers() (map[string]string, error) {
	if strings.TrimSpace(w.Key) == "" || w.User == uuid.Nil {
		return nil, fmt.Errorf("payrolla: a write needs its idempotency key and the person acting: %w", effects.ErrFatal)
	}
	name := strings.TrimSpace(w.Name)
	if len(name) > 200 {
		name = name[:200]
	}
	return map[string]string{"Idempotency-Key": w.Key, "X-Acting-User": ActingUser(w.User), "X-Acting-User-Name": name}, nil
}

// ErrBug is a refusal that means Taskiem sent something wrong
// (IDEMPOTENCY_KEY_REUSED, UNKNOWN_FIELD): logged, and the person told
// "Something went wrong on our side".
var ErrBug = errors.New("payrolla: Taskiem sent Payrolla something it does not accept")

// writeAttempts bounds how often one write is sent with its key.
const writeAttempts = 3

// write sends a change with its headers. A timeout, a 5xx, a 429 or
// IDEMPOTENCY_IN_PROGRESS is sent again with the same key, so Payrolla
// answers once (it replays the first answer for 24 hours).
func (cl *Client) write(ctx context.Context, method, path string, w Write, body, out any) error {
	h, err := w.headers()
	if err != nil {
		return err
	}
	for attempt := 1; ; attempt++ {
		err = cl.c.do(ctx, cl.req, method, path, h, body, out)
		if err == nil || attempt >= writeAttempts || !retryWrite(err) {
			break
		}
		d := 500 * time.Millisecond * time.Duration(attempt)
		var he *connector.HTTPError
		if errors.As(err, &he) && he.RetryAfter > 0 {
			d = min(he.RetryAfter, 5*time.Second)
		}
		if werr := cl.wait(ctx, d); werr != nil {
			return err
		}
	}
	if c := Code(err); c == "IDEMPOTENCY_KEY_REUSED" || c == "UNKNOWN_FIELD" {
		return fmt.Errorf("%w: %w", ErrBug, err)
	}
	return err
}

func retryWrite(err error) bool {
	if Code(err) == "IDEMPOTENCY_IN_PROGRESS" {
		return true
	}
	if errors.Is(err, connector.ErrCredentialsRejected) {
		return false
	}
	return errors.Is(err, effects.ErrRetryable) || errors.Is(err, effects.ErrUnknownOutcome)
}

// Code is Payrolla's error code in err ("" when Payrolla explained
// nothing).
func Code(err error) string {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// Ended says whether err means the connection has ended: a revoked key, a
// refused refresh token, a token Payrolla no longer takes.
func Ended(err error) bool {
	return errors.Is(err, ErrConnectionEnded) || errors.Is(err, connector.ErrCredentialsRejected)
}

// CompanyView is /connect/company: the company and the connection making
// the call (a key, or Taskiem's connection made in one step).
type CompanyView struct {
	Company struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		PayFrequency string `json:"pay_frequency"`
	} `json:"company"`
	Key struct {
		ID     string   `json:"id"`
		Name   string   `json:"name"`
		Kind   string   `json:"kind"`
		Scopes []string `json:"scopes"`
	} `json:"key"`
}

// Company is the company and what the connection may do.
func (cl *Client) Company(ctx context.Context) (CompanyView, error) {
	var v CompanyView
	err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/company", nil, nil, &v)
	return v, err
}

// Approver is someone in the company who may approve, lock or pay
// payroll (Payrolla's Approver), and the Taskiem person linked to them.
type Approver struct {
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	CanApprove bool   `json:"can_approve"`
	CanPay     bool   `json:"can_pay"`
	CanConfirm bool   `json:"can_confirm"`
	// LinkedAs is the ActingUser of the Taskiem person linked to them.
	LinkedAs    string `json:"linked_as,omitempty"`
	LinkedSince string `json:"linked_since,omitempty"`
}

// LinkedUser is the Taskiem person an approver is linked to, or Nil.
func (a Approver) LinkedUser() uuid.UUID {
	id, err := uuid.Parse(strings.TrimPrefix(a.LinkedAs, "tsk_user_"))
	if err != nil || !strings.HasPrefix(a.LinkedAs, "tsk_user_") {
		return uuid.Nil
	}
	return id
}

// Challenge is a code on its way to a person's own factor (or, with an
// authenticator, ready to type).
type Challenge struct {
	ID              string `json:"challenge_id"`
	Channel         string `json:"channel"` // TOTP, SMS or EMAIL
	DestinationHint string `json:"destination_hint,omitempty"`
	ExpiresAt       string `json:"expires_at"`
}

// Approvers is who may approve or pay, and whom Taskiem's people are
// linked to (payroll:approve).
func (cl *Client) Approvers(ctx context.Context) ([]Approver, error) {
	var out struct {
		Approvers []Approver `json:"approvers"`
	}
	if err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/approvers", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Approvers, nil
}

// StartLink asks Payrolla to send the Payrolla user a code that links the
// acting person to them.
func (cl *Client) StartLink(ctx context.Context, user string, w Write) (Challenge, error) {
	var out struct {
		Challenge Challenge `json:"challenge"`
	}
	err := cl.write(ctx, http.MethodPost, "/connect/approvers/"+url.PathEscape(user)+"/link", w, map[string]any{}, &out)
	return out.Challenge, err
}

// ConfirmLink links the acting person with the code the user received.
// The code is sent once and kept nowhere.
func (cl *Client) ConfirmLink(ctx context.Context, user, challenge, code string, w Write) (Approver, error) {
	var out struct {
		Approver Approver `json:"approver"`
	}
	err := cl.write(ctx, http.MethodPost, "/connect/approvers/"+url.PathEscape(user)+"/link/confirm", w, map[string]any{"challenge_id": challenge, "code": code}, &out)
	return out.Approver, err
}

// Unlink removes a Payrolla user's link to Taskiem.
func (cl *Client) Unlink(ctx context.Context, user string, w Write) error {
	err := cl.write(ctx, http.MethodDelete, "/connect/approvers/"+url.PathEscape(user)+"/link", w, nil, nil)
	if errors.Is(err, connector.ErrNotFound) {
		return nil
	}
	return err
}

// Event is a delivery's envelope (the signed body).
type Event struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	CreatedAt  string          `json:"created_at"`
	APIVersion string          `json:"api_version"`
	Data       json.RawMessage `json:"data"`
}

// Disconnected reads company.disconnected: Payrolla's id for the
// connection that ended and why (revoked_by_owner, revoked_by_app). ok is
// false for any other event.
func Disconnected(body []byte) (keyID, reason string, ok bool) {
	var e Event
	if json.Unmarshal(body, &e) != nil || e.Type != "company.disconnected" {
		return "", "", false
	}
	var d struct {
		KeyID  string `json:"key_id"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(e.Data, &d)
	return d.KeyID, d.Reason, true
}
