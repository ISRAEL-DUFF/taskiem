package pgdock_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/pgdock"
	"github.com/israel-duff/taskiem/connectors/pgdock/pgdocktest"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

var ctx = context.Background()

const (
	project = "7d2c9a10-5b4e-4f00-9c11-0000000000a1"
	other   = "7d2c9a10-5b4e-4f00-9c11-0000000000b2"
	token   = "pgd_test_restricted"
	secret  = "xb_sec_test_shop"
)

func setup(t *testing.T) (*pgdocktest.Fake, *connector.Connector, map[string]string) {
	t.Helper()
	f := pgdocktest.New(t)
	f.AddProject(project, "shop")
	f.AddProject(other, "blog")
	f.AddTable(project, "orders", []string{"id"}, "id", "customer", "phone", "status", "total")
	f.AddToken(pgdocktest.Token{Secret: token, ID: "tok-1", Name: "taskiem", Scopes: []string{"read", "write"}, ProjectIDs: []string{project}})
	f.AddSecretKey(project, secret)
	c := pgdock.New(pgdock.Options{BaseURL: f.URL})
	return f, c, map[string]string{"token": token, "project_id": project, "secret_key": secret, "api_url": f.APIURL(project)}
}

func call(t *testing.T, c *connector.Connector, creds map[string]string, action string, in map[string]any) (map[string]any, error) {
	t.Helper()
	resp, err := c.Actions[action].Execute(ctx, connector.Request{Input: in, Credentials: creds, HTTP: http.DefaultClient, Attempt: 1})
	if err != nil {
		return nil, err
	}
	return resp.Output.(map[string]any), nil
}

// write calls a write action as the engine does: with its derived
// idempotency key, on the given attempt.
func write(t *testing.T, c *connector.Connector, creds map[string]string, action, key string, attempt int, in map[string]any) (map[string]any, error) {
	t.Helper()
	resp, err := c.Actions[action].Execute(ctx, connector.Request{Input: in, Credentials: creds, HTTP: http.DefaultClient, IdempotencyKey: key, KeyFirstSent: time.Now(), Attempt: attempt})
	if err != nil {
		return nil, err
	}
	return resp.Output.(map[string]any), nil
}

var writeActions = []string{"insert_rows", "upsert_rows", "update_rows", "delete_rows", "call_function"}

func TestManifest(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(pgdock.New(pgdock.Options{BaseURL: "https://pgdock.example.com"})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("pgdock@1")
	if !ok {
		t.Fatal("pgdock@1 not registered")
	}
	if c.Manifest.Name != "Xenibase" || c.Manifest.Version != "1.1.0" {
		t.Errorf("name %q version %q", c.Manifest.Name, c.Manifest.Version)
	}
	for name, a := range c.Manifest.Actions {
		if slices.Contains(writeActions, name) {
			// Writes carry the engine's key in Idempotency-Key, which
			// Xenibase honours for 24 hours (P1-G2).
			sp := a.Idempotency
			if a.Class != effects.IdempotentWrite || sp == nil || sp.Field != "header:Idempotency-Key" || sp.Encoding != effects.Base64URL || sp.Length != 22 || sp.Prefix != "tsk-" ||
				sp.Limits == nil || sp.Limits.MaxLength != 255 || sp.Limits.Charset != "A-Za-z0-9_-" {
				t.Errorf("%s: class %s, idempotency %+v", name, a.Class, sp)
			}
			if key, err := sp.Key(effects.KeyInput{TenantID: "t", Seed: "r", StepID: "s"}); err != nil || len(key) != 26 {
				t.Errorf("%s key %q %v", name, key, err)
			}
			continue
		}
		if a.Class != effects.Read || a.Idempotency != nil {
			t.Errorf("%s is %s", name, a.Class)
		}
	}
	tr := c.Manifest.Triggers["row_changed"]
	if !tr.Remote() || tr.Verify.Scheme != "hmac_sha256_timestamped" || tr.Verify.SignatureFormat != "t_v1" {
		t.Errorf("row_changed: %+v", tr)
	}
	hosts := c.Manifest.HostsFor(map[string]string{"base_url": "https://db.acme.example", "api_url": "https://k7f3m2q9.api.xenibase.ng"})
	if !slices.Equal(hosts, []string{"pgdock.example.com", "db.acme.example", "k7f3m2q9.api.xenibase.ng"}) {
		t.Errorf("hosts %v", hosts)
	}
	if h := pgdock.New(pgdock.Options{}).Manifest.HostsFor(nil); len(h) != 0 {
		t.Errorf("no platform Xenibase and no server URL still reaches %v", h)
	}
	var fields []string
	for _, f := range c.Manifest.Auth.Fields {
		fields = append(fields, f.Key)
		if f.Key == "secret_key" && !f.Secret || f.Key == "api_url" && f.Secret {
			t.Errorf("field %+v", f)
		}
	}
	if !slices.Equal(fields, []string{"token", "project_id", "org_id", "base_url", "secret_key", "api_url"}) {
		t.Errorf("fields %v", fields)
	}
}

func TestConnectionTestNamesOrganisationAndProjects(t *testing.T) {
	f, c, creds := setup(t)
	out, err := call(t, c, creds, "test_connection", nil)
	if err != nil {
		t.Fatal(err)
	}
	org := out["organisation"].(map[string]any)
	if org["name"] != "Acme Foods" || out["project"].(map[string]any)["name"] != "shop" || out["restricted"] != true {
		t.Errorf("%v", out)
	}
	if ps := out["projects"].([]any); len(ps) != 1 {
		t.Errorf("a restricted token names only its projects: %v", ps)
	}
	if w := out["warnings"].([]any); len(w) != 0 {
		t.Errorf("warnings for a restricted read+write token with the project's key and URL: %v", w)
	}
	if out["api_url"] != f.APIURL(project) {
		t.Errorf("api_url %v", out["api_url"])
	}
	if !slices.Contains(f.Requests, "GET /api/v1/me") || !slices.Contains(f.Requests, "GET /api/v1/projects/"+project+"/services") {
		t.Errorf("requests %v", f.Requests)
	}

	// A token for every project, without write, close to expiry: warned.
	f.AddToken(pgdocktest.Token{Secret: "pgd_wide", Scopes: []string{"read"}, ExpiresAt: time.Now().Add(48 * time.Hour)})
	wide := map[string]string{"token": "pgd_wide", "project_id": project, "secret_key": secret, "api_url": f.APIURL(project)}
	out, err = call(t, c, wide, "test_connection", nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := out["warnings"].([]any); len(w) != 3 || out["restricted"] != false || len(out["projects"].([]any)) != 2 {
		t.Errorf("%v", out)
	}

	// The data API: a URL that is not the project's, no secret key, and
	// services that are off are warnings, never failures.
	warnings := func(creds map[string]string) string {
		t.Helper()
		out, err := call(t, c, creds, "test_connection", nil)
		if err != nil {
			t.Fatalf("%v: %v", creds, err)
		}
		var b strings.Builder
		for _, w := range out["warnings"].([]any) {
			b.WriteString(w.(string) + "\n")
		}
		return b.String()
	}
	if w := warnings(map[string]string{"token": token, "project_id": project, "secret_key": secret, "api_url": "https://other.api.xenibase.ng"}); !strings.Contains(w, "is not the project's") || strings.Contains(w, "secret key") {
		t.Errorf("other URL: %q", w)
	}
	if w := warnings(map[string]string{"token": token, "project_id": project}); !strings.Contains(w, "no data API URL") || !strings.Contains(w, "no project secret key") {
		t.Errorf("no data API fields: %q", w)
	}
	f.AddToken(pgdocktest.Token{Secret: "pgd_blog", Scopes: []string{"read", "write"}, ProjectIDs: []string{other}})
	if w := warnings(map[string]string{"token": "pgd_blog", "project_id": other, "secret_key": "xb_sec_x", "api_url": "https://x.api.xenibase.ng"}); !strings.Contains(w, "backend services are off") {
		t.Errorf("services off: %q", w)
	}
	f.Fail(pgdocktest.Failure{Method: http.MethodGet, PathPrefix: "/api/v1/projects/" + project + "/services", Status: 503, RetryAfter: 10})
	if w := warnings(creds); !strings.Contains(w, "could not read the project's backend services") {
		t.Errorf("services unavailable: %q", w)
	}

	// The connection's project is not one the token reaches.
	_, err = call(t, c, map[string]string{"token": token, "project_id": other}, "test_connection", nil)
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "not one this token reaches") {
		t.Errorf("other project: %v", err)
	}
	// Another organisation than the connection says.
	_, err = call(t, c, map[string]string{"token": token, "project_id": project, "org_id": "00000000-0000-4000-8000-00000000dead"}, "test_connection", nil)
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "acts in organisation") {
		t.Errorf("org mismatch: %v", err)
	}
	// A bad token.
	_, err = call(t, c, map[string]string{"token": "pgd_nope", "project_id": project}, "test_connection", nil)
	var pe *pgdock.Error
	if !errors.As(err, &pe) || pe.Class != pgdock.ClassAuth || effects.Classify(err) != effects.KindFatal {
		t.Errorf("bad token: %v", err)
	}
}

