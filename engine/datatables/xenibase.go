package datatables

// The Xenibase management API, as far as provisioning and schema changes
// need it, built from what Xenibase publishes (decision 0018; read
// 17 October 2026): api/openapi.yaml for POST /projects
// (ProjectCredentials), GET /operations/{id} (Operation), POST
// /projects/{id}/services (BackendServicesEnabled: the keys shown once),
// GET /projects/{id}/services (BackendServices), POST
// /projects/{id}/services/keys (CreatedApiKey), POST /tokens
// (CreatedToken: a token made by a token never exceeds it), POST
// /projects/{id}/schema/preview (SchemaPlan) and /apply (SchemaApplied,
// 409 stale_plan, confirm on drops); docs/errors.md for the flat
// {code, message, quota} error body and the codes mapped below;
// docs/cli.md for scopes and token expiry.
//
// Every call goes through the egress guard to one host, with a 20 s
// timeout, and carries one token: the platform's for provisioning, the
// connection's own for schema changes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/egress"
)

// Error is a Xenibase refusal, with the published code.
type Error struct {
	Status  int
	Code    string
	Message string
	// Quota says which organisation quota was reached (quota_exceeded).
	Quota map[string]any
	// RetryAfter is Xenibase's Retry-After, when it sent one.
	RetryAfter time.Duration
	// Statement is the DDL Postgres refused (ddl_failed).
	Statement string
	// Call is the request refused (GET /api/v1/projects), so an
	// operator can tell which one: a wrong server or organisation answers
	// 404 like a missing project.
	Call string
}

func (e *Error) Error() string {
	s := fmt.Sprintf("xenibase: http %d", e.Status)
	if e.Call != "" {
		s = "xenibase: " + e.Call + ": http " + fmt.Sprint(e.Status)
	}
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// Temporary reports whether sending the same request later may succeed
// (no_capacity, project_resuming, rate_limited, a 5xx).
func (e *Error) Temporary() bool {
	switch e.Code {
	case "no_capacity", "project_resuming", "project_restoring", "rate_limited", "capacity_pending_approval", "internal":
		return true
	}
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// IsQuota reports whether an organisation quota was reached: the
// platform's Xenibase organisation cannot take another project.
func (e *Error) IsQuota() bool { return e.Code == "quota_exceeded" || e.Code == "plan_required" }

// QuotaLimit names the quota reached, as the API's limit field.
func (e *Error) QuotaLimit() string {
	if l, _ := e.Quota["limit"].(string); l != "" {
		return "xenibase_" + l
	}
	return "xenibase_projects"
}

// AsError returns err as a Xenibase refusal, if it is one.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// IsNotFound reports whether Xenibase answered 404.
func IsNotFound(err error) bool {
	e, ok := AsError(err)
	return ok && e.Status == http.StatusNotFound
}

// mapError turns an HTTP refusal into an Error with Xenibase's code;
// transport errors pass through.
func mapError(err error) error {
	var he *connector.HTTPError
	if err == nil || !errors.As(err, &he) {
		return err
	}
	var body struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Quota     map[string]any `json:"quota"`
		Statement string         `json:"statement"`
	}
	_ = json.Unmarshal(he.Body, &body)
	e := &Error{Status: he.Status, Code: body.Code, Message: body.Message, Quota: body.Quota, RetryAfter: he.RetryAfter, Statement: body.Statement}
	if e.Message == "" {
		e.Message = strings.TrimSpace(string(he.Body))
		if len(e.Message) > 300 {
			e.Message = e.Message[:300]
		}
	}
	return e
}

// client calls one Xenibase server with one token.
type client struct {
	base  string
	token string
	hc    *http.Client
}

// newClient makes a client through the egress guard for base (https://
// host, or a loopback http address in tests) on behalf of tenant.
func (s *Service) newClient(tenant, base, token, purpose string) (*client, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("%w: the Xenibase URL must be https://host", ErrInvalid)
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("%w: no Xenibase token", ErrInvalid)
	}
	hc := s.guard().Client(egress.Policy{Tenant: tenant, Hosts: []string{u.Hostname()}, Purpose: purpose}, callTimeout)
	return &client{base: base, token: token, hc: hc}, nil
}

// platform is a client on the platform's Xenibase with its token.
func (s *Service) platform(tenant string) (*client, error) {
	if !s.Available() {
		return nil, ErrUnavailable
	}
	return s.newClient(tenant, s.Platform.URL, s.Platform.Token, "xenibase:provision")
}

func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	err := mapError(connector.DoJSON(ctx, c.hc, method, c.base+path, map[string]string{"Authorization": "Bearer " + c.token}, body, out))
	if e, ok := AsError(err); ok {
		// With the server URL's own path: one ending in /api/v1 shows
		// as /api/v1/api/v1/… here.
		call, _, _ := strings.Cut(path, "?")
		if u, perr := url.Parse(c.base); perr == nil {
			call = strings.TrimRight(u.Path, "/") + call
		}
		e.Call = method + " " + call
	}
	return err
}

// --- shapes ---

type operation struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

type projectInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	OrgID  string `json:"org_id"`
	Status string `json:"status"`
}

type services struct {
	Enabled bool   `json:"enabled"`
	Ref     string `json:"ref"`
	URL     string `json:"url"`
}

type createdKey struct {
	Key struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"key"`
	Value string `json:"value"`
}

type createdToken struct {
	Secret string `json:"secret"`
	Token  struct {
		ID        string    `json:"id"`
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"token"`
}

// --- calls ---

// findProject looks for a project by name in the organisation.
func (c *client) findProject(ctx context.Context, org, name string) (*projectInfo, error) {
	var list struct {
		Items []projectInfo `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/projects?limit=500&org="+url.QueryEscape(org), nil, &list); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if list.Items[i].Name == name {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

// createProject queues a shared-tier project and returns it with its
// create operation.
func (c *client) createProject(ctx context.Context, org, name, region string) (projectInfo, operation, error) {
	req := map[string]any{"name": name, "org_id": org, "tier": "shared"}
	if region != "" {
		req["region"] = region
	}
	var out struct {
		Project   projectInfo `json:"project"`
		Operation operation   `json:"operation"`
	}
	err := c.do(ctx, http.MethodPost, "/api/v1/projects", req, &out)
	return out.Project, out.Operation, err
}

func (c *client) project(ctx context.Context, id string) (projectInfo, error) {
	var p projectInfo
	err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(id), nil, &p)
	return p, err
}

func (c *client) operation(ctx context.Context, id string) (operation, error) {
	var op operation
	err := c.do(ctx, http.MethodGet, "/api/v1/operations/"+url.PathEscape(id), nil, &op)
	return op, err
}

func (c *client) services(ctx context.Context, project string) (services, error) {
	var s services
	err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(project)+"/services", nil, &s)
	return s, err
}

// enableServices turns backend services on; the secret key comes back
// this once.
func (c *client) enableServices(ctx context.Context, project string) (services, operation, string, error) {
	var out struct {
		Services  services     `json:"services"`
		Operation operation    `json:"operation"`
		Keys      []createdKey `json:"keys"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(project)+"/services", nil, &out); err != nil {
		return services{}, operation{}, "", err
	}
	secret := ""
	for _, k := range out.Keys {
		if k.Key.Kind == "secret" {
			secret = k.Value
		}
	}
	return out.Services, out.Operation, secret, nil
}

// createSecretKey mints another secret key (after a crash lost the first
// answer, or for an own project without one).
func (c *client) createSecretKey(ctx context.Context, project, name string) (string, error) {
	var out createdKey
	if err := c.do(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(project)+"/services/keys", map[string]any{"kind": "secret", "name": name}, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

// createToken mints a project-restricted token with the scopes, lasting
// days (at most 365, never past the caller's own expiry).
func (c *client) createToken(ctx context.Context, org, name, project string, scopes []string, days int) (createdToken, error) {
	var out createdToken
	err := c.do(ctx, http.MethodPost, "/api/v1/tokens", map[string]any{"name": name, "org_id": org, "scopes": scopes, "project_ids": []string{project}, "expires_in_days": days}, &out)
	return out, err
}

// plan is a schema preview.
type plan struct {
	Hash    string `json:"hash"`
	Confirm string `json:"confirm"`
	Risks   []struct {
		Level, Message string
	} `json:"risks"`
	Statements []struct {
		SQL string `json:"sql"`
	} `json:"statements"`
}

// previewSchema asks Xenibase what a change would run.
func (c *client) previewSchema(ctx context.Context, project string, change map[string]any) (plan, error) {
	var p plan
	err := c.do(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(project)+"/schema/preview", map[string]any{"change": change}, &p)
	return p, err
}

// applySchema runs a previewed change with its hash; drops carry the
// typed confirmation.
func (c *client) applySchema(ctx context.Context, project string, change map[string]any, hash, confirm string) error {
	req := map[string]any{"change": change, "hash": hash}
	if confirm != "" {
		req["confirm"] = confirm
	}
	var out struct {
		Plan plan `json:"plan"`
	}
	return c.do(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(project)+"/schema/apply", req, &out)
}

// wait polls an operation until it succeeds, fails or the provisioning
// timeout passes.
func (s *Service) wait(ctx context.Context, c *client, opID string) error {
	every := s.PollInterval
	if every <= 0 {
		every = 2 * time.Second
	}
	limit := s.ProvisionTimeout
	if limit <= 0 {
		limit = 5 * time.Minute
	}
	deadline := time.Now().Add(limit) // the wall clock: a frozen test clock must not wait forever
	for {
		op, err := c.operation(ctx, opID)
		if err != nil {
			return err
		}
		switch op.Status {
		case "succeeded":
			return nil
		case "failed":
			msg := op.Error
			if msg == "" {
				msg = "the operation failed"
			}
			return fmt.Errorf("%w: Xenibase operation %s (%s): %s", ErrProjectFailed, op.ID, op.Kind, msg)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("xenibase operation %s (%s) is still %s after %s: %w", op.ID, op.Kind, op.Status, limit, ErrProvisioning)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
}
