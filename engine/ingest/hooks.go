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
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/httpsec"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/remote"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/wd"
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
//	POST /hooks/{tenant}/{path...}                          webhook triggers (and GET, PUT, PATCH,
//	                                                        DELETE where the trigger takes them; OPTIONS for CORS)
//	ANY  /hooks/{tenant}/_listen/{listen}/{token}           a test call, kept for the editor (listen.go)
//	POST /hooks/{tenant}/connectors/{connector}/{trigger}   connector events
//	POST /hooks/apps/{connector}/{trigger}                  events Taskiem's platform app receives for
//	                                                        every business, routed by account (apps.go); GET checks
//	POST /hooks/{tenant}/connectors/{connector}/{trigger}/{env}/{connection}/{token}
//	                                                        the same, for providers whose callback URLs may not carry a query string (path_secret)
//	POST /hooks/{tenant}/callbacks/{run}/{token}/{step}     a call back to a run's callback step (wd-v1 callback steps)
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
	// Egress guards calls a connector's enricher makes to its provider
	// before a run starts (a truncated event's row); nil refuses them.
	Egress *egress.Guard
	// MoneyLog records every normalised money event heard in prod (decision
	// 0045); nil records none.
	MoneyLog *moneylog.Log
	// Apps are Taskiem's platform apps by client name (decision 0051), for
	// triggers delivered to /hooks/apps/{connector}/{trigger}.
	Apps map[string]App

	apps    appGate
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
		rt.Post("/apps/{connector}/{trigger}", h.appEvent)
		rt.Get("/apps/{connector}/{trigger}", h.appHandshake)
		rt.Post("/{tenant}/connectors/{connector}/{trigger}", h.connectorEvent)
		rt.Put("/{tenant}/connectors/{connector}/{trigger}", h.connectorEvent)
		rt.Post("/{tenant}/connectors/{connector}/{trigger}/{env}/{connection}/{token}", h.connectorEvent)
		rt.Put("/{tenant}/connectors/{connector}/{trigger}/{env}/{connection}/{token}", h.connectorEvent)
		rt.Get("/{tenant}/connectors/{connector}/{trigger}", h.connectorHandshake)
		rt.Post("/{tenant}/callbacks/{run}/{token}/{inst}", h.callback)
		rt.Put("/{tenant}/callbacks/{run}/{token}/{inst}", h.callback)
		rt.HandleFunc("/{tenant}"+listenPrefix+"/{listen}/{token}", h.listen)
		rt.Options("/{tenant}/*", h.webhookPreflight)
		for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			rt.Method(m, "/{tenant}/*", http.HandlerFunc(h.webhook))
		}
		h.router = rt
	})
	kind := "webhook"
	if strings.HasPrefix(r.URL.Path, "/apps/") {
		kind = "app"
	} else if strings.Contains(r.URL.Path, "/connectors/") {
		kind = "connector"
	} else if strings.Contains(r.URL.Path, "/callbacks/") {
		kind = "callback"
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
	options       webhookOptions
}

