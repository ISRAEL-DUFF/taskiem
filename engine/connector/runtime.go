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
)

// Request is what an action handler receives (spec 6.3).
type Request struct {
	Input          map[string]any
	Credentials    map[string]string // decrypted for this call only
	IdempotencyKey string            // also placed in the manifest's idempotency field
	Attempt        int
	Logger         *slog.Logger
	HTTP           *http.Client
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
}

// Ref is the "id@major" a workflow pins.
func (c *Connector) Ref() string {
	major, _, _ := strings.Cut(c.Manifest.Version, ".")
	return c.Manifest.ID + "@" + major
}

// Registry holds the connectors compiled into this binary.
type Registry struct {
	mu sync.RWMutex
	m  map[string]*Connector
}

func NewRegistry() *Registry { return &Registry{m: map[string]*Connector{}} }

// Register checks that every manifest action has a handler, then adds the
// connector. A newer minor version of the same major replaces an older one.
func (r *Registry) Register(c *Connector) error {
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[c.Ref()] = c
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
