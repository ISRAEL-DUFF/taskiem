package connector

import (
	"context"
	"net/http"
)

// Remote trigger registration (decision 0021). A trigger whose manifest
// says registration: remote is set up at the provider by Taskiem: when a
// workflow using it is deployed, the connector's Registrar creates the
// provider's subscription (a webhook pointed at Taskiem's ingest URL),
// updates it on a redeploy, and deletes it when the workflow leaves the
// environment. The engine (engine/remote) records the intent before every
// call and reconciles until the provider agrees; the connector only speaks
// the provider's API.

// RemoteSpec is the subscription Taskiem wants the provider to hold.
type RemoteSpec struct {
	// Name tags the subscription as Taskiem's and is unique to it, so it
	// can be found again when Taskiem crashed between creating it and
	// recording its id. Providers keep it as the subscription's name or
	// description.
	Name string
	// URL is where the provider delivers: Taskiem's ingest URL for this
	// subscription.
	URL string
	// Events are the events the workflow subscribes to (empty: all the
	// trigger sends).
	Events []string
	// Options are the workflow's trigger options, checked against the
	// manifest trigger's options schema.
	Options map[string]any
}

// RemoteState is what the provider holds.
type RemoteState struct {
	ID   string // the provider's id for the subscription
	Name string
	// Secret is the signing secret, when the provider just returned one
	// (on create or rotation); empty otherwise. Taskiem keeps it in the
	// vault and never shows it.
	Secret string
	// Status is the provider's view, mapped to one of RemoteHealthy,
	// RemoteFailing, RemotePaused or RemoteBroken; empty when unknown.
	Status string
	Reason string
}

// Remote statuses. RemoteMissing is Taskiem's: the provider no longer has
// the subscription.
const (
	RemoteHealthy = "healthy"
	RemoteFailing = "failing"
	RemotePaused  = "paused"
	RemoteBroken  = "broken"
	RemoteMissing = "missing"
)

// RemoteCall is what a Registrar gets for one call: the connection's
// credentials and an egress-guarded client.
type RemoteCall struct {
	Credentials map[string]string
	HTTP        *http.Client
}

// Registrar manages a trigger's subscriptions at the provider. Errors wrap
// the effects sentinels, as action errors do; ErrNotFound means the
// provider has no such subscription.
type Registrar interface {
	// Create makes the subscription and returns it with its secret.
	Create(ctx context.Context, c RemoteCall, spec RemoteSpec) (RemoteState, error)
	// Update makes an existing subscription match spec and repairs it
	// (resumes a paused one, reinstalls a broken one) where the provider
	// allows. ErrNotFound: it is gone, create it again.
	Update(ctx context.Context, c RemoteCall, id string, spec RemoteSpec) (RemoteState, error)
	// Get reads it. ErrNotFound: it is gone.
	Get(ctx context.Context, c RemoteCall, id string) (RemoteState, error)
	// Find looks a subscription up by its Name; ok is false when there is none.
	Find(ctx context.Context, c RemoteCall, name string) (st RemoteState, ok bool, err error)
	// RotateSecret replaces its signing secret and returns the new one
	// (for a subscription Taskiem found but whose secret it never saw).
	RotateSecret(ctx context.Context, c RemoteCall, id string) (string, error)
	// Delete removes it. A subscription already gone is not an error.
	Delete(ctx context.Context, c RemoteCall, id string) error
}

// EventCall is what an Enricher gets: the workflow's trigger options, and
// Connect, which loads the connection's credentials (a recorded secret
// read) and an egress-guarded client for its hosts, for an enricher that
// has to call the provider.
type EventCall struct {
	Options map[string]any
	Connect func() (map[string]string, *http.Client, error)
}

// Enricher completes a verified delivery before the runs it starts are
// recorded (a provider that leaves large records out, for example). It
// returns the body the run sees. A retryable or unknown-outcome error
// refuses the delivery so the provider sends it again; a fatal one is the
// connector's to describe in the body it returns instead.
type Enricher func(ctx context.Context, c EventCall, body any) (any, error)
