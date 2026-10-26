package ingest_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
)

// call sends a request to the tenant's hooks and returns the answer.
func (w *world) call(t *testing.T, method, path string, body []byte, hdr ...string) rawResponse {
	t.Helper()
	req, _ := http.NewRequest(method, w.srv.URL+"/hooks/"+w.Tenant.String()+path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return rawResponse{status: resp.StatusCode, headers: resp.Header, body: string(raw)}
}

func hookDoc(config, inputs string) string {
	if inputs == "" {
		inputs = `{"type":"object"}`
	}
	return `{"schema":"wd/v1","id":"wf_hook","version":1,"name":"hook",
  "trigger":{"type":"webhook","config":` + config + `},
  "inputs":{"schema":` + inputs + `},
  "steps":[{"id":"echo","type":"transform","config":{"output":{"method":"=trigger.method","body":"=trigger.body"}}}]}`
}

func TestWebhookGETReadsTheQuery(t *testing.T) {
	w := newWorld(t)
	wf := w.publish(t, hookDoc(`{"path":"/status","auth":"none","methods":["GET"]}`,
		`{"type":"object","required":["ref"],"properties":{"ref":{"type":"string"}}}`))

	first := w.call(t, "GET", "/status?ref=A1", nil)
	if first.status != 202 {
		t.Fatalf("GET: %d %s", first.status, first.body)
	}
	if again := w.call(t, "GET", "/status?ref=A1", nil); again.status != 202 || !strings.Contains(again.body, `"duplicate":true`) {
		t.Errorf("the same query again: %d %s", again.status, again.body)
	}
	if other := w.call(t, "GET", "/status?ref=B2", nil); other.status != 202 || !strings.Contains(other.body, `"duplicate":false`) {
		t.Errorf("another query: %d %s", other.status, other.body)
	}
	if missing := w.call(t, "GET", "/status?other=1", nil); missing.status != 422 {
		t.Errorf("a query the inputs schema refuses: %d", missing.status)
	}
	post := w.call(t, "POST", "/status", []byte(`{"ref":"A1"}`))
	if post.status != 405 || post.header("Allow") != "GET" {
		t.Errorf("POST to a GET webhook: %d Allow %q", post.status, post.header("Allow"))
	}
	if n := w.runCount(t, wf); n != 2 {
		t.Errorf("%d runs, want 2", n)
	}
	w.Drain(t)
	var out map[string]any
	_ = json.Unmarshal([]byte(first.body), &out)
	hist, err := w.Store.RunHistory(ctx, refOf(w, out))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(hist[0].Payload, []byte(`"method": "GET"`)) || !bytes.Contains(hist[0].Payload, []byte(`"ref": "A1"`)) {
		t.Errorf("the run's trigger: %s", hist[0].Payload)
	}
}

func TestWebhookMethodsDefaultToPOST(t *testing.T) {
	w := newWorld(t)
	w.publish(t, hookDoc(`{"path":"/orders","auth":"none"}`, ""))
	if r := w.call(t, "GET", "/orders", nil); r.status != 405 || r.header("Allow") != "POST" {
		t.Errorf("GET to a POST webhook: %d Allow %q", r.status, r.header("Allow"))
	}
	if r := w.call(t, "PUT", "/orders", []byte(`{}`)); r.status != 405 {
		t.Errorf("PUT: %d", r.status)
	}
	if r := w.call(t, "POST", "/orders", []byte(`{}`)); r.status != 202 {
		t.Errorf("POST: %d", r.status)
	}
}

func TestWebhookBasicAuth(t *testing.T) {
	w := newWorld(t)
	w.publish(t, hookDoc(`{"path":"/basic","auth":"basic","methods":["POST","GET"]}`, ""))
	if _, err := w.Vault.Put(ctx, w.Tenant, "prod", "webhook_wf_hook", []byte("shop:s3cret"), "test"); err != nil {
		t.Fatal(err)
	}
	basic := func(s string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(s)) }
	r := w.call(t, "POST", "/basic", []byte(`{}`))
	if r.status != 401 || !strings.HasPrefix(r.header("WWW-Authenticate"), "Basic") {
		t.Errorf("no credentials: %d %q", r.status, r.header("WWW-Authenticate"))
	}
	if r := w.call(t, "POST", "/basic", []byte(`{}`), "Authorization", basic("shop:wrong")); r.status != 401 {
		t.Errorf("wrong password: %d", r.status)
	}
	if r := w.call(t, "GET", "/basic?x=1", nil, "Authorization", basic("shop:s3cret")); r.status != 202 {
		t.Errorf("right password: %d %s", r.status, r.body)
	}
}

