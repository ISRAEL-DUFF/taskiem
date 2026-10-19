package datatables

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Project is the organisation's Xenibase project as Taskiem records it.
type Project struct {
	Status         string     `json:"status"` // none, creating, enabling, ready, failed
	Own            bool       `json:"own"`
	ProjectName    string     `json:"project_name,omitempty"`
	ProjectID      string     `json:"project_id,omitempty"`
	Ref            string     `json:"project_ref,omitempty"`
	APIURL         string     `json:"api_url,omitempty"`
	ConnectionID   uuid.UUID  `json:"-"`
	TokenExpiresAt *time.Time `json:"token_expires_at,omitempty"`
	Error          string     `json:"error,omitempty"`
	operationID    string
	leaseUntil     *time.Time
}

// Ready reports whether tables can be made in the project.
func (p Project) Ready() bool { return p.Status == "ready" }

// ProjectName is the name of the project Taskiem makes for a tenant.
func ProjectName(tenant uuid.UUID) string { return "taskiem-" + tenant.String() }

// leaseFor is how long one request may hold provisioning before another
// may take over (the provisioning timeout plus the calls around it).
const leaseFor = 6 * time.Minute

// loadProject reads the tenant's project record (status none when there
// is none). tx must have the tenant in scope.
func loadProject(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Project, error) {
	p := Project{Status: "none"}
	var projectID, operationID, ref, apiURL, errText *string
	var conn *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT status, own, project_name, project_id, operation_id, ref, api_url, connection_id, token_expires_at, lease_until, error
		FROM data_table_projects WHERE tenant_id = $1`, tenant).
		Scan(&p.Status, &p.Own, &p.ProjectName, &projectID, &operationID, &ref, &apiURL, &conn, &p.TokenExpiresAt, &p.leaseUntil, &errText)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	for dst, src := range map[*string]*string{&p.ProjectID: projectID, &p.operationID: operationID, &p.Ref: ref, &p.APIURL: apiURL, &p.Error: errText} {
		if src != nil {
			*dst = *src
		}
	}
	if conn != nil {
		p.ConnectionID = *conn
	}
	return p, nil
}

// Status is the project's state for the API, with whether tables can be
// made at all.
type Status struct {
	Project
	// Available: the platform can provision a project, or the
	// organisation's own is ready.
	Available bool `json:"available"`
	// Configured: the platform's Xenibase is set up.
	Configured bool   `json:"configured"`
	Connection string `json:"connection"`
}

// Status reads the project's state.
func (s *Service) Status(ctx context.Context, tenant uuid.UUID) (Status, error) {
	var p Project
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		p, err = loadProject(ctx, tx, tenant)
		return err
	})
	if err != nil {
		return Status{}, err
	}
	return Status{Project: p, Available: s.Available() || p.Ready(), Configured: s.Available(), Connection: ConnectionName}, nil
}

// EnsureProject returns the organisation's project, provisioning it in
// the platform's Xenibase organisation when there is none: a project
// named after the tenant, its backend services, a secret key, a
// project-restricted token, and the data_tables connection holding them.
// Each step is recorded before the next (data_table_projects.status), so
// a request that finds a half-made project carries on from where the
// last one stopped and never makes a second project; while one request
// provisions, another gets ErrProvisioning.
func (s *Service) EnsureProject(ctx context.Context, tenant uuid.UUID) (Project, error) {
	var p Project
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		p, err = loadProject(ctx, tx, tenant)
		return err
	})
	if err != nil || p.Ready() {
		return p, err
	}
	if !s.Available() {
		return p, ErrUnavailable
	}
	if p.Own {
		// An own project that is not ready has nothing to provision.
		return p, fmt.Errorf("%w: the organisation's own project is %s", ErrProjectFailed, p.Status)
	}
	// Record the intent and take the lease before any call to Xenibase.
	claimed := false
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO data_table_projects (tenant_id, status, project_name) VALUES ($1, 'creating', $2) ON CONFLICT (tenant_id) DO NOTHING`,
			tenant, ProjectName(tenant)); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE data_table_projects SET lease_until = now() + $2, updated_at = now()
			WHERE tenant_id = $1 AND status <> 'ready' AND (lease_until IS NULL OR lease_until < now())`, tenant, leaseFor)
		if err != nil {
			return err
		}
		claimed = tag.RowsAffected() == 1
		p, err = loadProject(ctx, tx, tenant)
		return err
	})
	if err != nil {
		return p, err
	}
	if p.Ready() {
		return p, nil
	}
	if !claimed {
		return p, ErrProvisioning
	}
	release := func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = db.InTenantTx(rctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(rctx, `UPDATE data_table_projects SET lease_until = NULL, updated_at = now() WHERE tenant_id = $1`, tenant)
			return err
		})
	}
	defer release()
	c, err := s.platform(tenant.String())
	if err != nil {
		return p, err
	}
	if err := s.provision(ctx, c, tenant, &p); err != nil {
		s.log().Error("data tables: provisioning failed", "tenant", tenant, "status", p.Status, "err", pii.Redact(err.Error()))
		return p, err
	}
	return p, nil
}

// save writes the project's state after a step.
func (s *Service) save(ctx context.Context, tenant uuid.UUID, p *Project) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE data_table_projects SET status = $2, project_id = NULLIF($3, ''), operation_id = NULLIF($4, ''), ref = NULLIF($5, ''),
			api_url = NULLIF($6, ''), error = NULLIF($7, ''), updated_at = now() WHERE tenant_id = $1`,
			tenant, p.Status, p.ProjectID, p.operationID, p.Ref, p.APIURL, p.Error)
		return err
	})
}

