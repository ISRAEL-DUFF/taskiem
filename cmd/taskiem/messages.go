package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/sender"
)

// messagesConfig is the platform's own SMS and email for people outside a
// tenant (docs/messages.md): customers of storefronts, respondents of
// hosted forms.
type messagesConfig struct {
	// Termii (TASKIEM_SMS_TERMII_*): the first SMS provider when set.
	TermiiKey, TermiiSender, TermiiChannel, TermiiURL string
	// Africa's Talking (TASKIEM_SMS_AT_*): the fallback when set.
	ATUser, ATKey, ATSender, ATURL string
	// Rate caps sends per second to each provider (TASKIEM_SMS_RATE).
	Rate float64
	// Countries SMS may go to (TASKIEM_SMS_COUNTRIES, 234).
	Countries []string
	// From is the no-reply address emails come from (TASKIEM_MESSAGE_FROM,
	// default TASKIEM_ALERT_FROM).
	From string
	// HashKey keys the recipient hash the platform-wide limits count by
	// (TASKIEM_MESSAGE_HASH_KEY, 32 bytes base64).
	HashKey []byte
}

func messagesConfigFromEnv() (messagesConfig, error) {
	c := messagesConfig{
		TermiiKey: os.Getenv("TASKIEM_SMS_TERMII_API_KEY"), TermiiSender: os.Getenv("TASKIEM_SMS_TERMII_SENDER_ID"),
		TermiiChannel: envOr("TASKIEM_SMS_TERMII_CHANNEL", "dnd"), TermiiURL: envOr("TASKIEM_SMS_TERMII_URL", "https://api.ng.termii.com"),
		ATUser: os.Getenv("TASKIEM_SMS_AT_USERNAME"), ATKey: os.Getenv("TASKIEM_SMS_AT_API_KEY"), ATSender: os.Getenv("TASKIEM_SMS_AT_SENDER_ID"),
		ATURL: envOr("TASKIEM_SMS_AT_URL", "https://api.africastalking.com"),
		Rate:  10,
		From:  envOr("TASKIEM_MESSAGE_FROM", os.Getenv("TASKIEM_ALERT_FROM")),
	}
	if (c.TermiiKey == "") != (c.TermiiSender == "") {
		return c, errors.New("TASKIEM_SMS_TERMII_API_KEY and TASKIEM_SMS_TERMII_SENDER_ID go together")
	}
	if (c.ATUser == "") != (c.ATKey == "") {
		return c, errors.New("TASKIEM_SMS_AT_USERNAME and TASKIEM_SMS_AT_API_KEY go together")
	}
	if c.TermiiChannel != "dnd" && c.TermiiChannel != "generic" {
		return c, fmt.Errorf("TASKIEM_SMS_TERMII_CHANNEL must be dnd or generic, not %q", c.TermiiChannel)
	}
	if v := os.Getenv("TASKIEM_SMS_RATE"); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n <= 0 || n > 1000 {
			return c, fmt.Errorf("TASKIEM_SMS_RATE: %q is not a rate from 0 to 1000 a second", v)
		}
		c.Rate = n
	}
	for _, cc := range strings.Split(envOr("TASKIEM_SMS_COUNTRIES", "234"), ",") {
		if cc = strings.TrimPrefix(strings.TrimSpace(cc), "+"); cc != "" {
			if _, err := strconv.Atoi(cc); err != nil || len(cc) > 3 {
				return c, fmt.Errorf("TASKIEM_SMS_COUNTRIES: %q is not a calling code", cc)
			}
			c.Countries = append(c.Countries, cc)
		}
	}
	if v := os.Getenv("TASKIEM_MESSAGE_HASH_KEY"); v != "" {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(k) != 32 {
			return c, errors.New("TASKIEM_MESSAGE_HASH_KEY must be 32 bytes, base64 (openssl rand -base64 32)")
		}
		c.HashKey = k
	}
	return c, nil
}

