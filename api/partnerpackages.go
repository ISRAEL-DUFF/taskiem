package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Workflow packages (decision 0030; docs/embedding.md#14-workflow-
// packages). A partner shares one of its own workflows as a package and
// installs a version of it into a sub-tenant, with the values of the
// variables it reads and the custom roles it needs. The installed copy is
// the sub-tenant's own workflow: it runs with the sub-tenant's secrets,
// variables and connections, never the partner's, and publishing it
// follows the sub-tenant's governance. Installing a newer version later
// makes a new draft, which the sub-tenant's people (or a publish request)
// put into production: nothing changes in production silently. The partner
// gets the webhook URLs back, never a secret: the sub-tenant's own people
// set its secrets, and make the webhook's signing secret on the workflow's
// Endpoints page.

var (
	// envRefRe and secretRefRe find what a definition's expressions read:
	// env.<variable> and secrets.<name>.
	envRefRe    = regexp.MustCompile(`\benv\.([a-z][a-z0-9_]{0,62})\b`)
	secretRefRe = regexp.MustCompile(`\bsecrets\.([A-Za-z0-9_]+)\b`)
)

// refs lists the distinct names re captures in doc, sorted.
func refs(re *regexp.Regexp, doc []byte) []string {
	var out []string
	for _, m := range re.FindAllSubmatch(doc, -1) {
		out = append(out, string(m[1]))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

type sharedWorkflow struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	LatestVersion int       `json:"latest_version"`
	SharedBy      string    `json:"shared_by"`
	SharedAt      time.Time `json:"shared_at"`
}

func (s *Server) listSharedWorkflows(w http.ResponseWriter, r *http.Request) {
	var out []sharedWorkflow
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT w.id, w.name, (SELECT max(v.version) FROM workflow_versions v WHERE v.workflow_id = w.id), s.shared_by, s.shared_at
			FROM shared_workflows s JOIN workflows w ON w.id = s.workflow_id ORDER BY w.name, w.id`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[sharedWorkflow])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": nonNil(out)})
}

// shareWorkflow shares (or stops sharing) one of the partner's own
// workflows as a package its sub-tenants can be given. Unsharing stops new
// installs; installed copies are the sub-tenants' and stay.
func (s *Server) shareWorkflow(share bool) http.HandlerFunc {
	action := map[bool]string{true: "partner.workflow.share", false: "partner.workflow.unshare"}[share]
	return func(w http.ResponseWriter, r *http.Request) {
		wf, err := uuid.Parse(chi.URLParam(r, "wf"))
		if err != nil {
			s.fail(w, r, pgx.ErrNoRows)
			return
		}
		p := principalFrom(r.Context())
		err = s.tx(r, func(tx pgx.Tx) error {
			ctx := r.Context()
			if share {
				var exists bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflows WHERE id = $1)`, wf).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return pgx.ErrNoRows
				}
				if _, err := tx.Exec(ctx, `INSERT INTO shared_workflows (tenant_id, workflow_id, shared_by) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
					p.TenantID, wf, p.Actor()); err != nil {
					return err
				}
			} else {
				tag, err := tx.Exec(ctx, `DELETE FROM shared_workflows WHERE workflow_id = $1`, wf)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return pgx.ErrNoRows
				}
			}
			return auditTx(r, tx, action, wf.String(), nil)
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": wf, "shared": share})
	}
}

type installReq struct {
	Package      uuid.UUID                             `json:"package"`
	Version      int                                   `json:"version,omitempty"`
	Environments []string                              `json:"environments,omitempty"`
	Variables    map[string]map[string]json.RawMessage `json:"variables,omitempty"`
	Roles        []partnerRoleReq                      `json:"roles,omitempty"`
}

type packageEndpoint struct {
	Environment     string `json:"environment"`
	Type            string `json:"type"`
	URL             string `json:"url"`
	Auth            string `json:"auth"`
	SecretName      string `json:"secret_name,omitempty"`
	DeployedVersion *int   `json:"deployed_version"`
}

type envName struct {
	Environment string `json:"environment"`
	Name        string `json:"name"`
}

type neededSecret struct {
	Environment string `json:"environment"`
	Name        string `json:"name"`
	Set         bool   `json:"set"`
}

type installResult struct {
	WorkflowID       uuid.UUID         `json:"workflow_id"`
	Package          uuid.UUID         `json:"package"`
	PackageVersion   int               `json:"package_version"`
	Version          int               `json:"version"`
	State            string            `json:"state"` // published, pending_approval, draft, unchanged
	Environments     []string          `json:"environments"`
	Endpoints        []packageEndpoint `json:"endpoints"`
	RolesCreated     []string          `json:"roles_created"`
	RolesKept        []string          `json:"roles_kept"`
	VariablesMissing []envName         `json:"variables_missing"`
	Secrets          []neededSecret    `json:"secrets"`
	Problems         []problem         `json:"problems"`
}

// installPackage installs a version of a shared workflow into a sub-tenant,
// or, where an earlier version is installed, adds it as a new draft.
func (s *Server) installPackage(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req installReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Package == uuid.Nil || req.Version < 0 {
		s.fail(w, r, fmt.Errorf("%w: package (a shared workflow's id) is required, and version is a positive number", errBadRequest))
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	if err := s.ownSubTenant(r, sub); err != nil {
		s.fail(w, r, err)
		return
	}

	// The package, from the partner's own tenant.
	var name string
	var pkgVersion int
	var doc []byte
	err = s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT w.name, v.version, v.definition FROM shared_workflows s
			JOIN workflows w ON w.id = s.workflow_id JOIN workflow_versions v ON v.workflow_id = w.id
			WHERE s.workflow_id = $1 AND ($2 = 0 OR v.version = $2) ORDER BY v.version DESC LIMIT 1`, req.Package, req.Version).Scan(&name, &pkgVersion, &doc)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, fmt.Errorf("%w: no shared workflow %s with that version (share it: PUT /v1/partner/workflows/{wf}/share)", errBadRequest, req.Package))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if doc, err = canonical(doc); err != nil {
		s.fail(w, r, err)
		return
	}
	def, err := wd.Load(doc)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: version %d of the package is not a valid definition: %w", errBadRequest, pkgVersion, err))
		return
	}
	digest := sha256.Sum256(doc)
	vars, secretNames := refs(envRefRe, doc), refs(secretRefRe, doc)
	for env, values := range req.Variables {
		if !envNameRe.MatchString(env) {
			s.fail(w, r, fmt.Errorf("%w: %q is not an environment", errBadRequest, env))
			return
		}
		for k, v := range values {
			if !slices.Contains(vars, k) {
				s.fail(w, r, fmt.Errorf("%w: the workflow reads no variable %q (it reads: %v)", errBadRequest, k, vars))
				return
			}
			if t := bytes.TrimSpace(v); len(t) == 0 || bytes.Equal(t, []byte("null")) {
				s.fail(w, r, fmt.Errorf("%w: variable %s in %s needs a value", errBadRequest, k, env))
				return
			}
		}
	}
	roles := make([]partnerRoleReq, len(req.Roles))
	for i, role := range req.Roles {
		perms, err := checkPartnerRole(role.Name, role.Permissions)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		roles[i] = partnerRoleReq{Name: role.Name, Description: role.Description, Permissions: perms}
	}
	webhook := def.Trigger.Type == "webhook"
	hookPath, hookAuth := "", ""
	if webhook {
		hookPath, _ = def.Trigger.Config["path"].(string)
		hookAuth, _ = def.Trigger.Config["auth"].(string)
		if hookAuth != "" && hookAuth != "none" {
			secretNames = append(secretNames, ingest.WebhookSecret(def))
		}
	}

	out := installResult{Package: req.Package, PackageVersion: pkgVersion, RolesCreated: []string{}, RolesKept: []string{},
		VariablesMissing: []envName{}, Secrets: []neededSecret{}, Endpoints: []packageEndpoint{}, Problems: []problem{}}
	fresh := false
	keyID := p.KeyID
	detail := map[string]any{"package": req.Package, "package_version": pkgVersion, "environments": req.Environments}
	err = s.partnerTx(r, sub, "partner.subtenant.workflow.install", detail, func(tx pgx.Tx) error {
		// The environments: deployed to (ungated ones only), and those
		// variables are given for.
		rows, err := tx.Query(ctx, `SELECT name, promotion_from IS NULL FROM environments ORDER BY name`)
		if err != nil {
			return err
		}
		gated := map[string]bool{}
		var ungated []string
		for rows.Next() {
			var env string
			var open bool
			if err := rows.Scan(&env, &open); err != nil {
				rows.Close()
				return err
			}
			gated[env] = !open
			if open {
				ungated = append(ungated, env)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		envs := ungated
		if req.Environments != nil {
			envs = slices.Compact(slices.Sorted(slices.Values(req.Environments)))
			for _, env := range envs {
				g, ok := gated[env]
				switch {
				case !ok:
					return fmt.Errorf("%w: the sub-tenant has no environment %q", errBadRequest, env)
				case g:
					return fmt.Errorf("%w: %s takes promotions only: its people promote into it", errBadRequest, env)
				}
			}
		}
		out.Environments = nonNil(envs)
		for env := range req.Variables {
			if _, ok := gated[env]; !ok {
				return fmt.Errorf("%w: the sub-tenant has no environment %q", errBadRequest, env)
			}
		}

		for _, role := range roles {
			created, err := putPartnerRoleTx(r, tx, sub, role.Name, role.Description, role.Permissions, false, false)
			if err != nil {
				return err
			}
			if created {
				out.RolesCreated = append(out.RolesCreated, role.Name)
			} else {
				out.RolesKept = append(out.RolesKept, role.Name)
			}
		}
		envKeys := make([]string, 0, len(req.Variables))
		for env := range req.Variables {
			envKeys = append(envKeys, env)
		}
		sort.Strings(envKeys)
		for _, env := range envKeys {
			names := make([]string, 0, len(req.Variables[env]))
			for k := range req.Variables[env] {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				if _, err := tx.Exec(ctx, `INSERT INTO variables (tenant_id, environment, name, value) VALUES ($1, $2, $3, $4)
					ON CONFLICT (tenant_id, environment, name) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
					sub, env, k, []byte(req.Variables[env][k])); err != nil {
					return err
				}
				if err := subAudit(r, tx, sub, "variable.write", env+"/"+k, map[string]any{"via": "partner"}); err != nil {
					return err
				}
			}
		}

		var wf uuid.UUID
		var installedDigest []byte
		err = tx.QueryRow(ctx, `SELECT workflow_id, digest FROM package_installs WHERE package_id = $1 FOR UPDATE`, req.Package).Scan(&wf, &installedDigest)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			fresh = true
			if err := s.checkCount(ctx, tx, sub, "max_workflows"); err != nil {
				return err
			}
			wf = uuid.Must(uuid.NewV7())
			if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by, workspace_id)
				VALUES ($1, $2, $3, $4, (SELECT id FROM workspaces WHERE tenant_id = $2 ORDER BY created_at LIMIT 1))`, wf, sub, name, keyID); err != nil {
				return err
			}
			if err := insertVersionTx(ctx, tx, sub, wf, 1, doc, nil, partnerActor(p), ""); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO package_installs (tenant_id, package_id, workflow_id, package_version, digest, installed_by) VALUES ($1, $2, $3, $4, $5, $6)`,
				sub, req.Package, wf, pkgVersion, digest[:], partnerActor(p)); err != nil {
				if isUnique(err) {
					return fmt.Errorf("%w: the package is being installed at the same moment; try again", errConflict)
				}
				return err
			}
			if err := subAudit(r, tx, sub, "workflow.create", wf.String(), map[string]any{"name": name, "package": req.Package, "package_version": pkgVersion, "via": "partner"}); err != nil {
				return err
			}
			out.Version = 1
			g, err := governanceTx(ctx, tx)
			if err != nil {
				return err
			}
			wait, err := publishNeedsApproval(ctx, tx, g, envs)
			if err != nil {
				return err
			}
			if wait {
				// The partner asks; a second person decides (a member of the
				// sub-tenant with workflow.publish, or another of the partner's
				// people through their own key).
				if _, err := tx.Exec(ctx, `INSERT INTO publish_requests (tenant_id, workflow_id, version, requested_by) VALUES ($1, $2, 1, $3)`, sub, wf, p.Human()); err != nil {
					return err
				}
				out.State = "pending_approval"
				if err := subAudit(r, tx, sub, "publish_request.create", fmt.Sprintf("%s/1", wf), map[string]any{"via": "partner"}); err != nil {
					return err
				}
			} else {
				probs, d, _, err := s.publishToTx(ctx, tx, sub, wf, 1, &keyID, "", envs)
				if errors.Is(err, errInvalid) {
					out.Problems = probs
				}
				if err != nil {
					return err
				}
				out.State = "published"
				if err := subAudit(r, tx, sub, "workflow.publish", fmt.Sprintf("%s/1", wf), map[string]any{"digest": d, "environments": envs, "via": "partner"}); err != nil {
					return err
				}
			}
		case err != nil:
			return err
		default:
			if err := tx.QueryRow(ctx, `SELECT 1 FROM workflows WHERE id = $1 FOR UPDATE`, wf).Scan(new(int)); err != nil {
				return err
			}
			if err := s.refuseGitManaged(ctx, tx, wf); err != nil {
				return err
			}
			var latest int
			var latestDigest []byte
			if err := tx.QueryRow(ctx, `SELECT version, digest FROM workflow_versions WHERE workflow_id = $1 ORDER BY version DESC LIMIT 1`, wf).Scan(&latest, &latestDigest); err != nil {
				return err
			}
			if bytes.Equal(latestDigest, digest[:]) {
				out.Version, out.State = latest, "unchanged"
			} else {
				out.Version, out.State = latest+1, "draft"
				if err := insertVersionTx(ctx, tx, sub, wf, latest+1, doc, nil, partnerActor(p), ""); err != nil {
					return err
				}
				if err := subAudit(r, tx, sub, "workflow.version.create", fmt.Sprintf("%s/%d", wf, latest+1),
					map[string]any{"package": req.Package, "package_version": pkgVersion, "via": "partner"}); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE package_installs SET package_version = $2, digest = $3, installed_by = $4, updated_at = now() WHERE package_id = $1`,
				req.Package, pkgVersion, digest[:], partnerActor(p)); err != nil {
				return err
			}
		}
		out.WorkflowID = wf

		// What the partner needs to connect it, and what the sub-tenant's
		// people still have to set.
		for _, env := range envs {
			if webhook {
				ep := packageEndpoint{Environment: env, Type: "webhook", URL: "/hooks/" + sub.String() + hookPath + "?env=" + env, Auth: hookAuth}
				if hookAuth != "" && hookAuth != "none" {
					ep.SecretName = ingest.WebhookSecret(def)
				}
				v, err := deployedVersion(ctx, tx, wf, env)
				if err != nil {
					return err
				}
				if v > 0 {
					ep.DeployedVersion = &v
				}
				out.Endpoints = append(out.Endpoints, ep)
			}
			for _, k := range vars {
				var set bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM variables WHERE environment = $1 AND name = $2)`, env, k).Scan(&set); err != nil {
					return err
				}
				if !set {
					out.VariablesMissing = append(out.VariablesMissing, envName{env, k})
				}
			}
			for _, k := range secretNames {
				var set bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM secrets WHERE environment = $1 AND name = $2)`, env, k).Scan(&set); err != nil {
					return err
				}
				out.Secrets = append(out.Secrets, neededSecret{env, k, set})
			}
		}
		return nil
	})
	if errors.Is(err, errInvalid) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the package's definition is not valid in the sub-tenant", "problems": out.Problems})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if out.State == "draft" {
		out.Problems = nonNil(s.check(ctx, sub, doc))
	}
	status := http.StatusOK
	if fresh {
		status = http.StatusCreated
	}
	writeJSON(w, status, out)
}

