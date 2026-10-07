package ingest

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/httpsec"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Secrets reads an environment secret (webhook HMAC keys and tokens).
type Secrets interface {
	Get(ctx context.Context, tenant uuid.UUID, env, name string) (string, error)
}

// Connections reads a connection's credentials (connector webhook keys).
type Connections interface {
	Credentials(ctx context.Context, tenant uuid.UUID, env, connector, name string) (map[string]string, error)
}

// Handler receives webhooks (role "edge"), mounted at /hooks:
//
//	POST /hooks/{tenant}/{path...}                          webhook triggers
//	POST /hooks/{tenant}/connectors/{connector}/{trigger}   connector events
//	POST /hooks/{tenant}/connectors/{connector}/{trigger}/{env}/{connection}/{token}
//	                                                        the same, for providers whose callback URLs may not carry a query string (path_secret)
//
// Connector events are also taken as PUT (MTN MoMo calls back with PUT or
// POST). A delivery names its environment with ?env= (default prod), or in
// the path form.
type Handler struct {
	Store       *runtime.Store
	Secrets     Secrets
	Connections Connections
	Registry    *connector.Registry
	Logger      *slog.Logger
	// Rate and Burst, when set, replace every tenant's hard ingest ceiling
	// (tests). Otherwise each tenant's limits apply (spec 8.3, 16): above
	// its soft rate deliveries are accepted and their runs queued; above
	// its ceiling they get 429 with Retry-After.
	Rate  rate.Limit
	Burst int

	once    sync.Once
	router  http.Handler
	tenants tenantGate
	exprs   *expr.Engine // connector manifest expressions: body, headers, query
	wdExprs *expr.Engine // WD expressions: trigger
}

const (
	budget          = 5 * time.Second // spec 8.2
	signatureHeader = "X-Taskiem-Signature"
)

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.once.Do(func() {
		if h.Logger == nil {
			h.Logger = slog.Default()
		}
		h.exprs = expr.MustNewTriggerEngine("body", "headers", "query", "item")
		h.wdExprs = expr.MustNew()
		rt := chi.NewRouter()
		rt.Post("/{tenant}/connectors/{connector}/{trigger}", h.connectorEvent)
		rt.Put("/{tenant}/connectors/{connector}/{trigger}", h.connectorEvent)
		rt.Post("/{tenant}/connectors/{connector}/{trigger}/{env}/{connection}/{token}", h.connectorEvent)
		rt.Put("/{tenant}/connectors/{connector}/{trigger}/{env}/{connection}/{token}", h.connectorEvent)
		rt.Get("/{tenant}/connectors/{connector}/{trigger}", h.connectorHandshake)
		rt.Post("/{tenant}/*", h.webhook)
		h.router = rt
	})
	kind := "webhook"
	if strings.Contains(r.URL.Path, "/connectors/") {
		kind = "connector"
	}
	ctx, span := telemetry.Tracer().Start(r.Context(), "ingest "+kind)
	defer span.End()
	// Answers come from the hooks origin; some carry text a manifest chose.
	httpsec.Set(w.Header())
	ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
	h.router.ServeHTTP(ww, r.WithContext(ctx))
	span.SetAttributes(attribute.Int("http.status_code", ww.Status()))
	telemetry.Ingest.WithLabelValues(kind, strconv.Itoa(ww.Status())).Inc()
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func replyErr(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}

// errUnavailable asks the provider to retry: the delivery was not recorded.
func (h *Handler) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	h.Logger.Error("ingest failed", "path", r.URL.Path, "err", err)
	w.Header().Set("Retry-After", "5")
	replyErr(w, http.StatusServiceUnavailable, "not recorded; retry")
}

// delivery is what receive learned about a request.
type delivery struct {
	tenant uuid.UUID
	env    string
	body   []byte
	// throttled: the tenant is above its soft ingest rate, so runs this
	// delivery starts wait as queued (spec 8.3). Signals are not held back.
	throttled bool
}

