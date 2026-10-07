package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// The partner connector bridge (spec 13.4 step 4; docs/embedding.md#the-
// partner-connector-bridge): a partner shares its own connectors (tenant
// WebAssembly connectors, x_...) read-only with its sub-tenants, and
// provisions each sub-tenant's credentials for them, so that end users use
// the partner's product already signed in, without ever seeing a
// credential. Credentials go into the sub-tenant's own vault, encrypted
// under its own key, through taskiem_partner_enter (audited in both
// chains); no route returns them, to the partner or to anyone else.

type sharedConnector struct {
	ID       string    `json:"id"`
	SharedBy string    `json:"shared_by"`
	SharedAt time.Time `json:"shared_at"`
	Versions []string  `json:"versions"` // enabled versions sub-tenants can use
}

func (s *Server) listSharedConnectors(w http.ResponseWriter, r *http.Request) {
	var out []sharedConnector
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT sc.connector_id, sc.shared_by, sc.shared_at,
			COALESCE((SELECT array_agg(c.version ORDER BY c.uploaded_at) FROM tenant_connectors c
			           WHERE c.tenant_id = sc.tenant_id AND c.connector_id = sc.connector_id AND c.disabled_at IS NULL), '{}')
			FROM shared_connectors sc ORDER BY sc.connector_id`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[sharedConnector])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connectors": nonNil(out)})
}

// shareConnector shares (or with unshare, stops sharing) one of the
// partner's own connectors, every enabled version of it, with all its
// sub-tenants. Sharing makes it usable, not allowed: an app's end users
// may use it only once the app lists it in allowed_connectors.
func (s *Server) shareConnector(share bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		p := principalFrom(r.Context())
		err := s.tx(r, func(tx pgx.Tx) error {
			var tag interface{ RowsAffected() int64 }
			var err error
			if share {
				var exists bool
				if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM tenant_connectors WHERE connector_id = $1 AND disabled_at IS NULL)`, id).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return fmt.Errorf("%w: upload the connector first (POST /v1/tenant-connectors); %q has no enabled version", errBadRequest, id)
				}
				tag, err = tx.Exec(r.Context(), `INSERT INTO shared_connectors (tenant_id, connector_id, shared_by) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
					p.TenantID, id, p.Actor())
			} else {
				tag, err = tx.Exec(r.Context(), `DELETE FROM shared_connectors WHERE connector_id = $1`, id)
			}
			if err != nil {
				return err
			}
			if !share && tag.RowsAffected() == 0 {
				return pgx.ErrNoRows
			}
			action := "partner.connector.share"
			if !share {
				action = "partner.connector.unshare"
			}
			return auditTx(r, tx, action, id, nil)
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if s.Connectors != nil {
			s.Connectors.ForgetAll()
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "shared": share})
	}
}

// checkCredentials checks credentials against a connector's auth fields:
// every required one, and no others.
func checkCredentials(c *connector.Connector, creds map[string]string) error {
	known := map[string]bool{}
	for _, f := range c.Manifest.Auth.Fields {
		known[f.Key] = true
		if (f.Required == nil || *f.Required) && creds[f.Key] == "" {
			return fmt.Errorf("%w: credential %q is required", errBadRequest, f.Key)
		}
	}
	for k := range creds {
		if !known[k] {
			return fmt.Errorf("%w: %s takes no credential %q", errBadRequest, c.Manifest.ID, k)
		}
	}
	return nil
}

// partnerActor is how the vault records the partner's key inside a
// sub-tenant, as taskiem_partner_enter does.
func partnerActor(p *Principal) string { return "partner:" + p.TenantID.String() + "/" + p.Actor() }

type subConnection struct {
	ID            uuid.UUID `json:"id"`
	Environment   string    `json:"environment"`
	Connector     string    `json:"connector"`
	Name          string    `json:"name"`
	AuthType      string    `json:"auth_type"`
	Status        string    `json:"status"`
	ProvisionedBy *string   `json:"provisioned_by"`
	CreatedAt     time.Time `json:"created_at"`
}

// listSubTenantConnections lists a sub-tenant's connections: names and
// status, never credentials.
func (s *Server) listSubTenantConnections(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out []subConnection
	err = s.partnerTx(r, sub, "partner.connection.list", nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, environment, connector, name, auth_type, status, provisioned_by, created_at
			FROM connections ORDER BY environment, connector, name`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[subConnection])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": nonNil(out)})
}

