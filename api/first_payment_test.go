package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

const payFlow = `{"schema":"wd/v1","id":"wf_paySupplier","version":1,"name":"Pay a supplier","trigger":{"type":"manual"},
  "steps":[{"id":"ok","type":"approval","config":{"role":"owner","subject":{
    "amount_kobo":"=trigger.body.amount","account_name":"Kunle Supplies","account_number":"=trigger.body.account","bank_code":"058"}}}]}`

// An approval to pay an account the business has never paid says so
// (docs/money-log.md#accounts-paid); once the account has been paid, it
// does not.
func TestApprovalSaysFirstPayment(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Kunle Buyers", "owner@kunle.test")
	tenant := uuid.MustParse(tenantOf(t, owner))
	wf := publishFlow(t, owner, payFlow)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 4500000, "account": "0123456789"}})
	w.env.Drain(t)

	first := func() any {
		t.Helper()
		list := owner.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any)
		if len(list) != 1 {
			t.Fatalf("approvals: %v", list)
		}
		a := list[0].(map[string]any)
		if a["environment"] != "prod" {
			t.Fatalf("environment %v", a["environment"])
		}
		return a["first_payment"]
	}
	if got := first(); got != true {
		t.Fatalf("never paid: first_payment %v", got)
	}

	// Paid at another bank is another account.
	remember := func(bank string) {
		t.Helper()
		if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			return moneylog.RememberAccount(context.Background(), tx, w.env.Vault, tenant, "0123456789", bank, time.Now())
		}); err != nil {
			t.Fatal(err)
		}
	}
	remember("044")
	if got := first(); got != true {
		t.Fatalf("paid at another bank: first_payment %v", got)
	}
	remember("058")
	if got := first(); got != nil {
		t.Errorf("paid before: first_payment %v", got)
	}
}
