package main

import (
	"log/slog"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// storefrontOrders builds the storefront's orders (docs/storefronts.md),
// or nil when the public host's key (TASKIEM_PUBLIC_KEY) is unset: then
// no order can arrive, and the storefront connector's actions fail plainly.
func storefrontOrders(cfg config, e *engine, log *slog.Logger) (*storefront.Orders, error) {
	// The same key the public host's forms are signed with (public.go).
	if len(cfg.Public.Key) == 0 {
		return nil, nil
	}
	key, err := storefront.InboxKey(cfg.Public.Key)
	if err != nil {
		return nil, err
	}
	o := &storefront.Orders{Pool: e.pool, Vault: e.vault, InboxKey: key, Start: e.store.Start, Signal: e.store.DeliverSignalOnce, Logger: log.With("component", "storefront")}
	o.Payments = &storefront.PaystackPayments{Registry: e.registry, Vault: e.vault, Guard: &egress.Guard{Logger: log}}
	stock := &storefront.XenibaseStock{Pool: o.Pool, Registry: e.registry, Vault: e.vault, Guard: &egress.Guard{Logger: log}}
	o.Stock, o.Live = stock, stock
	o.Cancel = e.store.CancelRun
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
