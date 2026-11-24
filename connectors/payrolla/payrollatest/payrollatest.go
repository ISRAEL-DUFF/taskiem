// Package payrollatest is a fake Payrolla for tests: its OAuth endpoints,
// the Connect API routes Taskiem uses, and its webhooks, as Payrolla's
// replies (docs/integrations/payrolla-reference/05 to 07) and its OpenAPI
// file (api/payrolla-connect-v1-2026-11-24.yaml) describe them. Never real
// money: nothing here pays.
//
// What it serves now (P4b-1):
//   - GET /oauth/authorize (the sign-in app's consent page; it allows at
//     once, or refuses when Deny is set), POST /oauth/token
//     (authorization_code with PKCE S256, refresh_token with rotation:
//     a spent refresh token used again ends the token chain), POST
//     /oauth/revoke;
//   - GET /connect/company; /connect/webhooks (list, create, delete);
//   - /connect/approvers (list, link with a code, confirm, unlink), with
//     Payrolla's codes: 5 minutes, 5 tries, one use, at most 5 codes in
//     10 minutes for a person;
//   - company.disconnected to a connection's webhooks when it ends.
//
// Every write needs Idempotency-Key and X-Acting-User; the same key with
// the same request answers the first answer again (Idempotent-Replayed),
// with another request IDEMPOTENCY_KEY_REUSED. Later slices add their
// routes with Handle, on the same connections, keys and writes.
package payrollatest

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client is an app Payrolla registered (Taskiem): its id, secret and
// exact redirect URIs.
type Client struct {
	ID, Secret string
	Redirects  []string
}

// Conn is one connection to the company: a key the owner made, or an
// app's OAuth connection. Its ID is what company.disconnected names
// (key_id).
type Conn struct {
	ID      string
	OAuth   bool
	Scopes  []string
	Ended   bool
	webhook map[string]*Webhook
}

// Webhook is a subscription a connection made.
type Webhook struct {
	ID     string   `json:"id"`
	URL    string   `json:"url"`
	Events []string `json:"event_types"`
	Secret string   `json:"-"`
}

// Approver is a Payrolla user who may approve or pay.
type Approver struct {
	UserID     string
	Name       string
	Email      string
	Role       string
	CanApprove bool
	CanPay     bool
	CanConfirm bool
	Channel    string // SMS, EMAIL or TOTP
	Hint       string
	LinkedAs   string
	LinkedAt   time.Time
}

// Write is a write Payrolla took, as sent.
type Write struct {
	Method, Path string
	Key          string
	ActingUser   string
	ActingName   string
	Body         string
	Replayed     bool
}

type challenge struct {
	id, user, code, acting string
	expires                time.Time
	tries                  int
	used                   bool
}

type grant struct {
	conn                string
	redirect, challenge string
	used                bool
	tokens              []string // access and refresh tokens it gave, cancelled if it is sent again
}

type refresh struct {
	conn  string
	spent bool
	chain int
}

type answer struct {
	status int
	body   []byte
	req    string
}

// Fake is a fake Payrolla deployment: API and sign-in app on one server.
type Fake struct {
	*httptest.Server
	mu sync.Mutex
	// Now is the fake's clock.
	Now func() time.Time
	// Company is the company every connection is to.
	CompanyID, CompanyName string
	// Deny makes the consent page refuse (the owner said no).
	Deny bool
	// AccessTTL is how long an access token lasts (an hour by default).
	AccessTTL time.Duration

	clients    map[string]Client
	keys       map[string]*Conn // a pasted key -> its connection
	conns      map[string]*Conn
	grants     map[string]*grant
	access     map[string]accessTok
	refreshes  map[string]*refresh
	chains     map[int]bool // ended chains
	approvers  []*Approver
	challenges map[string]*challenge
	sent       map[string][]time.Time // codes sent to an acting person
	answers    map[string]answer
	writes     []Write
	refreshN   int
	signUps    []url.Values
	extra      []route
	refusals   map[string]refusal // method + " " + path -> what it answers instead
	n          int
}

