package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/flowcode"
	"github.com/israel-duff/taskiem/engine/gitprovider"
	"github.com/israel-duff/taskiem/engine/policy"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

// Git integration (spec 10.3). Credentials and the webhook secret are
// tenant secrets in the reserved _git environment, so they are encrypted
// like every other secret, never returned, and out of reach of workflows
// and of the secrets API.
// policiesDir holds approval policies in a repository (spec 10.3 layout).
const policiesDir = "policies"

const gitVaultEnv = "_git"

func gitCredName(env string) string        { return "credentials_" + env }
func gitHookName(env string) string        { return "webhook_secret_" + env }
func gitPendingCredName(env string) string { return "pending_credentials_" + env }

type gitConnection struct {
	Environment string    `json:"environment"`
	Provider    string    `json:"provider"`
	APIURL      string    `json:"api_url"`
	Repo        string    `json:"repo"`
	Branch      string    `json:"branch"`
	Path        string    `json:"path"`
	TestsPath   string    `json:"tests_path"`
	Mode        string    `json:"mode"`
	CreatedBy   string    `json:"created_by"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const gitColumns = `environment, provider, api_url, repo, branch, path, tests_path, mode, created_by, updated_at`

func scanGit(row pgx.Row) (gitConnection, error) {
	var c gitConnection
	err := row.Scan(&c.Environment, &c.Provider, &c.APIURL, &c.Repo, &c.Branch, &c.Path, &c.TestsPath, &c.Mode, &c.CreatedBy, &c.UpdatedAt)
	return c, err
}

var envRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// gitHTTP is a client for one Git host: through the egress guard, which
// refuses private and metadata addresses, so a self-hosted URL cannot
// reach inside the platform.
func (s *Server) gitHTTP(tenant uuid.UUID, apiURL, provider string) (*http.Client, error) {
	if apiURL == "" {
		apiURL = map[string]string{"github": "https://api.github.com", "gitlab": "https://gitlab.com/api/v4", "bitbucket": "https://api.bitbucket.org/2.0"}[provider]
	}
	u, err := url.Parse(apiURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("%w: api_url must be an http(s) URL", errBadRequest)
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	return g.Client(egress.Policy{Tenant: tenant.String(), Hosts: []string{u.Hostname()}, Purpose: "git"}, 30*time.Second), nil
}

// provider builds the client for a connection, with its stored credentials.
func (s *Server) provider(ctx context.Context, tenant uuid.UUID, c gitConnection) (gitprovider.Provider, error) {
	raw, err := s.Vault.Get(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindGit, Purpose: secrets.PurposeGitSync}), tenant, gitVaultEnv, gitCredName(c.Environment))
	if err != nil {
		return nil, fmt.Errorf("git credentials for %s: %w", c.Environment, err)
	}
	var auth gitprovider.Auth
	if err := json.Unmarshal([]byte(raw), &auth); err != nil {
		return nil, fmt.Errorf("git credentials are not valid JSON: %w", err)
	}
	hc, err := s.gitHTTP(tenant, c.APIURL, c.Provider)
	if err != nil {
		return nil, err
	}
	return gitprovider.New(gitprovider.Config{Provider: c.Provider, APIURL: c.APIURL, Repo: c.Repo, Branch: c.Branch, Auth: auth, HTTP: hc})
}

// --- settings ---

func (s *Server) listGit(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	type entry struct {
		gitConnection
		WebhookURL string     `json:"webhook_url"`
		LastSync   *syncEntry `json:"last_sync,omitempty"`
	}
	var out []entry
	var requests []gitRequest
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+gitColumns+` FROM git_connections ORDER BY environment`)
		if err != nil {
			return err
		}
		conns, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (gitConnection, error) { return scanGit(row) })
		if err != nil {
			return err
		}
		for _, c := range conns {
			e := entry{gitConnection: c, WebhookURL: fmt.Sprintf("/git-hooks/%s/%s", p.TenantID, c.Environment)}
			if syncs, err := recentSyncs(r.Context(), tx, c.Environment, 1); err == nil && len(syncs) > 0 {
				e.LastSync = &syncs[0]
			}
			out = append(out, e)
		}
		rows, err = tx.Query(r.Context(), `SELECT environment, provider, api_url, repo, branch, path, tests_path, mode, credentials, requested_by, requested_at
			FROM git_connection_requests WHERE status = 'pending' ORDER BY requested_at`)
		if err != nil {
			return err
		}
		requests, err = pgx.CollectRows(rows, pgx.RowToStructByPos[gitRequest])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": nonNil(out), "requests": nonNil(requests)})
}

// gitRequest is a change to a Git-led connection waiting for a second
// person (see putGit).
type gitRequest struct {
	Environment    string    `json:"environment"`
	Provider       string    `json:"provider"`
	APIURL         string    `json:"api_url"`
	Repo           string    `json:"repo"`
	Branch         string    `json:"branch"`
	Path           string    `json:"path"`
	TestsPath      string    `json:"tests_path"`
	Mode           string    `json:"mode"`
	NewCredentials bool      `json:"new_credentials"`
	RequestedBy    uuid.UUID `json:"requested_by"`
	RequestedAt    time.Time `json:"requested_at"`
}

type gitReq struct {
	Provider  string            `json:"provider"`
	APIURL    string            `json:"api_url"`
	Repo      string            `json:"repo"`
	Branch    string            `json:"branch"`
	Path      string            `json:"path"`
	TestsPath string            `json:"tests_path"`
	Mode      string            `json:"mode"`
	Auth      *gitprovider.Auth `json:"auth"`
	// RotateWebhookSecret issues a new webhook secret (shown once).
	RotateWebhookSecret bool `json:"rotate_webhook_secret"`
}

// putGit connects an environment to a repository, or changes its settings.
// The repository is reached once with the credentials before anything is
// saved. The webhook secret is returned only when it is created.
//
// Stored credentials are reused only for the same provider, api_url and
// repo: sending them anywhere new takes auth again, from someone who has
// them. A Git-led connection deploys and activates policies on its own, so
// when the tenant has four-eyes on, or the environment is gated, creating
// one, switching to Git-led, or pointing one elsewhere waits for a second
// person (decideGit); until then the connection keeps its old settings.
func (s *Server) putGit(w http.ResponseWriter, r *http.Request) {
	env := chi.URLParam(r, "env")
	if !envRe.MatchString(env) {
		s.fail(w, r, fmt.Errorf("%w: environment names are lower-case letters, digits, _ and -", errBadRequest))
		return
	}
	var req gitReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Path == "" {
		req.Path = "flows"
	}
	if req.TestsPath == "" {
		req.TestsPath = "tests"
	}
	req.Path, req.TestsPath = strings.Trim(req.Path, "/"), strings.Trim(req.TestsPath, "/")
	if req.Mode != "platform_led" && req.Mode != "git_led" {
		s.fail(w, r, fmt.Errorf("%w: mode is platform_led or git_led", errBadRequest))
		return
	}
	if req.Branch == "" || req.Repo == "" || strings.Contains(req.Path, "..") || strings.Contains(req.TestsPath, "..") {
		s.fail(w, r, fmt.Errorf("%w: repo and branch are required, and paths stay inside the repository", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	ctx := r.Context()
	conn := gitConnection{Environment: env, Provider: req.Provider, APIURL: req.APIURL, Repo: req.Repo, Branch: req.Branch, Path: req.Path, TestsPath: req.TestsPath, Mode: req.Mode}

	// The connection as it stands, and whether this change is governed.
	var current *gitConnection
	governed := false
	err := s.tx(r, func(tx pgx.Tx) error {
		c, err := scanGit(tx.QueryRow(ctx, `SELECT `+gitColumns+` FROM git_connections WHERE environment = $1`, env))
		switch {
		case err == nil:
			current = &c
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		g, err := governanceTx(ctx, tx)
		if err != nil {
			return err
		}
		var gated bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM environments WHERE name = $1 AND promotion_from IS NOT NULL)`, env).Scan(&gated); err != nil {
			return err
		}
		governed = g.FourEyesPublish || g.FourEyesPolicies || gated
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sameRepo := current != nil && current.Provider == conn.Provider && current.APIURL == conn.APIURL && current.Repo == conn.Repo
	needsApproval := governed && conn.Mode == "git_led" && (!sameRepo || current.Mode != "git_led" || current.Branch != conn.Branch ||
		current.Path != conn.Path || current.TestsPath != conn.TestsPath)
	if needsApproval && p.UserID == uuid.Nil {
		s.fail(w, r, fmt.Errorf("%w: a Git-led connection here is asked for by a person and approved by another, not by an API key", errForbidden))
		return
	}

	// Credentials: new ones are checked against the repository; omitted
	// ones keep what is stored, for the same repository only.
	var creds []byte
	if req.Auth != nil {
		creds, _ = json.Marshal(req.Auth) //nolint:gosec // serialised only to be encrypted into the vault
	} else if !sameRepo {
		s.fail(w, r, fmt.Errorf("%w: auth is required to connect a repository, or to change its provider, api_url or repo", errBadRequest))
		return
	} else if raw, err := s.Vault.Get(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindGit, Purpose: "git.connect", Actor: p.Actor()}), p.TenantID, gitVaultEnv, gitCredName(env)); err == nil {
		creds = []byte(raw)
	} else {
		s.fail(w, r, fmt.Errorf("%w: auth is required to connect a repository", errBadRequest))
		return
	}
	var auth gitprovider.Auth
	_ = json.Unmarshal(creds, &auth)
	hc, err := s.gitHTTP(p.TenantID, req.APIURL, req.Provider)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	prov, err := gitprovider.New(gitprovider.Config{Provider: req.Provider, APIURL: req.APIURL, Repo: req.Repo, Branch: req.Branch, Auth: auth, HTTP: hc})
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
		return
	}
	head, err := prov.Head(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "cannot read branch " + req.Branch + " of " + req.Repo + ": " + err.Error()})
		return
	}
	credName := gitCredName(env)
	if needsApproval {
		credName = gitPendingCredName(env)
		_ = s.Vault.Delete(ctx, p.TenantID, gitVaultEnv, credName, p.Actor())
	}
	if req.Auth != nil {
		if _, err := s.Vault.Put(ctx, p.TenantID, gitVaultEnv, credName, creds, p.Actor()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	secret := ""
	if ok, err := s.Vault.Exists(ctx, p.TenantID, gitVaultEnv, gitHookName(env)); err != nil || !ok || req.RotateWebhookSecret {
		secret = newToken()
		if _, err := s.Vault.Put(ctx, p.TenantID, gitVaultEnv, gitHookName(env), []byte(secret), p.Actor()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if needsApproval {
			if _, err := tx.Exec(ctx, `INSERT INTO git_connection_requests (tenant_id, environment, provider, api_url, repo, branch, path, tests_path, mode, credentials, requested_by)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
				ON CONFLICT (tenant_id, environment) DO UPDATE SET provider = EXCLUDED.provider, api_url = EXCLUDED.api_url, repo = EXCLUDED.repo,
				  branch = EXCLUDED.branch, path = EXCLUDED.path, tests_path = EXCLUDED.tests_path, mode = EXCLUDED.mode, credentials = EXCLUDED.credentials,
				  requested_by = EXCLUDED.requested_by, requested_at = now(), status = 'pending', decided_by = NULL, decided_at = NULL, comment = NULL`,
				p.TenantID, env, conn.Provider, conn.APIURL, conn.Repo, conn.Branch, conn.Path, conn.TestsPath, conn.Mode, req.Auth != nil, p.UserID); err != nil {
				return err
			}
			return auditTx(r, tx, "git.connect_request", env, map[string]any{"provider": conn.Provider, "api_url": conn.APIURL, "repo": conn.Repo, "branch": conn.Branch,
				"mode": conn.Mode, "credentials_changed": req.Auth != nil, "webhook_secret_issued": secret != ""})
		}
		// A change made directly supersedes any change still waiting.
		if _, err := tx.Exec(ctx, `UPDATE git_connection_requests SET status = 'withdrawn', decided_at = now() WHERE environment = $1 AND status = 'pending'`, env); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO git_connections (tenant_id, environment, provider, api_url, repo, branch, path, tests_path, mode, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (tenant_id, environment) DO UPDATE SET provider = EXCLUDED.provider, api_url = EXCLUDED.api_url, repo = EXCLUDED.repo,
			  branch = EXCLUDED.branch, path = EXCLUDED.path, tests_path = EXCLUDED.tests_path, mode = EXCLUDED.mode, updated_at = now()`,
			p.TenantID, env, conn.Provider, conn.APIURL, conn.Repo, conn.Branch, conn.Path, conn.TestsPath, conn.Mode, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "git.connect", env, map[string]any{"provider": conn.Provider, "repo": conn.Repo, "branch": conn.Branch, "mode": conn.Mode,
			"credentials_changed": req.Auth != nil, "webhook_secret_issued": secret != ""})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"environment": env, "head": head, "webhook_url": fmt.Sprintf("/git-hooks/%s/%s", p.TenantID, env)}
	if secret != "" {
		out["webhook_secret"] = secret
	}
	if needsApproval {
		out["state"] = "pending_approval"
		writeJSON(w, http.StatusAccepted, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// decideGit approves or rejects a waiting change to a Git-led connection.
// A person approves it, other than whoever asked; on approval the
// connection takes the asked-for settings and credentials.
func (s *Server) decideGit(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		env := chi.URLParam(r, "env")
		p := principalFrom(r.Context())
		if p.UserID == uuid.Nil {
			writeErr(w, http.StatusForbidden, "Git-led connections are approved by people, not API keys")
			return
		}
		var req struct {
			Comment string `json:"comment"`
		}
		_ = decodeBody(r, &req)
		ctx := r.Context()
		var q gitRequest
		err := s.tx(r, func(tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `SELECT environment, provider, api_url, repo, branch, path, tests_path, mode, credentials, requested_by, requested_at
				FROM git_connection_requests WHERE environment = $1 AND status = 'pending' FOR UPDATE`, env).
				Scan(&q.Environment, &q.Provider, &q.APIURL, &q.Repo, &q.Branch, &q.Path, &q.TestsPath, &q.Mode, &q.NewCredentials, &q.RequestedBy, &q.RequestedAt); err != nil {
				return err
			}
			if q.RequestedBy == p.UserID {
				return fmt.Errorf("%w: a Git-led connection needs a second person: not whoever asked for it", errForbidden)
			}
			next := "rejected"
			if approve {
				next = "approved"
				if _, err := tx.Exec(ctx, `INSERT INTO git_connections (tenant_id, environment, provider, api_url, repo, branch, path, tests_path, mode, created_by)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
					ON CONFLICT (tenant_id, environment) DO UPDATE SET provider = EXCLUDED.provider, api_url = EXCLUDED.api_url, repo = EXCLUDED.repo,
					  branch = EXCLUDED.branch, path = EXCLUDED.path, tests_path = EXCLUDED.tests_path, mode = EXCLUDED.mode, updated_at = now()`,
					p.TenantID, env, q.Provider, q.APIURL, q.Repo, q.Branch, q.Path, q.TestsPath, q.Mode, q.RequestedBy.String()); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE git_connection_requests SET status = $2, decided_by = $3, decided_at = now(), comment = NULLIF($4, '') WHERE environment = $1`,
				env, next, p.UserID, req.Comment); err != nil {
				return err
			}
			return auditTx(r, tx, "git.connect_"+map[bool]string{true: "approve", false: "reject"}[approve], env,
				map[string]any{"repo": q.Repo, "branch": q.Branch, "mode": q.Mode, "requested_by": q.RequestedBy, "comment": req.Comment})
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		// The waiting credentials become the connection's, or are dropped.
		if q.NewCredentials {
			pending := gitPendingCredName(env)
			if approve {
				raw, err := s.Vault.Get(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindGit, Purpose: "git.connect", Actor: p.Actor()}), p.TenantID, gitVaultEnv, pending)
				if err == nil {
					_, err = s.Vault.Put(ctx, p.TenantID, gitVaultEnv, gitCredName(env), []byte(raw), q.RequestedBy.String())
				}
				if err != nil {
					s.fail(w, r, err)
					return
				}
			}
			_ = s.Vault.Delete(ctx, p.TenantID, gitVaultEnv, pending, p.Actor())
		}
		writeJSON(w, http.StatusOK, map[string]any{"environment": env, "state": map[bool]string{true: "approved", false: "rejected"}[approve]})
	}
}

