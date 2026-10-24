package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
)

// accountRails stands for a business's iswallet connection: it opens one
// account per key, as iswallet does per reference.
type accountRails struct {
	mu     sync.Mutex
	opened map[string]string
	asked  int
	fail   map[string]error
}

var walletRail = invoicing.Rail{Connector: "iswallet", Connection: "main", Name: "iswallet", Events: map[string]bool{},
	Caps: map[string]connector.CapabilityMap{
		"virtual_account": {Requires: []string{"account"}, Ignores: []string{"customer.email", "customer.phone", "customer.bvn", "customer.nin"}},
		"list_credits":    {Requires: []string{"account"}},
	}}

func (f *accountRails) Capable(context.Context, uuid.UUID, string) ([]invoicing.Rail, error) {
	return []invoicing.Rail{walletRail}, nil
}

func (f *accountRails) ResolveChannel(_ context.Context, _ uuid.UUID, channel, _ string) (invoicing.Rail, error) {
	if channel == walletRail.Channel() {
		return walletRail, nil
	}
	return invoicing.Rail{}, invoicing.ErrNoRail
}

func (f *accountRails) Call(_ context.Context, _ uuid.UUID, _ invoicing.Rail, _ string, in map[string]any, key effects.KeyInput) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	if err := f.fail[in["customer"].(map[string]any)["name"].(string)]; err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(key.TenantID + key.Seed + key.StepID))
	k := hex.EncodeToString(sum[:])
	if f.opened[k] == "" {
		f.opened[k] = fmt.Sprintf("99%08d", len(f.opened)+1)
	}
	return map[string]any{"account_number": f.opened[k], "bank_name": "Wema Bank", "reference": "tsk_" + k[:24]}, nil
}

