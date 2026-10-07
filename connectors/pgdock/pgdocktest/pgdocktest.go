// Package pgdocktest is a fake PGDock for Taskiem's tests (plan item
// P1-T7), built only from what PGDock publishes: docs/webhooks.md (the
// transactional outbox, payload, PGDock-Signature, ordering, retries,
// dead letters, pausing, truncation, test events), docs/cli.md (tokens)
// and api/openapi.yaml (the API's paths, request and response shapes, and
// the {code, message} error). Where PGDock publishes no detail, the fake
// makes the most conservative assumption and the test that relies on it
// says so (see docs/integrations/pgdock.md, "What PGDock does not publish").
//
// Changes are made in transactions (Begin, then Insert, Update, Delete,
// and Commit or Rollback): a committed change writes an event to the
// outbox of every webhook on its table, a rolled-back one writes nothing,
// as PGDock's triggers do. Deliver posts queued events in commit order,
// one webhook at a time, holding a webhook's queue at its first failure.
package pgdocktest

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Token is an API token the fake accepts.
type Token struct {
	Secret     string // what the client sends as Bearer
	ID, Name   string
	OrgID      string
	Scopes     []string
	ProjectIDs []string // nil: every project
	ExpiresAt  time.Time
}

// Table is a table with its columns and primary key.
type Table struct {
	Columns []string
	Key     []string
	rows    []map[string]any
}

// Webhook is a webhook as PGDock holds it.
type Webhook struct {
	ID, Name            string
	Tables, Events      []string
	Columns             []string
	URL                 string
	Headers             map[string]string
	Enabled             bool
	Status              string // healthy, failing, paused, broken
	StatusReason        string
	ConsecutiveFailures int
	Secret              string
	// PreviousSecret, while set, also signs deliveries (a second v1 value),
	// modelling a rotation overlap PGDock has not confirmed (plan Q22).
	PreviousSecret string
	CreatedAt      time.Time
	queue          []*Event
	Dead           []*Event
	Delivered      []*Event
}

// Event is one outbox entry.
type Event struct {
	ID       string
	Body     []byte // the JSON payload, as signed
	Attempts int
	Seq      int64
}

// Delivery is one attempt the fake made, for assertions.
type Delivery struct {
	Webhook, EventID string
	Status           int
	Err              error
	Headers          http.Header
	Body             []byte
}

type project struct {
	ID, Name, OrgID string
	Tables          map[string]*Table
	Webhooks        []*Webhook
}

// Failure is an answer the fake gives instead of serving a request.
type Failure struct {
	Method, PathPrefix string
	Status             int
	Body               any // JSON; default {code, message}
	RetryAfter         int // seconds; 0: no header
}

// Fake is a fake PGDock server.
type Fake struct {
	*httptest.Server
	t testing.TB

	mu       sync.Mutex
	OrgID    string
	OrgName  string
	tokens   map[string]Token
	projects map[string]*project
	failures []Failure
	seq      int64
	hookSeq  int
	// TruncateAbove is the size above which an event's records are left
	// out (PGDock: 256 KB).
	TruncateAbove int
	// Now is the clock for signatures and committed_at.
	Now func() time.Time
	// Client posts deliveries (default: a plain client with a 10 s timeout).
	Client *http.Client
	// Deliveries are every delivery attempt, in order.
	Deliveries []Delivery
	// Requests are the API requests served ("GET /api/v1/me"), in order.
	Requests []string
}

// New starts a fake PGDock with one organisation and no projects.
func New(t testing.TB) *Fake {
	t.Helper()
	f := &Fake{t: t, OrgID: "0b0c0d0e-0000-4000-8000-000000000001", OrgName: "Acme Foods",
		tokens: map[string]Token{}, projects: map[string]*project{}, TruncateAbove: 256 << 10,
		Now: time.Now, Client: &http.Client{Timeout: 10 * time.Second}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// AddProject creates a project and returns its id.
func (f *Fake) AddProject(id, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[id] = &project{ID: id, Name: name, OrgID: f.OrgID, Tables: map[string]*Table{}}
	return id
}

// AddTable adds a table ("orders" or "billing.invoices") to a project.
func (f *Fake) AddTable(projectID, name string, key []string, columns ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[projectID].Tables[qualify(name)] = &Table{Columns: columns, Key: key}
}

// AddToken lets a token in.
func (f *Fake) AddToken(tok Token) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tok.OrgID == "" {
		tok.OrgID = f.OrgID
	}
	if tok.ExpiresAt.IsZero() {
		tok.ExpiresAt = f.Now().Add(90 * 24 * time.Hour)
	}
	f.tokens[tok.Secret] = tok
}

// Fail makes the next request matching method and path prefix get this
// answer instead (once per call).
func (f *Fake) Fail(fl Failure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, fl)
}

