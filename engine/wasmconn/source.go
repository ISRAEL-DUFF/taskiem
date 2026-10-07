package wasmconn

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
)

// Source loads tenants' connectors from the database: for each id and
// major version, the newest enabled version, as the registry does for
// built-ins. It is a connector.TenantSource.
type Source struct {
	// Pool is the source's own (a few connections): lookups happen inside
	// callers' transactions, and waiting on the pool those hold would
	// deadlock once it is exhausted.
	Pool    *pgxpool.Pool
	Runtime *Runtime
	TTL     time.Duration // how long a tenant's list is reused; default 10s
	Logger  *slog.Logger
	// BaseURLs override manifests' base_url by connector id (sandboxes,
	// tests), as builtin.Options does for built-ins.
	BaseURLs map[string]string

	mu      sync.Mutex
	tenants map[string]cached
	loaded  map[string]*connector.Connector // by tenant, id and version
}

type cached struct {
	at   time.Time
	list []*connector.Connector
}

// Connectors implements connector.TenantSource.
func (s *Source) Connectors(ctx context.Context, tenant string) ([]*connector.Connector, error) {
	ttl := s.TTL
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	s.mu.Lock()
	if c, ok := s.tenants[tenant]; ok && time.Since(c.at) < ttl {
		s.mu.Unlock()
		return c.list, nil
	}
	s.mu.Unlock()
	tid, err := uuid.Parse(tenant)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, version string
		partner     uuid.UUID // set for a connector the tenant's partner shares
	}
	newest := map[string]row{} // by id@major
	var installed []row
	collect := func(rows pgx.Rows, shared bool) error {
		defer rows.Close()
		for rows.Next() {
			var r row
			var err error
			if shared {
				err = rows.Scan(&r.partner, &r.id, &r.version)
			} else {
				err = rows.Scan(&r.id, &r.version)
			}
			if err != nil {
				return err
			}
			ref := r.id + "@" + major(r.version)
			prev, ok := newest[ref]
			// The partner's shared connector wins over a sub-tenant's own of
			// the same id: end users get what the partner provides.
			switch {
			case !ok, shared && prev.partner == uuid.Nil, (shared == (prev.partner != uuid.Nil)) && Newer(r.version, prev.version):
				newest[ref] = r
			}
		}
		return rows.Err()
	}
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tid}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT connector_id, version FROM tenant_connectors WHERE disabled_at IS NULL`)
		if err != nil {
			return err
		}
		if err := collect(rows, false); err != nil {
			return err
		}
		// A sub-tenant's partner's connectors shared with it (the partner
		// connector bridge, docs/embedding.md): read-only, through a
		// function that checks the parent; none for other tenants.
		if rows, err = tx.Query(ctx, `SELECT partner_id, connector_id, version FROM taskiem_shared_connectors($1)`, tid); err != nil {
			return err
		}
		if err := collect(rows, true); err != nil {
			return err
		}
		// Catalogue connectors the tenant installed (p_ ids, one pinned
		// version per major): published and from a verified publisher
		// only, so a revoked version or a suspended publisher stops here.
		rows, err = tx.Query(ctx, `SELECT connector_id, version FROM taskiem_catalogue_installed($1)`, tid)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, version string
			if err := rows.Scan(&id, &version); err != nil {
				return err
			}
			installed = append(installed, row{id: id, version: version})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	var list []*connector.Connector
	for _, r := range installed {
		c, err := s.loadInstalled(ctx, tid, r.id, r.version)
		if err != nil {
			if s.Logger != nil {
				s.Logger.Error("catalogue connector does not load", "tenant", tenant, "connector", r.id, "version", r.version, "err", err)
			}
			continue
		}
		list = append(list, c)
	}
	for _, r := range newest {
		var c *connector.Connector
		var err error
		if r.partner != uuid.Nil {
			c, err = s.loadShared(ctx, tid, r.partner, r.id, r.version)
		} else {
			c, err = s.load(ctx, tid, r.id, r.version)
		}
		if err != nil {
			// Checked when uploaded; failing now means the deployment's
			// limits changed. The tenant's other connectors still work.
			if s.Logger != nil {
				s.Logger.Error("tenant connector does not load", "tenant", tenant, "connector", r.id, "version", r.version, "err", err)
			}
			continue
		}
		list = append(list, c)
	}
	s.mu.Lock()
	if s.tenants == nil {
		s.tenants = map[string]cached{}
	}
	s.tenants[tenant] = cached{at: time.Now(), list: list}
	s.mu.Unlock()
	return list, nil
}

func (s *Source) load(ctx context.Context, tenant uuid.UUID, id, version string) (*connector.Connector, error) {
	key := tenant.String() + "/" + id + "@" + version
	s.mu.Lock()
	c, ok := s.loaded[key]
	s.mu.Unlock()
	if ok {
		return c, nil
	}
	var manifest string
	var module []byte
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT manifest, module FROM tenant_connectors WHERE connector_id = $1 AND version = $2`, id, version).Scan(&manifest, &module)
	})
	if err != nil {
		return nil, err
	}
	c, err = s.Runtime.Load(ctx, []byte(manifest), module)
	if err != nil {
		return nil, err
	}
	if c.Manifest.ID != id || c.Manifest.Version != version {
		return nil, fmt.Errorf("stored as %s %s but the manifest says %s %s", id, version, c.Manifest.ID, c.Manifest.Version)
	}
	c.Manifest.OverrideBaseURL(s.BaseURLs[id])
	s.mu.Lock()
	if s.loaded == nil {
		s.loaded = map[string]*connector.Connector{}
	}
	s.loaded[key] = c
	s.mu.Unlock()
	return c, nil
}

