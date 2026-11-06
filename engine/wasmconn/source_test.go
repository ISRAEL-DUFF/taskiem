package wasmconn

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// TestLoadedConnectorsAreBounded: every version a tenant uploads is
// loaded once listed or pinned, and each keeps its module's bytes, so the
// source keeps at most MaxLoaded of them, least recently used out first.
// An evicted version loads again from the database when next asked for.
func TestLoadedConnectorsAreBounded(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	ctx := context.Background()
	src := &Source{Pool: d.App, Runtime: shared, MaxLoaded: 3}
	upload := func(version string, n int) {
		t.Helper()
		manifest := strings.Replace(tinyManifest, "version: 1.0.0", "version: "+version, 1)
		module := tinyModule(n)
		sum := sha256.Sum256(module)
		err := db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO tenant_connectors (tenant_id, connector_id, version, manifest, module, digest, uploaded_by)
				VALUES ($1, 'x_tiny', $2, $3, $4, $5, 'test')`, tn.ID, version, manifest, module, sum[:])
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range 6 {
		upload(fmt.Sprintf("1.0.%d", i), 1000+i)
		src.ForgetAll()
		list, err := src.Connectors(ctx, tn.ID.String())
		if err != nil || len(list) != 1 || list[0].Manifest.Version != fmt.Sprintf("1.0.%d", i) {
			t.Fatalf("after 1.0.%d: %v %v", i, list, err)
		}
		if n := src.loadedLen(); n > 3 {
			t.Fatalf("after 1.0.%d: %d connectors loaded", i, n)
		}
	}
	c, err := src.Version(ctx, tn.ID.String(), "x_tiny", "1.0.0")
	if err != nil || c == nil || c.Manifest.Version != "1.0.0" {
		t.Fatalf("an evicted version: %v %v", c, err)
	}
	if out, err := runTiny(c, &gateTransport{entered: make(chan struct{}, 1), release: closed()}); err != nil || fmt.Sprint(out) != "map[n:1000]" {
		t.Errorf("the reloaded version runs: %v %v", out, err)
	}
}

func closed() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

func (s *Source) loadedLen() int { return s.cache().Len() }