func TestServerURL(t *testing.T) {
	_, _, creds := setup(t)
	none := pgdock.New(pgdock.Options{})
	if _, err := call(t, none, creds, "test_connection", nil); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "no Xenibase server") {
		t.Errorf("no server: %v", err)
	}
	creds["base_url"] = "http://pgdock.internal"
	if _, err := call(t, none, creds, "test_connection", nil); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "https") {
		t.Errorf("plain http: %v", err)
	}
}

func TestQueryRows(t *testing.T) {
	f, c, creds := setup(t)
	tx := f.Begin(project)
	for i, st := range []string{"new", "paid", "paid", "new", "paid"} {
		tx.Insert("orders", map[string]any{"id": i + 1, "customer": "c" + string(rune('a'+i)), "status": st, "total": (i + 1) * 1000})
	}
	tx.Commit()
	in := map[string]any{"table": "orders",
		"filters": []any{map[string]any{"column": "status", "op": "eq", "value": "paid"}, map[string]any{"column": "total", "op": "gte", "value": 2000}},
		"limit":   float64(2)}
	out, err := call(t, c, creds, "query_rows", in)
	if err != nil {
		t.Fatal(err)
	}
	rows := out["rows"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["id"] != "2" || rows[1].(map[string]any)["id"] != "3" || out["next"] == "" {
		t.Fatalf("first page: %v", out)
	}
	in["after"] = out["next"]
	out, err = call(t, c, creds, "query_rows", in)
	if err != nil {
		t.Fatal(err)
	}
	if rows := out["rows"].([]any); len(rows) != 1 || rows[0].(map[string]any)["id"] != "5" || out["next"] != "" {
		t.Errorf("second page: %v", out)
	}
	// Order, in, null checks; never a raw where condition.
	out, err = call(t, c, creds, "query_rows", map[string]any{"table": "orders", "order": []any{map[string]any{"column": "total", "desc": true}},
		"filters": []any{map[string]any{"column": "customer", "op": "in", "values": []any{"ca", "ce"}}, map[string]any{"column": "phone", "op": "is_null"}}})
	if err != nil {
		t.Fatal(err)
	}
	if rows := out["rows"].([]any); len(rows) != 2 || rows[0].(map[string]any)["id"] != "5" || rows[0].(map[string]any)["phone"] != nil {
		t.Errorf("ordered: %v", out)
	}
	// A value that looks like SQL stays a value.
	out, err = call(t, c, creds, "query_rows", map[string]any{"table": "orders", "filters": []any{map[string]any{"column": "customer", "op": "eq", "value": "x' OR '1'='1"}}})
	if err != nil || len(out["rows"].([]any)) != 0 {
		t.Errorf("injection-shaped value: %v %v", out, err)
	}
	for _, r := range f.Requests {
		if strings.Contains(r, "/sql") {
			t.Errorf("the SQL endpoint was called: %s", r)
		}
	}
	// Bad input is refused before anything is sent.
	n := len(f.Requests)
	for _, bad := range []map[string]any{
		{"table": "orders", "limit": float64(1001)},
		{"table": "orders", "filters": []any{map[string]any{"column": "id", "op": "like", "value": "1"}}},
		{"table": "orders", "filters": []any{map[string]any{"column": "id", "op": "in", "value": "1"}}},
		{"table": "orders", "filters": []any{map[string]any{"column": "id", "op": "eq"}}},
	} {
		if _, err := call(t, c, creds, "query_rows", bad); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if len(f.Requests) != n {
		t.Error("bad input reached Xenibase")
	}
}

func TestGetRow(t *testing.T) {
	f, c, creds := setup(t)
	f.Begin(project).Insert("orders", map[string]any{"id": 7, "status": "paid"}).Commit()
	out, err := call(t, c, creds, "get_row", map[string]any{"table": "orders", "key": map[string]any{"id": float64(7)}})
	if err != nil || out["found"] != true || out["row"].(map[string]any)["status"] != "paid" {
		t.Errorf("%v %v", out, err)
	}
	out, err = call(t, c, creds, "get_row", map[string]any{"table": "orders", "key": map[string]any{"id": float64(8)}})
	if err != nil || out["found"] != false {
		t.Errorf("missing row: %v %v", out, err)
	}
}

func TestErrorMapping(t *testing.T) {
	f, c, creds := setup(t)
	q := map[string]any{"table": "orders"}
	rows := "/api/v1/projects/" + project + "/tables/"
	for _, tc := range []struct {
		fail  pgdocktest.Failure
		class string
		kind  effects.ErrorKind
		retry time.Duration
	}{
		{pgdocktest.Failure{Status: 429, RetryAfter: 30}, pgdock.ClassRateLimited, effects.KindRetryable, 30 * time.Second},
		{pgdocktest.Failure{Status: 503, RetryAfter: 5}, pgdock.ClassUnavailable, effects.KindRetryable, 5 * time.Second},
		{pgdocktest.Failure{Status: 500}, pgdock.ClassServer, effects.KindUnknownOutcome, 0},
		{pgdocktest.Failure{Status: 504}, pgdock.ClassTimeout, effects.KindUnknownOutcome, 0},
		{pgdocktest.Failure{Status: 400, Body: map[string]any{"code": "invalid_filter", "message": "no column x"}}, pgdock.ClassValidation, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 401}, pgdock.ClassAuth, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 403, Body: map[string]any{"code": "reauth_required", "message": "re-authenticate"}}, pgdock.ClassPermission, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 404}, pgdock.ClassNotFound, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 409}, pgdock.ClassConflict, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 402, Body: map[string]any{"code": "quota_exceeded", "message": "over", "quota": map[string]any{"name": "rows"}}}, pgdock.ClassQuota, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 400, Body: map[string]any{"code": "sql_error", "message": "", "sql_error": map[string]any{"code": "40001", "message": "could not serialise access"}}}, pgdock.ClassConflict, effects.KindRetryable, 0},
		{pgdocktest.Failure{Status: 400, Body: map[string]any{"code": "sql_error", "message": "", "sql_error": map[string]any{"code": "40P01", "message": "deadlock"}}}, pgdock.ClassConflict, effects.KindRetryable, 0},
		{pgdocktest.Failure{Status: 400, Body: map[string]any{"code": "sql_error", "message": "", "sql_error": map[string]any{"code": "57014", "message": "canceling statement due to statement timeout"}}}, pgdock.ClassTimeout, effects.KindRetryable, 0},
		{pgdocktest.Failure{Status: 400, Body: map[string]any{"code": "sql_error", "message": "", "sql_error": map[string]any{"code": "23505", "message": "duplicate key"}}}, pgdock.ClassConflict, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 400, Body: map[string]any{"code": "sql_error", "message": "", "sql_error": map[string]any{"code": "42501", "message": "permission denied"}}}, pgdock.ClassPermission, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 500, Body: map[string]any{"code": "sql_error", "message": "", "sql_error": map[string]any{"code": "53300", "message": "too many connections"}}}, pgdock.ClassUnavailable, effects.KindRetryable, 0},
	} {
		tc.fail.Method, tc.fail.PathPrefix = http.MethodGet, rows
		f.Fail(tc.fail)
		_, err := call(t, c, creds, "query_rows", q)
		var pe *pgdock.Error
		if !errors.As(err, &pe) || pe.Class != tc.class || effects.Classify(err) != tc.kind || pe.RetryAfterDelay() != tc.retry {
			t.Errorf("%d %v: %v (class %v, kind %v)", tc.fail.Status, tc.fail.Body, err, pe, effects.Classify(err))
		}
		if pe != nil && pe.Class == pgdock.ClassNotFound && !errors.Is(err, connector.ErrNotFound) {
			t.Error("a 404 does not read as not found")
		}
	}
}

