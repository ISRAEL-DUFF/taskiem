package publicweb_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/publicweb"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// TestPublicSubmit: taskiem_public_submit takes a submission only for a
// published storefront's enabled form, and a repeated idempotency key
// answers with the first reference.
func TestPublicSubmit(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	tn := d.SeedTenant(t, nil)
	sf := uuid.New()
	if _, err := d.Admin.Exec(ctx, `INSERT INTO storefronts (id, tenant_id, slug, status, created_by) VALUES ($1, $2, 'ada-kitchen', 'draft', 'user:ada')`, sf, tn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO storefront_actions (storefront_id, kind, tenant_id, enabled, label) VALUES
		($1, 'order', $2, true, 'Order now'), ($1, 'enquiry', $2, false, 'Ask a question')`, sf, tn.ID); err != nil {
		t.Fatal(err)
	}
	pool := d.PublicPool(t, 2)
	submit, status := publicweb.DBSubmit(pool), publicweb.DBOrderStatus(pool)
	idem := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }
	hash := storefront.StatusHash("the key")
	payload := []byte("sealed")

	if _, ok, err := submit(ctx, sf, "order", "AD-AAAAAA", idem(1), hash, payload); err != nil || ok {
		t.Fatalf("a draft storefront took an order: %v %v", ok, err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE storefronts SET status = 'published' WHERE id = $1`, sf); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := submit(ctx, sf, "enquiry", "AD-AAAAAB", idem(2), hash, payload); err != nil || ok {
		t.Fatalf("a disabled form took a submission: %v %v", ok, err)
	}
	if _, ok, err := submit(ctx, uuid.New(), "order", "AD-AAAAAC", idem(3), hash, payload); err != nil || ok {
		t.Fatalf("an unknown storefront took an order: %v %v", ok, err)
	}
	ref, ok, err := submit(ctx, sf, "order", "AD-AAAAAD", idem(4), hash, payload)
	if err != nil || !ok || ref != "AD-AAAAAD" {
		t.Fatalf("an enabled form: %q %v %v", ref, ok, err)
	}
	ref, ok, err = submit(ctx, sf, "order", "AD-AAAAAE", idem(4), hash, payload)
	if err != nil || !ok || ref != "AD-AAAAAD" {
		t.Fatalf("a repeat must answer with the first reference: %q %v %v", ref, ok, err)
	}
	if _, _, err := submit(ctx, sf, "order", "AD-AAAAAD", idem(5), hash, payload); !errors.Is(err, publicweb.ErrReferenceTaken) {
		t.Fatalf("a taken reference: %v", err)
	}
	var n int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM storefront_inbox WHERE storefront_id = $1`, sf).Scan(&n); err != nil || n != 1 {
		t.Fatalf("inbox rows %d %v", n, err)
	}

	// The status page's read: by the reference and its key's hash only.
	o, ok, err := status(ctx, "AD-AAAAAD", hash)
	if err != nil || !ok || o.Status != "processing" || o.Kind != "order" || o.Storefront != sf {
		t.Fatalf("status: %+v %v %v", o, ok, err)
	}
	if _, ok, err := status(ctx, "AD-AAAAAD", storefront.StatusHash("another key")); err != nil || ok {
		t.Fatalf("a wrong key: %v %v", ok, err)
	}
}
