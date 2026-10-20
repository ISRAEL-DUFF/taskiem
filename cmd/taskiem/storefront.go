package main

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"os"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// storefrontOrders builds the storefront's orders (docs/storefronts.md),
// or nil when the public host's key (TASKIEM_PUBLIC_KEY) is unset: then
// no order can arrive, and the storefront connector's actions fail plainly.
func storefrontOrders(cfg config, e *engine, log *slog.Logger) (*storefront.Orders, error) {
	raw := os.Getenv("TASKIEM_PUBLIC_KEY")
	if raw == "" {
		return nil, nil
	}
	master, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(master) != 32 {
		return nil, errors.New("TASKIEM_PUBLIC_KEY must be 32 bytes, base64 (openssl rand -base64 32)")
	}
	key, err := storefront.InboxKey(master)
	if err != nil {
		return nil, err
	}
	o := &storefront.Orders{Pool: e.pool, Vault: e.vault, InboxKey: key, Start: e.store.Start, Signal: e.store.DeliverSignalOnce, Logger: log.With("component", "storefront")}
	o.Payments = &storefront.PaystackPayments{Registry: e.registry, Vault: e.vault, Guard: &egress.Guard{Logger: log}}
	if len(cfg.Public.Domains) > 0 {
		o.PreviewDomain = cfg.Public.Domains[0]
	}
	if len(cfg.Messages.HashKey) == 32 {
		o.Messages = &sender.Queue{Vault: e.vault, HashKey: cfg.Messages.HashKey, Countries: cfg.Messages.Countries}
	} else {
		log.Warn("TASKIEM_MESSAGE_HASH_KEY is unset: storefront customers get no SMS or email")
	}
	return o, nil
}