// A token Xenibase revoked or let expire answers 401 invalid_token
// (docs/errors.md); the error says so to the engine, which expires an
// OAuth connection on it (decision 0034). Any other 401 does not.
func TestRevokedTokenReadsAsRejected(t *testing.T) {
	f, c, creds := setup(t)
	f.RevokeToken(token)
	_, err := call(t, c, creds, "test_connection", map[string]any{})
	var pe *pgdock.Error
	if !errors.As(err, &pe) || pe.Class != pgdock.ClassAuth || pe.Code != "invalid_token" || !errors.Is(err, connector.ErrCredentialsRejected) || effects.Classify(err) != effects.KindFatal {
		t.Fatalf("revoked token: %v", err)
	}
	f.Fail(pgdocktest.Failure{Method: http.MethodGet, PathPrefix: "/api/v1/me", Status: 401, Body: map[string]any{"code": "unauthenticated", "message": "no token"}})
	if _, err := call(t, c, creds, "test_connection", map[string]any{}); errors.Is(err, connector.ErrCredentialsRejected) {
		t.Fatalf("a 401 without invalid_token reads as rejected credentials: %v", err)
	}
}

func spec(name, hookURL string) connector.RemoteSpec {
	return connector.RemoteSpec{Name: name, URL: hookURL, Events: []string{"INSERT", "TEST"}, Options: map[string]any{"tables": []any{"orders"}}}
}