func TestWebhookJWT(t *testing.T) {
	w := newWorld(t)
	w.publish(t, hookDoc(`{"path":"/jwt","auth":"jwt","jwt":{"issuer":"shop","audience":"taskiem"}}`, ""))
	key := "whsec_jwtkey"
	if _, err := w.Vault.Put(ctx, w.Tenant, "prod", "webhook_wf_hook", []byte(key), "test"); err != nil {
		t.Fatal(err)
	}
	token := func(claims map[string]any) string {
		enc := func(v any) string {
			raw, _ := json.Marshal(v)
			return base64.RawURLEncoding.EncodeToString(raw)
		}
		signed := enc(map[string]any{"alg": "HS256", "typ": "JWT"}) + "." + enc(claims)
		m := hmac.New(sha256.New, []byte(key))
		m.Write([]byte(signed))
		return "Bearer " + signed + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	}
	exp := time.Now().Add(5 * time.Minute).Unix()
	if r := w.call(t, "POST", "/jwt", []byte(`{}`), "Authorization", token(map[string]any{"exp": exp, "iss": "shop", "aud": "taskiem"})); r.status != 202 {
		t.Errorf("a good token: %d %s", r.status, r.body)
	}
	if r := w.call(t, "POST", "/jwt", []byte(`{}`), "Authorization", token(map[string]any{"exp": exp, "iss": "shop", "aud": "elsewhere"})); r.status != 401 {
		t.Errorf("another audience: %d", r.status)
	}
	if r := w.call(t, "POST", "/jwt", []byte(`{}`), "Authorization", token(map[string]any{"exp": time.Now().Add(-time.Hour).Unix(), "iss": "shop", "aud": "taskiem"})); r.status != 401 {
		t.Errorf("expired: %d", r.status)
	}
}