// Webhooks returns copies of a project's webhooks.
func (f *Fake) Webhooks(projectID string) []Webhook {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Webhook
	for _, w := range f.projects[projectID].Webhooks {
		out = append(out, *w)
	}
	return out
}

// Hook returns a webhook by id (nil when there is none).
func (f *Fake) Hook(projectID, id string) *Webhook {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.projects[projectID].Webhooks {
		if w.ID == id {
			c := *w
			return &c
		}
	}
	return nil
}

// SetStatus changes a webhook as PGDock would on its own: broken (its
// triggers were dropped), paused (enabled false).
func (f *Fake) SetStatus(projectID, id, status, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.projects[projectID].Webhooks {
		if w.ID == id {
			w.Status, w.StatusReason = status, reason
			if status == "paused" {
				w.Enabled = false
			}
		}
	}
}

// RemoveWebhook deletes a webhook out of band (a person in PGDock's UI).
func (f *Fake) RemoveWebhook(projectID, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.projects[projectID]
	p.Webhooks = slices.DeleteFunc(p.Webhooks, func(w *Webhook) bool { return w.ID == id })
}

// --- transactions and the outbox ---

// Tx is a transaction on a project's database.
type Tx struct {
	f       *Fake
	project string
	changes []change
	done    bool
}

type change struct {
	table, kind string
	record, old map[string]any
}

// Begin starts a transaction.
func (f *Fake) Begin(projectID string) *Tx { return &Tx{f: f, project: projectID} }

// Insert adds a row.
func (tx *Tx) Insert(table string, row map[string]any) *Tx {
	tx.changes = append(tx.changes, change{table: qualify(table), kind: "INSERT", record: row})
	return tx
}

// Update changes the row with key's values.
func (tx *Tx) Update(table string, key, set map[string]any) *Tx {
	tx.changes = append(tx.changes, change{table: qualify(table), kind: "UPDATE", old: key, record: set})
	return tx
}

// Delete removes the row with key's values.
func (tx *Tx) Delete(table string, key map[string]any) *Tx {
	tx.changes = append(tx.changes, change{table: qualify(table), kind: "DELETE", old: key})
	return tx
}

// Rollback discards the transaction: no row changes, no event.
func (tx *Tx) Rollback() { tx.done = true }

// Commit applies the changes and writes each to the outbox of every
// enabled webhook on its table, in the same step: committed changes
// always produce events.
func (tx *Tx) Commit() {
	f := tx.f
	f.mu.Lock()
	defer f.mu.Unlock()
	if tx.done {
		f.t.Fatal("pgdocktest: transaction already finished")
	}
	tx.done = true
	p := f.projects[tx.project]
	at := f.Now().UTC()
	for _, c := range tx.changes {
		t := p.Tables[c.table]
		if t == nil {
			f.t.Fatalf("pgdocktest: no table %s", c.table)
		}
		var record, old map[string]any
		switch c.kind {
		case "INSERT":
			record = clone(c.record)
			t.rows = append(t.rows, record)
			record = clone(record)
		case "UPDATE":
			i := t.find(c.old)
			if i < 0 {
				continue
			}
			old = clone(t.rows[i])
			for k, v := range c.record {
				t.rows[i][k] = v
			}
			record = clone(t.rows[i])
		case "DELETE":
			i := t.find(c.old)
			if i < 0 {
				continue
			}
			old = t.rows[i]
			t.rows = slices.Delete(t.rows, i, i+1)
		}
		for _, w := range p.Webhooks {
			if !w.Enabled || !slices.Contains(w.Tables, c.table) && !slices.Contains(w.Tables, strings.TrimPrefix(c.table, "public.")) {
				continue
			}
			if !slices.Contains(w.Events, c.kind) {
				continue
			}
			if c.kind == "UPDATE" && len(w.Columns) > 0 && !changed(old, record, w.Columns) {
				continue
			}
			f.seq++
			id := fmt.Sprintf("evt_%s_%d", strings.ReplaceAll(w.ID, "-", "")[:12], f.seq)
			payload := map[string]any{"id": id, "webhook": w.Name, "project": p.ID, "table": c.table, "type": c.kind,
				"record": record, "old_record": old, "committed_at": at.Format(time.RFC3339)}
			body, _ := json.Marshal(payload)
			if len(body) > f.TruncateAbove {
				// PGDock: both records omitted, truncated and the key sent.
				delete(payload, "record")
				delete(payload, "old_record")
				payload["truncated"] = true
				src := record
				if src == nil {
					src = old
				}
				pk := map[string]any{}
				for _, k := range t.Key {
					pk[k] = src[k]
				}
				payload["primary_key"] = pk
				body, _ = json.Marshal(payload)
			}
			w.queue = append(w.queue, &Event{ID: id, Body: body, Seq: f.seq})
		}
	}
}