type refusal struct {
	status int
	code   string
}

// Refuse makes every request of method to path (under /api/v1, as
// "/connect/employees/{id}") answer status with Payrolla's code instead,
// until Refuse is called again with status 0.
func (f *Fake) Refuse(method, path string, status int, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refusals == nil {
		f.refusals = map[string]refusal{}
	}
	if status == 0 {
		delete(f.refusals, method+" "+path)
		return
	}
	f.refusals[method+" "+path] = refusal{status: status, code: code}
}

type accessTok struct {
	conn    string
	chain   int
	expires time.Time
}

type route struct {
	method, prefix string
	h              func(w http.ResponseWriter, r *http.Request, c *Conn)
}

// New starts a fake Payrolla with one company and Taskiem registered as
// client.
func New(client Client) *Fake {
	f := &Fake{Now: time.Now, CompanyID: "c0ffee00-0000-4000-8000-000000000001", CompanyName: "Amara Foods Ltd", AccessTTL: time.Hour,
		clients: map[string]Client{client.ID: client}, keys: map[string]*Conn{}, conns: map[string]*Conn{}, grants: map[string]*grant{},
		access: map[string]accessTok{}, refreshes: map[string]*refresh{}, chains: map[int]bool{}, challenges: map[string]*challenge{},
		sent: map[string][]time.Time{}, answers: map[string]answer{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// API is the Connect API's base URL (what TASKIEM_PAYROLLA_TEST_URL names).
func (f *Fake) API() string { return f.URL + "/api/v1" }

// App is the sign-in app's address (TASKIEM_PAYROLLA_TEST_APP_URL).
func (f *Fake) App() string { return f.URL + "/app" }

func (f *Fake) id() string {
	f.n++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", f.n)
}

func (f *Fake) token(prefix string) string {
	f.n++
	return prefix + strconv.Itoa(f.n) + "_" + strconv.FormatInt(f.Now().UnixNano()%1e9, 36)
}

// AddKey makes a company key with these scopes, as the owner does in
// Payrolla, and returns it.
func (f *Fake) AddKey(scopes ...string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &Conn{ID: f.id(), Scopes: scopes, webhook: map[string]*Webhook{}}
	f.conns[c.ID] = c
	k := f.token("pyk_test_")
	f.keys[k] = c
	return k
}

// RevokeKey revokes a key in Payrolla.
func (f *Fake) RevokeKey(key string) {
	f.mu.Lock()
	c := f.keys[key]
	delete(f.keys, key)
	f.mu.Unlock()
	if c != nil {
		f.end(c, "revoked_by_owner")
	}
}

// AddApprover adds a Payrolla user who may approve or pay.
func (f *Fake) AddApprover(a Approver) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a.UserID == "" {
		a.UserID = f.id()
	}
	if a.Channel == "" {
		a.Channel, a.Hint = "SMS", "•••• 4567"
	}
	f.approvers = append(f.approvers, &a)
}

// Approvers are the approvers as they are now.
func (f *Fake) Approvers() []Approver {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Approver, 0, len(f.approvers))
	for _, a := range f.approvers {
		out = append(out, *a)
	}
	return out
}

// Code is the last code Payrolla sent to a Payrolla user (read off their
// phone).
func (f *Fake) Code(user string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var last *challenge
	for _, c := range f.challenges {
		if c.user == user && (last == nil || c.id > last.id) {
			last = c
		}
	}
	if last == nil {
		return ""
	}
	return last.code
}

// Writes are the writes Payrolla took, in order.
func (f *Fake) Writes() []Write {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.writes)
}

// Refreshes counts refresh tokens spent.
func (f *Fake) Refreshes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshN
}

// SignUps are the sign-up fields consent pages were opened with.
func (f *Fake) SignUps() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.signUps)
}

// OAuthConn is the app's OAuth connection to the company, or nil.
func (f *Fake) OAuthConn() *Conn {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		if c.OAuth && !c.Ended {
			return c
		}
	}
	return nil
}