// Students' account numbers through the API (docs/spec/schools.md B4):
// the owner chooses the issuing connection; a manager plans and confirms a
// batch, which issues each student once; a student's page lists their
// account (its name masked); a payment into it pays their invoice.
func TestSchoolAccountNumbers(t *testing.T) {
	w := newWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	rails := &accountRails{opened: map[string]string{}, fail: map[string]error{}}
	w.srv.AccountRails = rails
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	admin := addMember(t, w, owner, "ade", "admin")
	viewer := addMember(t, w, owner, "vic", "viewer")
	j1 := owner.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "arm": "A", "order": 7})
	csv := "Name,Admission number,Class,Guardian name,Phone\nEbube Eze,GCA/001,JSS 1 A,Ngozi Eze,08031234567\nChidi Eze,GCA/002,JSS 1 A,Ngozi Eze,08031234567\nAda Obi,GCA/003,JSS 1 A,Bola Obi,08031234568\n"
	plan := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv})
	owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv, "confirm": plan["hash"]})
	_ = j1

	// No issuing connection yet: issuing is refused.
	admin.must(409, "POST", "/v1/schools/accounts/issue", map[string]any{})
	// The settings list what can issue; only the owner chooses.
	st := owner.must(200, "GET", "/v1/settings/schools", nil)
	if is := st["issuers"].([]any); len(is) != 1 || is[0].(map[string]any)["needs_account"] != true {
		t.Fatalf("issuers %v", st["issuers"])
	}
	admin.must(403, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "issuing": map[string]any{"connection": "iswallet/main", "account": "wal_1"}})
	owner.must(400, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "issuing": map[string]any{"connection": "iswallet/main"}})
	owner.must(400, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "issuing": map[string]any{"connection": "paystack/main"}})
	st = owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "issuing": map[string]any{"connection": "iswallet/main", "account": "wal_1", "siblings_share_credit": true}})
	if is := st["issuing"].(map[string]any); is["connection"] != "iswallet/main" || is["account"] != "wal_1" || is["siblings_share_credit"] != true {
		t.Fatalf("issuing %v", st)
	}
	if n := schoolAudits(t, w, "Greenfield", "settings.schools.issuing"); n != 1 {
		t.Errorf("%d issuing audits", n)
	}

	// One student first.
	viewer.must(403, "POST", "/v1/schools/students/GCA%2F001/account", map[string]any{})
	one := admin.must(200, "POST", "/v1/schools/students/GCA%2F001/account", map[string]any{})
	if one["status"] != "issued" || one["account_number"] == "" || one["account_name"] != "G•••" {
		t.Fatalf("one %v", one)
	}
	// The plan: the other two; the batch issues them once, however often
	// it is confirmed or worked.
	p := admin.must(200, "POST", "/v1/schools/accounts/issue", map[string]any{})["plan"].(map[string]any)
	if p["to_issue"] != float64(2) || p["already_issued"] != float64(1) || p["active"] != float64(3) {
		t.Fatalf("plan %v", p)
	}
	viewer.must(403, "POST", "/v1/schools/accounts/issue", map[string]any{"confirm": p["hash"]})
	admin.must(409, "POST", "/v1/schools/accounts/issue", map[string]any{"confirm": "stale"})
	rails.fail["Ada Obi"] = fmt.Errorf("iswallet PROVIDER_NOT_CONFIGURED: no provider: %w", effects.ErrFatal)
	job := admin.must(202, "POST", "/v1/schools/accounts/issue", map[string]any{"confirm": p["hash"]})
	if again := admin.must(202, "POST", "/v1/schools/accounts/issue", map[string]any{"confirm": p["hash"]}); again["id"] != job["id"] {
		t.Fatalf("confirmed twice: %v %v", again["id"], job["id"])
	}
	s := &schools.Schools{Pool: w.env.DB.App, Vault: w.env.Vault, Invoices: w.srv.Invoices, Rails: rails}
	for range 3 {
		if err := s.RunJobs(context.Background(), 50); err != nil {
			t.Fatal(err)
		}
	}
	done := viewer.must(200, "GET", "/v1/schools/accounts/jobs/"+job["id"].(string), nil)
	probs := done["problems"].([]any)
	if done["status"] != "done" || done["issued"] != float64(1) || done["failed"] != float64(1) || len(probs) != 1 ||
		probs[0].(map[string]any)["reason"] != "iswallet PROVIDER_NOT_CONFIGURED: no provider" {
		t.Fatalf("job %v", done)
	}
	if l := viewer.must(200, "GET", "/v1/schools/accounts/jobs", nil)["jobs"].([]any); len(l) != 1 {
		t.Fatalf("jobs %v", l)
	}
	if rails.asked != 3 || len(rails.opened) != 2 {
		t.Fatalf("asked %d, opened %d", rails.asked, len(rails.opened))
	}
	// The student's page.
	acc := viewer.must(200, "GET", "/v1/schools/students/GCA%2F001/accounts", nil)
	list := acc["accounts"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["account_number"] != one["account_number"] || acc["issuing_connection"] != "iswallet/main" {
		t.Fatalf("accounts %v", acc)
	}
	viewer.must(403, "GET", "/v1/schools/students/GCA%2F001/accounts?reveal=true", nil)
	if got := owner.must(200, "GET", "/v1/schools/students/GCA%2F001/accounts?reveal=true", nil)["accounts"].([]any)[0].(map[string]any); !strings.Contains(got["account_name"].(string), "EBUBE EZE") {
		t.Fatalf("revealed %v", got)
	}
	ada := viewer.must(200, "GET", "/v1/schools/students/GCA%2F003/accounts", nil)["accounts"].([]any)
	if len(ada) != 1 || ada[0].(map[string]any)["status"] != "failed" {
		t.Fatalf("ada %v", ada)
	}
	// One by one, the rail's refusal: 422 with its reason.
	code, out := admin.do("POST", "/v1/schools/students/GCA%2F003/account", map[string]any{})
	if code != 422 || out["code"] != "rail_refused" || out["error"] != "iswallet PROVIDER_NOT_CONFIGURED: no provider" {
		t.Fatalf("refused %d %v", code, out)
	}
}
