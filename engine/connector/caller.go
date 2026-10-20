package connector

import (
	"context"

	"github.com/google/uuid"
)

// Caller is who a connector action runs for: the run's tenant, the run,
// the step and the environment. The worker attaches it to every action's
// context. Provider connectors have no use for it; platform connectors,
// which act on Taskiem's own records (the storefront's orders), act only
// for this tenant and never one named in their input.
type Caller struct {
	Tenant      uuid.UUID
	Run         uuid.UUID
	Step        string
	Environment string
}

type callerKey struct{}

// WithCaller attaches c to ctx.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the caller the worker attached; ok is false outside a
// run (a test call, the API).
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok && c.Tenant != uuid.Nil
}
