package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/remote"
)

// Remote trigger registration (decision 0021): the subscriptions Taskiem
// keeps at providers for remotely registered triggers, shown on the
// workflow's triggers and on its connections, applied right after a
// publish and removed by an undeploy.

// remoteWait bounds how long a publish waits for the provider.
const remoteWait = 15 * time.Second

// applyRemote applies wf's pending subscriptions now and returns them all,
// so the answer to a publish or undeploy shows whether the provider took
// them. The scheduler retries whatever is left.
func (s *Server) applyRemote(r *http.Request, wf uuid.UUID) []remote.Status {
	p := principalFrom(r.Context())
	if s.Remote != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), remoteWait)
		defer cancel()
		if err := s.Remote.ReconcileWorkflow(ctx, p.TenantID, wf); err != nil {
			s.Logger.Warn("remote subscriptions: applying after a publish", "workflow", wf, "err", err)
		}
	}
	var out []remote.Status
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = remote.List(r.Context(), tx, `workflow_id = $1`, wf)
		return err
	}); err != nil {
		s.Logger.Warn("remote subscriptions: listing", "workflow", wf, "err", err)
	}
	return out
}

// declareConnectionSubscriptions records, in tx, the subscriptions a
// connection holds at its provider (connector/v1 registration:
// connection), for every event each such trigger sends. A connection
// whose key is a shared client's (decision 0031) holds none: its events
// come through the subscription of the partner that holds the client.
func declareConnectionSubscriptions(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env string, c *connector.Connector, name string, creds map[string]string) (bool, error) {
	if creds["shared_client"] == "true" {
		return false, nil
	}
	declared := false
	for _, trig := range slices.Sorted(maps.Keys(c.Manifest.Triggers)) {
		t := c.Manifest.Triggers[trig]
		if !t.PerConnection() {
			continue
		}
		if _, err := remote.DeclareConnection(ctx, tx, tenant, env, c.Ref(), trig, name, t.Events); err != nil {
			return declared, err
		}
		declared = true
	}
	return declared, nil
}

// applyConnectionSubscriptions asks the provider now for what a
// connection's subscriptions say, so a page shows the result; the
// scheduler finishes whatever is left.
func (s *Server) applyConnectionSubscriptions(r *http.Request) {
	if s.Remote == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), remoteWait)
	defer cancel()
	if err := s.Remote.ReconcileTenant(ctx, principalFrom(r.Context()).TenantID); err != nil {
		s.Logger.Warn("remote subscriptions: applying a connection's", "err", err)
	}
}

// releaseConnectionSubscriptions records that a connection about to be
// deleted no longer holds its subscriptions, and asks the provider to
// remove them now, while its credentials can still reach it. Nothing is
// released for a connection deployed workflows use unless forced: the
// deletion is refused then.
func (s *Server) releaseConnectionSubscriptions(r *http.Request, reg connector.Lookup, id uuid.UUID, force bool) error {
	released := false
	err := s.tx(r, func(tx pgx.Tx) error {
		var connID, name, env string
		err := tx.QueryRow(r.Context(), `SELECT connector, name, environment FROM connections WHERE id = $1`, id).Scan(&connID, &name, &env)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // the deletion says it is not there
		}
		if err != nil {
			return err
		}
		c, ok := connectorByID(reg, connID)
		if !ok || !slices.ContainsFunc(slices.Collect(maps.Values(c.Manifest.Triggers)), connector.TriggerSpec.PerConnection) {
			return nil
		}
		if !force {
			uses, err := connectionUses(r.Context(), tx, env, connID, name)
			if err != nil || len(uses) > 0 {
				return err
			}
		}
		released = true
		return remote.ReleaseConnection(r.Context(), tx, env, c.Ref(), name)
	})
	if err == nil && released {
		s.applyConnectionSubscriptions(r)
	}
	return err
}

// remoteSummary is a connection's subscriptions at its provider.
type remoteSummary struct {
	Subscriptions int `json:"subscriptions"`
	// Health is the worst of them: failed, missing, broken, paused,
	// failing, removing, pending or ok.
	Health   string          `json:"health"`
	Problems []remote.Status `json:"problems"`
}

var healthRank = map[string]int{"ok": 0, "pending": 1, "removing": 2, "failing": 3, "paused": 4, "broken": 5, "missing": 6, "failed": 7}

// summarise attaches each connection's subscriptions: those of its
// connector in its environment that name it, or name no connection.
func summarise(conns []connectionInfo, subs []remote.Status) {
	for i := range conns {
		c := &conns[i]
		for _, s := range subs {
			id, _, _ := strings.Cut(s.Connector, "@")
			if id != c.Connector || s.Environment != c.Environment || (s.Connection != nil && *s.Connection != c.Name) {
				continue
			}
			if c.Remote == nil {
				c.Remote = &remoteSummary{Health: "ok", Problems: []remote.Status{}}
			}
			c.Remote.Subscriptions++
			if healthRank[s.Health] > healthRank[c.Remote.Health] {
				c.Remote.Health = s.Health
			}
			if s.Health != "ok" && s.Health != "pending" {
				c.Remote.Problems = append(c.Remote.Problems, s)
			}
		}
	}
}

// undeployTx takes a workflow out of an environment in tx: its triggers,
// its form and email addresses, its remote subscriptions (released for
// deletion at the provider) and its deployment.
func undeployTx(ctx context.Context, tx pgx.Tx, wf uuid.UUID, env string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM triggers WHERE workflow_id = $1 AND environment = $2`, wf, env); err != nil {
		return err
	}
	// Nothing runs there now: its form and email address stop taking
	// submissions and mail (deploying again opens them).
	for _, q := range []string{
		`UPDATE form_endpoints SET status = 'closed', updated_at = now() WHERE workflow_id = $1 AND environment = $2 AND status IN ('live', 'paused')`,
		`UPDATE email_endpoints SET status = 'closed', updated_at = now() WHERE workflow_id = $1 AND environment = $2 AND status = 'live'`,
	} {
		if _, err := tx.Exec(ctx, q, wf, env); err != nil {
			return err
		}
	}
	if err := remote.Release(ctx, tx, wf, []string{env}); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM deployments WHERE workflow_id = $1 AND environment = $2`, wf, env)
	return err
}

// undeploy takes a workflow out of an environment: its deployment and
// triggers go, and the subscriptions of a remotely registered trigger are
// deleted at the provider. Runs already started carry on.
func (s *Server) undeploy(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	env := chi.URLParam(r, "env")
	p := principalFrom(r.Context())
	if p.Environment != "" && p.Environment != env {
		writeErr(w, http.StatusForbidden, "this key is limited to "+p.Environment)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := s.refuseGitManaged(r.Context(), tx, wf); err != nil {
			return err
		}
		v, err := deployedVersion(r.Context(), tx, wf, env)
		if err != nil {
			return err
		}
		if v == 0 {
			return fmt.Errorf("%w: workflow has no version deployed in %s", errConflict, env)
		}
		if err := undeployTx(r.Context(), tx, wf, env); err != nil {
			return err
		}
		return auditTx(r, tx, "workflow.undeploy", fmt.Sprintf("%s/%d", wf, v), map[string]any{"environment": env})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": wf, "environment": env, "deployed": false, "remote_subscriptions": s.applyRemote(r, wf)})
}
