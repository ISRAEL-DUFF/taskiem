package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/catalogue"
	"github.com/israel-duff/taskiem/engine/catalogue/cataloguetest"
	"github.com/israel-duff/taskiem/engine/db"
)

const catalogueFlow = `{"schema":"wd/v1","id":"wf_pay","version":1,"name":"pay","trigger":{"type":"manual"},
  "steps":[{"id":"pay","type":"connector","connector":"p_acme_ledger@1","action":"create_payment",
    "connection":"main","input":{"amount":"=trigger.body.amount","account_number":"=trigger.body.account_number"}}]}`

// fullChecklist confirms every review checklist item (engine/catalogue).
const fullChecklist = `{"identity": true, "classes": true, "hosts": true, "pii": true, "credentials": true, "conformance": true, "licence": true, "docs": true}`

// TestCatalogueSubmitReviewInstall takes a connector from a publisher
// through the automated checks, a four-eyes review and publication, to a
// second tenant that installs it, pays through it (drift monitored),
// upgrades with consent, and is alerted when it is revoked.
func TestCatalogueSubmitReviewInstall(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	paid := map[string]bool{}
	status := "pending"
	ledger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		ref, _ := b["reference"].(string)
		if r.Header.Get("Authorization") != "Bearer ledger-key" || r.URL.Path != "/v1/payments" || !strings.HasPrefix(ref, "tsk_") || paid[ref] {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		paid[ref] = true
		_, _ = w.Write([]byte(`{"id":"pay_7","reference":"` + ref + `","status":"` + status + `"}`))
	}))
	defer ledger.Close()
	w := newWorld(t)
	w.srv.Catalogue = &catalogue.Checker{Resolver: cataloguetest.Public}
	w.env.Connectors.BaseURLs = map[string]string{"p_acme_ledger": ledger.URL}
	pool := w.env.Store.Pool
	acme := w.tenant(t, "Acme", "owner@acme.test")
	globex := w.tenant(t, "Globex", "owner@globex.test")
	acmeID, globexID := uuid.MustParse(tenantOf(t, acme)), uuid.MustParse(tenantOf(t, globex))
	key, pubB64 := cataloguetest.Key()

	// A namespace is asked for, then verified by an operator; until then
	// nothing can be submitted. Names cannot be squatted or reserved.
	acme.must(404, "GET", "/v1/catalogue/publisher", nil)
	acme.must(400, "PUT", "/v1/catalogue/publisher", map[string]any{"slug": "taskiem", "name": "Taskiem", "public_key": pubB64})
	pub := acme.must(201, "PUT", "/v1/catalogue/publisher", map[string]any{"slug": "acme", "name": "Acme Ltd", "public_key": pubB64})
	if pub["status"] != "pending" || pub["key_id"] == "" {
		t.Fatalf("publisher: %v", pub)
	}
	if got := acme.must(200, "PUT", "/v1/catalogue/publisher", map[string]any{"name": "Acme", "public_key": pubB64}); got["name"] != "Acme" {
		t.Errorf("publisher update: %v", got)
	}
	acme.must(200, "PUT", "/v1/catalogue/publisher", map[string]any{"name": "Acme Ltd", "public_key": pubB64})
	if got := acme.must(200, "GET", "/v1/catalogue/publisher", nil); got["slug"] != "acme" {
		t.Errorf("publisher: %v", got)
	}
	globex.must(409, "PUT", "/v1/catalogue/publisher", map[string]any{"slug": "acme", "name": "Not Acme", "public_key": pubB64})
	acme.must(409, "PUT", "/v1/catalogue/publisher", map[string]any{"slug": "acme2", "name": "Acme Ltd", "public_key": pubB64})
	v1 := cataloguetest.Package(t, "acme", "1.0.0", key, nil)
	acme.must(403, "POST", "/v1/catalogue/submissions", v1)
	// A tenant cannot verify itself.
	err := db.InTenantTx(ctx, pool, []uuid.UUID{acmeID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE connector_publishers SET status = 'verified'`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("tenant verified itself: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT taskiem_catalogue_set_publisher('acme', 'verified', 'cli:ops', '')`); err != nil {
		t.Fatal(err)
	}

	// Automated checks: a good package goes to review; a tampered one is
	// kept as checks_failed with the reason; another namespace is refused.
	sub := acme.must(201, "POST", "/v1/catalogue/submissions", v1)
	if sub["state"] != "in_review" {
		t.Fatalf("submit: %s", toJSON(sub))
	}
	subID := sub["id"].(string)
	acme.must(409, "POST", "/v1/catalogue/submissions", v1)
	bad := cataloguetest.Package(t, "acme", "1.0.1", key, nil)
	bad.Licence = "GPL-3.0-only"
	got := acme.must(201, "POST", "/v1/catalogue/submissions", bad)
	if got["state"] != "checks_failed" || !strings.Contains(toJSON(got), "signature") {
		t.Errorf("tampered: %s", toJSON(got))
	}
	// The publisher withdraws it.
	if out := acme.must(200, "POST", "/v1/catalogue/submissions/"+got["id"].(string)+"/withdraw", nil); out["state"] != "withdrawn" {
		t.Errorf("withdraw: %v", out)
	}
	acme.must(422, "POST", "/v1/catalogue/submissions", cataloguetest.Package(t, "globex", "1.0.0", key, nil))

	// Only a reviewer moves it on: not the publisher through the API, not
	// the publisher's database role, not a member of the publisher.
	acme.must(409, "POST", "/v1/catalogue/submissions/"+subID+"/publish", nil)
	err = db.InTenantTx(ctx, pool, []uuid.UUID{acmeID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE catalogue_versions SET state = 'approved' WHERE id = $1`, subID)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "only a reviewer") {
		t.Errorf("publisher approved its own: %v", err)
	}
	review := func(reviewer string, approve bool, note string) error {
		_, err := pool.Exec(ctx, `SELECT taskiem_catalogue_review($1, $2, $3, $4, $5)`, subID, reviewer, approve, note, fullChecklist)
		return err
	}
	if err := review("reviewer@taskiem.test", true, "fine"); err == nil || !strings.Contains(err.Error(), "not a catalogue reviewer") {
		t.Errorf("unlisted reviewer: %v", err)
	}
	for _, r := range []string{"owner@acme.test", "reviewer@taskiem.test"} {
		if _, err := pool.Exec(ctx, `SELECT taskiem_catalogue_set_reviewer($1, true, 'cli:ops')`, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := review("Owner@Acme.test", true, "mine"); err == nil || !strings.Contains(err.Error(), "four eyes") {
		t.Errorf("publisher's own member reviewed: %v", err)
	}
	if err := review("reviewer@taskiem.test", true, " "); err == nil {
		t.Error("a review without a note")
	}
	// Approving needs every checklist item, in the database too.
	if _, err := pool.Exec(ctx, `SELECT taskiem_catalogue_review($1, 'reviewer@taskiem.test', true, 'skimmed', '{"identity": true}')`, subID); err == nil ||
		!strings.Contains(err.Error(), "confirm every checklist item") {
		t.Errorf("approved without the checklist: %v", err)
	}
	if err := review("reviewer@taskiem.test", true, "Classes, hosts and fixtures checked"); err != nil {
		t.Fatal(err)
	}
	if err := review("reviewer@taskiem.test", false, "again"); err == nil {
		t.Error("reviewed twice")
	}
	// Approved is not yet published: no one else sees it.
	if list := globex.must(200, "GET", "/v1/catalogue", nil)["connectors"].([]any); len(list) != 0 {
		t.Errorf("unpublished in the catalogue: %v", list)
	}
	acme.must(200, "POST", "/v1/catalogue/submissions/"+subID+"/publish", nil)
	if v := globex.must(200, "GET", "/v1/catalogue/connectors/"+v1.ID+"/1.0.0", nil); v["state"] != "published" {
		t.Errorf("version: %v", v)
	}
	got = acme.must(200, "GET", "/v1/catalogue/submissions/"+subID, nil)
	var events []string
	for _, e := range got["events"].([]any) {
		events = append(events, e.(map[string]any)["event"].(string))
	}
	if strings.Join(events, ",") != "submitted,checks_passed,approved,published" || got["reviewed_by"] != "reviewer@taskiem.test" {
		t.Errorf("history: %v %v", events, got["reviewed_by"])
	}
	// A version never changes, even for its publisher's role.
	err = db.InTenantTx(ctx, pool, []uuid.UUID{acmeID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE catalogue_versions SET state = 'published', licence = 'MIT' WHERE id = $1`, subID)
		return err
	})
	if err == nil {
		t.Error("a published version changed")
	}

	// Globex browses and sees it; it sees nothing of Acme's submissions.
	cat := globex.must(200, "GET", "/v1/catalogue", nil)["connectors"].([]any)
	if len(cat) != 1 || !strings.Contains(toJSON(cat), `"create_payment":"reconcilable_write"`) || !strings.Contains(toJSON(cat), `"publisher_name":"Acme Ltd"`) {
		t.Fatalf("catalogue: %s", toJSON(cat))
	}
	if subs := globex.must(200, "GET", "/v1/catalogue/submissions", nil)["submissions"].([]any); len(subs) != 0 {
		t.Errorf("globex sees acme's submissions: %v", subs)
	}
	var n int
	_ = db.InTenantTx(ctx, pool, []uuid.UUID{globexID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM catalogue_versions`).Scan(&n)
	})
	if n != 0 {
		t.Errorf("globex reads the catalogue table: %d rows", n)
	}

	// Not installed, it cannot be used; installing needs consent to the
	// version's hosts and writes.
	if probs := globex.must(200, "POST", "/v1/validate", map[string]any{"definition": json.RawMessage(catalogueFlow)})["problems"].([]any); len(probs) == 0 {
		t.Error("an uninstalled catalogue connector validates")
	}
	globex.must(400, "POST", "/v1/catalogue/installs", map[string]any{"connector": "p_acme_ledger", "version": "1.0.0"})
	consent := map[string]any{"hosts": []string{"ledger.example.com"}, "writes": map[string]string{"create_payment": "reconcilable_write"}}
	globex.must(400, "POST", "/v1/catalogue/installs", map[string]any{"connector": "p_acme_ledger", "version": "1.0.0", "consent": map[string]any{"hosts": []string{}}})
	globex.must(201, "POST", "/v1/catalogue/installs", map[string]any{"connector": "p_acme_ledger", "version": "1.0.0", "consent": consent})
	globex.must(409, "POST", "/v1/catalogue/installs", map[string]any{"connector": "p_acme_ledger", "version": "1.0.0", "consent": consent})

	// Globex pays through it; a response outside the declared enum is
	// drift, recorded for Globex.
	globex.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "p_acme_ledger@1", "name": "main",
		"credentials": map[string]string{"api_key": "ledger-key"}})
	wf := publishFlow(t, globex, catalogueFlow)
	mu.Lock()
	status = "settled"
	mu.Unlock()
	run := globex.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 5000, "account_number": "0123456789"}})["run_id"].(string)
	w.env.Drain(t)
	res := globex.must(200, "GET", "/v1/runs/"+run, nil)
	if st := res["run"].(map[string]any)["status"]; st != "completed" || !strings.Contains(toJSON(res), "pay_7") {
		t.Fatalf("run: %v\n%s", st, toJSON(res["events"]))
	}
	if strings.Contains(toJSON(res), "0123456789") {
		t.Error("the account number the manifest declares personal is shown in the clear")
	}
	drift := toJSON(globex.must(200, "GET", "/v1/connector-drift", nil))
	if !strings.Contains(drift, "p_acme_ledger") || !strings.Contains(drift, "settled") {
		t.Errorf("drift: %s", drift)
	}
	if strings.Contains(toJSON(acme.must(200, "GET", "/v1/connector-drift", nil)), "p_acme_ledger") {
		t.Error("globex's drift shows to acme")
	}

	// 1.1.0 adds a host: the upgrade shows it and needs consent again.
	v11 := cataloguetest.Package(t, "acme", "1.1.0", key, func(m string) string {
		return strings.Replace(m, "base_url: https://ledger.example.com", "base_url: https://ledger.example.com\negress_hosts: [ledger.example.com, files.example.net]", 1)
	})
	sub11 := acme.must(201, "POST", "/v1/catalogue/submissions", v11)
	if sub11["state"] != "in_review" {
		t.Fatalf("1.1.0: %s", toJSON(sub11))
	}
	if _, err := pool.Exec(ctx, `SELECT taskiem_catalogue_review($1, 'reviewer@taskiem.test', true, 'new file host is the provider''s', $2)`, sub11["id"], fullChecklist); err != nil {
		t.Fatal(err)
	}
	acme.must(200, "POST", "/v1/catalogue/submissions/"+sub11["id"].(string)+"/publish", nil)
	inst := globex.must(200, "GET", "/v1/catalogue/installs", nil)["installs"].([]any)
	if len(inst) != 1 || inst[0].(map[string]any)["latest"] != "1.1.0" || inst[0].(map[string]any)["state"] != "published" {
		t.Errorf("installs: %s", toJSON(inst))
	}
	diff := globex.must(200, "GET", "/v1/catalogue/installs/p_acme_ledger/1/upgrade?to=1.1.0", nil)["diff"].(map[string]any)
	if diff["needs_consent"] != true || toJSON(diff["added_hosts"]) != `["files.example.net"]` {
		t.Errorf("diff: %v", diff)
	}
	globex.must(400, "POST", "/v1/catalogue/installs/p_acme_ledger/1/upgrade", map[string]any{"version": "1.1.0"})
	consent["hosts"] = []string{"files.example.net", "ledger.example.com"}
	globex.must(200, "POST", "/v1/catalogue/installs/p_acme_ledger/1/upgrade", map[string]any{"version": "1.1.0", "consent": consent})
	globex.must(400, "GET", "/v1/catalogue/installs/p_acme_ledger/1/upgrade?to=2.0.0", nil)

	// Revoked: new steps stop using it and Globex is alerted, once.
	acme.must(400, "POST", "/v1/catalogue/submissions/"+sub11["id"].(string)+"/revoke", map[string]any{})
	out := acme.must(200, "POST", "/v1/catalogue/submissions/"+sub11["id"].(string)+"/revoke", map[string]any{"reason": "sends the API key to the file host"})
	if out["tenants_alerted"] != float64(1) {
		t.Errorf("revoke: %v", out)
	}
	if n, err := catalogue.NotifyRevoked(ctx, pool, "p_acme_ledger", "1.1.0", "again", "cli:ops", ""); err != nil || n != 0 {
		t.Errorf("alerted twice: %d %v", n, err)
	}
	var alerts int
	_ = db.InTenantTx(ctx, pool, []uuid.UUID{globexID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE kind = 'connector_revoked' AND body LIKE '%sends the API key%'`).Scan(&alerts)
	})
	if alerts != 1 {
		t.Errorf("revocation alerts: %d", alerts)
	}
	if probs := globex.must(200, "POST", "/v1/validate", map[string]any{"definition": json.RawMessage(catalogueFlow)})["problems"].([]any); len(probs) == 0 {
		t.Error("a revoked version still validates")
	}
	if inst := toJSON(globex.must(200, "GET", "/v1/catalogue/installs", nil)); !strings.Contains(inst, `"state":"revoked"`) {
		t.Errorf("installs after revocation: %s", inst)
	}
	// Revoked is final, and the version can never be submitted again.
	acme.must(409, "POST", "/v1/catalogue/submissions/"+sub11["id"].(string)+"/publish", nil)
	acme.must(409, "POST", "/v1/catalogue/submissions", v11)
	globex.must(204, "DELETE", "/v1/catalogue/installs/p_acme_ledger/1", nil)

	// A builder can browse but not install.
	globex.must(201, "POST", "/v1/members", map[string]any{"email": "bld@globex.test", "password": "correct horse battery", "roles": []string{"builder"}})
	bld := w.login(t, "bld@globex.test", "correct horse battery")
	bld.must(200, "GET", "/v1/catalogue", nil)
	bld.must(403, "POST", "/v1/catalogue/installs", map[string]any{"connector": "p_acme_ledger", "version": "1.0.0", "consent": consent})
}