func TestRegistrar(t *testing.T) {
	f, c, creds := setup(t)
	reg := c.Registrars["row_changed"]
	rc := connector.RemoteCall{Credentials: creds, HTTP: http.DefaultClient}
	if _, ok, err := reg.Find(ctx, rc, "taskiem-1"); err != nil || ok {
		t.Fatalf("find before create: %v %v", ok, err)
	}
	st, err := reg.Create(ctx, rc, spec("taskiem-1", "https://hooks.example/x"))
	if err != nil || st.ID == "" || st.Secret == "" || st.Status != connector.RemoteHealthy {
		t.Fatalf("create: %+v %v", st, err)
	}
	h := f.Hook(project, st.ID)
	if h.Name != "taskiem-1" || !slices.Equal(h.Events, []string{"INSERT"}) || !slices.Equal(h.Tables, []string{"orders"}) || h.Secret != st.Secret ||
		h.Description != "Managed by Taskiem" || h.Metadata["taskiem_subscription"] != "1" {
		t.Errorf("webhook %+v", h)
	}
	found, ok, err := reg.Find(ctx, rc, "taskiem-1")
	if err != nil || !ok || found.ID != st.ID || found.Secret != "" {
		t.Errorf("find: %+v %v %v", found, ok, err)
	}
	// A person renamed it in Xenibase: the metadata tag still finds it.
	rename, _ := http.NewRequest(http.MethodPatch, f.URL+"/api/v1/projects/"+project+"/webhooks/"+st.ID, strings.NewReader(`{"name":"orders to whatsapp"}`))
	rename.Header.Set("Authorization", "Bearer "+token)
	if resp, err := http.DefaultClient.Do(rename); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("rename: %v %v", resp, err)
	} else {
		_ = resp.Body.Close()
	}
	if found, ok, err := reg.Find(ctx, rc, "taskiem-1"); err != nil || !ok || found.ID != st.ID {
		t.Errorf("find after a rename: %+v %v %v", found, ok, err)
	}
	// A webhook created before tags were sent is found by its name.
	f.Begin(project).Commit()
	untagged, _ := http.NewRequest(http.MethodPost, f.URL+"/api/v1/projects/"+project+"/webhooks", strings.NewReader(`{"name":"taskiem-0","tables":["orders"],"events":["INSERT"],"url":"https://hooks.example/z"}`))
	untagged.Header.Set("Authorization", "Bearer "+token)
	if resp, err := http.DefaultClient.Do(untagged); err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("untagged create: %v %v", resp, err)
	} else {
		_ = resp.Body.Close()
	}
	if found, ok, err := reg.Find(ctx, rc, "taskiem-0"); err != nil || !ok || found.Name != "taskiem-0" {
		t.Errorf("find by name: %+v %v %v", found, ok, err)
	}
	// Update: columns, events, and repair of a broken or paused webhook.
	f.SetStatus(project, st.ID, "broken", "triggers dropped")
	s2 := spec("taskiem-1", "https://hooks.example/y")
	s2.Events, s2.Options["columns"] = nil, []any{"status"}
	up, err := reg.Update(ctx, rc, st.ID, s2)
	if err != nil || up.Status != connector.RemoteHealthy {
		t.Fatalf("update: %+v %v", up, err)
	}
	h = f.Hook(project, st.ID)
	if h.URL != "https://hooks.example/y" || len(h.Events) != 3 || !slices.Equal(h.Columns, []string{"status"}) || h.Name != "taskiem-1" || h.Metadata["taskiem_subscription"] != "1" {
		t.Errorf("updated %+v", h)
	}
	f.SetStatus(project, st.ID, "paused", "50 failures in a row")
	if got, err := reg.Get(ctx, rc, st.ID); err != nil || got.Status != connector.RemotePaused || got.Reason != "50 failures in a row" {
		t.Errorf("paused: %+v %v", got, err)
	}
	if up, err = reg.Update(ctx, rc, st.ID, spec("taskiem-1", "https://hooks.example/y")); err != nil || up.Status != connector.RemoteHealthy || f.Hook(project, st.ID).Columns != nil {
		t.Errorf("resume and clear columns: %+v %v %+v", up, err, f.Hook(project, st.ID))
	}
	sec, err := reg.RotateSecret(ctx, rc, st.ID)
	if err != nil || sec == st.Secret || f.Hook(project, st.ID).Secret != sec {
		t.Errorf("rotate: %v", err)
	}
	if err := reg.Delete(ctx, rc, st.ID); err != nil || f.Hook(project, st.ID) != nil {
		t.Errorf("delete: %v", err)
	}
	if err := reg.Delete(ctx, rc, st.ID); err != nil {
		t.Errorf("deleting again: %v", err)
	}
	if _, err := reg.Get(ctx, rc, st.ID); !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("get after delete: %v", err)
	}
	if _, err := reg.Update(ctx, rc, st.ID, spec("taskiem-1", "https://hooks.example/y")); !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("update after delete: %v", err)
	}
	// Without the write scope Xenibase refuses: a permission error.
	f.AddToken(pgdocktest.Token{Secret: "pgd_ro", Scopes: []string{"read"}, ProjectIDs: []string{project}})
	_, err = reg.Create(ctx, connector.RemoteCall{Credentials: map[string]string{"token": "pgd_ro", "project_id": project}, HTTP: http.DefaultClient}, spec("taskiem-2", "https://hooks.example/x"))
	var pe *pgdock.Error
	if !errors.As(err, &pe) || pe.Class != pgdock.ClassPermission {
		t.Errorf("read-only token: %v", err)
	}
	if _, err := reg.Create(ctx, rc, connector.RemoteSpec{Name: "x", URL: "https://h"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no tables: %v", err)
	}
}

func TestEnrichTruncated(t *testing.T) {
	f, c, creds := setup(t)
	f.Begin(project).Insert("orders", map[string]any{"id": 9, "status": "paid", "customer": "ada"}).Commit()
	enrich := c.Enrichers["row_changed"]
	connects := 0
	call := func(opts map[string]any) connector.EventCall {
		return connector.EventCall{Options: opts, Connect: func() (map[string]string, *http.Client, error) {
			connects++
			return creds, http.DefaultClient, nil
		}}
	}
	ev := func(typ string, pk any) map[string]any {
		return map[string]any{"id": "evt_1", "table": "public.orders", "type": typ, "truncated": true, "primary_key": pk}
	}
	// Not asked for: passed through untouched, without reading the connection.
	out, err := enrich(ctx, call(nil), ev("UPDATE", map[string]any{"id": float64(9)}))
	if err != nil || out.(map[string]any)["record"] != nil || connects != 0 {
		t.Errorf("pass through: %v %v", out, err)
	}
	full := map[string]any{"id": "evt_2", "type": "INSERT", "record": map[string]any{"id": float64(1)}}
	if out, _ := enrich(ctx, call(map[string]any{"refetch_truncated": true}), full); out.(map[string]any)["refetched"] != nil || connects != 0 {
		t.Errorf("an event that is not truncated: %v", out)
	}
	on := map[string]any{"refetch_truncated": true}
	out, err = enrich(ctx, call(on), ev("UPDATE", map[string]any{"id": float64(9)}))
	m := out.(map[string]any)
	if err != nil || m["refetched"] != true || m["truncated"] != true || m["record"].(map[string]any)["customer"] != "ada" {
		t.Errorf("refetched: %v %v", out, err)
	}
	for _, c := range []struct {
		ev   map[string]any
		want string
	}{
		{ev("UPDATE", map[string]any{"id": float64(10)}), "no longer exists"},
		{ev("DELETE", map[string]any{"id": float64(9)}), "deleted row"},
		{ev("UPDATE", "9"), "not an object"},
	} {
		out, err := enrich(ctx, call(on), c.ev)
		m := out.(map[string]any)
		if err != nil || m["refetched"] != false || !strings.Contains(m["refetch_error"].(string), c.want) {
			t.Errorf("%v: %v %v", c.ev, out, err)
		}
	}
	// The row came through the data API by its key (Q23), not the rows endpoint.
	for _, r := range f.Requests {
		if strings.Contains(r, "/tables/") {
			t.Errorf("the rows endpoint was used with the data API available: %s", r)
		}
	}
	// Xenibase busy: the delivery is refused, so Xenibase sends it again.
	f.Fail(pgdocktest.Failure{Method: http.MethodGet, PathPrefix: "/edge/", Status: 503})
	if _, err := enrich(ctx, call(on), ev("UPDATE", map[string]any{"id": float64(9)})); effects.Classify(err) != effects.KindRetryable {
		t.Errorf("busy: %v", err)
	}
	// Without the data API credentials, the management rows endpoint.
	delete(creds, "secret_key")
	n := len(f.Requests)
	out, err = enrich(ctx, call(on), ev("UPDATE", map[string]any{"id": float64(9)}))
	if m := out.(map[string]any); err != nil || m["refetched"] != true || m["record"].(map[string]any)["customer"] != "ada" || !strings.Contains(f.Requests[n], "/tables/public/orders/rows") {
		t.Errorf("fallback: %v %v %v", out, err, f.Requests[n:])
	}
	f.Fail(pgdocktest.Failure{Method: http.MethodGet, PathPrefix: "/api/v1/projects/", Status: 503})
	if _, err := enrich(ctx, call(on), ev("UPDATE", map[string]any{"id": float64(9)})); effects.Classify(err) != effects.KindRetryable {
		t.Errorf("busy on the rows endpoint: %v", err)
	}
}