// Webhooks are a connection's subscriptions.
func (f *Fake) Webhooks(c *Conn) []Webhook {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Webhook
	for _, w := range c.webhook {
		out = append(out, *w)
	}
	return out
}

// ExpireAccessTokens makes every access token lapse (an hour passing).
func (f *Fake) ExpireAccessTokens() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, t := range f.access {
		t.expires = f.Now().Add(-time.Second)
		f.access[k] = t
	}
}

// EndTokenChains ends every token chain, as when a spent refresh token is
// used again (Payrolla assumes it was copied): refreshing is refused with
// invalid_grant. No event is sent.
func (f *Fake) EndTokenChains() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.refreshes {
		f.endChain(r.chain)
	}
}

// RevokeByOwner ends the app's OAuth connection from Payrolla's settings.
func (f *Fake) RevokeByOwner() {
	if c := f.OAuthConn(); c != nil {
		f.end(c, "revoked_by_owner")
	}
}

// Handle adds a route for a later slice: method and a path prefix under
// /api/v1 (/connect/runs/), served after the connection is authenticated.
func (f *Fake) Handle(method, prefix string, h func(w http.ResponseWriter, r *http.Request, c *Conn)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.extra = append(f.extra, route{method, prefix, h})
}

// end ends a connection: its tokens stop working and its webhooks get one
// last company.disconnected.
func (f *Fake) end(c *Conn, reason string) {
	f.mu.Lock()
	c.Ended = true
	for k, t := range f.access {
		if t.conn == c.ID {
			delete(f.access, k)
		}
	}
	for _, r := range f.refreshes {
		if r.conn == c.ID {
			r.spent = true
		}
	}
	hooks := make([]Webhook, 0, len(c.webhook))
	for _, w := range c.webhook {
		hooks = append(hooks, *w)
	}
	f.mu.Unlock()
	f.deliver(hooks, "company.disconnected", map[string]any{"key_id": c.ID, "reason": reason})
}

// Deliver sends an event to a connection's webhooks that take it, signed
// as Payrolla signs (Payrolla-Signature: t=<unix>,v1=<hex>).
func (f *Fake) Deliver(c *Conn, typ string, data map[string]any) {
	f.deliver(f.Webhooks(c), typ, data)
}

func (f *Fake) deliver(hooks []Webhook, typ string, data map[string]any) {
	f.mu.Lock()
	id := "evt_" + f.id()
	f.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"id": id, "type": typ, "created_at": f.Now().UTC().Format(time.RFC3339), "api_version": "2026-11-01", "data": data})
	for _, w := range hooks {
		if !slices.Contains(w.Events, "*") && !slices.Contains(w.Events, typ) {
			continue
		}
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(w.Secret))
		mac.Write([]byte(ts + "." + string(body)))
		req, _ := http.NewRequest(http.MethodPost, w.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Payrolla-Event-Id", id)
		req.Header.Set("Payrolla-Signature", "t="+ts+",v1="+hex.EncodeToString(mac.Sum(nil)))
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}
}

// --- serving ---