func changed(old, now map[string]any, cols []string) bool {
	for _, c := range cols {
		if fmt.Sprint(old[c]) != fmt.Sprint(now[c]) {
			return true
		}
	}
	return false
}

func (t *Table) find(key map[string]any) int {
	for i, r := range t.rows {
		ok := true
		for k, v := range key {
			if fmt.Sprint(r[k]) != fmt.Sprint(v) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func clone(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func qualify(table string) string {
	if strings.Contains(table, ".") {
		return table
	}
	return "public." + table
}

// Queued is how many events wait for a webhook.
func (f *Fake) Queued(projectID, webhookID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.projects[projectID].Webhooks {
		if w.ID == webhookID {
			return len(w.queue)
		}
	}
	return 0
}

// Deliver makes one delivery pass, as PGDock's worker does after a commit
// or a backoff: each webhook's queue is posted in commit order until an
// event fails, which holds back the ones behind it. 429, 5xx and network
// errors are retried on a later pass; other 4xx and redirects dead-letter
// the event at once. After 50 failures in a row the webhook is paused. It
// returns how many events were delivered.
func (f *Fake) Deliver() int {
	f.mu.Lock()
	type job struct {
		w *Webhook
		e *Event
	}
	var hooks []*Webhook
	for _, p := range f.projects {
		hooks = append(hooks, p.Webhooks...)
	}
	f.mu.Unlock()
	sort.Slice(hooks, func(i, j int) bool { return hooks[i].CreatedAt.Before(hooks[j].CreatedAt) })
	n := 0
	for _, w := range hooks {
		for {
			f.mu.Lock()
			if !w.Enabled || w.Status == "broken" || len(w.queue) == 0 {
				f.mu.Unlock()
				break
			}
			e := w.queue[0]
			e.Attempts++
			f.mu.Unlock()
			status, err := f.post(w, e)
			f.mu.Lock()
			switch {
			case err == nil && status >= 200 && status < 300:
				w.queue = w.queue[1:]
				w.Delivered = append(w.Delivered, e)
				w.ConsecutiveFailures, w.Status, w.StatusReason = 0, "healthy", ""
				n++
				f.mu.Unlock()
				continue
			case err == nil && status != http.StatusTooManyRequests && status < 500:
				w.queue = w.queue[1:]
				w.Dead = append(w.Dead, e)
				w.Status = "failing"
				f.mu.Unlock()
				continue
			}
			w.ConsecutiveFailures++
			w.Status = "failing"
			if w.ConsecutiveFailures >= 50 {
				w.Enabled, w.Status, w.StatusReason = false, "paused", "50 failures in a row"
			}
			f.mu.Unlock()
			break
		}
	}
	return n
}

// Redeliver posts an event that was already delivered again (PGDock
// delivers at least once).
func (f *Fake) Redeliver(projectID, webhookID, eventID string) (int, error) {
	f.mu.Lock()
	var w *Webhook
	var e *Event
	for _, h := range f.projects[projectID].Webhooks {
		if h.ID == webhookID {
			w = h
			for _, d := range h.Delivered {
				if d.ID == eventID {
					e = d
				}
			}
		}
	}
	f.mu.Unlock()
	if e == nil {
		return 0, fmt.Errorf("pgdocktest: no delivered event %s", eventID)
	}
	return f.post(w, e)
}

// Sign returns the PGDock-Signature header for body at t with secrets.
func Sign(t time.Time, body []byte, secrets ...string) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	parts := []string{"t=" + ts}
	for _, s := range secrets {
		m := hmac.New(sha256.New, []byte(s))
		m.Write([]byte(ts + "."))
		m.Write(body)
		parts = append(parts, "v1="+hex.EncodeToString(m.Sum(nil)))
	}
	return strings.Join(parts, ",")
}

func (f *Fake) post(w *Webhook, e *Event) (int, error) {
	f.mu.Lock()
	secrets := []string{w.Secret}
	if w.PreviousSecret != "" {
		secrets = append(secrets, w.PreviousSecret)
	}
	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	hdr.Set("PGDock-Event-Id", e.ID)
	hdr.Set("PGDock-Webhook", w.Name)
	hdr.Set("PGDock-Signature", Sign(f.Now(), e.Body, secrets...))
	for k, v := range w.Headers {
		hdr.Set(k, v)
	}
	target := w.URL
	client := f.Client
	f.mu.Unlock()
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(e.Body))
	if err != nil {
		return 0, err
	}
	req.Header = hdr
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse } // never followed
	resp, err := c.Do(req)
	status := 0
	if err == nil {
		status = resp.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}
	f.mu.Lock()
	f.Deliveries = append(f.Deliveries, Delivery{Webhook: w.ID, EventID: e.ID, Status: status, Err: err, Headers: hdr, Body: e.Body})
	f.mu.Unlock()
	return status, err
}

// --- the API ---

type apiErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func refuse(w http.ResponseWriter, status int, code, msg string) {
	reply(w, status, apiErr{Code: code, Message: msg})
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func newSecret() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return "whsec_" + hex.EncodeToString(b[:])
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.Requests = append(f.Requests, r.Method+" "+r.URL.Path)
	for i, fl := range f.failures {
		if fl.Method == r.Method && strings.HasPrefix(r.URL.Path, fl.PathPrefix) {
			f.failures = slices.Delete(f.failures, i, i+1)
			f.mu.Unlock()
			if fl.RetryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(fl.RetryAfter))
			}
			body := fl.Body
			if body == nil {
				body = apiErr{Code: "injected", Message: http.StatusText(fl.Status)}
			}
			reply(w, fl.Status, body)
			return
		}
	}
	tok, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	f.mu.Unlock()
	if !ok || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		refuse(w, http.StatusUnauthorized, "unauthorized", "sign in or send an API token")
		return
	}
	if f.Now().After(tok.ExpiresAt) {
		refuse(w, http.StatusUnauthorized, "token_expired", "the API token has expired")
		return
	}
	seg := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me":
		grant := map[string]any{"id": tok.ID, "name": tok.Name, "org_id": tok.OrgID, "scopes": tok.Scopes,
			"project_ids": tok.ProjectIDs, "expires_at": tok.ExpiresAt.UTC().Format(time.RFC3339)}
		reply(w, http.StatusOK, map[string]any{"id": "u-1", "email": "owner@acme.example", "platform_role": "user", "token": grant})
	case r.Method == http.MethodGet && len(seg) == 4 && seg[2] == "orgs":
		if seg[3] != tok.OrgID {
			refuse(w, http.StatusNotFound, "not_found", "no such organisation")
			return
		}
		reply(w, http.StatusOK, map[string]any{"id": f.OrgID, "name": f.OrgName, "slug": "acme", "personal": false, "role": "owner",
			"plan": "team", "status": "active", "members_can_create_projects": true, "member_count": 2, "project_count": len(f.projects), "created_at": "2026-01-01T00:00:00Z"})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects":
		f.mu.Lock()
		items := []any{}
		ids := make([]string, 0, len(f.projects))
		for id := range f.projects {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			p := f.projects[id]
			if p.OrgID == tok.OrgID && (tok.ProjectIDs == nil || slices.Contains(tok.ProjectIDs, id)) {
				items = append(items, map[string]any{"id": p.ID, "org_id": p.OrgID, "name": p.Name, "slug": strings.ToLower(p.Name), "status": "active"})
			}
		}
		f.mu.Unlock()
		reply(w, http.StatusOK, map[string]any{"items": items})
	case len(seg) >= 5 && seg[2] == "projects":
		f.serveProject(w, r, tok, seg[3], seg[4:])
	default:
		refuse(w, http.StatusNotFound, "not_found", "no such route")
	}
}