// provision carries the project from its recorded state to ready.
func (s *Service) provision(ctx context.Context, c *client, tenant uuid.UUID, p *Project) error {
	fail := func(err error) error {
		p.Error = pii.Redact(err.Error())
		if errors.Is(err, ErrProjectFailed) {
			p.Status = "failed"
		}
		_ = s.save(ctx, tenant, p)
		return err
	}
	if p.Status == "creating" || p.Status == "failed" {
		p.Error = ""
		if p.ProjectID == "" {
			// A project may exist from an attempt whose record was lost:
			// adopt it rather than make a second.
			found, err := c.findProject(ctx, s.Platform.Org, p.ProjectName)
			if err != nil {
				return fail(err)
			}
			if found != nil {
				p.ProjectID, p.operationID = found.ID, ""
			} else {
				proj, op, err := c.createProject(ctx, s.Platform.Org, p.ProjectName, s.Platform.Region)
				if err != nil {
					return fail(err)
				}
				p.ProjectID, p.operationID = proj.ID, op.ID
			}
			p.Status = "creating"
			if err := s.save(ctx, tenant, p); err != nil {
				return err
			}
		}
		if p.operationID != "" {
			if err := s.wait(ctx, c, p.operationID); err != nil {
				return fail(err)
			}
		} else if err := s.waitProject(ctx, c, p.ProjectID); err != nil {
			return fail(err)
		}
		p.Status, p.operationID = "enabling", ""
		if err := s.save(ctx, tenant, p); err != nil {
			return err
		}
	}
	if p.Status != "enabling" {
		return fmt.Errorf("%w: unexpected project state %q", ErrProjectFailed, p.Status)
	}
	// Backend services: on, with the secret key from that one answer; a
	// project whose services are already on (an earlier attempt stopped
	// after this step) gets a fresh secret key instead.
	svc, err := c.services(ctx, p.ProjectID)
	if err != nil {
		return fail(err)
	}
	var secret string
	if !svc.Enabled {
		enabled, op, key, err := c.enableServices(ctx, p.ProjectID)
		if err != nil {
			return fail(err)
		}
		if op.ID != "" {
			if err := s.wait(ctx, c, op.ID); err != nil {
				return fail(err)
			}
		}
		svc, secret = enabled, key
		if svc.URL == "" || svc.Ref == "" {
			if svc, err = c.services(ctx, p.ProjectID); err != nil {
				return fail(err)
			}
		}
	}
	if secret == "" {
		if secret, err = c.createSecretKey(ctx, p.ProjectID, TokenName); err != nil {
			return fail(err)
		}
	}
	if svc.URL == "" {
		return fail(fmt.Errorf("%w: the project has no data API URL (the Xenibase server has no API domain)", ErrProjectFailed))
	}
	tok, err := c.createToken(ctx, s.Platform.Org, TokenName, p.ProjectID, []string{"read", "write", "admin"}, 365)
	if err != nil {
		return fail(err)
	}
	p.Ref, p.APIURL = svc.Ref, svc.URL
	creds := map[string]string{"token": tok.Secret, "project_id": p.ProjectID, "org_id": s.Platform.Org, "base_url": s.Platform.URL, "secret_key": secret, "api_url": svc.URL}
	expires := tok.Token.ExpiresAt
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		id, err := s.storeConnection(ctx, tx, tenant, creds)
		if err != nil {
			return err
		}
		p.ConnectionID, p.Status, p.Error = id, "ready", ""
		if !expires.IsZero() {
			p.TokenExpiresAt = &expires
		}
		if _, err := tx.Exec(ctx, `UPDATE data_table_projects SET status = 'ready', project_id = $2, operation_id = NULL, ref = $3, api_url = $4,
			connection_id = $5, token_expires_at = $6, error = NULL, updated_at = now() WHERE tenant_id = $1`,
			tenant, p.ProjectID, p.Ref, p.APIURL, id, p.TokenExpiresAt); err != nil {
			return err
		}
		return audit(ctx, tx, tenant, "table.project.provision", p.ProjectID, map[string]any{"project_name": p.ProjectName, "ref": p.Ref, "connection_id": id})
	})
}

