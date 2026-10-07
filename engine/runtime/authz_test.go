package runtime_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// member adds a user holding roles in the test tenant.
func member(t *testing.T, e *rt.Env, roles ...string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	inTenant(t, e, `INSERT INTO users (id, email, name) VALUES ($1, $2, 'm')`, id, id.String()+"@acme.test")
	for _, role := range roles {
		inTenant(t, e, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3)`, e.Tenant, id, role)
	}
	return id
}

func inTenant(t *testing.T, e *rt.Env, q string, args ...any) {
	t.Helper()
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, q, args...)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

const officerApproval = `{"id":"ok","type":"approval","config":{"role":"officer"}}`

// A run started with an API key is its owner's: the owner cannot approve
// it (maker-checker), though someone else can.
func TestMakerThroughAPIKeyCannotApprove(t *testing.T) {
	e := rt.New(t)
	maker, checker := member(t, e, "officer"), member(t, e, "officer")
	key := uuid.Must(uuid.NewV7())
	inTenant(t, e, `INSERT INTO api_keys (id, tenant_id, name, key_hash, prefix, permissions, created_by, owner_id, expires_at)
		VALUES ($1, $2, 'ci', $3, 'tsk_key_x', '{run.start}', $4, $5, now() + interval '1 day')`, key, e.Tenant, append(key[:], key[:]...), maker.String(), maker)
	wf := e.Publish(t, wfDoc(officerApproval, ""))
	ref, _, err := e.Store.StartRun(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod",
		Trigger: map[string]any{}, StartedBy: "key:" + key.String()})
	if err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	_, err = e.Store.VoteApproval(ctx, ref, "ok", runtime.Vote{UserID: maker, Roles: []string{"officer"}, Decision: "approved", Channel: "web"})
	if !errors.Is(err, runtime.ErrNotAllowed) {
		t.Fatalf("the key's owner approved their own run: %v", err)
	}
	res, err := e.Store.VoteApproval(ctx, ref, "ok", runtime.Vote{UserID: checker, Roles: []string{"officer"}, Decision: "approved", Channel: "web"})
	if err != nil || res.Status != "approved" {
		t.Fatalf("checker: %v %v", res, err)
	}
}

// A delegation stops counting once the delegator no longer holds the role.
func TestDelegationNeedsTheDelegatorsRole(t *testing.T) {
	e := rt.New(t)
	from, to := member(t, e, "officer"), member(t, e, "viewer")
	inTenant(t, e, `INSERT INTO delegations (id, tenant_id, from_user, to_user, roles, starts_at, ends_at, reason, created_by)
		VALUES ($1, $2, $3, $4, '{officer}', now() - interval '1 hour', now() + interval '1 day', 'leave', $5)`, uuid.Must(uuid.NewV7()), e.Tenant, from, to, from.String())
	wf := e.Publish(t, wfDoc(officerApproval, ""))
	vote := runtime.Vote{UserID: to, Roles: []string{"viewer"}, Decision: "approved", Channel: "web"}

	first := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if res, err := e.Store.VoteApproval(ctx, first, "ok", vote); err != nil || res.Status != "approved" {
		t.Fatalf("under the delegation: %v %v", res, err)
	}

	inTenant(t, e, `DELETE FROM memberships WHERE user_id = $1 AND role = 'officer'`, from)
	second := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if _, err := e.Store.VoteApproval(ctx, second, "ok", vote); !errors.Is(err, runtime.ErrNotAllowed) {
		t.Fatalf("a delegation outlived the delegator's role: %v", err)
	}
}

// Workflows cannot read the platform's own secrets, by expression or from
// a code step.
func TestWorkflowsCannotReadReservedSecrets(t *testing.T) {
	e := rt.New(t)
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"webhook_wf_orders", "git_credentials", "git_webhook_secret", "api_token"} {
		e.Secrets[name] = "s3cret"
	}
	start := func(steps string) string {
		t.Helper()
		ref := e.Start(t, e.Publish(t, wfDoc(steps, "")), map[string]any{})
		e.Drain(t)
		return e.Status(t, ref)
	}
	for _, name := range []string{"webhook_wf_orders", "git_credentials", "git_webhook_secret"} {
		if st := start(`{"id":"post","type":"http","retry":{"max":0},"config":{"method":"GET","url":"` + srv.URL + `","headers":{"Authorization":"=secrets.` + name + `"}}}`); st != "failed" || hit {
			t.Errorf("expression read %s: %s (request sent: %v)", name, st, hit)
		}
		if st := start(`{"id":"calc","type":"code","config":{"language":"javascript","secrets":["` + name + `"],"source":"export default (i, host) => host.secret('` + name + `')"}}`); st != "failed" {
			t.Errorf("code step read %s: %s", name, st)
		}
	}
	// An ordinary secret still resolves.
	if st := start(`{"id":"post","type":"http","retry":{"max":0},"config":{"method":"GET","url":"` + srv.URL + `","headers":{"Authorization":"=secrets.api_token"}}}`); st != "completed" || !hit {
		t.Errorf("ordinary secret: %s", st)
	}
}
