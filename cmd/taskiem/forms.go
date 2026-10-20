package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/sender"
)

// formsService builds hosted forms' api side (docs/forms.md), or nil when
// the public host's key (TASKIEM_PUBLIC_KEY) is unset: then no form can
// be submitted and the form connector's actions fail plainly. Files need
// a malware scanner: TASKIEM_CLAMD_ADDR (host:port of a clamd the
// operator runs); without it forms that ask for files cannot be deployed.
func formsService(cfg config, e *engine, log *slog.Logger) (*forms.Service, error) {
	if len(cfg.Public.Key) == 0 {
		return nil, nil
	}
	keys, err := forms.NewKeys(cfg.Public.Key)
	if err != nil {
		return nil, err
	}
	s := &forms.Service{Pool: e.pool, Vault: e.vault, Keys: keys, Start: e.store.Start, Signal: e.store.DeliverSignalOnce,
		FormHost: formHost(cfg), AppURL: cfg.PublicURL, Logger: log.With("component", "forms")}
	s.Files = &media.Service{Credentials: e.tables().Credentials, Guard: &egress.Guard{Logger: log}, Logger: log}
	if addr := strings.TrimSpace(os.Getenv("TASKIEM_CLAMD_ADDR")); addr != "" {
		s.Scanner = &forms.ClamdScanner{Addr: addr}
	}
	if len(cfg.Messages.HashKey) == 32 {
		s.Messages = &sender.Queue{Vault: e.vault, HashKey: cfg.Messages.HashKey, Countries: cfg.Messages.Countries, Allowance: e.smsAllowance}
	} else {
		log.Warn("TASKIEM_MESSAGE_HASH_KEY is unset: form steps' links and codes are not sent")
	}
	return s, nil
}

// formHost is the form host's name: TASKIEM_PUBLIC_FORM_HOST, or forms.
// under the first public domain.
func formHost(cfg config) string {
	if h := strings.TrimSpace(os.Getenv("TASKIEM_PUBLIC_FORM_HOST")); h != "" {
		return strings.ToLower(h)
	}
	if len(cfg.Public.Domains) > 0 {
		return "forms." + strings.ToLower(cfg.Public.Domains[0])
	}
	return ""
}
