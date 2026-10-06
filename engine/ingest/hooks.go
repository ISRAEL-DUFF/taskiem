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
//
// A delivery names its environment with ?env= (default prod).
type Handler struct {
	Store       *runtime.Store
	Secrets     Secrets
	Connections Connections
	Registry    *connector.Registry
	Logger      *slog.Logger
	// Rate and Burst are each tenant's hard ingest ceiling; beyond it
	// deliveries get 429 with Retry-After (spec 8.3).
	Rate  rate.Limit
	Burst int

	once     sync.Once
	router   http.Handler
	limiters sync.Map
	exprs    *expr.Engine // connector manifest expressions: body, headers, query
	wdExprs  *expr.Engine // WD expressions: trigger
}

const (
	maxBody         = 1 << 20
	budget          = 5 * time.Second // spec 8.2
	signatureHeader = "X-Taskiem-Signature"
)

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.once.Do(func() {
		if h.Logger == nil {
			h.Logger = slog.Default()
		}
		if h.Rate == 0 {
			h.Rate, h.Burst = 50, 200
		}
		h.exprs = expr.MustNewWithRoots("body", "headers", "query", "item")
		h.wdExprs = expr.MustNew()
		rt := chi.NewRouter()
		rt.Post("/{tenant}/connectors/{connector}/{trigger}", h.connectorEvent)
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

// receive does the checks shared by both routes: tenant, ceiling, body.
func (h *Handler) receive(w http.ResponseWriter, r *http.Request) (tenant uuid.UUID, env string, body []byte, ok bool) {
	tenant, err := uuid.Parse(chi.URLParam(r, "tenant"))
	if err != nil {
		replyErr(w, http.StatusNotFound, "not found")
		return
	}
	l, _ := h.limiters.LoadOrStore(tenant, rate.NewLimiter(h.Rate, h.Burst))
	if !l.(*rate.Limiter).Allow() {
		w.Header().Set("Retry-After", "1")
		replyErr(w, http.StatusTooManyRequests, "ingest rate exceeded")
		return
	}
	body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		replyErr(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	env = r.URL.Query().Get("env")
	if env == "" {
		env = "prod"
	}
	return tenant, env, body, true
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
	tenant, env, body, ok := h.receive(w, r)
	if !ok {
		return
	}
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
			secret, err = h.Secrets.Get(ctx, tenant, env, *t.secret)
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
	ref, created, err := h.Store.StartRun(ctx, runtime.StartRequest{
		TenantID: tenant, WorkflowID: t.workflow, Version: t.version, Environment: env,
		Trigger:   trigger,
		StartedBy: "webhook:" + path, TriggerID: "webhook/" + t.workflow.String() + "/" + env, DedupKey: dedup,
	})
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	reply(w, http.StatusAccepted, map[string]any{"run_id": ref.ID, "duplicate": !created})
}

// connectorEvent handles a provider webhook declared in a connector
// manifest: it verifies with the connection's credential, delivers a signal
// named "<connector>:<trigger>" to runs waiting on the correlation, and
// starts every workflow subscribed to the event.
func (h *Handler) connectorEvent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	tenant, env, body, ok := h.receive(w, r)
	if !ok {
		return
	}
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
	creds, err := h.Connections.Credentials(ctx, tenant, env, conn.Manifest.ID, connection)
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
		event, dedup, correlation := name, bodyHash(body), ""
		if spec.Split != "" {
			dedup += ":" + strconv.Itoa(i)
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
			woke, fresh, err := h.Store.DeliverSignalOnce(ctx, tenant, ref+":"+name, correlation, dedup, payload)
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
			run, _, err := h.Store.StartRun(ctx, runtime.StartRequest{
				TenantID: tenant, WorkflowID: s.workflow, Version: s.version, Environment: env,
				Trigger: trig, StartedBy: "connector:" + ref, TriggerID: "event/" + s.workflow.String() + "/" + env, DedupKey: dedup,
			})
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
	tenant, env, _, ok := h.receive(w, r)
	if !ok {
		return
	}
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