// receive does the checks shared by both routes: tenant, ceiling, soft
// limit, body size.
func (h *Handler) receive(w http.ResponseWriter, r *http.Request) (d delivery, ok bool) {
	tenant, err := uuid.Parse(chi.URLParam(r, "tenant"))
	if err != nil {
		replyErr(w, http.StatusNotFound, "not found")
		return
	}
	g, err := h.gate(r.Context(), tenant)
	switch {
	case errors.Is(err, errBusy):
		w.Header().Set("Retry-After", "1")
		replyErr(w, http.StatusTooManyRequests, "ingest rate exceeded")
		return
	case err != nil:
		h.unavailable(w, r, err)
		return
	case g.hard == nil:
		replyErr(w, http.StatusNotFound, "not found")
		return
	case g.suspended:
		h.suspended(w, r, tenant)
		return
	}
	if !g.hard.Allow() {
		h.Store.LimitHit(r.Context(), tenant, "ingest_ceiling")
		w.Header().Set("Retry-After", "1")
		reply(w, http.StatusTooManyRequests, map[string]string{"error": "ingest rate exceeded", "code": "rate_limited"})
		return
	}
	d.throttled = !g.soft.Allow()
	max := int64(g.maxBody)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		h.Store.LimitHit(r.Context(), tenant, "max_payload_bytes")
		replyErr(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	d.tenant, d.body = tenant, body
	d.env = r.URL.Query().Get("env")
	if e := chi.URLParam(r, "env"); e != "" {
		d.env = e // the path form of a connector event
	}
	if d.env == "" {
		d.env = "prod"
	}
	return d, true
}

// suspended refuses a delivery to a suspended tenant with 423 Locked, so
// providers that retry deliver it again after the tenant is resumed, and
// counts it per day (ingest_refusals). Nothing else of it is recorded.
func (h *Handler) suspended(w http.ResponseWriter, r *http.Request, tenant uuid.UUID) {
	kind := "webhook"
	if strings.Contains(r.URL.Path, "/connectors/") {
		kind = "connector"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	if err := db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ingest_refusals (tenant_id, day, kind, reason) VALUES ($1, (now() AT TIME ZONE 'UTC')::date, $2, 'suspended')
			ON CONFLICT (tenant_id, day, kind, reason) DO UPDATE SET hits = ingest_refusals.hits + 1, last_at = now()`, tenant, kind)
		return err
	}); err != nil {
		h.Logger.Warn("ingest: recording a refused delivery", "tenant", tenant, "err", err)
	}
	reply(w, http.StatusLocked, map[string]string{"error": "the organisation is suspended", "code": "suspended"})
}

// limited answers a start refused by a tenant limit: 429 with a code and,
// when waiting helps, Retry-After. Nothing was recorded for the run.
func limited(w http.ResponseWriter, le *runtime.LimitError) {
	if le.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(le.RetryAfter.Round(time.Second).Seconds())))
	}
	reply(w, http.StatusTooManyRequests, map[string]string{"error": le.Message, "code": le.Code, "limit": le.Limit})
}

// tenantGate holds the ingest limiters of each tenant that exists. It is
// bounded, and forgets idle tenants, so deliveries to made-up tenant ids
// cost a rate-limited lookup and no memory that lasts. Limiters are per
// process: with several edge replicas a tenant's rates apply to each.
type tenantGate struct {
	mu      sync.Mutex
	entries map[uuid.UUID]*gateEntry
	lookups *rate.Limiter // database checks for tenants not cached
}

type gateEntry struct {
	hard    *rate.Limiter // the ceiling; nil: no such tenant
	soft    *rate.Limiter // the soft ingest rate
	maxBody int
	// suspended: the tenant takes no deliveries until resumed (423).
	suspended bool
	checked   time.Time
	seen      time.Time
}

// rateOf turns a limit into a limiter's rate; 0 is no limit.
func rateOf(perSecond float64, burst int) (rate.Limit, int) {
	if perSecond <= 0 {
		return rate.Inf, 0
	}
	return rate.Limit(perSecond), max(burst, 1)
}

func (e *gateEntry) apply(l runtime.Limits, hardRate rate.Limit, hardBurst int) {
	if hardRate == 0 {
		hardRate, hardBurst = rateOf(l.IngestCeiling, l.IngestCeilingBurst)
	}
	softRate, softBurst := rateOf(l.IngestRate, l.IngestBurst)
	if e.hard == nil {
		e.hard, e.soft = rate.NewLimiter(hardRate, hardBurst), rate.NewLimiter(softRate, softBurst)
	} else {
		e.hard.SetLimit(hardRate)
		e.hard.SetBurst(hardBurst)
		e.soft.SetLimit(softRate)
		e.soft.SetBurst(softBurst)
	}
	e.maxBody = l.MaxPayloadBytes
	if e.maxBody <= 0 || e.maxBody > runtime.MaxPayload {
		e.maxBody = runtime.MaxPayload
	}
}

const (
	gateMax    = 10000            // tenants remembered at once
	gateTTL    = time.Minute      // how long whether a tenant exists is trusted
	gateIdle   = 10 * time.Minute // a tenant not seen for this long may be forgotten
	gateLookup = 100              // tenant checks per second, across all tenants
)

// errBusy: too many unknown tenants are being looked up; retry shortly.
var errBusy = errors.New("ingest: too many tenant lookups")

// gate returns the tenant's limiters (a copy; hard is nil if there is no
// such tenant). Its limits are re-read every gateTTL.
func (h *Handler) gate(ctx context.Context, tenant uuid.UUID) (gateEntry, error) {
	g := &h.tenants
	now := time.Now()
	g.mu.Lock()
	if g.entries == nil {
		g.entries = map[uuid.UUID]*gateEntry{}
		g.lookups = rate.NewLimiter(gateLookup, gateLookup)
	}
	e := g.entries[tenant]
	if e != nil && now.Sub(e.checked) < gateTTL {
		e.seen = now
		g.mu.Unlock()
		return *e, nil
	}
	allowed := g.lookups.Allow()
	g.mu.Unlock()
	if !allowed {
		return gateEntry{}, errBusy
	}
	var status string
	err := db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id = $1`, tenant).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return gateEntry{}, err
	}
	exists := status != "" && status != "deleted"
	var lim runtime.Limits
	if exists {
		if lim, err = h.Store.LimitsFor(ctx, tenant); err != nil {
			return gateEntry{}, err
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if e = g.entries[tenant]; e == nil {
		if len(g.entries) >= gateMax {
			g.evict(now)
		}
		e = &gateEntry{}
		g.entries[tenant] = e
	}
	e.checked, e.seen = now, now
	e.suspended = status == "suspended"
	if !exists {
		e.hard, e.soft = nil, nil
	} else {
		e.apply(lim, h.Rate, h.Burst)
	}
	return *e, nil
}

// evict makes room: idle tenants first, then unknown ones, then any.
func (g *tenantGate) evict(now time.Time) {
	for id, e := range g.entries {
		if now.Sub(e.seen) > gateIdle {
			delete(g.entries, id)
		}
	}
	for id, e := range g.entries {
		if len(g.entries) < gateMax {
			return
		}
		if e.hard == nil {
			delete(g.entries, id)
		}
	}
	for id := range g.entries {
		if len(g.entries) < gateMax {
			return
		}
		delete(g.entries, id)
	}
}

// parsed is a delivery as expressions and the run's trigger root see it.
type parsed struct {
	body    any
	headers map[string]any
	query   map[string]any
}

var hiddenHeaders = map[string]bool{"authorization": true, "cookie": true, strings.ToLower(signatureHeader): true}

func parse(r *http.Request, body []byte, hide ...string) parsed {
	p := parsed{headers: map[string]any{}, query: map[string]any{}}
	if v, err := expr.DecodeJSON(body); err == nil {
		p.body = v
	} else if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "application/x-www-form-urlencoded" {
		// Form posts (Africa's Talking callbacks, Slack interactions):
		// fields become the body's keys.
		fields := map[string]any{}
		if vals, err := url.ParseQuery(string(body)); err == nil {
			for k, v := range vals {
				if len(v) > 0 {
					fields[k] = v[0]
				}
			}
		}
		p.body = fields
	} else {
		p.body = string(body)
	}
	for k, v := range r.Header {
		k = strings.ToLower(k)
		if hiddenHeaders[k] || slices.Contains(hide, k) || len(v) == 0 {
			continue
		}
		p.headers[k] = v[0]
	}
	for k, v := range r.URL.Query() {
		if k != "env" && !slices.Contains(hide, "query:"+k) && len(v) > 0 {
			p.query[k] = v[0]
		}
	}
	return p
}

func (p parsed) activation() map[string]any {
	return map[string]any{"body": p.body, "headers": p.headers, "query": p.query}
}

func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func evalString(e *expr.Engine, src string, act map[string]any) (string, error) {
	v, err := e.Eval(src, act)
	if err != nil {
		return "", err
	}
	switch t := v.(type) {
	case string:
		return t, nil
	case nil:
		return "", nil
	}
	return fmt.Sprint(v), nil
}

type webhookTrigger struct {
	id, workflow  uuid.UUID
	version       int
	auth          string
	dedup, secret *string
}

// webhook handles a webhook trigger delivery.
func (h *Handler) webhook(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	d, ok := h.receive(w, r)
	if !ok {
		return
	}
	tenant, env, body := d.tenant, d.env, d.body
	path := "/" + chi.URLParam(r, "*")
	var t webhookTrigger
	err := db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, workflow_id, version, auth, dedup, secret_name FROM triggers WHERE type = 'webhook' AND environment = $1 AND path = $2`,
			env, path).Scan(&t.id, &t.workflow, &t.version, &t.auth, &t.dedup, &t.secret)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		replyErr(w, http.StatusNotFound, "no webhook at this path")
		return
	}
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	if t.auth != "none" {
		secret := ""
		if t.secret != nil {
			use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindWebhook, Purpose: secrets.PurposeIngestVerify})
			secret, err = h.Secrets.Get(use, tenant, env, *t.secret)
			if err != nil && !errors.Is(err, secrets.ErrNotFound) {
				h.unavailable(w, r, err)
				return
			}
		}
		scheme := map[string]string{"hmac": "hmac_sha256", "bearer": "bearer"}[t.auth]
		if err := connector.VerifyWebhook(&connector.VerifySpec{Scheme: scheme, Header: signatureHeader}, secret, r.Header, body); err != nil {
			replyErr(w, http.StatusUnauthorized, "signature or token invalid")
			return
		}
	}
	p := parse(r, body)
	def, err := h.Store.Definition(ctx, tenant, t.workflow, t.version)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	raw, _ := json.Marshal(p.body)
	if msgs := def.ValidateInputs(raw); len(msgs) > 0 {
		reply(w, http.StatusUnprocessableEntity, map[string]any{"error": "body does not match the workflow's inputs schema", "problems": msgs})
		return
	}
	trigger := map[string]any{"type": "webhook", "body": p.body, "headers": p.headers, "query": p.query}
	dedup := bodyHash(body)
	if t.dedup != nil {
		// A WD expression: it sees the run's trigger root, as steps do.
		if dedup, err = evalString(h.wdExprs, *t.dedup, map[string]any{"trigger": trigger}); err != nil || dedup == "" {
			replyErr(w, http.StatusBadRequest, "dedup expression failed on this body")
			return
		}
	}
	st, err := h.Store.Start(ctx, runtime.StartRequest{
		TenantID: tenant, WorkflowID: t.workflow, Version: t.version, Environment: env,
		Trigger:   trigger,
		StartedBy: "webhook:" + path, TriggerID: "webhook/" + t.workflow.String() + "/" + env, DedupKey: dedup,
		Throttled: d.throttled,
	})
	if le, ok := runtime.IsLimit(err); ok {
		limited(w, le)
		return
	}
	if errors.Is(err, runtime.ErrTenantSuspended) {
		h.suspended(w, r, tenant)
		return
	}
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	out := map[string]any{"run_id": st.Ref.ID, "duplicate": !st.Created}
	if st.Queued {
		out["queued"] = true
	}
	reply(w, http.StatusAccepted, out)
}

// connectorEvent handles a provider webhook declared in a connector
// manifest: it verifies with the connection's credential, delivers a signal
// named "<connector>:<trigger>" to runs waiting on the correlation, and
// starts every workflow subscribed to the event.
func (h *Handler) connectorEvent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	d, ok := h.receive(w, r)
	if !ok {
		return
	}
	tenant, env, body := d.tenant, d.env, d.body
	ref, name := chi.URLParam(r, "connector"), chi.URLParam(r, "trigger")
	reg, err := h.Registry.For(ctx, tenant.String())
	if err != nil {
		replyErr(w, http.StatusServiceUnavailable, "try again")
		return
	}
	conn, ok := reg.Get(ref)
	var spec connector.TriggerSpec
	if ok {
		spec, ok = conn.Manifest.Triggers[name]
	}
	if !ok {
		replyErr(w, http.StatusNotFound, "no such connector trigger")
		return
	}
	connection := r.URL.Query().Get("connection")
	pathToken := chi.URLParam(r, "token")
	if pathToken != "" {
		// The path form exists only for triggers verified by the token in it.
		if spec.Verify == nil || spec.Verify.Scheme != "path_secret" {
			replyErr(w, http.StatusNotFound, "no such connector trigger")
			return
		}
		connection = chi.URLParam(r, "connection")
	}
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: secrets.PurposeIngestVerify})
	creds, err := h.Connections.Credentials(use, tenant, env, conn.Manifest.ID, connection)
	if errors.Is(err, secrets.ErrAmbiguous) {
		replyErr(w, http.StatusUnauthorized, "several connections match; name one with ?connection=")
		return
	}
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		h.unavailable(w, r, err)
		return
	}
	secret := ""
	if spec.Verify != nil {
		secret = creds[spec.Verify.SecretField]
	}
	verify := func() error { return connector.VerifyWebhook(spec.Verify, secret, r.Header, body) }
	if spec.Verify != nil && spec.Verify.Scheme == "query_secret" {
		verify = func() error { return connector.VerifyQuerySecret(spec.Verify, secret, r.URL.Query()) }
	}
	if spec.Verify != nil && spec.Verify.Scheme == "path_secret" {
		verify = func() error { return connector.VerifyPathSecret(secret, pathToken) }
	}
	if spec.Verify != nil && spec.Verify.Scheme == "connector" {
		verify = func() error {
			v := conn.Verifiers[name]
			if v == nil || secret == "" {
				return connector.ErrBadSignature
			}
			return v(secret, r.Header, body)
		}
	}
	if err := verify(); err != nil {
		replyErr(w, http.StatusUnauthorized, "signature invalid")
		return
	}
	hide := []string{}
	if spec.Verify != nil {
		hide = append(hide, strings.ToLower(spec.Verify.Header))
		if spec.Verify.Query != "" {
			hide = append(hide, "query:"+spec.Verify.Query) // the secret never reaches a run
		}
	}
	p := parse(r, body, hide...)
	act := p.activation()
	if hs := spec.Handshake; hs != nil && hs.Method == "POST" {
		v, err := h.exprs.Eval(hs.When, act)
		if b, _ := v.(bool); err == nil && b {
			answer, err := evalString(h.exprs, hs.Respond, act)
			if err != nil {
				replyErr(w, http.StatusBadRequest, "unexpected handshake for "+ref+" "+name)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(answer))
			return
		}
	}
	// Subscribed workflows, loaded once for every event in the delivery.
	type sub struct {
		workflow   uuid.UUID
		version    int
		events     []string
		connection *string
	}
	var subs []sub
	err = db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT workflow_id, version, COALESCE(events, '{}'), connection FROM triggers
			WHERE type = 'connector_event' AND environment = $1 AND connector = $2 AND trigger_name = $3`, env, ref, name)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s sub
			if err := rows.Scan(&s.workflow, &s.version, &s.events, &s.connection); err != nil {
				return err
			}
			subs = append(subs, s)
		}
		return rows.Err()
	})
	if err != nil {
		h.unavailable(w, r, err)
		return
	}

	// A provider that batches events gets one delivery per item.
	items := []any{nil}
	if spec.Split != "" {
		v, err := h.exprs.Eval(spec.Split, act)
		list, ok := v.([]any)
		if err != nil || !ok {
			replyErr(w, http.StatusBadRequest, "unexpected payload for "+ref+" "+name)
			return
		}
		items = list
	}
	results := []map[string]any{}
	for i, item := range items {
		ia := act
		if spec.Split != "" {
			ia = map[string]any{"body": act["body"], "headers": act["headers"], "query": act["query"], "item": item}
		}
		// The default dedup key hashes the verified body, so a replayed
		// delivery is recognised even when the manifest's key comes out empty.
		event, dedup, correlation := name, "", ""
		fallback := bodyHash(body)
		if spec.Split != "" {
			fallback += ":" + strconv.Itoa(i)
		}
		bad := false
		for _, f := range []struct {
			src string
			dst *string
		}{{spec.EventType, &event}, {spec.Dedup, &dedup}, {spec.Correlation, &correlation}} {
			if f.src == "" {
				continue
			}
			if *f.dst, err = evalString(h.exprs, f.src, ia); err != nil {
				bad = true
			}
		}
		if bad {
			replyErr(w, http.StatusBadRequest, "unexpected payload for "+ref+" "+name)
			return
		}
		if dedup == "" {
			dedup = fallback
		}
		if len(spec.Events) > 0 && !slices.Contains(spec.Events, event) {
			results = append(results, map[string]any{"ignored": event})
			continue
		}
		out := map[string]any{"event": event}
		payload := map[string]any{"event": event, "body": p.body}
		trig := map[string]any{"type": "connector_event", "connector": ref, "trigger": name, "event": event, "body": p.body}
		if spec.Split != "" {
			payload["item"], trig["item"] = item, item
		}
		if correlation != "" {
			woke, fresh, err := h.Store.DeliverSignalOnce(ctx, tenant, env, ref+":"+name, correlation, dedup, payload)
			if err != nil {
				h.unavailable(w, r, err)
				return
			}
			out["signalled"], out["duplicate"] = len(woke), !fresh
		}
		runs := []uuid.UUID{}
		for _, s := range subs {
			if len(s.events) > 0 && !slices.Contains(s.events, event) {
				continue
			}
			if s.connection != nil && *s.connection != connection {
				continue
			}
			// Signals above were delivered whatever the tenant's limits; a
			// refused start fails the delivery so the provider retries, and
			// the retry's signal is recognised as a duplicate.
			run, _, err := h.Store.StartRun(ctx, runtime.StartRequest{
				TenantID: tenant, WorkflowID: s.workflow, Version: s.version, Environment: env,
				Trigger: trig, StartedBy: "connector:" + ref, TriggerID: "event/" + s.workflow.String() + "/" + env, DedupKey: dedup,
				Throttled: d.throttled,
			})
			if le, ok := runtime.IsLimit(err); ok {
				limited(w, le)
				return
			}
			if errors.Is(err, runtime.ErrTenantSuspended) {
				h.suspended(w, r, tenant)
				return
			}
			if err != nil {
				h.unavailable(w, r, err)
				return
			}
			runs = append(runs, run.ID)
		}
		out["runs"] = runs
		results = append(results, out)
	}
	if a := spec.Ack; a != nil {
		status := a.Status
		if status == 0 {
			status = http.StatusOK
		}
		ct := a.ContentType
		if ct == "" {
			ct = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(a.Body))
		return
	}
	if spec.Split != "" {
		reply(w, http.StatusAccepted, map[string]any{"events": results})
		return
	}
	reply(w, http.StatusAccepted, results[0])
}

