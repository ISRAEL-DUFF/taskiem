package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/templates"
)

const payListFlow = `{"schema":"wd/v1","id":"wf_payList","version":1,"name":"Pay a list","trigger":{"type":"manual"},
  "steps":[{"id":"ok","type":"approval","config":{"role":"owner","subject":{
    "people":2,"accounts":"=trigger.body.accounts"}}}]}`

// The first-payment warning on approvals that pay accounts (H7,
// g-pay-first): a supplier's payment approval carries the account and its
// bank, and a list's approval its accounts, so the warning shows when any
// was never paid; the approver sees the accounts masked.
func TestApprovalListSaysFirstPayment(t *testing.T) {
	// The templates' subjects carry the accounts.
	for name, want := range map[string]string{"supplier-payment-approval": "account_number", "pay-riders-weekly": "accounts", "pay-a-list": "accounts"} {
		tpl, ok := templates.Default().Get(name)
		if !ok {
			t.Fatalf("no %s", name)
		}
		raw, err := tpl.Instantiate(tpl.Examples(), "")
		if err != nil {
			t.Fatal(err)
		}
		var def struct {
			Steps []json.RawMessage `json:"steps"`
		}
		if err := json.Unmarshal(raw, &def); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, s := range def.Steps {
			var st struct {
				Type   string `json:"type"`
				Config struct {
					Subject map[string]any `json:"subject"`
				} `json:"config"`
			}
			_ = json.Unmarshal(s, &st)
			if st.Type == "approval" {
				_, found = st.Config.Subject[want]
			}
		}
		if !found {
			t.Errorf("%s: its approval's subject has no %s", name, want)
		}
	}

	w := newWorld(t)
	owner := w.tenant(t, "Kunle Buyers", "owner@list.test")
	tenant := uuid.MustParse(tenantOf(t, owner))
	wf := publishFlow(t, owner, payListFlow)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"accounts": []map[string]any{
		{"account_number": "0123456789", "bank_code": "058"}, {"account_number": "0987654321", "bank_code": "044"}}}})
	w.env.Drain(t)
	first := func() map[string]any {
		t.Helper()
		list := owner.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any)
		if len(list) != 1 {
			t.Fatalf("approvals: %v", list)
		}
		return list[0].(map[string]any)
	}
	a := first()
	if a["first_payment"] != true || a["subject"].(map[string]any)["accounts"] != float64(2) {
		t.Fatalf("never paid: %v", a)
	}
	for _, acct := range [][2]string{{"0123456789", "058"}, {"0987654321", "044"}} {
		if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			return moneylog.RememberAccount(context.Background(), tx, w.env.Vault, tenant, acct[0], acct[1], time.Now())
		}); err != nil {
			t.Fatal(err)
		}
	}
	if a := first(); a["first_payment"] != nil {
		t.Fatalf("all paid before: %v", a)
	}

	// One account is shown masked.
	w2 := publishFlow(t, owner, payFlow)
	owner.must(201, "POST", "/v1/workflows/"+w2+"/runs", map[string]any{"input": map[string]any{"amount": 4500000, "account": "0555555555"}})
	w.env.Drain(t)
	for _, x := range owner.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any) {
		s := x.(map[string]any)["subject"].(map[string]any)
		if n, ok := s["account_number"]; ok && n != "••5555" {
			t.Fatalf("not masked: %v", s)
		}
	}
}
