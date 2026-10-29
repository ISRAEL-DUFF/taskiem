package main

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/invoicing"
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
	// Accepting an order lowers the stock as movements (docs/spec/stock.md).
	o.Moves = e.stock
	o.Cancel = e.store.CancelRun
	// A checkout payment the payment check finds is recorded in the money
	// log and matched to its order, as the provider's event would be.
	o.MoneyLog = e.moneyLog()
	if len(cfg.Public.Domains) > 0 {
		o.PreviewDomain = cfg.Public.Domains[0]
	}
	if len(cfg.Messages.HashKey) == 32 {
		o.Messages = &sender.Queue{Vault: e.vault, HashKey: cfg.Messages.HashKey, Countries: cfg.Messages.Countries, Allowance: e.smsAllowance}
	} else {
		log.Warn("TASKIEM_MESSAGE_HASH_KEY is unset: storefront customers get no SMS or email")
	}
	return o, nil
}

// wireOrderTransfers lets storefront orders be paid by bank transfer (N3):
// their account numbers are the invoices' accounts, issued on the form's
// rail; the invoices' request queue hands an order's request to the
// storefront, which checks the order, and its checker hands a payment it
// finds back to it.
func wireOrderTransfers(inv *invoicing.Invoices, o *storefront.Orders) {
	if inv == nil || o == nil {
		return
	}
	o.IssueTransfer = func(ctx context.Context, tenant, order uuid.UUID, connection string, amount int64, description string, payer map[string]any, minutes int) error {
		_, err := inv.IssueForOrder(ctx, tenant, order, invoicing.OrderTransfer{Connection: connection, AmountKobo: amount, Description: description, Payer: payer, Minutes: minutes})
		return err
	}
	inv.OrderRequested = o.TransferRequested
	inv.OrderReceived = func(ctx context.Context, tenant, order uuid.UUID, r invoicing.Received) error {
		return o.TransferReceived(ctx, tenant, order, r.PaymentID, r.Connection, r.AmountKobo, r.Account)
	}
}
