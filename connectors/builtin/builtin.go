// Package builtin registers the first-party connectors compiled into the
// binary (spec 6: in-process Go connectors).
package builtin

import (
	"github.com/israel-duff/taskiem/connectors/anchor"
	"github.com/israel-duff/taskiem/connectors/breet"
	"github.com/israel-duff/taskiem/connectors/dojah"
	"github.com/israel-duff/taskiem/connectors/flutterwave"
	"github.com/israel-duff/taskiem/connectors/interswitch"
	"github.com/israel-duff/taskiem/connectors/iswallet"
	"github.com/israel-duff/taskiem/connectors/lenco"
	"github.com/israel-duff/taskiem/connectors/moniepoint"
	"github.com/israel-duff/taskiem/connectors/mysql"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/connectors/postgres"
	"github.com/israel-duff/taskiem/connectors/s3"
	"github.com/israel-duff/taskiem/connectors/sftp"
	"github.com/israel-duff/taskiem/connectors/termii"
	"github.com/israel-duff/taskiem/engine/connector"
)

// Options override provider base URLs (sandboxes, tests).
type Options struct {
	PaystackURL, TermiiURL, DojahURL, IswalletURL string
	FlutterwaveURL, AnchorURL, LencoURL, BreetURL string
	MoniepointURL, InterswitchURL                 string
	S3URL                                         string
}

// Register adds every built-in connector to r.
func Register(r *connector.Registry, o Options) error {
	for _, c := range []*connector.Connector{
		paystack.New(paystack.Options{BaseURL: o.PaystackURL}),
		termii.New(termii.Options{BaseURL: o.TermiiURL}),
		dojah.New(dojah.Options{BaseURL: o.DojahURL}),
		iswallet.New(iswallet.Options{BaseURL: o.IswalletURL}),
		flutterwave.New(flutterwave.Options{BaseURL: o.FlutterwaveURL}),
		anchor.New(anchor.Options{BaseURL: o.AnchorURL}),
		lenco.New(lenco.Options{BaseURL: o.LencoURL}),
		breet.New(breet.Options{BaseURL: o.BreetURL}),
		moniepoint.New(moniepoint.Options{BaseURL: o.MoniepointURL}),
		interswitch.New(interswitch.Options{BaseURL: o.InterswitchURL}),
		postgres.New(),
		mysql.New(),
		s3.New(s3.Options{BaseURL: o.S3URL}),
		sftp.New(),
	} {
		if err := r.Register(c); err != nil {
			return err
		}
	}
	return nil
}
