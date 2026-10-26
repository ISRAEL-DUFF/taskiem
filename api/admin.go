package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/remote"
	"github.com/israel-duff/taskiem/engine/secrets"
)

var nameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

// envParam checks an environment named in a path or query against the
// caller's key scope.
func envParam(r *http.Request, v string) (string, error) {
	return environment(principalFrom(r.Context()), v)
}

// secretEnv is an environment the secrets API may touch: a tenant
// environment in the caller's scope, never a reserved one (_identity, _git,
// _alerts), which hold the platform's own secrets.
func secretEnv(r *http.Request, v string) (string, error) {
	env, err := envParam(r, v)
	if err == nil && !envNameRe.MatchString(env) {
		err = fmt.Errorf("%w: %q is not an environment secrets can be written to", errBadRequest, env)
	}
	return env, err
}

// tenantWide refuses API keys limited to one environment on routes whose
// effect reaches every environment.
func (s *Server) tenantWide(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := principalFrom(r.Context()); p.Environment != "" {
			writeErr(w, http.StatusForbidden, "this key is limited to "+p.Environment+"; this reaches every environment")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- connections ---

type connectionInfo struct {
	ID          uuid.UUID  `json:"id"`
	Environment string     `json:"environment"`
	Connector   string     `json:"connector"`
	Name        string     `json:"name"`
	AuthType    string     `json:"auth_type"`
	Status      string     `json:"status"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"` // last recorded read of its credentials
	// ProvisionedBy names what made the connection when it was not a
	// person: "taskiem:data_tables" for the data tables project's.
	ProvisionedBy *string `json:"provisioned_by"`
	// Mode is live or test, when the key says which (N2).
	Mode *string `json:"mode"`
	// Remote is the state of the subscriptions Taskiem keeps at the
	// provider through this connection (decision 0021), when there are any.
	Remote *remoteSummary `json:"remote,omitempty" db:"-"`
	// MoneyLogWebhooks are the addresses to set in a rail's dashboard so
	// the money log hears this prod connection's events (decision 0045).
	MoneyLogWebhooks []moneyWebhook `json:"money_log_webhooks,omitempty" db:"-"`
	// Notes are what a prod connection still needs from its service, in
	// plain words: Paystack's Pay with Transfer turned on by its support,
	// until a transfer has been collected (the readiness check says the
	// same for a workflow that collects by transfer).
	Notes []string `json:"notes,omitempty" db:"-"`
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	var out []connectionInfo
	only := principalFrom(r.Context()).Environment // a key limited to one environment sees only it
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT c.id, c.environment, c.connector, c.name, c.auth_type, c.status, c.expires_at, c.created_at,
			(SELECT max(sr.at) FROM secret_reads sr WHERE sr.tenant_id = c.tenant_id AND sr.connection_id = c.id), c.provisioned_by, c.mode
			FROM connections c WHERE $1 = '' OR c.environment = $1 ORDER BY c.environment, c.connector, c.name`, only)
		if err != nil {
			return err
		}
		if out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[connectionInfo]); err != nil {
			return err
		}
		for i, c := range out {
			if c.Environment != "prod" || c.Status != "active" || c.Connector != "paystack" {
				continue
			}
			seen, err := transfersSeen(r.Context(), tx, c.Connector, c.Name)
			if err != nil {
				return err
			}
			if !seen {
				out[i].Notes = []string{tr(lang.EN, "ready.paystack_transfer")}
			}
		}
		subs, err := remote.List(r.Context(), tx, `$1 = '' OR environment = $1`, only)
		summarise(out, subs)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if reg, err := s.Registry.For(r.Context(), p.TenantID.String()); err == nil {
		for i, c := range out {
			if c.Environment == "prod" && c.ProvisionedBy == nil {
				out[i].MoneyLogWebhooks = moneyWebhooks(latestConnector(reg, c.Connector), s.hooksBase(), p.TenantID, c.Name)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": nonNil(out)})
}

// DefaultConnectionName is what a connection is called when nobody names
// it, and what templates, the AI builder and storefront payments use
// (no-technical-steps N2): one name, so nothing has to match by hand.
const DefaultConnectionName = "main"

// freeConnectionName is main, or main_2, main_3 … when the business
// already has a connection of that name for the service in env.
func freeConnectionName(ctx context.Context, tx pgx.Tx, env, connector string) (string, error) {
	rows, err := tx.Query(ctx, `SELECT name FROM connections WHERE environment = $1 AND connector = $2`, env, connector)
	if err != nil {
		return "", err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	name := DefaultConnectionName
	for i := 2; slices.Contains(names, name); i++ {
		name = fmt.Sprintf("%s_%d", DefaultConnectionName, i)
	}
	return name, nil
}

type connectionReq struct {
	Environment string            `json:"environment"`
	Connector   string            `json:"connector"` // "paystack@1"
	Name        string            `json:"name"`
	Credentials map[string]string `json:"credentials"`
	// Mode is the choice the person made (live or test): when the
	// connector's keys say which they are, a key for the other is refused;
	// when its modes are hosts (iswallet, Payrolla), it is where the
	// connection's calls go.
	Mode string `json:"mode,omitempty"`
	// Check asks the provider first (the connector's test action): a key
	// it refuses is not saved.
	Check bool `json:"check,omitempty"`
}

// createConnection stores a connection's credentials, encrypted. They are
// never returned by the API.
func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	var req connectionReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	env, err := envParam(r, req.Environment)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	reg, err := s.Registry.For(r.Context(), principalFrom(r.Context()).TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, ok := reg.Get(req.Connector)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: connector %q is not available", errBadRequest, req.Connector))
		return
	}
	if err := checkCredentials(c, req.Credentials); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := checkKeys(c, req.Credentials, req.Mode); err != nil {
		s.fail(w, r, err)
		return
	}
	mode, err := connectionMode(c, req.Credentials, req.Mode)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Check {
		if err := s.testCredentials(r.Context(), p.TenantID, c, withMode(req.Credentials, mode)); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.tx(r, func(tx pgx.Tx) error { return s.checkCount(r.Context(), tx, p.TenantID, "max_connections") }); err != nil {
		s.fail(w, r, err)
		return
	}
	var id uuid.UUID
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if req.Name == "" {
			if req.Name, err = freeConnectionName(r.Context(), tx, env, c.Manifest.ID); err != nil {
				return err
			}
		}
		if id, err = s.Vault.CreateConnectionTx(r.Context(), tx, p.TenantID, env, c.Manifest.ID, req.Name, c.Manifest.Auth.Type, req.Credentials, p.Actor(), ""); err != nil {
			return err
		}
		if err := setConnectionMode(r.Context(), tx, id, mode); err != nil {
			return err
		}
		return secrets.SetRouteKeysTx(r.Context(), tx, p.TenantID, id, c.Manifest.RouteKeys(req.Credentials))
	})
	if err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
			err = fmt.Errorf("%w: a %s connection named %q already exists in %s", errConflict, c.Manifest.ID, req.Name, env)
		}
		s.fail(w, r, err)
		return
	}
	// Remote subscriptions that waited for a connection in this
	// environment are tried again now (decision 0021).
	if err := s.tx(r, func(tx pgx.Tx) error { return remote.Retry(r.Context(), tx, env, c.Ref()) }); err != nil {
		s.Logger.Warn("remote subscriptions: retrying after a new connection", "err", err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "environment": env, "connector": c.Manifest.ID, "name": req.Name})
}

// --- secrets (names only; values are write-only) ---

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	type secret struct {
		Environment string     `json:"environment"`
		Name        string     `json:"name"`
		CreatedBy   string     `json:"created_by"`
		UpdatedAt   time.Time  `json:"updated_at"`
		LastUsedAt  *time.Time `json:"last_used_at"` // last recorded read for use
	}
	var out []secret
	only := principalFrom(r.Context()).Environment
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT s.environment, s.name, s.created_by, s.updated_at,
			(SELECT max(sr.at) FROM secret_reads sr WHERE sr.tenant_id = s.tenant_id AND sr.connection_id IS NULL AND sr.environment = s.environment AND sr.name = s.name)
			FROM secrets s WHERE s.name IS NOT NULL AND s.environment NOT LIKE '\_%'
			AND ($1 = '' OR s.environment = $1) ORDER BY s.environment, s.name`, only)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[secret])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": nonNil(out)})
}

