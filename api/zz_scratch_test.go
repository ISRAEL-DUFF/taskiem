package api_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/assistant"
	"github.com/israel-duff/taskiem/engine/db"
)

func TestZZScratch(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Shop", "o@shop.test")
	tenant := uuid.MustParse(tenantOf(t, owner))
	ctx := context.Background()
	err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := (&assistant.Guard{}).Decide(ctx, tx, w.env.Vault, tenant, assistant.Defaults(),
			assistant.Message{Channel: "whatsapp@1/main/prod", Dedup: "wamid.OLD", Sender: "2348031234567", SenderCategory: "phone", Private: true, Kind: "text", Text: "hello"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.env.DB.Admin.Exec(ctx, `UPDATE assistant_contacts SET last_inbound_at = now() - interval '2 days', handover_until = NULL`)
	if err != nil { t.Fatal(err) }
	var cust string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT subject FROM assistant_contacts`).Scan(&cust)
	st, body := owner.do("POST", "/v1/conversations/reply", map[string]any{"channel": "whatsapp@1/main/prod", "customer": cust, "text": "x"})
	var until any
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT handover_until FROM assistant_contacts`).Scan(&until)
	t.Logf("reply refused %d %v; handover_until now %v", st, body, until)

	// expiry
	_, err = w.env.DB.Admin.Exec(ctx, `INSERT INTO assistant_turns (id, tenant_id, channel, subject, role, at) VALUES ($1, $2, 'x/y/prod', 'other:abc', 'customer', now() - interval '40 days')`, uuid.New(), tenant)
	if err != nil { t.Fatal(err) }
	n, err := w.srv.ExpireConversations(ctx)
	t.Logf("expired %d err %v", n, err)
}