// connectorHandshake answers a provider's GET endpoint check (Meta's
// hub.challenge): the token it sends must equal the connection's secret.
func (h *Handler) connectorHandshake(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	d, ok := h.receive(w, r)
	if !ok {
		return
	}
	tenant, env := d.tenant, d.env
	ref, name := chi.URLParam(r, "connector"), chi.URLParam(r, "trigger")
	reg, err := h.Registry.For(ctx, tenant.String())
	if err != nil {
		replyErr(w, http.StatusServiceUnavailable, "try again")
		return
	}
	conn, ok := reg.Get(ref)
	var hs *connector.HandshakeSpec
	if ok {
		hs = conn.Manifest.Triggers[name].Handshake
	}
	if hs == nil || hs.Method != "GET" {
		replyErr(w, http.StatusNotFound, "no such connector trigger")
		return
	}
	creds, err := h.Connections.Credentials(ctx, tenant, env, conn.Manifest.ID, r.URL.Query().Get("connection"))
	if err != nil && !errors.Is(err, secrets.ErrNotFound) && !errors.Is(err, secrets.ErrAmbiguous) {
		h.unavailable(w, r, err)
		return
	}
	want, got := creds[hs.SecretField], r.URL.Query().Get(hs.TokenQuery)
	if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		replyErr(w, http.StatusForbidden, "verification token does not match")
		return
	}
	answer, err := evalString(h.exprs, hs.Respond, parse(r, nil).activation())
	if err != nil {
		replyErr(w, http.StatusBadRequest, "unexpected handshake for "+ref+" "+name)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(answer))
}
