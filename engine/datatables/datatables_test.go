package datatables_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/pgdock"
	"github.com/israel-duff/taskiem/connectors/pgdock/pgdocktest"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

var ctx = context.Background()

const platformToken = "xb_platform_unrestricted"

type world struct {
	*rt.Env
	pg  *pgdocktest.Fake
	svc *datatables.Service
	now time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	e := rt.New(t)
	w := &world{Env: e, pg: pgdocktest.New(t), now: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)}
	w.pg.Now = func() time.Time { return w.now }
	// The platform's token: unrestricted, every scope, long-lived (a
	// minted token never outlives its maker).
	w.pg.AddToken(pgdocktest.Token{Secret: platformToken, ID: "tok-platform", Name: "taskiem-platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: w.now.Add(10 * 365 * 24 * time.Hour)})
	if err := e.Registry.Register(pgdock.New(pgdock.Options{BaseURL: w.pg.URL})); err != nil {
		t.Fatal(err)
	}
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'dev'), ($1, 'prod')`, e.Tenant)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	w.svc = &datatables.Service{Pool: e.DB.App, Vault: e.Vault, Egress: e.Egress, Now: func() time.Time { return w.now },
		Platform: datatables.Platform{URL: w.pg.URL, Token: platformToken, Org: w.pg.OrgID, Region: "lagos"}, PollInterval: time.Millisecond}
	return w
}

func (w *world) project(t *testing.T) datatables.Status {
	t.Helper()
	st, err := w.svc.Status(ctx, w.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (w *world) creds(t *testing.T) map[string]string {
	t.Helper()
	creds, err := w.svc.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "test"}), w.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	return creds
}

func count(reqs []string, prefix string) int {
	n := 0
	for _, r := range reqs {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// The first table provisions the project end to end: a project named
// after the tenant in the platform's organisation, its create operation
// waited for, backend services on with the secret key from that one
// answer, a project-restricted token with a year's life, and the
// data_tables connection in prod holding it all, marked as Taskiem's.
func TestFirstTableProvisionsProject(t *testing.T) {
	w := newWorld(t)
	if st := w.project(t); st.Status != "none" || !st.Available || !st.Configured {
		t.Fatalf("before: %+v", st)
	}
	p, err := w.svc.EnsureProject(ctx, w.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Ready() || p.Own || p.ProjectID == "" || p.Ref == "" || p.APIURL != w.pg.APIURL(p.ProjectID) || p.TokenExpiresAt == nil {
		t.Fatalf("project %+v", p)
	}
	if got := w.pg.Projects(); len(got) != 1 || got[0] != p.ProjectID {
		t.Fatalf("projects %v", got)
	}
	if exp := p.TokenExpiresAt.Sub(w.now); exp < 364*24*time.Hour || exp > 366*24*time.Hour {
		t.Errorf("token expires in %s, want a year", exp)
	}
	// The calls, in order: the search by name, the create, two polls,
	// the services (off), enable, two polls, the token.
	want := []string{"GET /api/v1/projects", "POST /api/v1/projects", "GET /api/v1/operations/", "GET /api/v1/operations/",
		"GET /api/v1/projects/" + p.ProjectID + "/services", "POST /api/v1/projects/" + p.ProjectID + "/services", "GET /api/v1/operations/", "GET /api/v1/operations/", "POST /api/v1/tokens"}
	if len(w.pg.Requests) != len(want) {
		t.Fatalf("requests %v", w.pg.Requests)
	}
	for i, pre := range want {
		if !strings.HasPrefix(w.pg.Requests[i], pre) {
			t.Errorf("request %d is %s, want %s", i, w.pg.Requests[i], pre)
		}
	}
	var tok pgdocktest.Token
	for _, x := range w.pg.Tokens() {
		if x.Name == datatables.TokenName {
			tok = x
		}
	}
	if tok.Secret == "" || !slices.Equal(tok.Scopes, []string{"read", "write", "admin"}) || !slices.Equal(tok.ProjectIDs, []string{p.ProjectID}) {
		t.Fatalf("token %+v", tok)
	}
	creds := w.creds(t)
	if creds["token"] != tok.Secret || creds["project_id"] != p.ProjectID || creds["org_id"] != w.pg.OrgID || creds["base_url"] != w.pg.URL ||
		!strings.HasPrefix(creds["secret_key"], "xb_sec_") || creds["api_url"] != p.APIURL {
		t.Fatalf("credentials %v", creds)
	}
	if creds["token"] == platformToken {
		t.Fatal("the platform token reached the tenant's vault")
	}
	var by string
	var name string
	err = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT provisioned_by, name FROM connections WHERE id = $1 AND environment = 'prod' AND connector = 'pgdock'`, p.ConnectionID).Scan(&by, &name)
	})
	if err != nil || by != datatables.ProvisionedBy || name != datatables.ConnectionName {
		t.Fatalf("connection: %v %q %q", err, by, name)
	}
	// A second call makes nothing.
	n := len(w.pg.Requests)
	if again, err := w.svc.EnsureProject(ctx, w.Tenant); err != nil || again.ProjectID != p.ProjectID || len(w.pg.Requests) != n {
		t.Fatalf("second call: %v %+v %v", err, again, w.pg.Requests[n:])
	}
	// The data API serves the project with the secret key.
	conn, _ := w.Registry.Get("pgdock@1")
	if err := w.svc.Apply(ctx, w.Tenant, "test", datatables.CreateTableChange("debtors", []datatables.Column{{Name: "name", Type: "text", Required: true}, {Name: "phone", Type: "phone"}})); err != nil {
		t.Fatal(err)
	}
	resp, err := conn.Actions["insert_rows"].Execute(ctx, connector.Request{Input: map[string]any{"table": "debtors", "row": map[string]any{"name": "Ada", "phone": "2348012345678"}},
		Credentials: creds, HTTP: http.DefaultClient, IdempotencyKey: "k1", KeyFirstSent: time.Now(), Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rows := resp.Output.(map[string]any)["rows"].([]any); len(rows) != 1 || rows[0].(map[string]any)["id"] == nil {
		t.Fatalf("insert answered %v", resp.Output)
	}
	tbl := w.pg.Table(p.ProjectID, "debtors")
	if tbl == nil || !slices.Equal(tbl.Columns, []string{"id", "created_at", "updated_at", "name", "phone"}) || tbl.Types["phone"] != "text" || tbl.Types["id"] != "bigint generated by default as identity" {
		t.Fatalf("table %+v", tbl)
	}
}

// Provisioning that stops half way resumes where it stopped: a crash
// after the project was created (Xenibase failing the services call)
// leaves the record at its step, and the next request carries on with
// the same project rather than making another.
func TestProvisioningResumesAfterCrash(t *testing.T) {
	w := newWorld(t)
	w.pg.Fail(pgdocktest.Failure{Method: http.MethodPost, PathPrefix: "/api/v1/projects/", Status: 500})
	_, err := w.svc.EnsureProject(ctx, w.Tenant)
	if err == nil {
		t.Fatal("provisioning succeeded through a failed services call")
	}
	st := w.project(t)
	if st.Status != "enabling" || st.ProjectID == "" || st.Error == "" {
		t.Fatalf("after the crash: %+v", st)
	}
	projects := w.pg.Projects()
	if len(projects) != 1 {
		t.Fatalf("projects %v", projects)
	}
	n := len(w.pg.Requests)
	p, err := w.svc.EnsureProject(ctx, w.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Ready() || p.ProjectID != projects[0] || len(w.pg.Projects()) != 1 {
		t.Fatalf("resumed %+v, projects %v", p, w.pg.Projects())
	}
	if count(w.pg.Requests[n:], "POST /api/v1/projects\x00") != 0 || count(w.pg.Requests[n:], "POST /api/v1/projects/"+projects[0]+"/services") != 1 {
		t.Fatalf("resume made %v", w.pg.Requests[n:])
	}
	for _, r := range w.pg.Requests[n:] {
		if r == "POST /api/v1/projects" {
			t.Fatalf("resume created a second project: %v", w.pg.Requests[n:])
		}
	}

	// A record whose project was made but whose create answer was lost
	// (no project id): the project is found by name, not made again.
	w2 := newWorld(t)
	w2.pg.Fail(pgdocktest.Failure{Method: http.MethodPost, PathPrefix: "/api/v1/projects", Status: 502, LoseAnswer: true})
	if _, err := w2.svc.EnsureProject(ctx, w2.Tenant); err == nil {
		t.Fatal("a lost create answer did not fail the request")
	}
	if st := w2.project(t); st.Status != "creating" || st.ProjectID != "" {
		t.Fatalf("after the lost answer: %+v", st)
	}
	p2, err := w2.svc.EnsureProject(ctx, w2.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if got := w2.pg.Projects(); len(got) != 1 || p2.ProjectID != got[0] {
		t.Fatalf("adopted %+v of %v", p2, got)
	}

	// Services already on from an earlier attempt whose secret key was
	// lost: a fresh key is minted rather than enabling again.
	w3 := newWorld(t)
	w3.pg.Fail(pgdocktest.Failure{Method: http.MethodPost, PathPrefix: "/api/v1/tokens", Status: 500})
	if _, err := w3.svc.EnsureProject(ctx, w3.Tenant); err == nil {
		t.Fatal("a failed token call did not fail the request")
	}
	n = len(w3.pg.Requests)
	p3, err := w3.svc.EnsureProject(ctx, w3.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if !p3.Ready() || count(w3.pg.Requests[n:], "POST /api/v1/projects/"+p3.ProjectID+"/services/keys") != 1 || count(w3.pg.Requests[n:], "POST /api/v1/projects/"+p3.ProjectID+"/services") != 1 {
		t.Fatalf("resume after a lost key made %v", w3.pg.Requests[n:])
	}
	if c := w3.creds(t); !strings.HasPrefix(c["secret_key"], "xb_sec_") {
		t.Fatalf("credentials %v", c)
	}
}

// The organisation's quota at Xenibase refuses the project with a clear
// error that names the quota; nothing is recorded as ready.
func TestQuotaRefusal(t *testing.T) {
	w := newWorld(t)
	w.pg.ProjectQuota(w.pg.OrgID, 0)
	_, err := w.svc.EnsureProject(ctx, w.Tenant)
	xe, ok := datatables.AsError(err)
	if !ok || !xe.IsQuota() || xe.Status != http.StatusConflict || xe.QuotaLimit() != "xenibase_projects" {
		t.Fatalf("err %v", err)
	}
	if st := w.project(t); st.Status != "creating" || st.ProjectID != "" || !strings.Contains(st.Error, "quota_exceeded") {
		t.Fatalf("status %+v", st)
	}
	// Unconfigured: unavailable before any call.
	w.svc.Platform = datatables.Platform{}
	if _, err := w.svc.EnsureProject(ctx, w.Tenant); !errors.Is(err, datatables.ErrUnavailable) {
		t.Fatalf("unconfigured: %v", err)
	}
	if st := w.project(t); st.Available || st.Configured {
		t.Fatalf("unconfigured status %+v", st)
	}
}

// A failed create operation is recorded as failed with Xenibase's reason.
func TestFailedOperation(t *testing.T) {
	w := newWorld(t)
	w.pg.FailOperation("create", "no room on the shared node")
	_, err := w.svc.EnsureProject(ctx, w.Tenant)
	if !errors.Is(err, datatables.ErrProjectFailed) {
		t.Fatalf("err %v", err)
	}
	if st := w.project(t); st.Status != "failed" || !strings.Contains(st.Error, "no room") {
		t.Fatalf("status %+v", st)
	}
}

// Tokens are renewed two weeks before they expire: a new one is minted
// with the platform token and replaces the connection's; an own project
// is left alone.
func TestTokenRenewal(t *testing.T) {
	w := newWorld(t)
	p, err := w.svc.EnsureProject(ctx, w.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	old := w.creds(t)["token"]
	if err := w.svc.RenewTick(ctx); err != nil {
		t.Fatal(err)
	}
	if w.creds(t)["token"] != old {
		t.Fatal("renewed a token with a year to go")
	}
	w.now = w.now.Add(352 * 24 * time.Hour) // 13 days before expiry
	if err := w.svc.RenewTick(ctx); err != nil {
		t.Fatal(err)
	}
	creds := w.creds(t)
	if creds["token"] == old || creds["secret_key"] == "" || creds["project_id"] != p.ProjectID {
		t.Fatalf("after renewal %v", creds)
	}
	st := w.project(t)
	if st.TokenExpiresAt == nil || st.TokenExpiresAt.Sub(w.now) < 364*24*time.Hour {
		t.Fatalf("expiry not moved: %+v", st)
	}
	// Two tokens now carry the name; the connection holds the new one.
	var tok pgdocktest.Token
	for _, x := range w.pg.Tokens() {
		if x.Secret == creds["token"] {
			tok = x
		}
	}
	if tok.Secret == "" || tok.Name != datatables.TokenName || !slices.Equal(tok.ProjectIDs, []string{p.ProjectID}) || !slices.Equal(tok.Scopes, []string{"read", "write", "admin"}) {
		t.Fatalf("token %+v", tok)
	}
	// The new token works for a schema change.
	if err := w.svc.Apply(ctx, w.Tenant, "test", datatables.CreateTableChange("stock", []datatables.Column{{Name: "item", Type: "text"}})); err != nil {
		t.Fatal(err)
	}
	// A renewal that fails is logged and raised to the tenant once a day.
	w.now = w.now.Add(352 * 24 * time.Hour)
	w.pg.Fail(pgdocktest.Failure{Method: http.MethodPost, PathPrefix: "/api/v1/tokens", Status: 503})
	if err := w.svc.RenewTick(ctx); err == nil {
		t.Fatal("a refused token call did not fail the tick")
	}
	var alerts int
	_ = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE kind = $1`, datatables.TokenAlert).Scan(&alerts)
	})
	if alerts != 1 {
		t.Fatalf("alerts %d", alerts)
	}
}

// Schema changes are a preview then an apply with the preview's hash;
// a drop is refused before it is sent when the typed name is not the
// object's, and the fake refuses one sent without the confirmation.
func TestSchemaPreviewApplyAndDrops(t *testing.T) {
	w := newWorld(t)
	p, err := w.svc.EnsureProject(ctx, w.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	n := len(w.pg.Requests)
	cols := []datatables.Column{{Name: "name", Type: "text", Required: true}, {Name: "balance_kobo", Type: "integer"}, {Name: "email", Type: "email"}, {Name: "meta", Type: "json"}}
	if err := w.svc.Apply(ctx, w.Tenant, "test", datatables.CreateTableChange("debtors", cols)); err != nil {
		t.Fatal(err)
	}
	if got := w.pg.Requests[n:]; len(got) != 2 || got[0] != "POST /api/v1/projects/"+p.ProjectID+"/schema/preview" || got[1] != "POST /api/v1/projects/"+p.ProjectID+"/schema/apply" {
		t.Fatalf("calls %v", got)
	}
	tbl := w.pg.Table(p.ProjectID, "debtors")
	if tbl == nil || tbl.Types["balance_kobo"] != "bigint" || tbl.Types["email"] != "text" || tbl.Types["meta"] != "jsonb" || !slices.Equal(tbl.Key, []string{"id"}) {
		t.Fatalf("table %+v", tbl)
	}
	if err := w.svc.Apply(ctx, w.Tenant, "test", datatables.CreateTableChange("debtors", cols)); err == nil {
		t.Fatal("a second create_table passed")
	} else if xe, ok := datatables.AsError(err); !ok || xe.Code != "ddl_failed" {
		t.Fatalf("second create: %v", err)
	}
	if err := w.svc.Apply(ctx, w.Tenant, "test", datatables.AddColumnChange("debtors", datatables.Column{Name: "paid", Type: "boolean"})); err != nil {
		t.Fatal(err)
	}
	if tbl := w.pg.Table(p.ProjectID, "debtors"); !slices.Contains(tbl.Columns, "paid") {
		t.Fatalf("column not added: %+v", tbl)
	}
	// Drops: the typed name must be the column's; nothing is sent otherwise.
	n = len(w.pg.Requests)
	err = w.svc.Apply(ctx, w.Tenant, "test", datatables.DropColumnChange("debtors", "paid", "debtors"))
	if !errors.Is(err, datatables.ErrInvalid) || len(w.pg.Requests)-n != 1 {
		t.Fatalf("drop with the wrong name: %v, calls %v", err, w.pg.Requests[n:])
	}
	if err := w.svc.Apply(ctx, w.Tenant, "test", datatables.DropColumnChange("debtors", "paid", "paid")); err != nil {
		t.Fatal(err)
	}
	if tbl := w.pg.Table(p.ProjectID, "debtors"); slices.Contains(tbl.Columns, "paid") {
		t.Fatalf("column not dropped: %+v", tbl)
	}
	// The fake itself refuses a drop without confirm, and by a token
	// without admin.
	creds := w.creds(t)
	apply := func(token string, body map[string]any) (int, string) {
		t.Helper()
		var out map[string]any
		err := connector.DoJSON(ctx, http.DefaultClient, http.MethodPost, w.pg.URL+"/api/v1/projects/"+p.ProjectID+"/schema/apply", map[string]string{"Authorization": "Bearer " + token}, body, &out)
		var he *connector.HTTPError
		if errors.As(err, &he) {
			return he.Status, string(he.Body)
		}
		if err != nil {
			t.Fatal(err)
		}
		return 200, ""
	}
	change := datatables.DropTableChange("debtors", "").Body()
	var preview struct{ Hash string }
	if err := connector.DoJSON(ctx, http.DefaultClient, http.MethodPost, w.pg.URL+"/api/v1/projects/"+p.ProjectID+"/schema/preview", map[string]string{"Authorization": "Bearer " + creds["token"]}, map[string]any{"change": change}, &preview); err != nil {
		t.Fatal(err)
	}
	if st, body := apply(creds["token"], map[string]any{"change": change, "hash": preview.Hash}); st != 400 || !strings.Contains(body, "confirm_required") {
		t.Fatalf("drop without confirm: %d %s", st, body)
	}
	if st, body := apply(creds["token"], map[string]any{"change": change, "hash": "stale", "confirm": "debtors"}); st != 409 || !strings.Contains(body, "stale_plan") {
		t.Fatalf("stale hash: %d %s", st, body)
	}
	w.pg.AddToken(pgdocktest.Token{Secret: "xb_writer", Scopes: []string{"read", "write"}, ProjectIDs: []string{p.ProjectID}})
	if st, body := apply("xb_writer", map[string]any{"change": change, "hash": preview.Hash, "confirm": "debtors"}); st != 403 || !strings.Contains(body, "insufficient_scope") {
		t.Fatalf("drop without admin: %d %s", st, body)
	}
	if err := w.svc.Apply(ctx, w.Tenant, "test", datatables.DropTableChange("debtors", "debtors")); err != nil {
		t.Fatal(err)
	}
	if w.pg.Table(p.ProjectID, "debtors") != nil {
		t.Fatal("table not dropped")
	}
}

// A workflow reads a data table's rows through read_rows on the
// data_tables connection, like any Xenibase table (decision 0033: nothing
// in the engine knows a data table).
func TestWorkflowReadsTableRows(t *testing.T) {
	w := newWorld(t)
	p, err := w.svc.EnsureProject(ctx, w.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Apply(ctx, w.Tenant, "test", datatables.CreateTableChange("debtors", []datatables.Column{{Name: "name", Type: "text"}, {Name: "balance_kobo", Type: "integer"}})); err != nil {
		t.Fatal(err)
	}
	w.pg.Begin(p.ProjectID).Insert("debtors", map[string]any{"id": 1, "name": "Ada", "balance_kobo": 500000}).Insert("debtors", map[string]any{"id": 2, "name": "Bisi", "balance_kobo": 0}).Commit()
	wf := w.Publish(t, `{"schema":"wd/v1","id":"wf_owing","version":1,"name":"owing","trigger":{"type":"manual"},
	  "steps":[{"id":"read","type":"connector","connector":"pgdock@1","action":"read_rows","connection":"data_tables",
	    "input":{"table":"debtors","filters":[{"column":"balance_kobo","op":"gt","value":0}]}}]}`)
	ref := w.Start(t, wf, map[string]any{})
	w.Drain(t)
	if st := w.Status(t, ref); st != "completed" {
		t.Fatalf("run %s", st)
	}
	var out string
	_ = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT payload::text FROM run_events WHERE run_id = $1 AND type = 'StepCompleted' ORDER BY seq DESC LIMIT 1`, ref.ID).Scan(&out)
	})
	if !strings.Contains(out, "Ada") || strings.Contains(out, "Bisi") {
		t.Fatalf("the step read %s", out)
	}
	// The read is recorded like any connection use.
	var reads int
	_ = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM secret_reads WHERE kind = 'connection' AND name = $1 AND purpose = 'step.connector'`, datatables.ConnectionName).Scan(&reads)
	})
	if reads == 0 {
		t.Fatal("the step's credential read was not recorded")
	}
}

// Values are checked against their column's type before anything is sent.
func TestCheckRow(t *testing.T) {
	cols := []datatables.Column{{Name: "name", Type: "text", Required: true}, {Name: "phone", Type: "phone", Personal: true}, {Name: "balance", Type: "number"},
		{Name: "count", Type: "integer"}, {Name: "paid", Type: "boolean"}, {Name: "due", Type: "date"}, {Name: "seen", Type: "datetime"}, {Name: "email", Type: "email"}, {Name: "meta", Type: "json"}}
	row, err := datatables.CheckRow(cols, map[string]any{"name": "Ada", "phone": "+234 801 234 5678", "balance": "12.5", "count": float64(3), "paid": "yes",
		"due": "2026-11-01", "seen": "2026-10-17 09:00:00", "email": "ada@example.com", "meta": `{"a":1}`}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if row["phone"] != "+2348012345678" || row["balance"] != 12.5 || row["count"] != int64(3) || row["paid"] != true || row["seen"] != "2026-10-17T09:00:00Z" || row["meta"].(map[string]any)["a"] != float64(1) {
		t.Fatalf("row %v", row)
	}
	for _, bad := range []map[string]any{
		{"name": "Ada", "count": 1.5}, {"name": "Ada", "due": "01/11/2026"}, {"name": "Ada", "email": "not-an-email"}, {"name": "Ada", "phone": "abc"},
		{"name": "Ada", "nope": 1}, {"phone": "2348012345678"}, {"name": nil}, {"name": "Ada", "id": 4},
	} {
		if _, err := datatables.CheckRow(cols, bad, true, false); !errors.Is(err, datatables.ErrInvalid) {
			t.Errorf("%v passed: %v", bad, err)
		}
	}
	// A patch need not carry required columns; an import may carry id.
	if _, err := datatables.CheckRow(cols, map[string]any{"paid": false}, false, false); err != nil {
		t.Error(err)
	}
	if r, err := datatables.CheckRow(cols, map[string]any{"id": "7", "name": "Ada", "created_at": "x"}, false, true); err != nil || r["id"] != int64(7) || len(r) != 2 {
		t.Errorf("import row %v %v", r, err)
	}
	masked := datatables.Mask(cols, row)
	if masked["phone"] != "[personal]" || masked["name"] != "Ada" {
		t.Errorf("mask %v", masked)
	}
	for _, name := range []string{"id", "created_at", "xb_x", "pg_y", "Name", "1a", strings.Repeat("a", 64)} {
		if datatables.CheckName("column", name) == nil {
			t.Errorf("%q accepted", name)
		}
	}
}