func (s *Server) deleteGit(w http.ResponseWriter, r *http.Request) {
	env := chi.URLParam(r, "env")
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE git_connection_requests SET status = 'withdrawn', decided_at = now() WHERE environment = $1 AND status = 'pending'`, env); err != nil {
			return err
		}
		tag, err := tx.Exec(r.Context(), `DELETE FROM git_connections WHERE environment = $1`, env)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		// Workflows a repository held become editable again once no
		// Git-led environment remains.
		if _, err := tx.Exec(r.Context(), `UPDATE workflows SET git_path = NULL WHERE git_path IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM git_connections WHERE mode = 'git_led')`); err != nil {
			return err
		}
		return auditTx(r, tx, "git.disconnect", env, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, name := range []string{gitCredName(env), gitHookName(env), gitPendingCredName(env)} {
		_ = s.Vault.Delete(r.Context(), p.TenantID, gitVaultEnv, name, p.Actor())
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Git-led: managed workflows are read-only ---

// refuseGitManaged rejects an edit to a workflow a Git-led repository holds.
func (s *Server) refuseGitManaged(ctx context.Context, tx pgx.Tx, wf uuid.UUID) error {
	var gitPath, repo string
	err := tx.QueryRow(ctx, `SELECT w.git_path, c.repo FROM workflows w JOIN git_connections c ON c.tenant_id = w.tenant_id AND c.mode = 'git_led'
		WHERE w.id = $1 AND w.git_path IS NOT NULL LIMIT 1`, wf).Scan(&gitPath, &repo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: this workflow is managed in Git: change %s in %s and push", errConflict, gitPath, repo)
}

// --- Git-led: push webhook and syncs ---

// GitHooks receives push webhooks at /git-hooks/{tenant}/{env}. A verified
// push of the connected branch's current head queues a sync, once per
// delivery; besides reading the head, nothing else happens in the request,
// so the Git host gets its answer quickly.
func (s *Server) GitHooks() http.Handler {
	r := chi.NewRouter()
	r.Post("/{tenant}/{env}", s.gitHook)
	return r
}

func (s *Server) gitHook(w http.ResponseWriter, r *http.Request) {
	// An unknown tenant or environment gets the same answer as a bad
	// signature, so the endpoint does not say which connections exist.
	deny := func() { writeErr(w, http.StatusUnauthorized, "signature does not match") }
	tenant, err := uuid.Parse(chi.URLParam(r, "tenant"))
	if err != nil {
		deny()
		return
	}
	env := chi.URLParam(r, "env")
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "cannot read body")
		return
	}
	ctx := r.Context()
	var conn gitConnection
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		conn, err = scanGit(tx.QueryRow(ctx, `SELECT `+gitColumns+` FROM git_connections WHERE environment = $1`, env))
		return err
	})
	if err != nil {
		deny()
		return
	}
	secret, err := s.Vault.Get(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindGit, Purpose: secrets.PurposeIngestVerify}), tenant, gitVaultEnv, gitHookName(env))
	if errors.Is(err, secrets.ErrNotFound) {
		deny()
		return
	}
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "webhook secret unavailable")
		return
	}
	// Verify before the credentials are decrypted: checking a signature
	// needs only the webhook secret.
	// The placeholder credentials are never used, so a complaint about them
	// does not matter; only an unknown provider leaves no verifier.
	verifier, _ := gitprovider.New(gitprovider.Config{Provider: conn.Provider, APIURL: conn.APIURL, Repo: conn.Repo, Branch: conn.Branch,
		Auth: gitprovider.Auth{Type: "token", Token: "unused"}})
	if verifier == nil {
		writeErr(w, http.StatusServiceUnavailable, "git connection unavailable")
		return
	}
	push, err := verifier.VerifyPush(r.Header, body, secret)
	if errors.Is(err, gitprovider.ErrSignature) {
		deny()
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "not a push event")
		return
	}
	switch {
	case push == nil:
		writeJSON(w, http.StatusAccepted, map[string]any{"ignored": "not a branch push"})
		return
	case push.Branch != conn.Branch:
		writeJSON(w, http.StatusAccepted, map[string]any{"ignored": "branch " + push.Branch + " is not " + conn.Branch})
		return
	case conn.Mode != "git_led":
		writeJSON(w, http.StatusAccepted, map[string]any{"ignored": "platform-led: pushes do not deploy"})
		return
	}
	// A push deploys only while it is the branch head, so an old delivery
	// sent again cannot roll the environment back; and each delivery is
	// taken once.
	prov, err := s.provider(ctx, tenant, conn)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "git connection unavailable")
		return
	}
	head, err := prov.Head(ctx)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "cannot read the branch head")
		return
	}
	if head != push.Commit {
		s.Logger.Info("git push ignored: not the branch head", "tenant", tenant, "environment", env, "commit", push.Commit, "head", head)
		writeJSON(w, http.StatusAccepted, map[string]any{"ignored": "commit " + push.Commit + " is not the head of " + conn.Branch})
		return
	}
	if delivery := deliveryID(r.Header); delivery != "" {
		fresh := false
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `INSERT INTO git_push_receipts (tenant_id, environment, delivery_id, commit) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
				tenant, env, delivery, push.Commit)
			fresh = err == nil && tag.RowsAffected() == 1
			return err
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !fresh {
			writeJSON(w, http.StatusAccepted, map[string]any{"ignored": "delivery " + delivery + " was already received"})
			return
		}
	}
	id, err := s.queueSync(ctx, tenant, env, push.Commit, "webhook")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sync_id": id})
}