func (s *Server) putSecret(w http.ResponseWriter, r *http.Request) {
	env, err := secretEnv(r, chi.URLParam(r, "env"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := chi.URLParam(r, "name")
	if !nameRe.MatchString(name) {
		s.fail(w, r, fmt.Errorf("%w: secret names are letters, digits and _", errBadRequest))
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM secrets WHERE environment = $1 AND name = $2)`, env, name).Scan(&exists); err != nil || exists {
			return err
		}
		return s.checkCount(r.Context(), tx, p.TenantID, "max_secrets")
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Vault.Put(r.Context(), p.TenantID, env, name, []byte(req.Value), p.Actor()); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	env, err := secretEnv(r, chi.URLParam(r, "env"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := chi.URLParam(r, "name")
	if !nameRe.MatchString(name) {
		s.fail(w, r, fmt.Errorf("%w: secret names are letters, digits and _", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	if err := s.Vault.Delete(r.Context(), p.TenantID, env, name, p.Actor()); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- variables ---

func (s *Server) listVariables(w http.ResponseWriter, r *http.Request) {
	type variable struct {
		Environment string          `json:"environment"`
		Name        string          `json:"name"`
		Value       json.RawMessage `json:"value"`
		UpdatedAt   time.Time       `json:"updated_at"`
	}
	var out []variable
	only := principalFrom(r.Context()).Environment
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT environment, name, value, updated_at FROM variables WHERE $1 = '' OR environment = $1 ORDER BY environment, name`, only)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[variable])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": nonNil(out)})
}

