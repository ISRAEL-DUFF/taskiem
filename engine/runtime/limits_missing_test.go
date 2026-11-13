package runtime_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// Limits are set for a tenant that exists (`taskiem tenants limits <id>
// --set`): an id that names none is refused, and nothing is stored or
// audited for it.
func TestSetLimitsRefusesATenantThatDoesNotExist(t *testing.T) {
	e := rt.New(t)
	missing := uuid.New()
	err := e.Store.SetLimits(ctx, missing, map[string]any{"max_workflows": 5}, "cli:operator")
	if !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("limits set for a tenant that does not exist: %v", err)
	}
	var rows int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM tenant_limits WHERE tenant_id = $1) + (SELECT count(*) FROM audit_log WHERE tenant_id = $1)`,
		missing).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("%d rows kept for it, %v", rows, err)
	}
}
