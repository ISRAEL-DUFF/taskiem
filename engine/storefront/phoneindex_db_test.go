package storefront_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/phoneindex"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The phone index (docs/spec/taskiem-for-everyone.md §13) is written in
// the transaction that makes a storefront order or booking, and a chat
// order; it keeps the order, the business and the phone's hash, nothing
// else; erasing the customer, forgetting them at retention or deleting
// the order takes the row out.
func TestOrdersWritePhoneIndex(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	idx := &phoneindex.Index{Pool: w.env.DB.App, Key: bytes.Repeat([]byte{8}, 32), Vault: w.env.Vault}
	w.orders.PhoneIndex = idx
	ref := w.submit("order", "after_accept", 900000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	o := w.order(ref)
	var chat storefront.Order
	w.in(func(tx pgx.Tx) error {
		var err error
		chat, err = w.orders.PlaceChatTx(ctx, tx, w.env.Tenant, storefront.ChatOrder{Customer: storefront.Customer{Name: "Bola", Phone: "08031234567"},
			Lines: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 1, PriceKobo: 450000}}, Fulfilment: "pickup", Pay: storefront.ChatPayLater, By: "user:ada"})
		return err
	})
	want := idx.Hash("+2348031234567")
	rows := func() map[uuid.UUID][]byte {
		t.Helper()
		out := map[uuid.UUID][]byte{}
		r, err := w.env.DB.Admin.Query(ctx, `SELECT order_id, phone_hash FROM customer_phone_index WHERE tenant_id = $1`, w.env.Tenant)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		for r.Next() {
			var id uuid.UUID
			var h []byte
			if err := r.Scan(&id, &h); err != nil {
				t.Fatal(err)
			}
			out[id] = h
		}
		return out
	}
	got := rows()
	if len(got) != 2 || !bytes.Equal(got[o.ID], want) || !bytes.Equal(got[chat.ID], want) {
		t.Fatalf("the index: %v", got)
	}
	// Only the order, the business, the hash and when.
	var cols []string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT array_agg(column_name::text ORDER BY column_name) FROM information_schema.columns WHERE table_name = 'customer_phone_index'`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if len(cols) != 4 || cols[0] != "created_at" || cols[1] != "order_id" || cols[2] != "phone_hash" || cols[3] != "tenant_id" {
		t.Fatalf("the index's columns: %v", cols)
	}

	// Retention forgets the chat order's customer: its row goes.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'completed', updated_at = now() - interval '400 days' WHERE id = $1`, chat.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.orders.ForgetExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rows(); len(got) != 1 || got[o.ID] == nil {
		t.Fatalf("after retention: %v", got)
	}

	// Erasing the customer: their orders leave the index.
	if err := w.env.Vault.Erase(ctx, w.env.Tenant, o.CustomerID(), "user:ada"); err != nil {
		t.Fatal(err)
	}
	if got := rows(); len(got) != 0 {
		t.Fatalf("after erasure: %v", got)
	}

	// Deleting an order takes its row with it.
	w.in(func(tx pgx.Tx) error {
		_, err := w.orders.PlaceChatTx(ctx, tx, w.env.Tenant, storefront.ChatOrder{Customer: storefront.Customer{Name: "Ade", Phone: "+2348039999999"},
			Lines: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 1, PriceKobo: 450000}}, Fulfilment: "pickup", Pay: storefront.ChatPayLater, By: "user:ada"})
		return err
	})
	if got := rows(); len(got) != 1 {
		t.Fatalf("a new order: %v", got)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM storefront_orders WHERE kind = 'chat' AND forgotten_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if got := rows(); len(got) != 0 {
		t.Fatalf("after deleting the order: %v", got)
	}
}