// waitProject polls a project adopted by name until it is active.
func (s *Service) waitProject(ctx context.Context, c *client, id string) error {
	every := s.PollInterval
	if every <= 0 {
		every = 2 * time.Second
	}
	limit := s.ProvisionTimeout
	if limit <= 0 {
		limit = 5 * time.Minute
	}
	deadline := time.Now().Add(limit)
	for {
		proj, err := c.project(ctx, id)
		if err != nil {
			return err
		}
		switch proj.Status {
		case "active":
			return nil
		case "error", "deleting":
			return fmt.Errorf("%w: project %s is in state %s; an operator must look at it in Xenibase", ErrProjectFailed, id, proj.Status)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("project %s is still %s after %s: %w", id, proj.Status, limit, ErrProvisioning)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
}

// storeConnection writes the data_tables connection, or replaces the
// credentials of one left by an earlier attempt.
func (s *Service) storeConnection(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, creds map[string]string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM connections WHERE tenant_id = $1 AND environment = $2 AND connector = $3 AND name = $4`,
		tenant, Environment, Connector, ConnectionName).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return s.Vault.CreateConnectionTx(ctx, tx, tenant, Environment, Connector, ConnectionName, "api_key", creds, "system", ProvisionedBy)
	case err != nil:
		return id, err
	}
	if _, err := tx.Exec(ctx, `UPDATE connections SET provisioned_by = $2 WHERE id = $1`, id, ProvisionedBy); err != nil {
		return id, err
	}
	return id, s.Vault.ReplaceCredentialsTx(ctx, tx, tenant, id, creds, "system")
}

// audit appends a system entry to the tenant's audit chain.
func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action, target string, detail map[string]any) error {
	raw, err := marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'system', $2, $3, $4)`, tenant, action, target, raw)
	return err
}

// SetOwnProject records an existing xenibase connection's project as the
// organisation's (Settings > Data tables). The caller checked the
// connection (test_connection, a secret key and a data API URL); here its
// credentials are copied into the data_tables connection, so workflows and
// the API find the project under the one name, and the record is marked
// own: Taskiem then never provisions, renews or deletes anything for it.
//
// While tables exist the project cannot change, but the organisation's own
// project can be set again from a connection to the same project: that is
// how its credentials are renewed when the old token expires or is revoked
// (Taskiem renews only projects it made), without which its tables could
// be neither used nor deleted. renewed reports that case.
func (s *Service) SetOwnProject(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, creds map[string]string, projectName, ref, apiURL string, expires *time.Time) (p Project, renewed bool, err error) {
	if p, err = loadProject(ctx, tx, tenant); err != nil {
		return p, false, err
	}
	n, err := CountTables(ctx, tx)
	if err != nil {
		return p, false, err
	}
	if n > 0 {
		if !p.Own || p.ProjectID == "" || p.ProjectID != strings.TrimSpace(creds["project_id"]) {
			return p, false, fmt.Errorf("%w: the organisation already has %d data tables in its current project; delete them first (a connection to the same project of your own renews its credentials)", ErrConflict, n)
		}
		renewed = true
	}
	if p.leaseUntil != nil && p.leaseUntil.After(s.now()) && !p.Ready() {
		return p, false, ErrProvisioning
	}
	id, err := s.storeConnection(ctx, tx, tenant, creds)
	if err != nil {
		return p, false, err
	}
	p = Project{Status: "ready", Own: true, ProjectName: projectName, ProjectID: creds["project_id"], Ref: ref, APIURL: apiURL, ConnectionID: id, TokenExpiresAt: expires}
	_, err = tx.Exec(ctx, `INSERT INTO data_table_projects (tenant_id, status, own, project_name, project_id, ref, api_url, connection_id, token_expires_at)
		VALUES ($1, 'ready', true, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7)
		ON CONFLICT (tenant_id) DO UPDATE SET status = 'ready', own = true, project_name = EXCLUDED.project_name, project_id = EXCLUDED.project_id,
		  operation_id = NULL, ref = EXCLUDED.ref, api_url = EXCLUDED.api_url, connection_id = EXCLUDED.connection_id,
		  token_expires_at = EXCLUDED.token_expires_at, lease_until = NULL, error = NULL, updated_at = now()`,
		tenant, projectName, p.ProjectID, ref, apiURL, id, expires)
	return p, renewed, err
}

// Credentials reads the data_tables connection's credentials with the
// use attached to ctx (secrets.WithUse), as any connection use is
// recorded.
func (s *Service) Credentials(ctx context.Context, tenant uuid.UUID) (map[string]string, error) {
	creds, err := s.Vault.Credentials(ctx, tenant, Environment, Connector, ConnectionName)
	if errors.Is(err, secrets.ErrNotFound) {
		return nil, fmt.Errorf("the %s connection is missing: %w", ConnectionName, ErrNotFound)
	}
	return creds, err
}
