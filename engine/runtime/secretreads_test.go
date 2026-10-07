package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// Every decryption a step makes is recorded once per attempt, with the
// run, step and attempt, whichever way the step uses it (spec 14.1).
func TestSecretReadsPerAttempt(t *testing.T) {
	e := rt.New(t)
	e.VaultSecrets = true
	if _, err := e.Vault.Put(ctx, e.Tenant, "prod", "api_token", []byte("tok-5e1f0a"), "admin"); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}

	// A connector that is busy on its first call.
	conn := e.Provider.Connector()
	conn.Manifest.ID = "authpay"
	conn.Manifest.Auth.Type = "api_key"
	conn.Manifest.Auth.Fields = []connector.AuthField{{Key: "secret_key", Secret: true}}
	var connCalls atomic.Int32
	conn.Actions["verify"] = connector.ActionFunc(func(_ context.Context, r connector.Request) (connector.Response, error) {
		if r.Credentials["secret_key"] != "sk_test_9" {
			return connector.Response{}, fmt.Errorf("no credential: %w", effects.ErrFatal)
		}
		if connCalls.Add(1) == 1 {
			return connector.Response{}, fmt.Errorf("busy: %w", effects.ErrRetryable)
		}
		return connector.Response{Output: map[string]any{"status": "success"}}, nil
	})
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	connID, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "authpay", "main", "api_key", map[string]string{"secret_key": "sk_test_9"}, "admin")
	if err != nil {
		t.Fatal(err)
	}

	// An HTTP endpoint that fails once.
	var httpCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-5e1f0a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if httpCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	src, _ := json.Marshal(`export default (input: any, host: any) => ({ n: host.secret("api_token").length })`)
	retry := `"retry":{"max":3,"initial":"10ms","backoff":"fixed"}`
	wf := e.Publish(t, wfDoc(`{"id":"check","type":"connector","connector":"authpay@1","action":"verify",`+retry+`,"input":{"reference":"r1"}},
	  {"id":"post","type":"http",`+retry+`,"config":{"method":"GET","url":"`+srv.URL+`","headers":{"Authorization":"='Bearer ' + secrets.api_token"}}},
	  {"id":"calc","type":"code","needs":["check","post"],"config":{"language":"typescript","secrets":["api_token"],"source":`+string(src)+`}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	rt.WaitFor(t, 20*time.Second, "run to end", func() bool {
		e.Drain(t)
		st := e.Status(t, ref)
		return st == "completed" || st == "failed"
	})
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}

	type read struct {
		kind, name, purpose, step, actor string
		attempt                          int
		conn                             *string
	}
	rows, err := e.DB.Admin.Query(ctx, `SELECT kind, name, purpose, step_id, attempt, actor, connection_id::text FROM secret_reads
		WHERE run_id = $1 ORDER BY step_id, attempt`, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []read
	for rows.Next() {
		var r read
		if err := rows.Scan(&r.kind, &r.name, &r.purpose, &r.step, &r.attempt, &r.actor, &r.conn); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	want := []string{
		"calc/1 secret api_token step.code",
		"check/1 connection main step.connector",
		"check/2 connection main step.connector",
		"post/1 secret api_token step.http",
		"post/2 secret api_token step.http",
	}
	if len(got) != len(want) {
		t.Fatalf("%d reads, want %d: %+v", len(got), len(want), got)
	}
	for i, r := range got {
		if s := fmt.Sprintf("%s/%d %s %s %s", r.step, r.attempt, r.kind, r.name, r.purpose); s != want[i] {
			t.Errorf("read %d: %s, want %s", i, s, want[i])
		}
		if r.kind == "connection" && (r.conn == nil || *r.conn != connID.String()) {
			t.Errorf("connection read without its id: %v", r.conn)
		}
		if r.actor != "system" {
			t.Errorf("actor %q", r.actor)
		}
	}
	// No row carries a value.
	var leaked int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM secret_reads r WHERE r::text LIKE '%tok-5e1f0a%' OR r::text LIKE '%sk_test_9%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Errorf("secret values in secret_reads: %d %v", leaked, err)
	}
}