// deliveryID is a webhook delivery's unique id, as the Git host sends it.
func deliveryID(h http.Header) string {
	for _, k := range []string{"X-GitHub-Delivery", "X-Gitlab-Event-UUID", "X-Request-UUID"} {
		if v := h.Get(k); v != "" {
			if len(v) > 200 {
				v = v[:200]
			}
			return v
		}
	}
	return ""
}

func (s *Server) queueSync(ctx context.Context, tenant uuid.UUID, env, commit, by string) (uuid.UUID, error) {
	id := uuid.Must(uuid.NewV7())
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO git_syncs (id, tenant_id, environment, commit, requested_by) VALUES ($1, $2, $3, $4, $5)`, id, tenant, env, commit, by)
		return err
	})
	return id, err
}

// syncNow queues a sync of the branch head (a manual "deploy from Git").
func (s *Server) syncNow(w http.ResponseWriter, r *http.Request) {
	env := chi.URLParam(r, "env")
	p := principalFrom(r.Context())
	var mode string
	err := s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT mode FROM git_connections WHERE environment = $1`, env).Scan(&mode)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if mode != "git_led" {
		s.fail(w, r, fmt.Errorf("%w: only a Git-led environment deploys from Git", errConflict))
		return
	}
	id, err := s.queueSync(r.Context(), p.TenantID, env, "", p.Actor())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sync_id": id})
}

