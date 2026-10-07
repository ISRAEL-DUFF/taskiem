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
)

func setup(t *testing.T) (*pgdocktest.Fake, *connector.Connector, map[string]string) {
	t.Helper()
	f := pgdocktest.New(t)
	f.AddProject(project, "shop")
	f.AddProject(other, "blog")
	f.AddTable(project, "orders", []string{"id"}, "id", "customer", "phone", "status", "total")
	f.AddToken(pgdocktest.Token{Secret: token, ID: "tok-1", Name: "taskiem", Scopes: []string{"read", "write"}, ProjectIDs: []string{project}})
	c := pgdock.New(pgdock.Options{BaseURL: f.URL})
	return f, c, map[string]string{"token": token, "project_id": project}
}

func call(t *testing.T, c *connector.Connector, creds map[string]string, action string, in map[string]any) (map[string]any, error) {
	t.Helper()
	resp, err := c.Actions[action].Execute(ctx, connector.Request{Input: in, Credentials: creds, HTTP: http.DefaultClient, Attempt: 1})
	if err != nil {
		return nil, err
	}
	return resp.Output.(map[string]any), nil
}

func TestManifest(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(pgdock.New(pgdock.Options{BaseURL: "https://pgdock.example.com"})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("pgdock@1")
	if !ok {
		t.Fatal("pgdock@1 not registered")
	}
	for name, a := range c.Manifest.Actions {
		if a.Class != effects.Read {
			t.Errorf("%s is %s: writes wait for a parameterised endpoint (P1-G1)", name, a.Class)
		}
	}
	tr := c.Manifest.Triggers["row_changed"]
	if !tr.Remote() || tr.Verify.Scheme != "hmac_sha256_timestamped" || tr.Verify.SignatureFormat != "t_v1" {
		t.Errorf("row_changed: %+v", tr)
	}
	hosts := c.Manifest.HostsFor(map[string]string{"base_url": "https://db.acme.example"})
	if !slices.Equal(hosts, []string{"pgdock.example.com", "db.acme.example"}) {
		t.Errorf("hosts %v", hosts)
	}
	if h := pgdock.New(pgdock.Options{}).Manifest.HostsFor(nil); len(h) != 0 {
		t.Errorf("no platform PGDock and no server URL still reaches %v", h)
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
		t.Errorf("warnings for a restricted read+write token: %v", w)
	}
	if !slices.Contains(f.Requests, "GET /api/v1/me") {
		t.Errorf("requests %v", f.Requests)
	}

	// A token for every project, without write, close to expiry: warned.
	f.AddToken(pgdocktest.Token{Secret: "pgd_wide", Scopes: []string{"read"}, ExpiresAt: time.Now().Add(48 * time.Hour)})
	out, err = call(t, c, map[string]string{"token": "pgd_wide", "project_id": project}, "test_connection", nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := out["warnings"].([]any); len(w) != 3 || out["restricted"] != false || len(out["projects"].([]any)) != 2 {
		t.Errorf("%v", out)
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
	if _, err := call(t, none, creds, "test_connection", nil); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "no PGDock server") {
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
		t.Error("bad input reached PGDock")
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
		{pgdocktest.Failure{Status: 400, Body: map[string]any{"code": "sql_error", "message": "", "sql_error": map[string]any{"code": "40001", "message": "could not serialize"}}}, pgdock.ClassConflict, effects.KindRetryable, 0},
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
	if h.Name != "taskiem-1" || !slices.Equal(h.Events, []string{"INSERT"}) || !slices.Equal(h.Tables, []string{"orders"}) || h.Secret != st.Secret {
		t.Errorf("webhook %+v", h)
	}
	found, ok, err := reg.Find(ctx, rc, "taskiem-1")
	if err != nil || !ok || found.ID != st.ID || found.Secret != "" {
		t.Errorf("find: %+v %v %v", found, ok, err)
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
	if h.URL != "https://hooks.example/y" || len(h.Events) != 3 || !slices.Equal(h.Columns, []string{"status"}) {
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
	// Without the write scope PGDock refuses: a permission error.
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
	// PGDock busy: the delivery is refused, so PGDock sends it again.
	f.Fail(pgdocktest.Failure{Method: http.MethodGet, PathPrefix: "/api/v1/projects/", Status: 503})
	if _, err := enrich(ctx, call(on), ev("UPDATE", map[string]any{"id": float64(9)})); effects.Classify(err) != effects.KindRetryable {
		t.Errorf("busy: %v", err)
	}
}
