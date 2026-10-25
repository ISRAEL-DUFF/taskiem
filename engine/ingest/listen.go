package ingest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wd"
)

// listenPrefix is where test calls arrive, under a tenant's hooks; no
// webhook trigger may use it.
const listenPrefix = "/_listen"

// ErrListenConfig is a webhook config a test address cannot listen with.
var ErrListenConfig = errors.New("the webhook could not be published as it is")

// ListenFor is how long a test address waits for its one call.
const ListenFor = 2 * time.Minute

// Listen is a test address the editor shows: the first call to it within
// ListenFor is kept, as a run would see it, and starts nothing.
type Listen struct {
	ID        uuid.UUID `json:"id"`
	Path      string    `json:"path"` // under the hooks URL: /<tenant>/_listen/<id>/<token>
	ExpiresAt time.Time `json:"expires_at"`
}

// StartListen makes a test address for a webhook trigger config being
// built in a workflow, checked as publishing would check it. Calls to it
// are checked with the environment's secret for the workflow (wdID's
// webhook_ secret), so a person sees whether the sender's signature or
// token would pass. The tenant's listens that ended over an hour ago are
// deleted here.
func StartListen(ctx context.Context, tx pgx.Tx, tenant, workflow uuid.UUID, wdID, env, person string, config map[string]any, now time.Time) (Listen, error) {
	if _, err := parseWebhook(config); err != nil {
		return Listen{}, fmt.Errorf("%w: %w", ErrListenConfig, err)
	}
	def := &wd.Definition{ID: wdID, Trigger: wd.Trigger{Type: "webhook", Config: config}}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return Listen{}, err
	}
	token := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	l := Listen{ID: uuid.New(), ExpiresAt: now.Add(ListenFor).UTC()}
	cfg, _ := json.Marshal(config)
	if _, err := tx.Exec(ctx, `DELETE FROM webhook_listens WHERE tenant_id = $1 AND expires_at < now() - interval '1 hour'`, tenant); err != nil {
		return Listen{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO webhook_listens (id, tenant_id, workflow_id, environment, token_hash, config, secret_name, person, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, l.ID, tenant, workflow, env, hash[:], cfg, WebhookSecret(def), person, l.ExpiresAt); err != nil {
		return Listen{}, err
	}
	l.Path = "/" + tenant.String() + listenPrefix + "/" + l.ID.String() + "/" + token
	return l, nil
}

// ListenState is a test address as the editor polls it.
type ListenState struct {
	Status    string         `json:"status"` // waiting, captured or expired
	ExpiresAt time.Time      `json:"expires_at"`
	Call      map[string]any `json:"call,omitempty"`
}

// ReadListen reads a test address of the workflow, opening its call.
func ReadListen(ctx context.Context, tx pgx.Tx, c pii.Cipher, tenant, workflow, id uuid.UUID, now time.Time) (ListenState, error) {
	var st ListenState
	var captured *time.Time
	var capture []byte
	err := tx.QueryRow(ctx, `SELECT expires_at, captured_at, capture FROM webhook_listens WHERE id = $1 AND workflow_id = $2`, id, workflow).
		Scan(&st.ExpiresAt, &captured, &capture)
	if err != nil {
		return st, err
	}
	switch {
	case captured != nil:
		st.Status = "captured"
		var v map[string]any
		if err := json.Unmarshal(capture, &v); err != nil {
			return st, err
		}
		if c != nil && pii.IsEnvelope(v) {
			opened, err := c.OpenTx(ctx, tx, tenant, v)
			if err != nil {
				return st, err
			}
			v, _ = opened.(map[string]any)
		}
		st.Call = v
	case now.After(st.ExpiresAt):
		st.Status = "expired"
	default:
		st.Status = "waiting"
	}
	return st, nil
}

// listen takes a call to a test address: the address's token names it,
// and the first call while it lasts is kept with what the webhook would
// have said of it (its method, origin and auth), sealed under the
// tenant's key. Later calls get 409; nothing is started.
func (h *Handler) listen(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	d, ok := h.receive(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "listen"))
	if err != nil {
		replyErr(w, http.StatusNotFound, "not found")
		return
	}
	var row struct {
		env, secret  string
		hash, config []byte
		expires      time.Time
		captured     *time.Time
	}
	err = db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{d.tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT environment, secret_name, token_hash, config, expires_at, captured_at FROM webhook_listens WHERE id = $1`, id).
			Scan(&row.env, &row.secret, &row.hash, &row.config, &row.expires, &row.captured)
	})
	got := sha256.Sum256([]byte(chi.URLParam(r, "token")))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && subtle.ConstantTimeCompare(got[:], row.hash) != 1) {
		replyErr(w, http.StatusNotFound, "no test address here")
		return
	}
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	var config map[string]any
	_ = json.Unmarshal(row.config, &config)
	wc, err := parseWebhook(config)
	if err != nil {
		replyErr(w, http.StatusConflict, err.Error())
		return
	}
	origin := r.Header.Get("Origin")
	if r.Method == http.MethodOptions {
		wc.options.preflight(w, r)
		return
	}
	switch {
	case row.captured != nil:
		replyErr(w, http.StatusConflict, "this test address already has its call: listen again for another")
		return
	case time.Now().After(row.expires):
		replyErr(w, http.StatusGone, "this test address has expired: listen again")
		return
	}
	// What the webhook would say of this call, each check on its own, so a
	// person sees every problem at once.
	checks := map[string]any{}
	if slices.Contains(wc.options.Methods, r.Method) {
		checks["method"] = "ok"
	} else {
		checks["method"] = "the webhook takes " + strings.Join(wc.options.Methods, ", ") + ", not " + r.Method
	}
	switch _, allowed := wc.options.origin(origin); {
	case origin == "":
	case len(wc.options.CORS) == 0:
		checks["origin"] = "the webhook allows no web pages (cors), so a browser at " + origin + " could not read its answer"
	case allowed:
		checks["origin"] = "ok"
		wc.options.corsHeaders(w, origin)
	default:
		checks["origin"] = origin + " is not among the webhook's cors origins"
	}
	secret := row.secret
	switch err := h.verifyCall(ctx, d.tenant, row.env, wc.auth, &secret, wc.options, r.Header, d.body); {
	case err == nil:
		checks["auth"] = "ok"
	case errors.Is(err, errCallRefused):
		checks["auth"] = strings.TrimPrefix(err.Error(), errCallRefused.Error()+": ")
	default:
		h.unavailable(w, r, err)
		return
	}
	p := parseWebhookCall(r, d.body)
	call := map[string]any{
		"method": r.Method, "body": p.body, "headers": p.headers, "query": p.query,
		"bytes": len(d.body), "received_at": history.FormatTime(time.Now()), "checks": checks,
	}
	var stored any = call
	err = db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{d.tenant}, func(tx pgx.Tx) error {
		if h.Store.PII != nil {
			env, err := h.Store.PII.SealTx(ctx, tx, d.tenant, "other", call)
			if err != nil {
				return err
			}
			stored = env
		}
		raw, _ := json.Marshal(stored)
		tag, err := tx.Exec(ctx, `UPDATE webhook_listens SET captured_at = now(), capture = $2 WHERE id = $1 AND captured_at IS NULL AND expires_at > now()`, id, raw)
		if err == nil && tag.RowsAffected() == 0 {
			return errAlreadyCaptured
		}
		return err
	})
	if errors.Is(err, errAlreadyCaptured) {
		replyErr(w, http.StatusConflict, "this test address already has its call: listen again for another")
		return
	}
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"captured": true, "checks": checks})
}

var errAlreadyCaptured = errors.New("already captured")

// parseWebhookCall is a webhook call as the run's trigger sees it: a call
// with no body by a bodiless method (a GET) has its query parameters as
// its body, so one inputs schema serves however the sender calls.
func parseWebhookCall(r *http.Request, body []byte) parsed {
	p := parse(r, body)
	if len(body) == 0 && wd.Bodiless(r.Method) {
		fields := make(map[string]any, len(p.query))
		for k, v := range p.query {
			fields[k] = v
		}
		p.body = fields
	}
	return p
}

// callQuery is a call's query in a fixed order, without env.
func callQuery(r *http.Request) string {
	q := url.Values{}
	for k, v := range r.URL.Query() {
		if k != "env" {
			q[k] = v
		}
	}
	return q.Encode()
}
