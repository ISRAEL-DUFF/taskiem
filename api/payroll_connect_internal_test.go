package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/payrolla"
)

// Each of Payrolla's refusals is said in plain words, never its code.
func TestPayrollaSaysInPlainWords(t *testing.T) {
	s := &Server{Logger: slog.New(slog.DiscardHandler)}
	owner := &Principal{UserID: uuid.New(), Roles: []string{"owner"}}
	staff := &Principal{UserID: uuid.New(), Roles: []string{"payroll"}}
	cases := []struct {
		code  string
		p     *Principal
		words string
	}{
		{"APPROVER_NOT_LINKED", staff, "Link your Payrolla account first. It takes one code."},
		{"PAYMENT_CONFIRMATION_NOT_SET_UP", staff, "Payrolla has nowhere to send you a code yet. Ask the owner to add your phone in Payrolla."},
		{"CODE_INVALID", staff, "That code isn't right. Check it and type it again."},
		{"CODE_EXPIRED", staff, "That code has expired."},
		{"CHALLENGE_NOT_FOUND", staff, "That code has expired."},
		{"CODE_LOCKED", staff, "Too many wrong tries for that code."},
		{"CODE_USED", staff, "That code was already used."},
		{"TOO_MANY_CODES", staff, "Payrolla has sent you too many codes. Try again in 10 minutes."},
		{"ALREADY_LINKED", staff, "You're linked to another Payrolla account. Unlink it first."},
		{"NOT_AN_APPROVER", staff, "In Payrolla you can't approve payroll. Ask the owner to make you an Approver in Payrolla."},
		{"SCOPE_REQUIRED", owner, "Payrolla hasn't allowed Taskiem to do this. Reconnect Payrolla and allow Approve and pay."},
		{"SCOPE_REQUIRED", staff, "Ask the owner to allow this in Payrolla."},
	}
	for _, c := range cases {
		err := s.payrollaSays(context.Background(), c.p, nil, fmt.Errorf("wrapped: %w", &payrolla.Error{Status: 403, Code: c.code, Message: "x"}))
		var pr payRefused
		if !errors.As(err, &pr) || pr.msg != c.words || strings.Contains(pr.msg, c.code) {
			t.Errorf("%s: %v", c.code, err)
		}
	}
	// Taskiem's own mistakes are not the person's to read.
	err := s.payrollaSays(context.Background(), staff, nil, fmt.Errorf("%w: %w", payrolla.ErrBug, &payrolla.Error{Code: "IDEMPOTENCY_KEY_REUSED"}))
	var pr payRefused
	if !errors.As(err, &pr) || pr.msg != "Something went wrong on our side. Try again in a minute." {
		t.Errorf("a bug: %v", err)
	}
	// An ended connection: the owner reconnects; others are told the owner knows.
	for p, words := range map[*Principal]string{owner: "Payrolla is no longer connected. Reconnect Payrolla.", staff: "Payroll isn't connected right now. The owner has been told."} {
		err := s.payrollaSays(context.Background(), p, nil, errPayrollaNotConnected)
		if !errors.As(err, &pr) || pr.msg != words || pr.code != "reconnect" {
			t.Errorf("ended: %v", err)
		}
	}
}
