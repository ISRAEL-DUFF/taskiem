package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
		if _, err := tx.Exec(r.Context(), `DELETE FROM triggers WHERE workflow_id = $1 AND environment = $2`, wf, env); err != nil {
			return err
		}
		if err := remote.Release(r.Context(), tx, wf, []string{env}); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM deployments WHERE workflow_id = $1 AND environment = $2`, wf, env); err != nil {
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
