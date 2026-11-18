package sender

import (
	"context"
	"errors"
	"fmt"
)

// CodeSender sends a person's own sign-in code by SMS, at once and through
// no outbox: a code is for a person, not a business, so it has no tenant
// to queue it under, and a code that waits is no use. The providers are
// tried in order; one that certainly did not send (ErrNotSent,
// ErrProvider) hands over to the next. A code that may have gone is never
// sent again: the person asks for a new one.
type CodeSender struct {
	Providers []SMSProvider
	// Countries are the calling codes SMS may go to (default 234).
	Countries []string
}

// ErrNoRoute is a number SMS cannot go to from here.
var ErrNoRoute = errors.New("sender: SMS cannot go to that number")

// SendCode texts one code message to an E.164 number.
func (c *CodeSender) SendCode(ctx context.Context, number, text string) error {
	countries := c.Countries
	if len(countries) == 0 {
		countries = []string{"234"}
	}
	to, err := NormalisePhone(number, countries)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNoRoute, err)
	}
	last := ErrNotSent
	for _, p := range c.Providers {
		_, err := p.Send(ctx, to, text)
		if err == nil {
			return nil
		}
		last = fmt.Errorf("%s: %w", p.Name(), err)
		if !errors.Is(err, ErrNotSent) && !errors.Is(err, ErrProvider) {
			return last
		}
	}
	return last
}
