package ingest_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/runtime"
)

// Erasing a customer erases what they said in the runs their messages
// started (their words are sealed under them), and a customer erased who
// writes again is answered afresh (decision 0062 P2).
func TestCustomerGuardErasure(t *testing.T) {
	g := newGuardWorld(t, `true`, "")
	const ada = "2348031234567"
	g.send(t, ada, text("My address is 12 Admiralty Way"))
	g.Drain(t)
	subject, err := g.Vault.SubjectFor(ctx, g.Tenant, "phone", ada)
	if err != nil {
		t.Fatal(err)
	}
	var run uuid.UUID
	var customer string
	if err := g.DB.Admin.QueryRow(ctx, `SELECT id, customer_subject FROM runs WHERE workflow_id = $1`, g.wf).Scan(&run, &customer); err != nil || customer != subject {
		t.Fatalf("customer %q, want %q (%v)", customer, subject, err)
	}
	var history string
	if err := g.DB.Admin.QueryRow(ctx, `SELECT string_agg(payload::text, ' ') FROM run_events WHERE run_id = $1`, run).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(history, "Admiralty") || strings.Contains(history, ada) {
		t.Fatal("the message is in the clear")
	}
	if !strings.Contains(history, `"subject": "`+subject+`"`) && !strings.Contains(history, `"subject":"`+subject+`"`) {
		t.Fatal("nothing is sealed under the customer")
	}
	if strings.Contains(history, `"other:`) {
		t.Fatal("the customer's words are sealed under a subject of their own, not the customer's")
	}
	if err := g.Vault.Erase(ctx, g.Tenant, subject, "test"); err != nil {
		t.Fatal(err)
	}
	opened, err := g.Store.OpenedHistory(ctx, runtime.RunRef{TenantID: g.Tenant, ID: run})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(opened), "Admiralty") {
		t.Fatal("the customer's words outlived their erasure")
	}
	// They write again: a new key, a new run, read as it should be.
	if s := g.send(t, ada, text("Hello again")); s["outcome"] != "answer" {
		t.Fatalf("after erasure: %v", s)
	}
	if out := g.lastOutput(t); !strings.HasPrefix(out, "answer||") {
		t.Fatalf("after erasure: %s", out)
	}
}