func writeData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func oauthErr(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": code, "error_description": desc})
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/app/oauth/authorize":
		f.authorize(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/oauth/token":
		f.tokenEndpoint(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/oauth/revoke":
		f.revoke(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/connect/"):
		f.connect(w, r)
	default:
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
	}
}

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorize is the consent page: the owner signs in (or up, with the
// sign-up fields filled in) and allows the app at once.
func (f *Fake) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	cl, ok := f.clients[q.Get("client_id")]
	switch {
	case !ok:
		http.Error(w, "UNKNOWN_CLIENT", http.StatusBadRequest)
		return
	case !slices.Contains(cl.Redirects, q.Get("redirect_uri")):
		http.Error(w, "INVALID_REDIRECT_URI", http.StatusBadRequest)
		return
	case q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256":
		http.Error(w, "PKCE_REQUIRED", http.StatusBadRequest)
		return
	case q.Get("response_type") != "code" || q.Get("state") == "":
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}
	if q.Get("company_name") != "" || q.Get("email") != "" {
		f.signUps = append(f.signUps, url.Values{"company_name": {q.Get("company_name")}, "rc_number": {q.Get("rc_number")}, "email": {q.Get("email")}})
	}
	back, _ := url.Parse(q.Get("redirect_uri"))
	bq := url.Values{"state": {q.Get("state")}}
	if f.Deny {
		bq.Set("error", "access_denied")
	} else {
		// One company has one connection per app: connecting again keeps
		// it, with the scopes allowed this time.
		var c *Conn
		for _, x := range f.conns {
			if x.OAuth && !x.Ended {
				c = x
			}
		}
		if c == nil {
			c = &Conn{ID: f.id(), OAuth: true, webhook: map[string]*Webhook{}}
			f.conns[c.ID] = c
		}
		c.Scopes = strings.Fields(q.Get("scope"))
		code := f.token("pyc_code_")
		f.grants[code] = &grant{conn: c.ID, redirect: q.Get("redirect_uri"), challenge: q.Get("code_challenge")}
		bq.Set("code", code)
	}
	back.RawQuery = bq.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound) //nolint:gosec // a fake: redirect_uri was checked against the registered ones above
}

func (f *Fake) issue(c *Conn, chain int) map[string]any {
	at, rt := f.token("pyt_"), f.token("pyr_")
	f.access[at] = accessTok{conn: c.ID, chain: chain, expires: f.Now().Add(f.AccessTTL)}
	f.refreshes[rt] = &refresh{conn: c.ID, chain: chain}
	return map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": int(f.AccessTTL.Seconds()), "refresh_token": rt,
		"scope": strings.Join(c.Scopes, " "), "company_id": f.CompanyID, "company_name": f.CompanyName}
}

func (f *Fake) endChain(chain int) {
	f.chains[chain] = true
	for k, t := range f.access {
		if t.chain == chain {
			delete(f.access, k)
		}
	}
	for _, r := range f.refreshes {
		if r.chain == chain {
			r.spent = true
		}
	}
}