// lookupWebhook finds the webhook trigger at a path in an environment.
func (h *Handler) lookupWebhook(ctx context.Context, tenant uuid.UUID, env, path string) (webhookTrigger, error) {
	var t webhookTrigger
	var opts []byte
	err := db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, workflow_id, version, auth, dedup, secret_name, options FROM triggers WHERE type = 'webhook' AND environment = $1 AND path = $2`,
			env, path).Scan(&t.id, &t.workflow, &t.version, &t.auth, &t.dedup, &t.secret, &opts)
	})
	t.options = loadWebhookOptions(opts)
	return t, err
}

// webhookPreflight answers a browser's OPTIONS before calling a webhook
// that allows web pages (cors).
func (h *Handler) webhookPreflight(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	d, ok := h.receive(w, r)
	if !ok {
		return
	}
	t, err := h.lookupWebhook(ctx, d.tenant, d.env, "/"+chi.URLParam(r, "*"))
	if errors.Is(err, pgx.ErrNoRows) {
		replyErr(w, http.StatusNotFound, "no webhook at this path")
		return
	}
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	t.options.preflight(w, r)
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
	t, err := h.lookupWebhook(ctx, tenant, env, path)
	if errors.Is(err, pgx.ErrNoRows) {
		replyErr(w, http.StatusNotFound, "no webhook at this path")
		return
	}
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	if !slices.Contains(t.options.Methods, r.Method) {
		w.Header().Set("Allow", strings.Join(t.options.Methods, ", "))
		replyErr(w, http.StatusMethodNotAllowed, "this webhook takes "+strings.Join(t.options.Methods, ", "))
		return
	}
	if !t.options.corsHeaders(w, r.Header.Get("Origin")) {
		replyErr(w, http.StatusForbidden, "this webhook does not take calls from web pages at this origin")
		return
	}
	if err := h.verifyCall(ctx, tenant, env, t.auth, t.secret, t.options, r.Header, body); err != nil {
		if !errors.Is(err, errCallRefused) {
			h.unavailable(w, r, err)
			return
		}
		if t.auth == "basic" {
			w.Header().Set("WWW-Authenticate", `Basic realm="taskiem", charset="UTF-8"`)
		}
		replyErr(w, http.StatusUnauthorized, "signature or token invalid")
		return
	}
	p := parseWebhookCall(r, body)
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
	trigger := map[string]any{"type": "webhook", "method": r.Method, "body": p.body, "headers": p.headers, "query": p.query}
	dedup := bodyHash(body)
	if len(body) == 0 && wd.Bodiless(r.Method) {
		// The query is the body: the same query is the same delivery.
		dedup = bodyHash([]byte(r.Method + " " + callQuery(r)))
	}
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
	if ce := (*runtime.CapabilityError)(nil); errors.As(err, &ce) {
		replyErr(w, http.StatusConflict, ce.Error())
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
	if def.Responds() && st.Created && !st.Queued {
		h.respond(w, r, st.Ref, def, out)
		return
	}
	reply(w, http.StatusAccepted, out)
}

// respondBudget is how long a synchronous webhook (respond: true) waits
// for the run's respond step (spec 8.2).
const respondBudget = 30 * time.Second

// respondHeaders are the response headers a respond step may set, besides
// X- headers; the rest are the server's.
var respondHeaders = map[string]bool{"content-type": true, "location": true, "cache-control": true}

// respond answers a synchronous webhook: it watches the run's history for
// its first completed respond step and sends what it says, or the run's
// end, or, when neither comes within respondBudget, the usual 202 with
// the run id so the caller can follow it.
func (h *Handler) respond(w http.ResponseWriter, r *http.Request, ref runtime.RunRef, def *wd.Definition, accepted map[string]any) {
	ctx, cancel := context.WithTimeout(r.Context(), respondBudget)
	defer cancel()
	var after int64
	wait := 50 * time.Millisecond
	for {
		events, err := h.Store.HistoryAfterOpened(ctx, ref, after, 500)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			h.Logger.Warn("ingest: reading the run for its response", "run", ref.ID, "err", err)
			break
		}
		for _, ev := range events {
			after = ev.Seq
			switch {
			case ev.Type == history.StepCompleted && isRespondStep(def, ev.StepID):
				var p history.CompletedPayload
				if err := json.Unmarshal(ev.Payload, &p); err != nil {
					break
				}
				writeResponse(w, p.Output, ref)
				return
			case ev.Type == history.RunCompleted:
				reply(w, http.StatusOK, map[string]any{"run_id": ref.ID, "status": "completed"})
				return
			case ev.Type == history.RunFailed:
				reply(w, http.StatusInternalServerError, map[string]any{"run_id": ref.ID, "status": "failed", "error": "the workflow failed before it answered"})
				return
			case ev.Type == history.RunCancelled:
				reply(w, http.StatusConflict, map[string]any{"run_id": ref.ID, "status": "cancelled"})
				return
			}
		}
		select {
		case <-ctx.Done():
			reply(w, http.StatusAccepted, accepted)
			return
		case <-time.After(wait):
		}
		if wait < 500*time.Millisecond {
			wait *= 2
		}
	}
	reply(w, http.StatusAccepted, accepted)
}

func isRespondStep(def *wd.Definition, inst string) bool {
	_, id := history.SplitInstance(inst)
	st := def.Step(id)
	return st != nil && st.Type == "respond"
}

// writeResponse sends a respond step's output: its status, the headers it
// may set, and its body as JSON, or as text when it is a string.
func writeResponse(w http.ResponseWriter, output any, ref runtime.RunRef) {
	out, _ := output.(map[string]any)
	status := http.StatusOK
	if n, ok := out["status"].(float64); ok && n >= 200 && n < 600 {
		status = int(n)
	}
	if hs, ok := out["headers"].(map[string]any); ok {
		for k, v := range hs {
			if lk := strings.ToLower(k); respondHeaders[lk] || strings.HasPrefix(lk, "x-") {
				if sv, ok := v.(string); ok {
					w.Header().Set(k, sv)
				}
			}
		}
	}
	w.Header().Set("X-Taskiem-Run", ref.ID.String())
	body := out["body"]
	if text, ok := body.(string); ok {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(text))
		return
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// callback takes a call back to a run's callback step (wd-v1 callback
// steps): the address names the run, a token derived from the run's key
// and the step instance, and the instance. A good token delivers the
// request as the step's signal; the run carries on from there.
func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	d, ok := h.receive(w, r)
	if !ok {
		return
	}
	run, err := uuid.Parse(chi.URLParam(r, "run"))
	if err != nil {
		replyErr(w, http.StatusNotFound, "not found")
		return
	}
	inst, err := url.PathUnescape(chi.URLParam(r, "inst"))
	if err != nil {
		replyErr(w, http.StatusNotFound, "not found")
		return
	}
	cr, err := h.Store.Callback(ctx, d.tenant, run)
	if errors.Is(err, runtime.ErrNotFound) {
		replyErr(w, http.StatusNotFound, "no run waits at this address")
		return
	}
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	want, got := history.CallbackToken(cr.Key, inst), chi.URLParam(r, "token")
	if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		replyErr(w, http.StatusUnauthorized, "the callback token does not match")
		return
	}
	switch cr.Status {
	case "completed", "failed", "cancelled":
		reply(w, http.StatusGone, map[string]any{"error": "the run has ended", "run_id": run, "status": cr.Status})
		return
	}
	p := parse(r, d.body)
	payload := map[string]any{"body": p.body, "query": p.query, "received_at": history.FormatTime(time.Now())}
	woke, err := h.Store.DeliverSignal(ctx, d.tenant, cr.Env, history.CallbackEvent, history.CallbackCorrelation(run.String(), inst), payload)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	// Not waiting yet (the call came before the step began) is buffered
	// for it; already called is kept too, and ignored when the step is
	// done, so a repeated call never answers 5xx.
	reply(w, http.StatusAccepted, map[string]any{"run_id": run, "step": inst, "delivered": len(woke) > 0})
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
	// A remotely registered trigger (decision 0021) is delivered to the
	// subscription Taskiem created, named in the URL it registered; it is
	// verified with the secret the provider returned for it, and starts
	// only the workflow that owns it.
	var sub *remote.Subscription
	secret := ""
	if spec.Remote() {
		id, err := uuid.Parse(r.URL.Query().Get("subscription"))
		if err == nil {
			err = db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
				var err error
				sub, err = remote.Lookup(ctx, tx, id)
				return err
			})
			if err != nil {
				h.unavailable(w, r, err)
				return
			}
		}
		if sub == nil || sub.Connector != ref || sub.Trigger != name || sub.Environment != env {
			replyErr(w, http.StatusNotFound, "no such subscription")
			return
		}
		if !sub.Present {
			// Being deleted at the provider: refuse for good, not for retry.
			replyErr(w, http.StatusGone, "this subscription was removed")
			return
		}
		connection = sub.Connection
		use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindWebhook, Purpose: secrets.PurposeIngestVerify})
		secret, err = h.Secrets.Get(use, tenant, remote.SecretEnv, remote.SecretName(sub.ID))
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			h.unavailable(w, r, err)
			return
		}
	} else {
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
		if spec.Verify != nil {
			secret = creds[spec.Verify.SecretField]
		}
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
	// A provider's test of the endpoint: verified and acknowledged, and
	// started only by workflows that subscribe to its event by name.
	test := false
	if spec.TestEvent != "" {
		v, err := h.exprs.Eval(spec.TestEvent, act)
		b, _ := v.(bool)
		test = b && err == nil
	}
	// Subscribed workflows, loaded once for every event in the delivery.
	var subs []subscriber
	err = db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if sub != nil && test {
			if err := remote.TestReceived(ctx, tx, sub.ID); err != nil {
				return err
			}
		}
		owner := uuid.Nil
		if sub != nil {
			owner = sub.WorkflowID
		}
		var err error
		subs, err = loadSubscribers(ctx, tx, env, ref, name, owner)
		return err
	})
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	here := target{tenant: tenant, connection: connection, throttled: d.throttled, subs: subs}
	// A partner holding a provider's one subscription for all its
	// customers routes each event on to the sub-tenants it is about
	// (decision 0031). Not for a provider's test, nor a remote trigger.
	route := spec.Route != nil && sub == nil && !test

	items, err := h.itemsOf(spec, name, act, body)
	if err != nil {
		replyErr(w, http.StatusBadRequest, "unexpected payload for "+ref+" "+name)
		return
	}
	results := []map[string]any{}
	for _, it := range items {
		if it.ignored {
			results = append(results, map[string]any{"ignored": it.event})
			continue
		}
		ev := eventItem{conn: conn, ref: ref, name: name, env: env, event: it.event, dedup: it.dedup, correlation: it.correlation,
			body: p.body, item: it.item, split: spec.Split != "", test: test}
		out, ok := h.deliverInto(ctx, w, r, here, ev)
		if !ok {
			return
		}
		if route {
			// An event without the key (or whose key expression fails)
			// matches no sub-tenant: it is recorded as unrouted.
			key, _ := evalString(h.exprs, spec.Route.Key, it.act)
			n, ok := h.routeEvent(ctx, w, r, tenant, spec.Route.Field, key, ev)
			if !ok {
				return
			}
			out["routed"] = n
		}
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

// deliveryItem is one event of a verified delivery, as the trigger's
// expressions read it.
type deliveryItem struct {
	item                      any
	act                       map[string]any // body, headers, query, and item
	event, dedup, correlation string
	ignored                   bool // an event the trigger does not send
}

// itemsOf reads a verified delivery's events: one, or one per element of
// the trigger's split, each with its event, dedup key and correlation. The
// default dedup key hashes the verified body (and the item's place), so a
// replayed delivery is recognised even when the manifest's key comes out
// empty. An expression that fails is an error: the payload is not what the
// trigger expects.
func (h *Handler) itemsOf(spec connector.TriggerSpec, name string, act map[string]any, body []byte) ([]deliveryItem, error) {
	items := []any{nil}
	if spec.Split != "" {
		v, err := h.exprs.Eval(spec.Split, act)
		list, ok := v.([]any)
		if err != nil || !ok {
			return nil, fmt.Errorf("split: %v", err)
		}
		items = list
	}
	out := make([]deliveryItem, 0, len(items))
	for i, item := range items {
		it := deliveryItem{item: item, act: act, event: name}
		if spec.Split != "" {
			it.act = map[string]any{"body": act["body"], "headers": act["headers"], "query": act["query"], "item": item}
		}
		fallback := bodyHash(body)
		if spec.Split != "" {
			fallback += ":" + strconv.Itoa(i)
		}
		for _, f := range []struct {
			src string
			dst *string
		}{{spec.EventType, &it.event}, {spec.Dedup, &it.dedup}, {spec.Correlation, &it.correlation}} {
			if f.src == "" {
				continue
			}
			v, err := evalString(h.exprs, f.src, it.act)
			if err != nil {
				return nil, err
			}
			*f.dst = v
		}
		if it.dedup == "" {
			it.dedup = fallback
		}
		it.ignored = len(spec.Events) > 0 && !slices.Contains(spec.Events, it.event)
		out = append(out, it)
	}
	return out, nil
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

// enrichTimeout bounds an enricher's calls to the provider, inside the
// delivery's budget.
const enrichTimeout = 3 * time.Second

// enrich lets a connector complete a verified event before the run it
// starts is recorded. The connection is read only if the enricher calls
// the provider.
func (h *Handler) enrich(ctx context.Context, f connector.Enricher, tenant uuid.UUID, env string, conn *connector.Connector, connection string, opts map[string]any, body any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, enrichTimeout)
	defer cancel()
	call := connector.EventCall{Options: opts, Connect: func() (map[string]string, *http.Client, error) {
		if h.Egress == nil {
			return nil, nil, errors.New("no egress guard: this edge cannot call providers")
		}
		use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: secrets.PurposeIngestEnrich})
		creds, err := h.Connections.Credentials(use, tenant, env, conn.Manifest.ID, connection)
		if err != nil {
			return nil, nil, err
		}
		pol := egress.Policy{Tenant: tenant.String(), Hosts: conn.Manifest.HostsFor(creds), Purpose: "connector:" + conn.Manifest.ID + ":ingest"}
		return creds, h.Egress.Client(pol, enrichTimeout), nil
	}}
	return f(ctx, call, body)
}

func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