func TestWebhookCORS(t *testing.T) {
	w := newWorld(t)
	w.publish(t, hookDoc(`{"path":"/contact","auth":"none","cors":{"origins":["https://shop.example.com"]}}`, ""))
	shop, evil := "https://shop.example.com", "https://evil.example.com"

	pre := w.call(t, "OPTIONS", "/contact", nil, "Origin", shop, "Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "content-type")
	if pre.status != 204 || pre.header("Access-Control-Allow-Origin") != shop || !strings.Contains(pre.header("Access-Control-Allow-Methods"), "POST") ||
		!strings.Contains(pre.header("Access-Control-Allow-Headers"), "Content-Type") {
		t.Errorf("preflight from the shop: %d %v", pre.status, pre.headers)
	}
	if pre := w.call(t, "OPTIONS", "/contact", nil, "Origin", evil, "Access-Control-Request-Method", "POST"); pre.status != 403 || pre.header("Access-Control-Allow-Origin") != "" {
		t.Errorf("preflight from elsewhere: %d %v", pre.status, pre.headers)
	}
	if pre := w.call(t, "OPTIONS", "/contact", nil, "Origin", shop, "Access-Control-Request-Method", "DELETE"); pre.status != 403 {
		t.Errorf("preflight for a method it does not take: %d", pre.status)
	}
	if pre := w.call(t, "OPTIONS", "/nowhere", nil, "Origin", shop, "Access-Control-Request-Method", "POST"); pre.status != 404 {
		t.Errorf("preflight for no webhook: %d", pre.status)
	}
	r := w.call(t, "POST", "/contact", []byte(`{"a":1}`), "Origin", shop)
	if r.status != 202 || r.header("Access-Control-Allow-Origin") != shop || r.header("Access-Control-Expose-Headers") != "X-Taskiem-Run" {
		t.Errorf("a call from the shop: %d %v", r.status, r.headers)
	}
	if r := w.call(t, "POST", "/contact", []byte(`{"a":2}`), "Origin", evil); r.status != 403 {
		t.Errorf("a call from elsewhere: %d", r.status)
	}
	if r := w.call(t, "POST", "/contact", []byte(`{"a":3}`)); r.status != 202 || r.header("Access-Control-Allow-Origin") != "" {
		t.Errorf("a server's call (no Origin): %d %v", r.status, r.headers)
	}
}

func TestWebhookRulesAtSave(t *testing.T) {
	w := newWorld(t)
	for config, want := range map[string]string{
		`{"path":"/a","auth":"hmac","methods":["GET"]}`:                                                    "an hmac signature signs the body",
		`{"path":"/a","auth":"bearer","cors":{"origins":["https://a.example"]}}`:                           "cannot keep a webhook's token secret",
		`{"path":"/a","auth":"basic","cors":{"origins":["https://a.example"]}}`:                            "password",
		`{"path":"/a","auth":"none","cors":{"origins":["https://a.example/"]}}`:                            "no path, query or trailing slash",
		`{"path":"/a","auth":"none","cors":{"origins":["http://a.example"]}}`:                              "only https origins",
		`{"path":"/a","auth":"none","cors":{"origins":["*","https://a.example"]}}`:                         "stands alone",
		`{"path":"/a","auth":"none","cors":{"origins":["https://A.example"]}}`:                             "lower case",
		`{"path":"/a","auth":"bearer","jwt":{"issuer":"x"}}`:                                               "jwt settings need auth jwt",
		`{"path":"/a","auth":"none","methods":["TRACE"]}`:                                                  "methods",
		`{"path":"/a","auth":"jwt","methods":["GET","POST"],"cors":{"origins":["http://localhost:5173"]}}`: "",
		`{"path":"/a","auth":"none","cors":{"origins":["*"]}}`:                                             "",
	} {
		probs := strings.Join(w.validate(hookDoc(config, "")), "; ")
		if want == "" && probs != "" || want != "" && !strings.Contains(probs, want) {
			t.Errorf("%s: %q, want %q", config, probs, want)
		}
	}
}

func TestWebhookListen(t *testing.T) {
	w := newWorld(t)
	wf := w.Publish(t, hookDoc(`{"path":"/a","auth":"none"}`, ""))
	start := func(config map[string]any) ingest.Listen {
		t.Helper()
		var l ingest.Listen
		err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
			var err error
			l, err = ingest.StartListen(ctx, tx, w.Tenant, wf, "wf_hook", "prod", "test", config, time.Now())
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	read := func(id uuid.UUID) ingest.ListenState {
		t.Helper()
		var st ingest.ListenState
		err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
			var err error
			st, err = ingest.ReadListen(ctx, tx, w.Store.PII, w.Tenant, wf, id, time.Now())
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	sub := func(l ingest.Listen) string { return strings.TrimPrefix(l.Path, "/"+w.Tenant.String()) }

	l := start(map[string]any{"path": "/a", "auth": "bearer", "methods": []any{"POST"}})
	if st := read(l.ID); st.Status != "waiting" {
		t.Errorf("before the call: %+v", st)
	}
	if r := w.call(t, "POST", sub(l)+"x", []byte(`{}`)); r.status != 404 {
		t.Errorf("a wrong token: %d", r.status)
	}
	// A GET to a POST webhook with no secret yet: kept, with what is wrong.
	r := w.call(t, "GET", sub(l)+"?order=A1", nil, "Authorization", "Bearer guess", "X-Shop", "lagos")
	if r.status != 200 {
		t.Fatalf("the test call: %d %s", r.status, r.body)
	}
	st := read(l.ID)
	checks, _ := st.Call["checks"].(map[string]any)
	body, _ := st.Call["body"].(map[string]any)
	headers, _ := st.Call["headers"].(map[string]any)
	if st.Status != "captured" || st.Call["method"] != "GET" || body["order"] != "A1" || headers["x-shop"] != "lagos" || headers["authorization"] != nil ||
		!strings.Contains(checks["method"].(string), "takes POST") || !strings.Contains(checks["auth"].(string), "no secret webhook_wf_hook") {
		t.Errorf("the capture: %+v", st)
	}
	if r := w.call(t, "POST", sub(l), []byte(`{}`)); r.status != 409 {
		t.Errorf("a second call: %d", r.status)
	}
	// With the secret in place, the same check passes.
	if _, err := w.Vault.Put(ctx, w.Tenant, "prod", "webhook_wf_hook", []byte("tok"), "test"); err != nil {
		t.Fatal(err)
	}
	l2 := start(map[string]any{"path": "/a", "auth": "bearer"})
	if r := w.call(t, "POST", sub(l2), []byte(`{"n":1}`), "Authorization", "Bearer tok"); r.status != 200 || !strings.Contains(r.body, `"auth":"ok"`) {
		t.Errorf("a call with the token: %d %s", r.status, r.body)
	}
	// Nothing a listen takes starts a run.
	if n := w.runCount(t, wf); n != 0 {
		t.Errorf("%d runs from test calls", n)
	}
	// Expired.
	l3 := start(map[string]any{"path": "/a", "auth": "none"})
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE webhook_listens SET expires_at = now() - interval '1 second' WHERE id = $1`, l3.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := w.call(t, "POST", sub(l3), []byte(`{}`)); r.status != 410 {
		t.Errorf("an expired address: %d", r.status)
	}
	if st := read(l3.ID); st.Status != "expired" {
		t.Errorf("expired state: %+v", st)
	}
	// A config publishing would refuse cannot be listened with.
	err = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		_, err := ingest.StartListen(ctx, tx, w.Tenant, wf, "wf_hook", "prod", "test", map[string]any{"path": "/_listen/x", "auth": "none"}, time.Now())
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "reserved for test calls") {
		t.Errorf("a reserved path: %v", err)
	}
}
