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

	"github.com/israel-duff/taskiem/engine/effects"
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
	// Rotate replaces credentials a provider rotates (QuickBooks issues a
	// new refresh token on every refresh and revokes the chain when an old
	// one is used again): see RotateCredentials. Nil where the request's
	// credentials are not a stored connection's.
	Rotate func(ctx context.Context, fn RotateFunc) (map[string]string, error)
}

// RotateFunc refreshes credentials. It is given the connection's stored
// credentials as they are now, read under a lock no other worker can hold
// at the same time, and returns them as they should be stored: the same
// map when another worker already refreshed them (its access token is
// still good), or the fields the provider handed back merged in. An error
// stores nothing; wrap ErrCredentialsRejected when the provider refused
// the refresh token, so the connection is marked expired.
type RotateFunc func(current map[string]string) (map[string]string, error)

// ErrNoRotate is a refresh asked for where the credentials cannot be
// stored back: refreshing anyway would spend a refresh token for good.
var ErrNoRotate = errors.New("these credentials cannot be refreshed and stored here")

// RotateCredentials refreshes the request's connection's credentials once
// across every worker: fn runs with the stored credentials under the
// connection's lock, and what it returns is stored before the lock is let
// go, so the next worker sees the new refresh token, never the spent one.
// It returns the credentials to use, which the caller keeps for the call.
func (r Request) RotateCredentials(ctx context.Context, fn RotateFunc) (map[string]string, error) {
	if r.Rotate == nil {
		return nil, fmt.Errorf("%w: %w", ErrNoRotate, effects.ErrFatal)
	}
	return r.Rotate(ctx, fn)
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

type manifestsOnlyKey struct{}

// ManifestsOnly marks ctx for a caller that reads connectors' manifests and
// runs none (checking a definition): a source may skip what only running
// needs, such as compiling a WebAssembly module, which takes seconds for a
// large one.
func ManifestsOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, manifestsOnlyKey{}, true)
}

// IsManifestsOnly reports whether ctx is ManifestsOnly's.
func IsManifestsOnly(ctx context.Context) bool {
	v, _ := ctx.Value(manifestsOnlyKey{}).(bool)
	return v
}

// ActionFunc adapts a function to Action.
type ActionFunc func(ctx context.Context, req Request) (Response, error)

func (f ActionFunc) Execute(ctx context.Context, req Request) (Response, error) { return f(ctx, req) }

// ErrNotFound is returned by a reconcile action when the provider has no
// record of the effect, so it is safe to send it again.
var ErrNotFound = errors.New("not_found")

// ErrCredentialsRejected is wrapped by a connector's error when the
// provider refused the connection's credentials as revoked or expired (a
// 401 that names the token, not a missing header). The engine marks an
// OAuth connection expired on it (decision 0034); the error stays fatal
// for the step.
var ErrCredentialsRejected = errors.New("credentials_rejected")

// Connector is a manifest plus its action handlers.
type Connector struct {
	Manifest *Manifest
	Actions  map[string]Action
	// Verifiers check webhooks for triggers whose verify scheme is
	// "connector": providers that sign something no manifest scheme
	// describes (fields inside the body, for example). Keyed by trigger.
	Verifiers map[string]WebhookVerifier
	// Registrars set up the provider's subscriptions for triggers whose
	// manifest says registration: remote (decision 0021). Keyed by trigger.
	Registrars map[string]Registrar
	// Enrichers complete a verified delivery before its runs start (a
	// record the provider left out). Keyed by trigger; optional.
	Enrichers map[string]Enricher
	// CheckCredentials, when set, refuses a connection's credentials
	// before they are saved (a combination of fields the connector cannot
	// use safely). Its error is shown to whoever saves them.
	CheckCredentials func(creds map[string]string) error
	// Link completes a connection made by OAuth before it is saved, for
	// providers whose grant is not yet the account (Meta: the user's token
	// becomes the one Page's own, and the Page is subscribed to Taskiem's
	// app). It fills the manifest's auth.oauth2.linked_fields (rule 21);
	// an error wrapping ErrLinkRefused is shown to the person as it is.
	Link func(ctx context.Context, c LinkCall) (map[string]string, error)
}

// LinkCall is what Link gets: the fields made so far, the token the
// provider granted, Taskiem's client, and an egress-guarded client for
// the connector's hosts.
type LinkCall struct {
	Credentials  map[string]string
	Token        string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
}

// ErrLinkRefused is a grant that makes no connection, in the person's
// words ("choose exactly one Page").
var ErrLinkRefused = errors.New("link refused")

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
	for name, t := range c.Manifest.Triggers {
		if t.Remote() && c.Registrars[name] == nil {
			return fmt.Errorf("connector %s: trigger %q is registered remotely but has no registrar", c.Manifest.ID, name)
		}
	}
	for name := range c.Registrars {
		if !c.Manifest.Triggers[name].Remote() {
			return fmt.Errorf("connector %s: registrar %q is not a remote trigger in the manifest", c.Manifest.ID, name)
		}
	}
	for name := range c.Enrichers {
		if _, ok := c.Manifest.Triggers[name]; !ok {
			return fmt.Errorf("connector %s: enricher %q is not a trigger in the manifest", c.Manifest.ID, name)
		}
	}
	if o := c.Manifest.Auth.OAuth2; o != nil && (len(o.LinkedFields) > 0) != (c.Link != nil) {
		return fmt.Errorf("connector %s: auth.oauth2.linked_fields and a Link handler go together", c.Manifest.ID)
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