// --- the data API ---

func seedOrders(f *pgdocktest.Fake) {
	tx := f.Begin(project)
	for i, st := range []string{"new", "paid", "paid", "new", "paid"} {
		row := map[string]any{"id": i + 1, "customer": "c" + string(rune('a'+i)), "status": st, "total": (i + 1) * 1000}
		if i%2 == 0 {
			row["phone"] = "+23480000000" + string(rune('0'+i))
		}
		tx.Insert("orders", row)
	}
	tx.Commit()
}

func ids(rows []any) []float64 {
	var out []float64
	for _, r := range rows {
		out = append(out, r.(map[string]any)["id"].(float64))
	}
	return out
}

func TestReadRows(t *testing.T) {
	f, c, creds := setup(t)
	seedOrders(f)
	in := map[string]any{"table": "orders", "limit": float64(2), "count": "exact",
		"filters": []any{map[string]any{"column": "status", "op": "eq", "value": "paid"}, map[string]any{"column": "total", "op": "gte", "value": float64(2000)}}}
	out, err := call(t, c, creds, "read_rows", in)
	if err != nil {
		t.Fatal(err)
	}
	// Values come back typed, unlike query_rows.
	if rows := out["rows"].([]any); !slices.Equal(ids(rows), []float64{2, 3}) || rows[0].(map[string]any)["total"] != float64(2000) || out["next_cursor"] == "" || out["count"] != int64(3) {
		t.Fatalf("first page: %v", out)
	}
	in["cursor"] = out["next_cursor"]
	if out, err = call(t, c, creds, "read_rows", in); err != nil || !slices.Equal(ids(out["rows"].([]any)), []float64{5}) || out["next_cursor"] != "" {
		t.Errorf("second page: %v %v", out, err)
	}
	// Order, in, null and not-null checks, select; the query body, never a where string.
	out, err = call(t, c, creds, "read_rows", map[string]any{"table": "orders", "select": "id,customer", "order": []any{map[string]any{"column": "total", "desc": true}},
		"filters": []any{map[string]any{"column": "customer", "op": "in", "values": []any{"ca", "cc", "ce"}}, map[string]any{"column": "phone", "op": "not_null"}}})
	if err != nil {
		t.Fatal(err)
	}
	if rows := out["rows"].([]any); !slices.Equal(ids(rows), []float64{5, 3, 1}) || len(rows[0].(map[string]any)) != 2 || out["count"] != nil {
		t.Errorf("ordered: %v", out)
	}
	if out, err = call(t, c, creds, "read_rows", map[string]any{"table": "orders", "filters": []any{map[string]any{"column": "phone", "op": "is_null"}, map[string]any{"column": "customer", "op": "ilike", "value": "C%"}}}); err != nil || !slices.Equal(ids(out["rows"].([]any)), []float64{2, 4}) {
		t.Errorf("is_null and ilike: %v %v", out, err)
	}
	// A value that looks like SQL stays a value; nothing matches it.
	if out, err = call(t, c, creds, "read_rows", map[string]any{"table": "orders", "filters": []any{map[string]any{"column": "customer", "op": "eq", "value": "x' OR '1'='1"}}}); err != nil || len(out["rows"].([]any)) != 0 {
		t.Errorf("injection-shaped value: %v %v", out, err)
	}
	for _, r := range f.Requests {
		if strings.HasPrefix(r, "POST /edge/") && !strings.HasSuffix(r, "/data/v1/orders/query") || strings.Contains(r, "/sql") {
			t.Errorf("unexpected request %s", r)
		}
	}
	// Unknown columns are Xenibase's refusal: fatal.
	_, err = call(t, c, creds, "read_rows", map[string]any{"table": "orders", "filters": []any{map[string]any{"column": "nope", "op": "eq", "value": 1}}})
	var pe *pgdock.Error
	if !errors.As(err, &pe) || pe.Code != "unknown_column" || pe.Class != pgdock.ClassValidation || effects.Classify(err) != effects.KindFatal {
		t.Errorf("unknown column: %v", err)
	}
	// Bad input is refused before anything is sent.
	n := len(f.Requests)
	for _, bad := range []map[string]any{
		{"table": "orders", "limit": float64(1001)},
		{"table": "orders; drop table orders"},
		{"table": "orders", "count": "roughly"},
		{"table": "orders", "filters": []any{map[string]any{"column": "id", "op": "between", "value": "1"}}},
		{"table": "orders", "filters": []any{map[string]any{"column": "id", "op": "in", "value": "1"}}},
		{"table": "orders", "order": []any{map[string]any{"column": "id:desc"}}},
	} {
		if _, err := call(t, c, creds, "read_rows", bad); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if len(f.Requests) != n {
		t.Error("bad input reached Xenibase")
	}
}

func TestInsertRows(t *testing.T) {
	f, c, creds := setup(t)
	// A webhook on the table: writes through the data API fire it like
	// any committed change.
	st, err := c.Registrars["row_changed"].Create(ctx, connector.RemoteCall{Credentials: creds, HTTP: http.DefaultClient}, spec("taskiem-1", "https://hooks.example/x"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := write(t, c, creds, "insert_rows", "tsk-k1", 1, map[string]any{"table": "orders", "row": map[string]any{"customer": "Ada", "status": "new", "total": float64(500)}})
	if err != nil {
		t.Fatal(err)
	}
	rows := out["rows"].([]any)
	if out["affected"] != 1 || out["replayed"] != false || len(rows) != 1 || rows[0].(map[string]any)["id"] != float64(1) || rows[0].(map[string]any)["customer"] != "Ada" {
		t.Errorf("%v", out)
	}
	if f.Queued(project, st.ID) != 1 || f.Changes(project, "orders") != 1 {
		t.Errorf("the insert did not go through the outbox: queued %d, changes %d", f.Queued(project, st.ID), f.Changes(project, "orders"))
	}
	out, err = write(t, c, creds, "insert_rows", "tsk-k2", 1, map[string]any{"table": "orders", "return": "minimal", "select": "id",
		"rows": []any{map[string]any{"id": float64(7), "customer": "Bayo"}, map[string]any{"customer": "Chi"}}})
	if err != nil || out["affected"] != 2 || len(out["rows"].([]any)) != 0 {
		t.Errorf("rows, minimal: %v %v", out, err)
	}
	if got := f.Rows(project, "orders"); len(got) != 3 || got[2]["id"] != int64(8) {
		t.Errorf("rows %v", got)
	}
	// A duplicate key is a conflict, fatal, with the SQLSTATE in details.
	_, err = write(t, c, creds, "insert_rows", "tsk-k3", 1, map[string]any{"table": "orders", "row": map[string]any{"id": float64(7)}})
	var pe *pgdock.Error
	if !errors.As(err, &pe) || pe.Code != "unique_violation" || pe.Class != pgdock.ClassConflict || pe.SQLState != "23505" || effects.Classify(err) != effects.KindFatal {
		t.Errorf("duplicate: %v", err)
	}
	// Refused before anything is sent.
	n := len(f.Requests)
	for _, bad := range []map[string]any{
		{"table": "orders"},
		{"table": "orders", "rows": []any{}},
		{"table": "orders", "row": map[string]any{}},
		{"table": "orders", "row": map[string]any{"a": 1}, "rows": []any{map[string]any{"a": 1}}},
		{"table": "orders", "rows": []any{"x"}},
		{"table": "orders", "row": map[string]any{"a": 1}, "return": "all"},
		{"table": "", "row": map[string]any{"a": 1}},
	} {
		if _, err := write(t, c, creds, "insert_rows", "tsk-k9", 1, bad); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if len(f.Requests) != n {
		t.Error("bad input reached Xenibase")
	}
	// Without the secret key or the data API URL, nothing is sent.
	for _, missing := range []string{"secret_key", "api_url"} {
		cr := map[string]string{}
		for k, v := range creds {
			cr[k] = v
		}
		delete(cr, missing)
		if _, err := write(t, c, cr, "insert_rows", "tsk-k9", 1, map[string]any{"table": "orders", "row": map[string]any{"a": 1}}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "the connection has no") {
			t.Errorf("without %s: %v", missing, err)
		}
	}
	creds["api_url"] = "http://xenibase.internal"
	if _, err := write(t, c, creds, "insert_rows", "tsk-k9", 1, map[string]any{"table": "orders", "row": map[string]any{"a": 1}}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "https") {
		t.Errorf("plain http: %v", err)
	}
	if len(f.Requests) != n {
		t.Error("a request without the data API credentials reached Xenibase")
	}
	// A management token is not a key: the data API refuses it.
	creds["api_url"], creds["secret_key"] = f.APIURL(project), token
	_, err = write(t, c, creds, "insert_rows", "tsk-k9", 1, map[string]any{"table": "orders", "row": map[string]any{"customer": "x"}})
	if !errors.As(err, &pe) || pe.Code != "invalid_key" || pe.Class != pgdock.ClassAuth {
		t.Errorf("token as key: %v", err)
	}
}

func TestUpsertRows(t *testing.T) {
	f, c, creds := setup(t)
	seedOrders(f)
	in := map[string]any{"table": "orders", "on_conflict": []any{"customer"},
		"rows": []any{map[string]any{"customer": "ca", "status": "shipped"}, map[string]any{"customer": "zz", "status": "new"}}}
	out, err := write(t, c, creds, "upsert_rows", "tsk-u1", 1, in)
	if err != nil {
		t.Fatal(err)
	}
	rows := out["rows"].([]any)
	if out["affected"] != 2 || len(rows) != 2 || rows[0].(map[string]any)["id"] != float64(1) || rows[0].(map[string]any)["status"] != "shipped" || rows[1].(map[string]any)["id"] != float64(6) {
		t.Errorf("merge: %v", out)
	}
	if got := f.Rows(project, "orders"); len(got) != 6 || got[0]["status"] != "shipped" || got[0]["total"] != 1000 {
		t.Errorf("rows %v", got)
	}
	in["resolution"], in["rows"] = "ignore", []any{map[string]any{"customer": "ca", "status": "lost"}, map[string]any{"customer": "yy"}}
	if out, err = write(t, c, creds, "upsert_rows", "tsk-u2", 1, in); err != nil || out["affected"] != 1 || f.Rows(project, "orders")[0]["status"] != "shipped" {
		t.Errorf("ignore: %v %v", out, err)
	}
	n := len(f.Requests)
	for _, bad := range []map[string]any{
		{"table": "orders", "row": map[string]any{"a": 1}},
		{"table": "orders", "row": map[string]any{"a": 1}, "on_conflict": []any{}},
		{"table": "orders", "row": map[string]any{"a": 1}, "on_conflict": []any{"a,b"}},
		{"table": "orders", "row": map[string]any{"a": 1}, "on_conflict": []any{"a"}, "resolution": "replace"},
	} {
		if _, err := write(t, c, creds, "upsert_rows", "tsk-u9", 1, bad); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if len(f.Requests) != n {
		t.Error("bad input reached Xenibase")
	}
}

func TestUpdateRows(t *testing.T) {
	f, c, creds := setup(t)
	seedOrders(f)
	// By key: PATCH /data/v1/orders/3.
	out, err := write(t, c, creds, "update_rows", "tsk-w1", 1, map[string]any{"table": "orders", "key": float64(3), "set": map[string]any{"status": "shipped"}})
	if err != nil {
		t.Fatal(err)
	}
	if out["affected"] != 1 || out["replayed"] != false || out["rows"].([]any)[0].(map[string]any)["status"] != "shipped" || f.Rows(project, "orders")[2]["status"] != "shipped" {
		t.Errorf("by key: %v", out)
	}
	if last := f.Requests[len(f.Requests)-1]; !strings.HasSuffix(last, "/data/v1/orders/3") || !strings.HasPrefix(last, "PATCH ") {
		t.Errorf("request %s", last)
	}
	// A key that matches nothing changes nothing.
	if out, err = write(t, c, creds, "update_rows", "tsk-w2", 1, map[string]any{"table": "orders", "key": "99", "set": map[string]any{"status": "x"}}); err != nil || out["affected"] != 0 {
		t.Errorf("missing key: %v %v", out, err)
	}
	// By filters: PATCH /data/v1/orders?where=…
	out, err = write(t, c, creds, "update_rows", "tsk-w3", 1, map[string]any{"table": "orders", "return": "minimal",
		"filters": []any{map[string]any{"column": "status", "op": "eq", "value": "paid"}, map[string]any{"column": "phone", "op": "is_null"}}, "set": map[string]any{"status": "unpaid"}})
	if err != nil || out["affected"] != 1 || len(out["rows"].([]any)) != 0 || f.Rows(project, "orders")[1]["status"] != "unpaid" {
		t.Errorf("by filters: %v %v", out, err)
	}
	// Over max_affected: Xenibase refuses and nothing changes.
	before := f.Changes(project, "orders")
	_, err = write(t, c, creds, "update_rows", "tsk-w4", 1, map[string]any{"table": "orders", "max_affected": float64(2),
		"filters": []any{map[string]any{"column": "id", "op": "gte", "value": float64(1)}}, "set": map[string]any{"status": "bulk"}})
	var pe *pgdock.Error
	if !errors.As(err, &pe) || pe.Code != "too_many_rows" || effects.Classify(err) != effects.KindFatal || f.Changes(project, "orders") != before {
		t.Errorf("too many rows: %v (changes %d, was %d)", err, f.Changes(project, "orders"), before)
	}
	// Neither key nor filters, both, an empty filter list, not_null, a
	// bad max_affected, or no set: refused before anything is sent.
	n := len(f.Requests)
	for _, bad := range []map[string]any{
		{"table": "orders", "set": map[string]any{"status": "x"}},
		{"table": "orders", "set": map[string]any{"status": "x"}, "filters": []any{}},
		{"table": "orders", "set": map[string]any{"status": "x"}, "key": nil},
		{"table": "orders", "set": map[string]any{"status": "x"}, "key": float64(1), "filters": []any{map[string]any{"column": "id", "op": "eq", "value": 1}}},
		{"table": "orders", "set": map[string]any{"status": "x"}, "key": map[string]any{"id": 1}},
		{"table": "orders", "set": map[string]any{"status": "x"}, "filters": []any{map[string]any{"column": "phone", "op": "not_null"}}},
		{"table": "orders", "set": map[string]any{"status": "x"}, "key": float64(1), "max_affected": float64(0)},
		{"table": "orders", "set": map[string]any{}, "key": float64(1)},
		{"table": "orders", "key": float64(1)},
	} {
		if _, err := write(t, c, creds, "update_rows", "tsk-w9", 1, bad); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if len(f.Requests) != n {
		t.Errorf("bad input reached Xenibase: %v", f.Requests[n:])
	}
}

func TestDeleteRows(t *testing.T) {
	f, c, creds := setup(t)
	seedOrders(f)
	out, err := write(t, c, creds, "delete_rows", "tsk-d1", 1, map[string]any{"table": "orders", "key": float64(2)})
	if err != nil || out["affected"] != 1 || out["replayed"] != false || out["rows"] != nil || len(f.Rows(project, "orders")) != 4 {
		t.Errorf("by key: %v %v", out, err)
	}
	out, err = write(t, c, creds, "delete_rows", "tsk-d2", 1, map[string]any{"table": "orders", "filters": []any{map[string]any{"column": "customer", "op": "in", "values": []any{"ca", "ce"}}}})
	if err != nil || out["affected"] != 2 || len(f.Rows(project, "orders")) != 2 {
		t.Errorf("by filters: %v %v", out, err)
	}
	_, err = write(t, c, creds, "delete_rows", "tsk-d3", 1, map[string]any{"table": "orders", "max_affected": float64(1), "filters": []any{map[string]any{"column": "id", "op": "gt", "value": float64(0)}}})
	var pe *pgdock.Error
	if !errors.As(err, &pe) || pe.Code != "too_many_rows" || len(f.Rows(project, "orders")) != 2 {
		t.Errorf("too many rows: %v", err)
	}
	n := len(f.Requests)
	if _, err := write(t, c, creds, "delete_rows", "tsk-d9", 1, map[string]any{"table": "orders"}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "key") || len(f.Requests) != n {
		t.Errorf("no key or filters: %v", err)
	}
}

func TestCallFunction(t *testing.T) {
	f, c, creds := setup(t)
	seedOrders(f)
	f.AddFunction(project, "total_for", func(args map[string]any) (any, error) {
		st, _ := args["status"].(string)
		sum := 0
		for _, r := range f.Rows(project, "orders") {
			if r["status"] == st {
				sum += r["total"].(int)
			}
		}
		return sum, nil
	})
	f.AddFunction(project, "close_cart", func(args map[string]any) (any, error) {
		return nil, errors.New("cart 4 is already closed")
	})
	out, err := write(t, c, creds, "call_function", "tsk-f1", 1, map[string]any{"function": "total_for", "args": map[string]any{"status": "paid"}})
	if err != nil || out["data"] != float64(10000) || out["replayed"] != false {
		t.Errorf("%v %v", out, err)
	}
	_, err = write(t, c, creds, "call_function", "tsk-f2", 1, map[string]any{"function": "close_cart", "args": map[string]any{"cart_id": 4}})
	var pe *pgdock.Error
	if !errors.As(err, &pe) || pe.Code != "database_error" || pe.SQLState != "P0001" || effects.Classify(err) != effects.KindFatal {
		t.Errorf("function error: %v", err)
	}
	if _, err = write(t, c, creds, "call_function", "tsk-f3", 1, map[string]any{"function": "nope"}); !errors.As(err, &pe) || pe.Code != "unknown_function" || !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("unknown function: %v", err)
	}
	n := len(f.Requests)
	for _, bad := range []map[string]any{{"function": "a b"}, {"function": "total_for", "args": "x"}, {}} {
		if _, err := write(t, c, creds, "call_function", "tsk-f9", 1, bad); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if len(f.Requests) != n {
		t.Error("bad input reached Xenibase")
	}
}

// The engine retries an idempotent write with the same key after an
// unknown outcome; Xenibase answers from the first attempt.
func TestIdempotentReplay(t *testing.T) {
	f, c, creds := setup(t)
	in := map[string]any{"table": "orders", "row": map[string]any{"customer": "Ada", "status": "new"}}
	first, err := write(t, c, creds, "insert_rows", "tsk-same", 1, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := write(t, c, creds, "insert_rows", "tsk-same", 2, in)
	if err != nil {
		t.Fatal(err)
	}
	if again["replayed"] != true || first["replayed"] != false || again["affected"] != 1 || len(f.Rows(project, "orders")) != 1 || f.Changes(project, "orders") != 1 {
		t.Errorf("replay: %v then %v; rows %v", first, again, f.Rows(project, "orders"))
	}
	// The answer lost after the commit: an unknown outcome, then the retry
	// with the same key replays it and the row is still one.
	f.Fail(pgdocktest.Failure{Method: http.MethodPatch, PathPrefix: "/edge/", LoseAnswer: true})
	up := map[string]any{"table": "orders", "key": float64(1), "set": map[string]any{"status": "notified"}}
	_, err = write(t, c, creds, "update_rows", "tsk-lost", 1, up)
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Fatalf("lost answer: %v", err)
	}
	out, err := write(t, c, creds, "update_rows", "tsk-lost", 2, up)
	if err != nil || out["replayed"] != true || out["affected"] != 1 || f.Changes(project, "orders") != 2 || f.Rows(project, "orders")[0]["status"] != "notified" {
		t.Errorf("retry after a lost answer: %v %v; changes %d", out, err, f.Changes(project, "orders"))
	}
	// The same key for a different request: refused, nothing written.
	_, err = write(t, c, creds, "insert_rows", "tsk-same", 1, map[string]any{"table": "orders", "row": map[string]any{"customer": "Bayo"}})
	var pe *pgdock.Error
	if !errors.As(err, &pe) || pe.Code != "idempotency_key_reused" || pe.Class != pgdock.ClassConflict || effects.Classify(err) != effects.KindFatal || len(f.Rows(project, "orders")) != 1 {
		t.Errorf("key reused: %v", err)
	}
	// A refused write records no key: the same key then runs a corrected request.
	if _, err := write(t, c, creds, "insert_rows", "tsk-fix", 1, map[string]any{"table": "orders", "row": map[string]any{"id": float64(1)}}); !errors.As(err, &pe) || pe.Code != "unique_violation" {
		t.Fatalf("duplicate: %v", err)
	}
	if out, err := write(t, c, creds, "insert_rows", "tsk-fix", 2, map[string]any{"table": "orders", "row": map[string]any{"id": float64(2)}}); err != nil || out["replayed"] != false || len(f.Rows(project, "orders")) != 2 {
		t.Errorf("after a refusal: %v %v", out, err)
	}
	// Functions honour the key too.
	calls := 0
	f.AddFunction(project, "bump", func(map[string]any) (any, error) { calls++; return calls, nil })
	for attempt := 1; attempt <= 2; attempt++ {
		out, err := write(t, c, creds, "call_function", "tsk-fn", attempt, map[string]any{"function": "bump"})
		if err != nil || out["data"] != float64(1) || out["replayed"] != (attempt == 2) {
			t.Errorf("attempt %d: %v %v", attempt, out, err)
		}
	}
	// A key older than Xenibase keeps it is not sent again.
	n := len(f.Requests)
	_, err = c.Actions["insert_rows"].Execute(ctx, connector.Request{Input: in, Credentials: creds, HTTP: http.DefaultClient, IdempotencyKey: "tsk-old", KeyFirstSent: time.Now().Add(-25 * time.Hour), Attempt: 2})
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "24 hours") || len(f.Requests) != n {
		t.Errorf("stale key: %v", err)
	}
	for _, r := range f.Requests {
		if strings.Contains(r, "/api/v1/projects/") && strings.Contains(r, "/tables/") {
			t.Errorf("a write used the management API: %s", r)
		}
	}
}

func TestDataErrorMapping(t *testing.T) {
	f, c, creds := setup(t)
	wrapped := func(code, msg string, details map[string]any) map[string]any {
		e := map[string]any{"code": code, "message": msg, "request_id": "req_abc123"}
		if details != nil {
			e["details"] = details
		}
		return map[string]any{"error": e}
	}
	for _, tc := range []struct {
		fail  pgdocktest.Failure
		class string
		kind  effects.ErrorKind
		retry time.Duration
	}{
		{pgdocktest.Failure{Status: 429, RetryAfter: 1, Body: wrapped("rate_limited", "slow down", nil)}, pgdock.ClassRateLimited, effects.KindRetryable, time.Second},
		{pgdocktest.Failure{Status: 429, RetryAfter: 86400, Body: wrapped("plan_limit_reached", "the month's requests are used up", nil)}, pgdock.ClassQuota, effects.KindFatal, 86400 * time.Second},
		{pgdocktest.Failure{Status: 503, RetryAfter: 10, Body: wrapped("project_resuming", "waking", nil)}, pgdock.ClassUnavailable, effects.KindRetryable, 10 * time.Second},
		{pgdocktest.Failure{Status: 503, RetryAfter: 5}, pgdock.ClassUnavailable, effects.KindRetryable, 5 * time.Second},
		{pgdocktest.Failure{Status: 504, Body: wrapped("statement_timeout", "8 s", nil)}, pgdock.ClassTimeout, effects.KindUnknownOutcome, 0},
		{pgdocktest.Failure{Status: 500}, pgdock.ClassServer, effects.KindUnknownOutcome, 0},
		{pgdocktest.Failure{Status: 502}, pgdock.ClassServer, effects.KindUnknownOutcome, 0},
		{pgdocktest.Failure{Status: 400, Body: wrapped("filter_required", "an update needs a filter", nil)}, pgdock.ClassValidation, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 400, Body: wrapped("database_error", "boom", map[string]any{"pg_code": "22P02"})}, pgdock.ClassValidation, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 401, Body: wrapped("invalid_key", "unknown key", nil)}, pgdock.ClassAuth, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 403, Body: wrapped("permission_denied", "no", nil)}, pgdock.ClassPermission, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 403, Body: wrapped("read_only", "storage limit", nil)}, pgdock.ClassConflict, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 404, Body: wrapped("unknown_table", "no table", nil)}, pgdock.ClassNotFound, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 405, Body: wrapped("method_not_allowed", "no", nil)}, pgdock.ClassValidation, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 409, Body: wrapped("unique_violation", "dup", map[string]any{"pg_code": "23505"})}, pgdock.ClassConflict, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 413, Body: wrapped("result_too_large", "over 10 MB", nil)}, pgdock.ClassValidation, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 422, Body: wrapped("not_null_violation", "null", nil)}, pgdock.ClassValidation, effects.KindFatal, 0},
		{pgdocktest.Failure{Status: 422, Body: wrapped("idempotency_key_reused", "reused", nil)}, pgdock.ClassConflict, effects.KindFatal, 0},
	} {
		tc.fail.Method, tc.fail.PathPrefix = http.MethodPost, "/edge/"
		f.Fail(tc.fail)
		_, err := call(t, c, creds, "read_rows", map[string]any{"table": "orders"})
		var pe *pgdock.Error
		if !errors.As(err, &pe) || pe.Class != tc.class || effects.Classify(err) != tc.kind || pe.RetryAfterDelay() != tc.retry {
			t.Errorf("%d %v: %v (class %v, kind %v)", tc.fail.Status, tc.fail.Body, err, pe, effects.Classify(err))
			continue
		}
		if tc.fail.Body != nil && (pe.RequestID != "req_abc123" || !strings.Contains(err.Error(), "(request req_abc123)")) {
			t.Errorf("request id: %v", err)
		}
		if pe.Class == pgdock.ClassNotFound && !errors.Is(err, connector.ErrNotFound) {
			t.Error("a 404 does not read as not found")
		}
		if pe.Code == "database_error" && pe.SQLState != "22P02" {
			t.Errorf("pg_code: %v", pe)
		}
	}
	// A Retry-After the engine honours comes with the error, so the
	// manifest's writes wait at least that long before the same key is
	// sent again.
	f.Fail(pgdocktest.Failure{Method: http.MethodPatch, PathPrefix: "/edge/", Status: 503, RetryAfter: 2, Body: wrapped("database_unavailable", "later", nil)})
	_, err := write(t, c, creds, "update_rows", "tsk-ra", 1, map[string]any{"table": "orders", "key": float64(1), "set": map[string]any{"status": "x"}})
	var ra interface{ RetryAfterDelay() time.Duration }
	if !errors.As(err, &ra) || ra.RetryAfterDelay() != 2*time.Second || effects.Classify(err) != effects.KindRetryable {
		t.Errorf("retry-after on a write: %v", err)
	}
}