func (f *Fake) serveProject(w http.ResponseWriter, r *http.Request, tok Token, id string, rest []string) {
	f.mu.Lock()
	p := f.projects[id]
	f.mu.Unlock()
	if p == nil || p.OrgID != tok.OrgID || tok.ProjectIDs != nil && !slices.Contains(tok.ProjectIDs, id) {
		refuse(w, http.StatusNotFound, "not_found", "no such project")
		return
	}
	writes := r.Method != http.MethodGet
	if writes && !slices.Contains(tok.Scopes, "write") {
		refuse(w, http.StatusForbidden, "forbidden", "the token lacks the write scope")
		return
	}
	switch {
	case rest[0] == "webhooks":
		f.serveWebhooks(w, r, p, rest[1:])
	case rest[0] == "tables" && len(rest) == 4 && rest[3] == "rows" && r.Method == http.MethodGet:
		f.serveRows(w, r, p, rest[1], rest[2])
	default:
		refuse(w, http.StatusNotFound, "not_found", "no such route")
	}
}

func (wh *Webhook) view() map[string]any {
	names := []string{}
	for k := range wh.Headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var reason any
	if wh.StatusReason != "" {
		reason = wh.StatusReason
	}
	return map[string]any{"id": wh.ID, "project_id": "", "name": wh.Name, "tables": wh.Tables, "events": wh.Events, "columns": wh.Columns,
		"url": wh.URL, "header_names": names, "enabled": wh.Enabled, "status": wh.Status, "status_reason": reason,
		"consecutive_failures": wh.ConsecutiveFailures, "backlog": len(wh.queue), "created_at": wh.CreatedAt.UTC().Format(time.RFC3339)}
}

