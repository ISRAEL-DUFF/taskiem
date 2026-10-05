// Package builtin registers the first-party connectors compiled into the
// binary (spec 6: in-process Go connectors).
package builtin

import (
	"github.com/israel-duff/taskiem/connectors/dojah"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/connectors/postgres"
	"github.com/israel-duff/taskiem/connectors/termii"
	"github.com/israel-duff/taskiem/engine/connector"
)

// Options override provider base URLs (sandboxes, tests).
type Options struct {
	PaystackURL, TermiiURL, DojahURL string
}

// Register adds every built-in connector to r.
func Register(r *connector.Registry, o Options) error {
	for _, c := range []*connector.Connector{
		paystack.New(paystack.Options{BaseURL: o.PaystackURL}),
		termii.New(termii.Options{BaseURL: o.TermiiURL}),
		dojah.New(dojah.Options{BaseURL: o.DojahURL}),
		postgres.New(),
	} {
		if err := r.Register(c); err != nil {
			return err
		}
	}
	return nil
}