func (f *Fake) tokenEndpoint(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	if cl, known := f.clients[id]; !ok || !known || cl.Secret != secret {
		oauthErr(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		g := f.grants[r.PostForm.Get("code")]
		switch {
		case g == nil:
			oauthErr(w, http.StatusBadRequest, "invalid_grant", "unknown code")
			return
		case g.used:
			// A code sent twice cancels the tokens it gave the first time.
			for _, t := range g.tokens {
				delete(f.access, t)
				if rr := f.refreshes[t]; rr != nil {
					f.endChain(rr.chain)
				}
			}
			oauthErr(w, http.StatusBadRequest, "invalid_grant", "code already used")
			return
		case g.redirect != r.PostForm.Get("redirect_uri") || s256(r.PostForm.Get("code_verifier")) != g.challenge:
			oauthErr(w, http.StatusBadRequest, "invalid_grant", "redirect_uri or code_verifier does not match")
			return
		}
		g.used = true
		f.n++
		ans := f.issue(f.conns[g.conn], f.n)
		g.tokens = []string{ans["access_token"].(string), ans["refresh_token"].(string)}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ans)
	case "refresh_token":
		rt := f.refreshes[r.PostForm.Get("refresh_token")]
		if rt == nil || f.conns[rt.conn].Ended || f.chains[rt.chain] {
			oauthErr(w, http.StatusBadRequest, "invalid_grant", "unknown or revoked refresh token")
			return
		}
		if rt.spent {
			// Used again: assumed copied, so the whole chain ends.
			f.endChain(rt.chain)
			oauthErr(w, http.StatusBadRequest, "invalid_grant", "refresh token already used")
			return
		}
		rt.spent = true
		f.refreshN++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.issue(f.conns[rt.conn], rt.chain))
	default:
		oauthErr(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (f *Fake) revoke(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	_ = r.ParseForm()
	f.mu.Lock()
	cl, known := f.clients[id]
	if !ok || !known || cl.Secret != secret {
		f.mu.Unlock()
		oauthErr(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	tok := r.PostForm.Get("token")
	var c *Conn
	if t, ok := f.access[tok]; ok {
		c = f.conns[t.conn]
	} else if rt := f.refreshes[tok]; rt != nil && !rt.spent {
		c = f.conns[rt.conn]
	}
	f.mu.Unlock()
	// A token that no longer works is ignored.
	if c != nil && !c.Ended {
		f.end(c, "revoked_by_app")
	}
	w.WriteHeader(http.StatusOK)
}

// caller is the connection a request's bearer token or key belongs to.
func (f *Fake) caller(r *http.Request) *Conn {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if c := f.keys[tok]; c != nil && !c.Ended {
		return c
	}
	if t, ok := f.access[tok]; ok && f.Now().Before(t.expires) && !f.conns[t.conn].Ended {
		return f.conns[t.conn]
	}
	return nil
}

func has(c *Conn, scope string) bool { return slices.Contains(c.Scopes, scope) }

func (f *Fake) connect(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	c := f.caller(r)
	if c == nil {
		f.mu.Unlock()
		writeErr(w, http.StatusUnauthorized, "API_KEY_INVALID", "unknown or revoked")
		return
	}
	if no, ok := f.refusals[r.Method+" "+path]; ok {
		f.mu.Unlock()
		writeErr(w, no.status, no.code, "refused as the test asked")
		return
	}
	write := r.Method != http.MethodGet && !strings.HasPrefix(path, "/connect/webhooks")
	var wr Write
	if write {
		wr = Write{Method: r.Method, Path: path, Key: r.Header.Get("Idempotency-Key"), ActingUser: r.Header.Get("X-Acting-User"), ActingName: r.Header.Get("X-Acting-User-Name"), Body: string(raw)}
		switch {
		case wr.Key == "":
			f.mu.Unlock()
			writeErr(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required")
			return
		case wr.ActingUser == "":
			f.mu.Unlock()
			writeErr(w, http.StatusBadRequest, "ACTING_USER_REQUIRED", "X-Acting-User is required")
			return
		}
		sig := r.Method + " " + path + " " + string(raw)
		if a, ok := f.answers[c.ID+"/"+wr.Key]; ok {
			f.mu.Unlock()
			if a.req != sig {
				writeErr(w, http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REUSED", "that key was used for another request")
				return
			}
			wr.Replayed = true
			f.mu.Lock()
			f.writes = append(f.writes, wr)
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(a.status)
			_, _ = w.Write(a.body)
			return
		}
		f.writes = append(f.writes, wr)
		rec := httptest.NewRecorder()
		f.route(rec, r, path, c, raw)
		if rec.Code < 500 {
			f.answers[c.ID+"/"+wr.Key] = answer{status: rec.Code, body: rec.Body.Bytes(), req: sig}
		}
		f.mu.Unlock()
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	extra := f.extraFor(r.Method, path)
	if extra != nil {
		f.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(raw))
		extra(w, r, c)
		return
	}
	defer f.mu.Unlock()
	f.route(w, r, path, c, raw)
}

func (f *Fake) extraFor(method, path string) func(http.ResponseWriter, *http.Request, *Conn) {
	for _, x := range f.extra {
		if x.method == method && strings.HasPrefix(path, x.prefix) {
			return x.h
		}
	}
	return nil
}

// route serves one authenticated request, with f.mu held.
func (f *Fake) route(w http.ResponseWriter, r *http.Request, path string, c *Conn, raw []byte) {
	parts := strings.Split(strings.Trim(path, "/"), "/") // connect, approvers, {id}, link, confirm
	switch {
	case r.Method == http.MethodGet && path == "/connect/company":
		kind := "CONNECTED_APP"
		writeData(w, http.StatusOK, map[string]any{"company": map[string]any{"id": f.CompanyID, "name": f.CompanyName, "pay_frequency": "MONTHLY"},
			"key": map[string]any{"id": c.ID, "name": "Taskiem", "kind": kind, "scopes": c.Scopes}})
	case path == "/connect/webhooks" || strings.HasPrefix(path, "/connect/webhooks/"):
		f.webhooks(w, r, path, c, raw)
	case len(parts) >= 2 && parts[1] == "approvers":
		if !has(c, "payroll:approve") {
			writeErr(w, http.StatusForbidden, "SCOPE_REQUIRED", "this connection needs payroll:approve")
			return
		}
		f.approversRoute(w, r, parts, raw)
	default:
		if x := f.extraFor(r.Method, path); x != nil {
			f.mu.Unlock()
			defer f.mu.Lock()
			r.Body = io.NopCloser(bytes.NewReader(raw))
			x(w, r, c)
			return
		}
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
	}
}

func (f *Fake) webhooks(w http.ResponseWriter, r *http.Request, path string, c *Conn, raw []byte) {
	if !has(c, "webhooks:manage") {
		writeErr(w, http.StatusForbidden, "SCOPE_REQUIRED", "this connection needs webhooks:manage")
		return
	}
	switch {
	case r.Method == http.MethodGet && path == "/connect/webhooks":
		list := []map[string]any{}
		for _, h := range c.webhook {
			list = append(list, map[string]any{"id": h.ID, "url": h.URL, "event_types": h.Events, "status": "ACTIVE", "created_at": "2026-11-24T09:00:00Z"})
		}
		writeData(w, http.StatusOK, map[string]any{"webhooks": list})
	case r.Method == http.MethodPost && path == "/connect/webhooks":
		var in struct {
			URL    string   `json:"url"`
			Events []string `json:"event_types"`
		}
		_ = json.Unmarshal(raw, &in)
		if len(in.Events) == 0 {
			writeErr(w, http.StatusBadRequest, "NO_EVENT_TYPES", "name at least one event type")
			return
		}
		h := &Webhook{ID: f.id(), URL: in.URL, Events: in.Events, Secret: "whsec_" + f.token("")}
		c.webhook[h.ID] = h
		writeData(w, http.StatusCreated, map[string]any{"webhook": map[string]any{"id": h.ID, "url": h.URL, "event_types": h.Events, "status": "ACTIVE"}, "signing_secret": h.Secret})
	case r.Method == http.MethodDelete:
		id := strings.TrimPrefix(path, "/connect/webhooks/")
		if _, ok := c.webhook[id]; !ok {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such subscription")
			return
		}
		delete(c.webhook, id)
		writeData(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
	}
}

func approverView(a *Approver) map[string]any {
	v := map[string]any{"user_id": a.UserID, "name": a.Name, "email": a.Email, "role": a.Role, "can_approve": a.CanApprove, "can_pay": a.CanPay, "can_confirm": a.CanConfirm}
	if a.LinkedAs != "" {
		v["linked_as"] = a.LinkedAs
		v["linked_since"] = a.LinkedAt.UTC().Format(time.RFC3339)
	}
	return v
}

func (f *Fake) approver(id string) *Approver {
	for _, a := range f.approvers {
		if a.UserID == id {
			return a
		}
	}
	return nil
}

// codeLimit and codeWindow: at most 5 codes in 10 minutes for a person.
const (
	codeLimit  = 5
	codeWindow = 10 * time.Minute
	codeTTL    = 5 * time.Minute
	codeTries  = 5
)

func (f *Fake) approversRoute(w http.ResponseWriter, r *http.Request, parts []string, raw []byte) {
	acting := r.Header.Get("X-Acting-User")
	switch {
	case r.Method == http.MethodGet && len(parts) == 2:
		list := []map[string]any{}
		for _, a := range f.approvers {
			list = append(list, approverView(a))
		}
		writeData(w, http.StatusOK, map[string]any{"approvers": list})
		return
	case len(parts) < 4 || parts[3] != "link":
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
		return
	}
	a := f.approver(parts[2])
	if a == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such user")
		return
	}
	switch {
	case r.Method == http.MethodPost && len(parts) == 4:
		for _, x := range f.approvers {
			if x.LinkedAs == acting && x != a {
				writeErr(w, http.StatusConflict, "ALREADY_LINKED", "the acting person is linked to someone else")
				return
			}
		}
		if !a.CanConfirm {
			writeErr(w, http.StatusForbidden, "PAYMENT_CONFIRMATION_NOT_SET_UP", "they have no factor to receive codes on")
			return
		}
		now := f.Now()
		recent := slices.DeleteFunc(slices.Clone(f.sent[acting]), func(t time.Time) bool { return now.Sub(t) >= codeWindow })
		if len(recent) >= codeLimit {
			writeErr(w, http.StatusTooManyRequests, "TOO_MANY_CODES", "more than 5 codes in 10 minutes")
			return
		}
		f.sent[acting] = append(recent, now)
		f.n++
		ch := &challenge{id: fmt.Sprintf("10000000-0000-4000-8000-%012d", f.n), user: a.UserID, acting: acting, code: fmt.Sprintf("%06d", 482910+f.n), expires: now.Add(codeTTL)}
		f.challenges[ch.id] = ch
		writeData(w, http.StatusAccepted, map[string]any{"challenge": map[string]any{"challenge_id": ch.id, "channel": a.Channel, "destination_hint": a.Hint, "expires_at": ch.expires.UTC().Format(time.RFC3339)}})
	case r.Method == http.MethodPost && len(parts) == 5 && parts[4] == "confirm":
		var in struct {
			Challenge string `json:"challenge_id"`
			Code      string `json:"code"`
		}
		_ = json.Unmarshal(raw, &in)
		ch := f.challenges[in.Challenge]
		switch {
		case ch == nil || ch.user != a.UserID || ch.acting != acting:
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such code for this person")
		case ch.used:
			writeErr(w, http.StatusForbidden, "CODE_USED", "that code was already used")
		case !f.Now().Before(ch.expires):
			writeErr(w, http.StatusForbidden, "CODE_EXPIRED", "that code has expired")
		case ch.tries >= codeTries:
			writeErr(w, http.StatusForbidden, "CODE_LOCKED", "too many attempts")
		case in.Code != ch.code:
			ch.tries++
			if ch.tries >= codeTries {
				writeErr(w, http.StatusForbidden, "CODE_LOCKED", "too many attempts")
				return
			}
			writeErr(w, http.StatusForbidden, "CODE_INVALID", "wrong code")
		default:
			ch.used = true
			a.LinkedAs, a.LinkedAt = acting, f.Now()
			writeData(w, http.StatusOK, map[string]any{"approver": approverView(a)})
		}
	case r.Method == http.MethodDelete && len(parts) == 4:
		a.LinkedAs = ""
		writeData(w, http.StatusOK, map[string]any{"unlinked": true})
	default:
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
	}
}

// Routes are the routes the fake serves, as METHOD /path with Payrolla's
// own parameter names, for checking against Payrolla's OpenAPI file.
func Routes() []string {
	return []string{
		"GET /connect/company",
		"GET /connect/webhooks", "POST /connect/webhooks", "DELETE /connect/webhooks/{webhookID}",
		"GET /connect/approvers", "POST /connect/approvers/{userID}/link", "DELETE /connect/approvers/{userID}/link",
		"POST /connect/approvers/{userID}/link/confirm",
	}
}

// ErrorCodes are the error codes the fake's API answers with. The consent
// page's own refusals (UNKNOWN_CLIENT, INVALID_REDIRECT_URI, PKCE_REQUIRED:
// the Phase C reply, 07) are words on a page the owner sees, not the API's.
func ErrorCodes() []string {
	return []string{"API_KEY_INVALID", "NOT_FOUND", "SCOPE_REQUIRED", "IDEMPOTENCY_KEY_REQUIRED", "ACTING_USER_REQUIRED", "IDEMPOTENCY_KEY_REUSED",
		"NO_EVENT_TYPES", "ALREADY_LINKED", "PAYMENT_CONFIRMATION_NOT_SET_UP", "TOO_MANY_CODES", "CODE_USED", "CODE_EXPIRED",
		"CODE_LOCKED", "CODE_INVALID", "invalid_client", "invalid_grant", "unsupported_grant_type", "invalid_request"}
}