// ownSubTenant answers not found unless sub is one of the partner's
// sub-tenants, before anything else of a request is looked at.
func (s *Server) ownSubTenant(r *http.Request, sub uuid.UUID) error {
	return partnerErr(s.tx(r, func(tx pgx.Tx) error {
		var ours bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM taskiem_partner_subtenants() WHERE id = $1)`, sub).Scan(&ours); err != nil {
			return err
		}
		if !ours {
			return pgx.ErrNoRows
		}
		return nil
	}))
}

type packageInstall struct {
	WorkflowID     uuid.UUID         `json:"workflow_id"`
	Name           string            `json:"name"`
	Package        uuid.UUID         `json:"package"`
	PackageVersion int               `json:"package_version"`
	LatestVersion  int               `json:"latest_version"`
	ActiveVersion  *int              `json:"active_version"`
	InstalledAt    time.Time         `json:"installed_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	Endpoints      []packageEndpoint `json:"endpoints"`
}

// listPackageInstalls lists the packages installed in a sub-tenant, with
// the webhook URLs each environment listens on now.
func (s *Server) listPackageInstalls(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out []packageInstall
	err = s.partnerTx(r, sub, "partner.subtenant.installs.read", nil, func(tx pgx.Tx) error {
		ctx := r.Context()
		rows, err := tx.Query(ctx, `SELECT i.workflow_id, w.name, i.package_id, i.package_version,
			(SELECT max(v.version) FROM workflow_versions v WHERE v.workflow_id = w.id), w.active_version, i.installed_at, i.updated_at
			FROM package_installs i JOIN workflows w ON w.id = i.workflow_id ORDER BY i.installed_at, i.workflow_id`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (packageInstall, error) {
			var x packageInstall
			err := row.Scan(&x.WorkflowID, &x.Name, &x.Package, &x.PackageVersion, &x.LatestVersion, &x.ActiveVersion, &x.InstalledAt, &x.UpdatedAt)
			return x, err
		})
		if err != nil {
			return err
		}
		for i := range out {
			if out[i].Endpoints, err = webhookEndpoints(ctx, tx, sub, out[i].WorkflowID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installs": nonNil(out)})
}

// webhookEndpoints lists where a workflow's webhook trigger listens now,
// per environment.
func webhookEndpoints(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID) ([]packageEndpoint, error) {
	rows, err := tx.Query(ctx, `SELECT environment, path, COALESCE(auth, ''), COALESCE(secret_name, ''), version FROM triggers
		WHERE workflow_id = $1 AND type = 'webhook' ORDER BY environment`, wf)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (packageEndpoint, error) {
		var e packageEndpoint
		var path string
		var v int
		if err := row.Scan(&e.Environment, &path, &e.Auth, &e.SecretName, &v); err != nil {
			return e, err
		}
		e.Type, e.URL, e.DeployedVersion = "webhook", "/hooks/"+tenant.String()+path+"?env="+e.Environment, &v
		if e.Auth == "none" {
			e.SecretName = ""
		}
		return e, nil
	})
	return nonNil(out), err
}
