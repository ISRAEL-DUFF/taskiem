// Package builtin registers the first-party connectors compiled into the
// binary (spec 6: in-process Go connectors).
package builtin

import (
	"github.com/israel-duff/taskiem/connectors/africastalking"
	"github.com/israel-duff/taskiem/connectors/anchor"
	"github.com/israel-duff/taskiem/connectors/breet"
	"github.com/israel-duff/taskiem/connectors/dojah"
	"github.com/israel-duff/taskiem/connectors/flutterwave"
	"github.com/israel-duff/taskiem/connectors/gmail"
	"github.com/israel-duff/taskiem/connectors/googlesheets"
	"github.com/israel-duff/taskiem/connectors/interswitch"
	"github.com/israel-duff/taskiem/connectors/iswallet"
	"github.com/israel-duff/taskiem/connectors/lenco"
	"github.com/israel-duff/taskiem/connectors/moniepoint"
	"github.com/israel-duff/taskiem/connectors/mono"
	"github.com/israel-duff/taskiem/connectors/mysql"
	"github.com/israel-duff/taskiem/connectors/opay"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/connectors/postgres"
	"github.com/israel-duff/taskiem/connectors/prembly"
	"github.com/israel-duff/taskiem/connectors/remita"
	"github.com/israel-duff/taskiem/connectors/s3"
	"github.com/israel-duff/taskiem/connectors/sftp"
	"github.com/israel-duff/taskiem/connectors/slack"
	"github.com/israel-duff/taskiem/connectors/telegram"
	"github.com/israel-duff/taskiem/connectors/termii"
	"github.com/israel-duff/taskiem/connectors/whatsapp"
	"github.com/israel-duff/taskiem/connectors/youverify"
	"github.com/israel-duff/taskiem/engine/connector"
)

// Options override provider base URLs (sandboxes, tests).
type Options struct {
	PaystackURL, TermiiURL, DojahURL, IswalletURL string
	FlutterwaveURL, AnchorURL, LencoURL, BreetURL string
	MoniepointURL, InterswitchURL                 string
	S3URL                                         string
	AfricasTalkingURL, TelegramURL, WhatsAppURL   string
	SlackURL, GmailURL, GooglesheetsURL           string
	OpayURL, RemitaURL                            string
	MonoURL, PremblyURL, YouverifyURL             string
	// GoogleTokenURL overrides Google's OAuth token endpoint (tests).
	GoogleTokenURL string
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
		africastalking.New(africastalking.Options{BaseURL: o.AfricasTalkingURL}),
		telegram.New(telegram.Options{BaseURL: o.TelegramURL}),
		whatsapp.New(whatsapp.Options{BaseURL: o.WhatsAppURL}),
		slack.New(slack.Options{BaseURL: o.SlackURL}),
		gmail.New(gmail.Options{BaseURL: o.GmailURL, TokenURL: o.GoogleTokenURL}),
		googlesheets.New(googlesheets.Options{BaseURL: o.GooglesheetsURL, TokenURL: o.GoogleTokenURL}),
		opay.New(opay.Options{BaseURL: o.OpayURL}),
		remita.New(remita.Options{BaseURL: o.RemitaURL}),
		mono.New(mono.Options{BaseURL: o.MonoURL}),
		prembly.New(prembly.Options{BaseURL: o.PremblyURL}),
		youverify.New(youverify.Options{BaseURL: o.YouverifyURL}),
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