// loadShared loads a version of a connector tenant's partner shares with
// it. The module is the partner's, cached once under the partner's key for
// all its sub-tenants; reading it goes through the sub-tenant's scope.
func (s *Source) loadShared(ctx context.Context, tenant, partner uuid.UUID, id, version string) (*connector.Connector, error) {
	key := partner.String() + "/" + id + "@" + version
	s.mu.Lock()
	c, ok := s.loaded[key]
	s.mu.Unlock()
	if ok {
		return c, nil
	}
	var manifest string
	var module []byte
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT manifest, module FROM taskiem_shared_connector_module($1, $2, $3)`, tenant, id, version).Scan(&manifest, &module)
	})
	if err != nil {
		return nil, err
	}
	c, err = s.Runtime.Load(ctx, []byte(manifest), module)
	if err != nil {
		return nil, err
	}
	if c.Manifest.ID != id || c.Manifest.Version != version {
		return nil, fmt.Errorf("stored as %s %s but the manifest says %s %s", id, version, c.Manifest.ID, c.Manifest.Version)
	}
	c.Manifest.OverrideBaseURL(s.BaseURLs[id])
	s.mu.Lock()
	if s.loaded == nil {
		s.loaded = map[string]*connector.Connector{}
	}
	s.loaded[key] = c
	s.mu.Unlock()
	return c, nil
}

// loadInstalled loads a catalogue version the tenant installed. The
// module is the catalogue's, the same bytes for every tenant, so it is
// cached once by id and version (and digest, which never changes for a
// version); reading it goes through the tenant's scope.
func (s *Source) loadInstalled(ctx context.Context, tenant uuid.UUID, id, version string) (*connector.Connector, error) {
	key := "catalogue/" + id + "@" + version
	s.mu.Lock()
	c, ok := s.loaded[key]
	s.mu.Unlock()
	if ok {
		return c, nil
	}
	var manifest string
	var module, digest []byte
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT manifest, module, module_digest FROM taskiem_catalogue_module($1, $2, $3)`, tenant, id, version).Scan(&manifest, &module, &digest)
	})
	if err != nil {
		return nil, err
	}
	if Digest(module) != fmt.Sprintf("%x", digest) {
		return nil, fmt.Errorf("the module stored for %s %s does not match its digest", id, version)
	}
	c, err = s.Runtime.LoadPublished(ctx, []byte(manifest), module)
	if err != nil {
		return nil, err
	}
	if c.Manifest.ID != id || c.Manifest.Version != version {
		return nil, fmt.Errorf("stored as %s %s but the manifest says %s %s", id, version, c.Manifest.ID, c.Manifest.Version)
	}
	c.Manifest.OverrideBaseURL(s.BaseURLs[id])
	s.mu.Lock()
	if s.loaded == nil {
		s.loaded = map[string]*connector.Connector{}
	}
	s.loaded[key] = c
	s.mu.Unlock()
	return c, nil
}

// Forget drops a tenant's cached list, after an upload or a disable.
func (s *Source) Forget(tenant string) {
	s.mu.Lock()
	delete(s.tenants, tenant)
	s.mu.Unlock()
}

// ForgetAll drops every tenant's cached list: a partner's upload, disable
// or share changes what its sub-tenants see.
func (s *Source) ForgetAll() {
	s.mu.Lock()
	s.tenants = nil
	s.mu.Unlock()
}

func major(v string) string {
	m, _, _ := strings.Cut(v, ".")
	return m
}

// Newer reports whether semantic version a is after b.
func Newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3 && i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x > y
		}
	}
	return false
}