type syncEntry struct {
	ID          uuid.UUID       `json:"id"`
	Commit      string          `json:"commit"`
	RequestedBy string          `json:"requested_by"`
	Status      string          `json:"status"`
	Report      json.RawMessage `json:"report,omitempty"`
	RequestedAt time.Time       `json:"requested_at"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
}

func recentSyncs(ctx context.Context, tx pgx.Tx, env string, n int) ([]syncEntry, error) {
	rows, err := tx.Query(ctx, `SELECT id, commit, requested_by, status, report, requested_at, finished_at FROM git_syncs
		WHERE environment = $1 ORDER BY requested_at DESC LIMIT $2`, env, n)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[syncEntry])
}

func (s *Server) listSyncs(w http.ResponseWriter, r *http.Request) {
	var out []syncEntry
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = recentSyncs(r.Context(), tx, chi.URLParam(r, "env"), 20)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"syncs": nonNil(out)})
}

// RunGitSyncs works queued Git-led syncs until ctx ends (role api).
func (s *Server) RunGitSyncs(ctx context.Context) error {
	for {
		if _, err := s.GitSyncOnce(ctx); err != nil && ctx.Err() == nil && s.Logger != nil {
			s.Logger.Error("git sync", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// GitSyncOnce claims and runs the queued syncs; it returns how many ran.
func (s *Server) GitSyncOnce(ctx context.Context) (int, error) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT sync_id, tenant_id FROM taskiem_claim_git_syncs(5)`)
	if err != nil {
		return 0, err
	}
	type claim struct{ id, tenant uuid.UUID }
	var claims []claim
	for rows.Next() {
		var c claim
		if err := rows.Scan(&c.id, &c.tenant); err != nil {
			rows.Close()
			return 0, err
		}
		claims = append(claims, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, c := range claims {
		s.runSync(ctx, c.tenant, c.id)
	}
	return len(claims), nil
}

// syncReport is what a sync did, kept with it.
type syncReport struct {
	Commit    string             `json:"commit,omitempty"`
	Error     string             `json:"error,omitempty"`
	Problems  []string           `json:"problems,omitempty"`
	Tests     *syncTests         `json:"tests,omitempty"`
	Workflows []syncWorkflowNote `json:"workflows,omitempty"`
}

type syncTests struct {
	Passed   int      `json:"passed"`
	Failed   int      `json:"failed"`
	Failures []string `json:"failures,omitempty"`
}

type syncWorkflowNote struct {
	Path     string `json:"path"`
	Key      string `json:"key"`
	Workflow string `json:"workflow_id,omitempty"`
	Action   string `json:"action"` // created | published | unchanged
	Version  int    `json:"version,omitempty"`
}

// runSync deploys one commit: every definition under the connection's path
// is checked and every workflow test run first; only if all pass is
// anything deployed, all in one transaction (spec 10.5: tests gate Git-led
// deploys).
func (s *Server) runSync(ctx context.Context, tenant, id uuid.UUID) {
	rep := &syncReport{}
	status, err := s.sync(ctx, tenant, id, rep)
	if err != nil {
		status, rep.Error = "failed", err.Error()
	}
	raw, _ := json.Marshal(rep)
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE git_syncs SET status = $2, report = $3, finished_at = now(), lease_until = NULL, commit = COALESCE(NULLIF($4, ''), commit) WHERE id = $1`,
			id, status, raw, rep.Commit); err != nil {
			return err
		}
		var env string
		_ = tx.QueryRow(ctx, `SELECT environment FROM git_syncs WHERE id = $1`, id).Scan(&env)
		return auditSystem(ctx, tx, tenant, "git:"+env, "git.sync", id.String(), map[string]any{"status": status, "commit": rep.Commit})
	})
}

func auditSystem(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, actor, action, target string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', $2, $3, $4, $5)`, tenant, actor, action, target, raw)
	return err
}

