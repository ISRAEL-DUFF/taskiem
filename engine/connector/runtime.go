package connector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Request is what an action handler receives (spec 6.3).
type Request struct {
	Input          map[string]any
	Credentials    map[string]string // decrypted for this call only
	IdempotencyKey string            // also placed in the manifest's idempotency field
	// KeyFirstSent is when a request carrying IdempotencyKey was first about
	// to be sent (database clock), for providers whose duplicate protection
	// expires; zero for reads.
	KeyFirstSent time.Time
	Attempt      int
	Logger       *slog.Logger
	HTTP         *http.Client
	// Dial opens egress-guarded TCP connections for connectors that do not
	// speak HTTP (databases, SFTP).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Response is a handler's result.
type Response struct {
	Output any
}

// Action executes one connector action. Errors must wrap one of the
// effects sentinels (ErrRetryable, ErrFatal, ErrUnknownOutcome, ErrNotSent);
// anything else is treated as an unknown outcome.
type Action interface {
	Execute(ctx context.Context, req Request) (Response, error)
}

// ActionFunc adapts a function to Action.
type ActionFunc func(ctx context.Context, req Request) (Response, error)

func (f ActionFunc) Execute(ctx context.Context, req Request) (Response, error) { return f(ctx, req) }

// ErrNotFound is returned by a reconcile action when the provider has no
// record of the effect, so it is safe to send it again.
var ErrNotFound = errors.New("not_found")

// Connector is a manifest plus its action handlers.
type Connector struct {
	Manifest *Manifest
	Actions  map[string]Action
	// Verifiers check webhooks for triggers whose verify scheme is
	// "connector": providers that sign something no manifest scheme
	// describes (fields inside the body, for example). Keyed by trigger.
	Verifiers map[string]WebhookVerifier
}

// WebhookVerifier checks one delivery; secret is the connection credential
// named by the trigger's secret_field. A non-nil error refuses it.
type WebhookVerifier func(secret string, h http.Header, body []byte) error

// Ref is the "id@major" a workflow pins.
func (c *Connector) Ref() string {
	major, _, _ := strings.Cut(c.Manifest.Version, ".")
	return c.Manifest.ID + "@" + major
}

// Lookup finds connectors by "id@major": a Registry (built-ins only), or a
// tenant's view of it from Registry.For.
type Lookup interface {
	Get(ref string) (*Connector, bool)
	List() []*Connector
	Pins() map[string]string
}

// TenantPrefix starts the id of every connector a tenant brings itself, so
// one can never shadow a built-in (spec 6: third-party connectors run as
// WebAssembly).
const TenantPrefix = "x_"

// CataloguePrefix starts the id of every connector in the public
// catalogue: p_<publisher>_<name>, in the publisher's verified namespace
// (docs/connector-submissions.md), distinct from built-ins and from
// tenants' own x_ connectors.
const CataloguePrefix = "p_"

// TenantSource returns a tenant's own connectors and the catalogue
// connectors it installed.
type TenantSource func(ctx context.Context, tenant string) ([]*Connector, error)

// Registry holds the connectors compiled into this binary, and reaches
// tenants' own connectors through its TenantSource.
type Registry struct {
	mu     sync.RWMutex
	m      map[string]*Connector
	tenant TenantSource
}

// SetTenantSource makes For include each tenant's own connectors.
func (r *Registry) SetTenantSource(f TenantSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tenant = f
}

// For is the connectors tenant can use: the built-ins and its own.
func (r *Registry) For(ctx context.Context, tenant string) (Lookup, error) {
	r.mu.RLock()
	src := r.tenant
	r.mu.RUnlock()
	if src == nil || tenant == "" {
		return r, nil
	}
	own, err := src(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("tenant connectors: %w", err)
	}
	v := &view{base: r, own: map[string]*Connector{}}
	for _, c := range own {
		if strings.HasPrefix(c.Manifest.ID, TenantPrefix) || strings.HasPrefix(c.Manifest.ID, CataloguePrefix) {
			v.own[c.Ref()] = c
		}
	}
	return v, nil
}

// view is a tenant's Lookup.
type view struct {
	base *Registry
	own  map[string]*Connector
}

func (v *view) Get(ref string) (*Connector, bool) {
	if c, ok := v.own[ref]; ok {
		return c, true
	}
	return v.base.Get(ref)
}

func (v *view) List() []*Connector {
	out := v.base.List()
	for _, c := range v.own {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
	return out
}

func (v *view) Pins() map[string]string {
	out := v.base.Pins()
	for k, c := range v.own {
		out[k] = c.Manifest.Version
	}
	return out
}

func NewRegistry() *Registry { return &Registry{m: map[string]*Connector{}} }

// Register checks that every manifest action has a handler, then adds the
// connector. A newer minor version of the same major replaces an older one.
func (r *Registry) Register(c *Connector) error {
	if err := c.Check(); err != nil {
		return err
	}
	if strings.HasPrefix(c.Manifest.ID, TenantPrefix) {
		return fmt.Errorf("connector %s: ids starting %q belong to tenants' own connectors", c.Manifest.ID, TenantPrefix)
	}
	if strings.HasPrefix(c.Manifest.ID, CataloguePrefix) {
		return fmt.Errorf("connector %s: ids starting %q belong to publishers in the catalogue", c.Manifest.ID, CataloguePrefix)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[c.Ref()] = c
	return nil
}

// Check reports an action without a handler or a handler not in the manifest.
func (c *Connector) Check() error {
	for name := range c.Manifest.Actions {
		if c.Actions[name] == nil {
			return fmt.Errorf("connector %s: no handler for action %q", c.Manifest.ID, name)
		}
	}
	for name := range c.Actions {
		if _, ok := c.Manifest.Actions[name]; !ok {
			return fmt.Errorf("connector %s: handler %q is not in the manifest", c.Manifest.ID, name)
		}
	}
	return nil
}

// Get returns the connector for "id@major".
func (r *Registry) Get(ref string) (*Connector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.m[ref]
	return c, ok
}

// List returns every registered connector, by ref.
func (r *Registry) List() []*Connector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Connector, 0, len(r.m))
	for _, c := range r.m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
	return out
}

// Pins returns "id@major" -> exact version for every registered connector.
func (r *Registry) Pins() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.m))
	keys := make([]string, 0, len(r.m))
	for k := range r.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = r.m[k].Manifest.Version
	}
	return out
}