// createSubTenantConnection provisions credentials for a connector in a
// sub-tenant: written to its vault under its own key, audited in both
// chains, and shown to no one afterwards. The connector must be one the
// sub-tenant can use (the catalogue's, or one the partner shares) and one
// at least one of the partner's active apps allows its end users.
func (s *Server) createSubTenantConnection(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req connectionReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	env := req.Environment
	if env == "" {
		env = "prod"
	}
	if !envNameRe.MatchString(env) {
		s.fail(w, r, fmt.Errorf("%w: %q is not an environment", errBadRequest, env))
		return
	}
	if req.Name == "" {
		req.Name = "default"
	}
	p := principalFrom(r.Context())
	// The sub-tenant must be the partner's before anything of it is read.
	err = s.tx(r, func(tx pgx.Tx) error {
		var ours bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM taskiem_partner_subtenants() WHERE id = $1)`, sub).Scan(&ours); err != nil {
			return err
		}
		if !ours {
			return pgx.ErrNoRows
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, partnerErr(err))
		return
	}
	reg, err := s.Registry.For(r.Context(), sub.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, ok := reg.Get(req.Connector)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: connector %q is not available to the sub-tenant (share your own with PUT /v1/partner/connectors/{id}/share)", errBadRequest, req.Connector))
		return
	}
	if err := checkCredentials(c, req.Credentials); err != nil {
		s.fail(w, r, err)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		var allowed bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM embed_apps WHERE status = 'active' AND $1 = ANY (allowed_connectors))`, c.Manifest.ID).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("%w: none of your active apps allows %s (add it to an app's allowed_connectors first)", errBadRequest, c.Manifest.ID)
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	by := partnerActor(p)
	var id uuid.UUID
	err = s.partnerTx(r, sub, "partner.connection.create", map[string]any{"connector": c.Manifest.ID, "environment": env, "name": req.Name}, func(tx pgx.Tx) error {
		if err := s.checkCount(r.Context(), tx, sub, "max_connections"); err != nil {
			return err
		}
		created, err := s.Vault.CreateConnectionTx(r.Context(), tx, sub, env, c.Manifest.ID, req.Name, c.Manifest.Auth.Type, req.Credentials, by, by)
		if isUnique(err) {
			return fmt.Errorf("%w: a %s connection named %q already exists in %s (replace its credentials instead)", errConflict, c.Manifest.ID, req.Name, env)
		}
		id = created
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "environment": env, "connector": c.Manifest.ID, "name": req.Name, "provisioned_by": by})
}

func connParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "conn"))
	if err != nil {
		return uuid.Nil, pgx.ErrNoRows
	}
	return id, nil
}

// subTenantConnection loads a connection the partner provisioned (the
// partner manages only those), returning its connector id.
func subTenantConnection(r *http.Request, tx pgx.Tx, conn uuid.UUID) (string, error) {
	var connector string
	err := tx.QueryRow(r.Context(), `SELECT connector FROM connections WHERE id = $1 AND provisioned_by IS NOT NULL`, conn).Scan(&connector)
	return connector, err
}

// replaceSubTenantConnection rotates a provisioned connection's
// credentials.
func (s *Server) replaceSubTenantConnection(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	conn, err := connParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Credentials map[string]string `json:"credentials"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	reg, err := s.Registry.For(r.Context(), sub.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	err = s.partnerTx(r, sub, "partner.connection.credentials_replace", map[string]any{"connection": conn}, func(tx pgx.Tx) error {
		id, err := subTenantConnection(r, tx, conn)
		if err != nil {
			return err
		}
		var c *connector.Connector
		for _, x := range reg.List() {
			if x.Manifest.ID == id {
				c = x
			}
		}
		if c == nil {
			return fmt.Errorf("%w: connector %s is no longer available to the sub-tenant", errConflict, id)
		}
		if err := checkCredentials(c, req.Credentials); err != nil {
			return err
		}
		return s.Vault.ReplaceCredentialsTx(r.Context(), tx, sub, conn, req.Credentials, partnerActor(p))
	})
	if errors.Is(err, secrets.ErrNotFound) {
		err = pgx.ErrNoRows
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": conn, "status": "active"})
}

// deleteSubTenantConnection removes a provisioned connection and its
// credentials. Runs that need it fail from then on.
func (s *Server) deleteSubTenantConnection(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	conn, err := connParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	err = s.partnerTx(r, sub, "partner.connection.delete", map[string]any{"connection": conn}, func(tx pgx.Tx) error {
		if _, err := subTenantConnection(r, tx, conn); err != nil {
			return err
		}
		var ref *uuid.UUID
		if err := tx.QueryRow(r.Context(), `DELETE FROM connections WHERE id = $1 RETURNING secret_ref`, conn).Scan(&ref); err != nil {
			return err
		}
		if ref != nil {
			if _, err := tx.Exec(r.Context(), `DELETE FROM secrets WHERE id = $1`, *ref); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