// envOr is env, with an empty value taken as unset: a blank line in an
// env file means the default.
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// smsProviders are the configured SMS providers, Termii first.
func (c messagesConfig) smsProviders(log *slog.Logger) []sender.SMSProvider {
	var out []sender.SMSProvider
	if c.TermiiKey != "" {
		out = append(out, &sender.Termii{BaseURL: c.TermiiURL, APIKey: c.TermiiKey, SenderID: c.TermiiSender, Channel: c.TermiiChannel,
			HTTP: providerClient(c.TermiiURL, "platform_sms:termii", log)})
	}
	if c.ATKey != "" {
		out = append(out, &sender.AfricasTalking{BaseURL: c.ATURL, Username: c.ATUser, APIKey: c.ATKey, SenderID: c.ATSender,
			HTTP: providerClient(c.ATURL, "platform_sms:africastalking", log)})
	}
	return out
}

// providerClient reaches one provider's host through the egress guard. A
// loopback address (a fake provider in tests) is let through for this
// purpose only, as for connectors.
func providerClient(raw string, purpose string, log *slog.Logger) *http.Client {
	u, _ := url.Parse(raw)
	g := &egress.Guard{Logger: log}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && ip.IsLoopback() {
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
		g.Loopback = map[string]string{purpose: port}
		log.Warn("an SMS provider points at this machine: for tests only", "url", raw)
	}
	return g.Client(egress.Policy{Tenant: "platform", Hosts: []string{u.Hostname()}, Purpose: purpose}, 20*time.Second)
}

// senderWorker is the outbox worker, or nil when nothing can be sent.
func (c config) senderWorker(e *engine, mailer alerts.Mailer, log *slog.Logger) *sender.Worker {
	sms := c.Messages.smsProviders(log)
	if len(sms) == 0 && (mailer == nil || c.Messages.From == "") {
		return nil
	}
	return &sender.Worker{Pool: e.pool, Vault: e.vault, SMS: sms, Rate: rate.Limit(c.Messages.Rate), Mailer: mailer, From: c.Messages.From,
		Logger: log.With("component", "sender")}
}

// messagesCmd is `taskiem messages test`: send one message now, through
// the platform's providers and no outbox, to check the configuration.
func messagesCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "test" {
		return errors.New("usage: taskiem messages test (--sms NUMBER | --email ADDRESS) [--provider termii|africastalking]")
	}
	fs := flag.NewFlagSet("messages test", flag.ContinueOnError)
	to := fs.String("sms", "", "a phone number to text")
	email := fs.String("email", "", "an email address")
	only := fs.String("provider", "", "use only this SMS provider")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	c, err := messagesConfigFromEnv()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const text = "Taskiem: this is a test message. No reply is needed."
	switch {
	case *to != "":
		num, err := sender.NormalisePhone(*to, c.Countries)
		if err != nil {
			return err
		}
		providers := c.smsProviders(log)
		if len(providers) == 0 {
			return errors.New("no SMS provider is configured (TASKIEM_SMS_TERMII_* or TASKIEM_SMS_AT_*)")
		}
		var errs []error
		for _, p := range providers {
			if *only != "" && p.Name() != *only {
				continue
			}
			ref, err := p.Send(ctx, num, text)
			if err != nil {
				errs = append(errs, err) // each provider names itself
				continue
			}
			fmt.Fprintf(stdout, "sent by %s, reference %s\n", p.Name(), ref)
			return nil
		}
		if len(errs) == 0 {
			return fmt.Errorf("no SMS provider named %q is configured", *only)
		}
		return errors.Join(errs...)
	case *email != "":
		addr, err := sender.NormaliseEmail(*email)
		if err != nil {
			return err
		}
		smtpURL := os.Getenv("TASKIEM_SMTP_URL")
		if smtpURL == "" || c.From == "" {
			return errors.New("email needs TASKIEM_SMTP_URL and TASKIEM_MESSAGE_FROM (or TASKIEM_ALERT_FROM)")
		}
		m, err := alerts.NewSMTPMailer(smtpURL)
		if err != nil {
			return err
		}
		if err := m.Send(ctx, c.From, []string{addr}, sender.ProbeEmail(c.From, addr, text, time.Now())); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "sent to %s\n", addr)
		return nil
	}
	return errors.New("give --sms NUMBER or --email ADDRESS")
}