type webhookReq struct {
	Name    *string           `json:"name"`
	Tables  []string          `json:"tables"`
	Events  []string          `json:"events"`
	Columns *[]string         `json:"columns"`
	URL     *string           `json:"url"`
	Headers map[string]string `json:"headers"`
	Enabled *bool             `json:"enabled"`
	raw     map[string]json.RawMessage
}

func (f *Fake) serveWebhooks(w http.ResponseWriter, r *http.Request, p *project, rest []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var hook *Webhook
	if len(rest) > 0 {
		for _, h := range p.Webhooks {
			if h.ID == rest[0] {
				hook = h
			}
		}
		if hook == nil {
			refuse(w, http.StatusNotFound, "not_found", "no such webhook")
			return
		}
	}
	decode := func() (webhookReq, bool) {
		var req webhookReq
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &req); err != nil {
			refuse(w, http.StatusBadRequest, "invalid_request", err.Error())
			return req, false
		}
		_ = json.Unmarshal(raw, &req.raw)
		return req, true
	}
	check := func(req webhookReq) bool {
		for _, t := range req.Tables {
			if p.Tables[qualify(t)] == nil {
				refuse(w, http.StatusBadRequest, "invalid_request", "no table "+t)
				return false
			}
		}
		for _, e := range req.Events {
			if !slices.Contains([]string{"INSERT", "UPDATE", "DELETE"}, e) {
				refuse(w, http.StatusBadRequest, "invalid_request", "unknown event "+e)
				return false
			}
		}
		if req.URL != nil {
			if u, err := url.Parse(*req.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
				refuse(w, http.StatusBadRequest, "invalid_request", "the URL must be https://")
				return false
			}
		}
		return true
	}
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		items := []any{}
		for _, h := range p.Webhooks {
			items = append(items, h.view())
		}
		reply(w, http.StatusOK, map[string]any{"items": items})
	case len(rest) == 0 && r.Method == http.MethodPost:
		req, ok := decode()
		if !ok || !check(req) {
			return
		}
		if req.Name == nil || len(req.Tables) == 0 || len(req.Events) == 0 || req.URL == nil {
			refuse(w, http.StatusBadRequest, "invalid_request", "name, tables, events and url are required")
			return
		}
		f.hookSeq++
		h := &Webhook{ID: newID(), Name: *req.Name, Tables: req.Tables, Events: req.Events, URL: *req.URL, Headers: req.Headers,
			Enabled: true, Status: "healthy", Secret: newSecret(), CreatedAt: f.Now().Add(time.Duration(f.hookSeq) * time.Microsecond)}
		if req.Columns != nil {
			h.Columns = *req.Columns
		}
		if req.Enabled != nil {
			h.Enabled = *req.Enabled
		}
		p.Webhooks = append(p.Webhooks, h)
		reply(w, http.StatusCreated, map[string]any{"webhook": h.view(), "secret": h.Secret})
	case len(rest) == 1 && r.Method == http.MethodGet:
		reply(w, http.StatusOK, hook.view())
	case len(rest) == 1 && r.Method == http.MethodPatch:
		req, ok := decode()
		if !ok || !check(req) {
			return
		}
		if req.Name != nil {
			hook.Name = *req.Name
		}
		if req.Tables != nil {
			hook.Tables = req.Tables
		}
		if req.Events != nil {
			hook.Events = req.Events
		}
		if _, set := req.raw["columns"]; set {
			hook.Columns = nil
			if req.Columns != nil {
				hook.Columns = *req.Columns
			}
		}
		if req.URL != nil {
			hook.URL = *req.URL
		}
		if req.Headers != nil {
			hook.Headers = req.Headers
		}
		if req.Enabled != nil {
			hook.Enabled = *req.Enabled
			if hook.Enabled && hook.Status == "paused" {
				hook.Status, hook.ConsecutiveFailures = "healthy", 0
			}
			if !hook.Enabled {
				hook.Status = "paused"
			}
		}
		// Saving reinstalls the triggers, which clears a broken status.
		if hook.Status == "broken" {
			hook.Status, hook.StatusReason = "healthy", ""
		}
		reply(w, http.StatusOK, hook.view())
	case len(rest) == 1 && r.Method == http.MethodDelete:
		p.Webhooks = slices.DeleteFunc(p.Webhooks, func(h *Webhook) bool { return h == hook })
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 2 && rest[1] == "rotate-secret" && r.Method == http.MethodPost:
		hook.Secret = newSecret()
		reply(w, http.StatusOK, map[string]any{"secret": hook.Secret})
	case len(rest) == 2 && rest[1] == "test" && r.Method == http.MethodPost:
		f.seq++
		id := fmt.Sprintf("evt_test_%d", f.seq)
		body, _ := json.Marshal(map[string]any{"id": id, "webhook": hook.Name, "project": p.ID, "table": nil, "type": "TEST",
			"record": nil, "old_record": nil, "committed_at": f.Now().UTC().Format(time.RFC3339)})
		f.mu.Unlock()
		status, err := f.post(hook, &Event{ID: id, Body: body})
		f.mu.Lock()
		out := map[string]any{"event_id": id, "ok": err == nil && status >= 200 && status < 300, "status_code": status}
		if err != nil {
			out["error"] = err.Error()
		}
		reply(w, http.StatusOK, out)
	case len(rest) == 2 && rest[1] == "deliveries" && r.Method == http.MethodGet:
		items := []any{}
		for i := len(f.Deliveries) - 1; i >= 0; i-- {
			d := f.Deliveries[i]
			if d.Webhook == hook.ID {
				items = append(items, map[string]any{"id": i + 1, "event_id": d.EventID, "attempt": 1, "status_code": d.Status,
					"succeeded": d.Err == nil && d.Status >= 200 && d.Status < 300, "dead_lettered": false, "created_at": f.Now().UTC().Format(time.RFC3339)})
			}
		}
		reply(w, http.StatusOK, map[string]any{"items": items})
	case len(rest) == 2 && rest[1] == "replay" && r.Method == http.MethodPost:
		var req struct {
			All bool `json:"all"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n := len(hook.Dead)
		if req.All {
			// Replayed dead letters are sent after the events waiting.
			hook.queue = append(hook.queue, hook.Dead...)
			hook.Dead = nil
		} else {
			n = 0
		}
		reply(w, http.StatusOK, map[string]any{"queued": n})
	default:
		refuse(w, http.StatusNotFound, "not_found", "no such route")
	}
}

// filter is the rows endpoint's structured filter.
type filter struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  any    `json:"value"`
	Values []any  `json:"values"`
}

func (fl filter) match(v any) bool {
	s, isNull := fmt.Sprint(v), v == nil
	cmp := func() int {
		a, errA := strconv.ParseFloat(s, 64)
		b, errB := strconv.ParseFloat(fmt.Sprint(fl.Value), 64)
		if errA == nil && errB == nil {
			switch {
			case a < b:
				return -1
			case a > b:
				return 1
			}
			return 0
		}
		return strings.Compare(s, fmt.Sprint(fl.Value))
	}
	switch fl.Op {
	case "is_null":
		return isNull
	case "not_null":
		return !isNull
	case "eq":
		return !isNull && cmp() == 0
	case "neq":
		return !isNull && cmp() != 0
	case "lt":
		return !isNull && cmp() < 0
	case "lte":
		return !isNull && cmp() <= 0
	case "gt":
		return !isNull && cmp() > 0
	case "gte":
		return !isNull && cmp() >= 0
	case "contains":
		return !isNull && strings.Contains(s, fmt.Sprint(fl.Value))
	case "in":
		for _, x := range fl.Values {
			if !isNull && s == fmt.Sprint(x) {
				return true
			}
		}
	}
	return false
}

// serveRows is GET /projects/{id}/tables/{schema}/{table}/rows: structured
// filters, order, limit (≤ 1,000, default 50) and keyset pages on the
// primary key. Values come back as text.
func (f *Fake) serveRows(w http.ResponseWriter, r *http.Request, p *project, schema, table string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := p.Tables[schema+"."+table]
	if t == nil {
		refuse(w, http.StatusNotFound, "not_found", "no such table")
		return
	}
	q := r.URL.Query()
	if q.Get("where") != "" {
		refuse(w, http.StatusBadRequest, "invalid_request", "the fake refuses raw where conditions: Taskiem must never send one")
		return
	}
	var filters []filter
	for _, raw := range q["filter"] {
		var fl filter
		if err := json.Unmarshal([]byte(raw), &fl); err != nil || !slices.Contains(t.Columns, fl.Column) {
			refuse(w, http.StatusBadRequest, "invalid_filter", "bad filter "+raw)
			return
		}
		filters = append(filters, fl)
	}
	type ord struct {
		Column string `json:"column"`
		Desc   bool   `json:"desc"`
	}
	var order []ord
	for _, raw := range q["order"] {
		var o ord
		if err := json.Unmarshal([]byte(raw), &o); err != nil || !slices.Contains(t.Columns, o.Column) {
			refuse(w, http.StatusBadRequest, "invalid_order", "bad order "+raw)
			return
		}
		order = append(order, o)
	}
	for _, k := range t.Key {
		order = append(order, ord{Column: k})
	}
	limit := 50
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 1000 {
			refuse(w, http.StatusBadRequest, "invalid_request", "limit is 1 to 1000")
			return
		}
		limit = n
	}
	var rows []map[string]any
	for _, row := range t.rows {
		ok := true
		for _, fl := range filters {
			if !fl.match(row[fl.Column]) {
				ok = false
				break
			}
		}
		if ok {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for _, o := range order {
			a, b := fmt.Sprint(rows[i][o.Column]), fmt.Sprint(rows[j][o.Column])
			x, ex := strconv.ParseFloat(a, 64)
			y, ey := strconv.ParseFloat(b, 64)
			c := strings.Compare(a, b)
			if ex == nil && ey == nil {
				c = map[bool]int{true: -1, false: 1}[x < y]
				if x == y {
					c = 0
				}
			}
			if c != 0 {
				return (c < 0) != o.Desc
			}
		}
		return false
	})
	keyOf := func(row map[string]any) string {
		vals := make([]string, len(t.Key))
		for i, k := range t.Key {
			vals[i] = fmt.Sprint(row[k])
		}
		raw, _ := json.Marshal(vals)
		return string(raw)
	}
	if after := q.Get("after"); after != "" {
		for i, row := range rows {
			if keyOf(row) == after {
				rows = rows[i+1:]
				break
			}
		}
	}
	page := map[string]any{"order": "primary_key", "key_columns": t.Key}
	if len(rows) > limit {
		page["next"] = keyOf(rows[limit-1])
		rows = rows[:limit]
	}
	cols := []any{}
	for _, c := range t.Columns {
		cols = append(cols, map[string]any{"name": c, "type": "text"})
	}
	out := [][]*string{}
	for _, row := range rows {
		cells := make([]*string, len(t.Columns))
		for i, c := range t.Columns {
			if v, ok := row[c]; ok && v != nil {
				s := fmt.Sprint(v)
				cells[i] = &s
			}
		}
		out = append(out, cells)
	}
	page["columns"], page["rows"] = cols, out
	reply(w, http.StatusOK, page)
}
