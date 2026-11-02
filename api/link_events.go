package api

// Workflows started by what happens between linked businesses (business
// links L6, §11; engine/links/events.go): the link worker starts each
// queued run once.

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// startLinkRun starts the run a link event queued: a plan's limit is a
// note, not a retry; another failure is tried again by the worker.
func (s *Server) startLinkRun(ctx context.Context, tenant, id uuid.UUID) error {
	var st links.Start
	var ok bool
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if st, ok, err = links.StartTx(ctx, tx, tenant, id); err != nil || ok {
			return err
		}
		return links.Undue(ctx, tx, tenant, "start", id) // started already
	}); err != nil || !ok {
		return err
	}
	ref, _, err := s.Store.StartRun(ctx, runtime.StartRequest{
		TenantID: tenant, WorkflowID: st.Workflow, Version: st.Version, Environment: "prod",
		Trigger: st.Trigger(), StartedBy: "link:" + st.Event, TriggerID: "link/" + st.Workflow.String(), DedupKey: st.Key,
	})
	var run *uuid.UUID
	note := ""
	if le, isLimit := runtime.IsLimit(err); isLimit {
		note = "Not started: the plan's limit " + le.Limit + " was reached."
	} else if err != nil {
		return err
	} else {
		run = &ref.ID
	}
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return links.StartDoneTx(ctx, tx, tenant, id, run, note)
	})
}
