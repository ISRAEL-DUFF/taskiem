package sender

import (
	"context"
	"errors"
	"testing"
)

type codeProvider struct {
	name string
	err  error
	sent []string
}

func (p *codeProvider) Name() string { return p.name }

func (p *codeProvider) Send(_ context.Context, to, text string) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	p.sent = append(p.sent, to+" "+text)
	return "ref", nil
}

// A code goes through the first provider that certainly takes it; one that
// may have sent it is not followed by another; a number outside the
// countries goes nowhere.
func TestCodeSender(t *testing.T) {
	ctx := context.Background()
	down := &codeProvider{name: "termii", err: ErrNotSent}
	up := &codeProvider{name: "africastalking"}
	c := &CodeSender{Providers: []SMSProvider{down, up}}
	if err := c.SendCode(ctx, "+2348031230101", "Your Taskiem code is 123456."); err != nil || len(up.sent) != 1 {
		t.Fatalf("handed over: %v %v", err, up.sent)
	}
	unsure := &codeProvider{name: "termii", err: errors.New("timeout after the request was sent")}
	c = &CodeSender{Providers: []SMSProvider{unsure, up}}
	if err := c.SendCode(ctx, "+2348031230101", "x"); err == nil || len(up.sent) != 1 {
		t.Fatalf("sent twice: %v %v", err, up.sent)
	}
	if err := c.SendCode(ctx, "+15550001111", "x"); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("outside the countries: %v", err)
	}
}