func (s *Server) sync(ctx context.Context, tenant, id uuid.UUID, rep *syncReport) (string, error) {
	var conn gitConnection
	var commit string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var env string
		if err := tx.QueryRow(ctx, `SELECT environment, commit FROM git_syncs WHERE id = $1`, id).Scan(&env, &commit); err != nil {
			return err
		}
		var err error
		conn, err = scanGit(tx.QueryRow(ctx, `SELECT `+gitColumns+` FROM git_connections WHERE environment = $1`, env))
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("the environment is no longer connected to Git")
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if conn.Mode != "git_led" {
		return "", errors.New("the environment is platform-led; pushes do not deploy")
	}
	prov, err := s.provider(ctx, tenant, conn)
	if err != nil {
		return "", err
	}
	snap, err := prov.Read(ctx, commit, []string{conn.Path, conn.TestsPath, policiesDir}, []string{".wd.json", ".test.json", ".policy.json"})
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", conn.Repo, err)
	}
	rep.Commit = snap.Commit
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return "", err
	}

	// Check every definition and run every test before touching anything.
	paths := make([]string, 0, len(snap.Files))
	for p := range snap.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	type def struct {
		path, key string
		doc       []byte
	}
	var defs []def
	keys := map[string]string{}
	inDefs := func(p string) bool { return strings.HasPrefix(p, conn.Path+"/") && strings.HasSuffix(p, ".wd.json") }
	for _, p := range paths {
		if !inDefs(p) {
			continue
		}
		doc := snap.Files[p]
		for _, pr := range s.checkWith(ctx, tenant, doc, reg) {
			rep.Problems = append(rep.Problems, p+": "+pr.String())
		}
		var head struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(doc, &head)
		if prev, dup := keys[head.ID]; dup && head.ID != "" {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s and %s both define %s", prev, p, head.ID))
		}
		keys[head.ID] = p
		defs = append(defs, def{p, head.ID, doc})
	}
	// Approval policies: policies/<name>.policy.json.
	policies := map[string]json.RawMessage{}
	for _, p := range paths {
		if !strings.HasPrefix(p, policiesDir+"/") || !strings.HasSuffix(p, ".policy.json") {
			continue
		}
		name := strings.TrimSuffix(path.Base(p), ".policy.json")
		if !policy.ValidName(name) {
			rep.Problems = append(rep.Problems, p+": policy file names are lower-case letters, digits and _")
			continue
		}
		if _, err := policy.Parse(snap.Files[p]); err != nil {
			rep.Problems = append(rep.Problems, p+": "+err.Error())
			continue
		}
		policies[name] = snap.Files[p]
	}
	tests := &syncTests{}
	for _, p := range paths {
		if !strings.HasSuffix(p, ".test.json") {
			continue
		}
		f, err := wdtest.Parse(p, snap.Files[p])
		if err != nil {
			rep.Problems = append(rep.Problems, err.Error())
			continue
		}
		target := path.Clean(path.Join(path.Dir(p), f.Workflow))
		doc, ok := snap.Files[target]
		if !ok {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s: workflow %s is not in the repository", p, target))
			continue
		}
		for name, doc := range policies {
			if _, own := f.Policies[name]; !own {
				if f.Policies == nil {
					f.Policies = map[string]json.RawMessage{}
				}
				f.Policies[name] = doc // the repository's policy, unless the test brings its own
			}
		}
		results, err := f.RunDefinition(doc, reg)
		if err != nil {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		for _, res := range results {
			if res.Passed() {
				tests.Passed++
				continue
			}
			tests.Failed++
			tests.Failures = append(tests.Failures, fmt.Sprintf("%s: %s: %s", p, res.Case, strings.Join(res.Failures, "; ")))
		}
	}
	rep.Tests = tests
	if len(rep.Problems) > 0 || tests.Failed > 0 {
		return "failed", nil
	}
	if len(defs) == 0 && len(policies) == 0 {
		return "unchanged", nil
	}

	changed := false
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		actor := "git:" + conn.Environment
		// Policies first: the workflows' checks need them active. Review
		// in Git stands in for four-eyes on these edits.
		names := make([]string, 0, len(policies))
		for n := range policies {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			doc, err := canonical(policies[name])
			if err != nil {
				return err
			}
			var current []byte
			err = tx.QueryRow(ctx, `SELECT document FROM approval_policies WHERE name = $1 AND state = 'active'`, name).Scan(&current)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if current != nil && sameJSON(current, doc) {
				continue
			}
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('policy:' || $1))`, name); err != nil {
				return err
			}
			var v int
			if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) + 1 FROM approval_policies WHERE name = $1`, name).Scan(&v); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE approval_policies SET state = 'superseded' WHERE name = $1 AND state = 'active'`, name); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO approval_policies (tenant_id, name, version, document, state, created_by, decided_by, decided_at)
				VALUES ($1, $2, $3, $4, 'active', $5, $5, now())`, tenant, name, v, doc, actor); err != nil {
				return err
			}
			if err := auditSystem(ctx, tx, tenant, actor, "policy.write", fmt.Sprintf("%s/%d", name, v), map[string]any{"commit": snap.Commit, "repo": conn.Repo}); err != nil {
				return err
			}
			changed = true
			rep.Workflows = append(rep.Workflows, syncWorkflowNote{Path: policiesDir + "/" + name + ".policy.json", Key: "policy:" + name, Action: "published", Version: v})
		}
		for _, d := range defs {
			doc, err := compactJSON(d.doc)
			if err != nil {
				return fmt.Errorf("%s: %w", d.path, err)
			}
			note := syncWorkflowNote{Path: d.path, Key: d.key}
			var wf uuid.UUID
			err = tx.QueryRow(ctx, `SELECT w.id FROM workflows w WHERE `+latestKey+` = $1 ORDER BY w.created_at LIMIT 1`, d.key).Scan(&wf)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				var name string
				var head struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(doc, &head)
				if name = head.Name; name == "" {
					name = d.key
				}
				wf = uuid.Must(uuid.NewV7())
				if err := s.checkCount(ctx, tx, tenant, "max_workflows"); err != nil {
					return fmt.Errorf("%s: %w", d.path, err)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by, git_path, workspace_id)
					VALUES ($1, $2, $3, $4, $5, (SELECT id FROM workspaces WHERE tenant_id = $2 ORDER BY created_at LIMIT 1))`, wf, tenant, name, uuid.Nil, d.path); err != nil {
					return err
				}
				if err := insertVersionTx(ctx, tx, tenant, wf, 1, doc, nil, actor, snap.Commit); err != nil {
					return err
				}
				note.Action, note.Version = "created", 1
			case err != nil:
				return err
			default:
				if _, err := tx.Exec(ctx, `UPDATE workflows SET git_path = $2 WHERE id = $1`, wf, d.path); err != nil {
					return err
				}
				var latest int
				var latestDef []byte
				if err := tx.QueryRow(ctx, `SELECT version, definition FROM workflow_versions WHERE workflow_id = $1 ORDER BY version DESC LIMIT 1`, wf).Scan(&latest, &latestDef); err != nil {
					return err
				}
				note.Version, note.Action = latest, "unchanged"
				if !sameJSON(latestDef, doc) {
					note.Version, note.Action = latest+1, "published"
					if err := insertVersionTx(ctx, tx, tenant, wf, latest+1, doc, nil, actor, snap.Commit); err != nil {
						return err
					}
				}
			}
			probs, digest, published, err := s.publishTx(ctx, tx, tenant, wf, note.Version, nil, conn.Environment)
			if errors.Is(err, errInvalid) {
				return fmt.Errorf("%s: %v", d.path, probs)
			}
			if err != nil {
				return fmt.Errorf("%s: %w", d.path, err)
			}
			if published {
				changed = true
				if note.Action == "unchanged" {
					note.Action = "published"
				}
				if err := auditSystem(ctx, tx, tenant, actor, "workflow.publish", fmt.Sprintf("%s/%d", wf, note.Version),
					map[string]any{"digest": digest, "commit": snap.Commit, "repo": conn.Repo, "path": d.path}); err != nil {
					return err
				}
			}
			note.Workflow = wf.String()
			rep.Workflows = append(rep.Workflows, note)
		}
		return nil
	})
	if err != nil {
		rep.Workflows = nil
		return "", err
	}
	if !changed {
		return "unchanged", nil
	}
	return "deployed", nil
}

func compactJSON(doc []byte) ([]byte, error) {
	return canonical(doc)
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return string(ja) == string(jb)
}

// --- platform-led: publishing opens a pull request ---

// proposeToGit opens a pull or merge request for a just-published version
// in every platform-led repository. Publishing has already happened: a
// failure here is reported, and can be retried, but never undoes it.
func (s *Server) proposeToGit(r *http.Request, wf uuid.UUID, v int) map[string]any {
	p := principalFrom(r.Context())
	var conns []gitConnection
	_ = s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+gitColumns+` FROM git_connections WHERE mode = 'platform_led' ORDER BY environment`)
		if err != nil {
			return err
		}
		conns, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (gitConnection, error) { return scanGit(row) })
		return err
	})
	if len(conns) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, c := range conns {
		u, err := s.openRequest(r, p.TenantID, c, wf, v)
		if err != nil {
			out[c.Environment] = map[string]any{"error": err.Error()}
			continue
		}
		out[c.Environment] = map[string]any{"url": u}
	}
	return out
}

func (s *Server) openRequest(r *http.Request, tenant uuid.UUID, c gitConnection, wf uuid.UUID, v int) (string, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var def []byte
	var name, digest string
	var gitPath *string
	err := s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT v.definition, w.name, encode(v.digest, 'hex'), w.git_path FROM workflow_versions v JOIN workflows w ON w.id = v.workflow_id
			WHERE v.workflow_id = $1 AND v.version = $2`, wf, v).Scan(&def, &name, &digest, &gitPath)
	})
	if err != nil {
		return "", err
	}
	var head struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(def, &head)
	file := c.Path + "/" + fileSlug(head.ID) + ".wd.json"
	if gitPath != nil && strings.HasSuffix(*gitPath, ".wd.json") {
		file = *gitPath
	}
	pretty, err := flowcode.Indent(def)
	if err != nil {
		return "", err
	}
	files := []gitprovider.File{{Path: file, Content: pretty}}
	var code string
	var genErr error
	if err := s.withCodeSlot(ctx, tenant, func() { code, genErr = flowcode.Generate(ctx, def) }); err == nil && genErr == nil {
		files = append(files, gitprovider.File{Path: strings.TrimSuffix(file, ".wd.json") + ".flow.ts", Content: []byte(code)})
	}
	prov, err := s.provider(ctx, tenant, c)
	if err != nil {
		return "", err
	}
	p := principalFrom(r.Context())
	title := fmt.Sprintf("Publish %s v%d", name, v)
	body := fmt.Sprintf("Published in Taskiem by %s.\n\n- Workflow: %s (`%s`)\n- Version: %d\n- Digest: `%s`\n\nTaskiem is the source of truth for this environment (platform-led); merging records the change in Git.",
		p.Actor(), name, head.ID, v, digest)
	u, err := prov.Propose(ctx, fmt.Sprintf("taskiem/%s-v%d", head.ID, v), title, body, files)
	if err != nil {
		return "", err
	}
	_ = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE workflow_versions SET git_pr = $3 WHERE workflow_id = $1 AND version = $2`, wf, v, u); err != nil {
			return err
		}
		return auditTx(r, tx, "git.propose", fmt.Sprintf("%s/%d", wf, v), map[string]any{"url": u, "repo": c.Repo, "path": file})
	})
	return u, nil
}

// retryProposal opens (or finds) the pull request for a published version
// again, after a failure.
func (s *Server) retryProposal(w http.ResponseWriter, r *http.Request) {
	wf, v, err := versionParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	g := s.proposeToGit(r, wf, v)
	if g == nil {
		s.fail(w, r, fmt.Errorf("%w: no platform-led Git connection", errConflict))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"git": g})
}

// fileSlug turns a definition id into a file name: wf_payrollaSalary ->
// payrolla-salary.
func fileSlug(id string) string {
	id = strings.TrimPrefix(id, "wf_")
	var b strings.Builder
	for i, r := range id {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		if r == '_' {
			b.WriteByte('-')
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "workflow"
	}
	return b.String()
}