func (s *Server) putVariable(w http.ResponseWriter, r *http.Request) {
	env, err := envParam(r, chi.URLParam(r, "env"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Value json.RawMessage `json:"value"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Value) == 0 {
		s.fail(w, r, fmt.Errorf("%w: value is required", errBadRequest))
		return
	}
	name := chi.URLParam(r, "name")
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO variables (tenant_id, environment, name, value) VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id, environment, name) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
			principalFrom(r.Context()).TenantID, env, name, []byte(req.Value)); err != nil {
			var pgErr interface{ SQLState() string }
			if errors.As(err, &pgErr) && pgErr.SQLState() == "23514" {
				return fmt.Errorf("%w: variable names are lower-case letters, digits and _", errBadRequest)
			}
			return err
		}
		return auditTx(r, tx, "variable.write", env+"/"+name, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- egress allow-list ---

var hostRe = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

func (s *Server) listEgress(w http.ResponseWriter, r *http.Request) {
	env, err := envParam(r, r.URL.Query().Get("environment"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	hosts, err := s.Store.EgressHosts(r.Context(), principalFrom(r.Context()).TenantID, env)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"environment": env, "hosts": nonNil(hosts)})
}

func (s *Server) allowEgress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Environment string `json:"environment"`
		Host        string `json:"host"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	env, err := envParam(r, req.Environment)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !hostRe.MatchString(req.Host) {
		s.fail(w, r, fmt.Errorf("%w: host must be a lower-case DNS name, optionally starting with \"*.\"", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	if err := s.Store.AllowEgress(r.Context(), p.TenantID, env, req.Host, p.Actor()); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- audit ---

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Seq       int64           `json:"seq"`
		ActorType string          `json:"actor_type"`
		ActorID   string          `json:"actor_id"`
		Action    string          `json:"action"`
		Target    string          `json:"target"`
		Detail    json.RawMessage `json:"detail"`
		At        time.Time       `json:"at"`
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	if before <= 0 {
		before = 1 << 62
	}
	var out []entry
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT chain_seq, actor_type, actor_id, action, target, detail, at FROM audit_log
			WHERE chain_seq < $1 AND ($2 = '' OR action = $2) ORDER BY chain_seq DESC LIMIT $3`, before, q.Get("action"), limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[entry])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": nonNil(out)})
}

// verifyAudit checks the tenant's hash chain end to end.
func (s *Server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	var broken *int64
	err := s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT taskiem_audit_verify($1)`, principalFrom(r.Context()).TenantID).Scan(&broken)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"intact": broken == nil, "first_broken_seq": broken})
}

// exportAudit streams the tenant's chain as JSON lines for the offline
// verifier (`taskiem audit verify FILE`).
func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-`+p.TenantID.String()+`.jsonl"`)
	err := s.tx(r, func(tx pgx.Tx) error {
		if err := auditTx(r, tx, "audit.export", p.TenantID.String(), nil); err != nil {
			return err
		}
		return audit.Export(r.Context(), tx, p.TenantID.String(), w)
	})
	if err != nil {
		s.Logger.Error("audit export failed", "err", err)
	}
}

// --- erasure ---

type eraseReq struct {
	Subject  string `json:"subject,omitempty"`
	Category string `json:"category,omitempty"`
	Value    any    `json:"value,omitempty"`
}

// erase destroys a data subject's key (NDPA erasure, spec 9.4), named
// either by subject id or by the identifying value and its category.
// Every sealed copy of their data becomes unreadable. It cannot be undone.
func (s *Server) erase(w http.ResponseWriter, r *http.Request) {
	var req eraseReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	subject := req.Subject
	if subject == "" {
		if req.Category == "" || req.Value == nil {
			s.fail(w, r, fmt.Errorf("%w: give subject, or category and value", errBadRequest))
			return
		}
		var err error
		if subject, err = s.Vault.SubjectFor(r.Context(), p.TenantID, req.Category, req.Value); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.Vault.Erase(r.Context(), p.TenantID, subject, p.Actor()); err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			err = fmt.Errorf("%w: no such subject, or already erased", secrets.ErrNotFound)
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subject": subject, "erased": true})
}

// listAnchors returns the tenant's signed audit-chain anchors and the key
// they verify with. With an export, they let an auditor show the chain was
// not rewritten after each anchor: taskiem audit verify --anchors.
func (s *Server) listAnchors(w http.ResponseWriter, r *http.Request) {
	var out []audit.Anchor
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = audit.ListAnchors(r.Context(), tx)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	resp := map[string]any{"anchors": nonNil(out)}
	if s.AnchorKey != nil {
		resp["public_key"] = base64.StdEncoding.EncodeToString(s.AnchorKey)
		resp["key_id"] = audit.KeyID(s.AnchorKey)
	}
	writeJSON(w, http.StatusOK, resp)
}

// checkKeys checks pasted values against the manifest's prefixes, for the
// mode chosen, in plain words (no-technical-steps N2).
func checkKeys(c *connector.Connector, creds map[string]string, mode string) error {
	for _, f := range c.Manifest.Auth.Fields {
		if err := f.CheckValue(creds[f.Key], mode); err != nil {
			return fmt.Errorf("%w: %w", errBadRequest, err)
		}
	}
	return nil
}

// connectionMode is the mode a new connection is in. Where the
// connector's keys say it (sk_test_), the keys decide. Where its modes are
// hosts (iswallet, Payrolla: connector/v1 rule 17), it is the mode chosen,
// one this deployment has an address for; left out, the connection is
// made as before modes, and its calls go to the connector's base URL.
func connectionMode(c *connector.Connector, creds map[string]string, chosen string) (string, error) {
	if len(c.Manifest.Auth.HostModes) == 0 {
		return c.Manifest.Auth.ModeOf(creds), nil
	}
	if chosen == "" {
		return "", nil
	}
	for _, h := range c.Manifest.Auth.HostModes {
		if h.Mode != chosen {
			continue
		}
		if _, ok := c.Manifest.ModeURL(chosen); !ok {
			return "", fmt.Errorf("%w: %s %s is not set up on this Taskiem yet: whoever runs it gives it the address first", errBadRequest, c.Manifest.Name, strings.ToLower(h.Label))
		}
		return chosen, nil
	}
	return "", fmt.Errorf("%w: %s has no mode %q", errBadRequest, c.Manifest.Name, chosen)
}

// withMode is credentials with the mode a connection would have, as the
// vault hands them out, so a check before saving reaches that mode's host.
func withMode(creds map[string]string, mode string) map[string]string {
	if mode == "" {
		return creds
	}
	out := make(map[string]string, len(creds)+1)
	for k, v := range creds {
		out[k] = v
	}
	out[connector.ModeKey] = mode
	return out
}

func setConnectionMode(ctx context.Context, tx pgx.Tx, id uuid.UUID, mode string) error {
	if mode == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE connections SET mode = $2 WHERE id = $1`, id, mode)
	return err
}

// testCredentials runs the connector's test action with credentials not
// saved yet, through the egress guard: a key the provider refuses is
// answered in plain words.
func (s *Server) testCredentials(ctx context.Context, tenant uuid.UUID, c *connector.Connector, creds map[string]string) error {
	if c.Manifest.Auth.Test == nil {
		return nil
	}
	action, ok := c.Actions[c.Manifest.Auth.Test.Action]
	if !ok {
		return nil
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: c.Manifest.HostsFor(creds), Purpose: "connector:" + c.Manifest.ID}, 20*time.Second)
	tctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if _, err := action.Execute(tctx, connector.Request{Input: map[string]any{}, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1}); err != nil {
		return fmt.Errorf("%w: %s did not accept these details. Check you copied the whole value from the right page, then try again", errBadRequest, c.Manifest.Name)
	}
	return nil
}

// checkConnection answers POST /v1/connections/check: the same checks as
// saving, without saving, as a key is pasted.
func (s *Server) checkConnection(w http.ResponseWriter, r *http.Request) {
	var req connectionReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, ok := reg.Get(req.Connector)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: connector %q is not available", errBadRequest, req.Connector))
		return
	}
	if err := checkCredentials(c, req.Credentials); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := checkKeys(c, req.Credentials, req.Mode); err != nil {
		s.fail(w, r, err)
		return
	}
	mode, err := connectionMode(c, req.Credentials, req.Mode)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.testCredentials(r.Context(), p.TenantID, c, withMode(req.Credentials, mode)); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// listConnectionNames answers GET /v1/connection-names: the business's
// connections by service, name, environment and status only, for the
// pickers wherever a step or template names a connection (workflow.read;
// the Connections page itself needs connection.manage).
func (s *Server) listConnectionNames(w http.ResponseWriter, r *http.Request) {
	type conn struct {
		Connector   string `json:"connector"`
		Name        string `json:"name"`
		Environment string `json:"environment"`
		Status      string `json:"status"`
		// Mode is live or test, when the key says.
		Mode *string `json:"mode"`
	}
	var out []conn
	connector := r.URL.Query().Get("connector")
	if i := strings.IndexByte(connector, '@'); i > 0 {
		connector = connector[:i]
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT connector, name, environment, status, mode FROM connections
			WHERE ($1 = '' OR connector = $1) AND environment NOT LIKE '\_%' ORDER BY connector, environment DESC, name LIMIT 500`, connector)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[conn])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": nonNil(out)})
}
