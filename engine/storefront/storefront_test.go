package storefront

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/media"
)

func TestCardCheck(t *testing.T) {
	good := Card{Name: " Ada's Kitchen ", Tagline: "Jollof, fried rice and small chops", Phone: "0803 123 4567", WhatsApp: "+2348031234567"}
	if err := good.Check(); err != nil {
		t.Fatal(err)
	}
	if good.Name != "Ada's Kitchen" || good.Phone != "+2348031234567" || good.Template != "classic" || good.Accent != "green" || good.Language != "en" {
		t.Fatalf("defaults and normalising: %+v", good)
	}
	for name, c := range map[string]Card{
		"no name":         {},
		"protected name":  {Name: "GTBank Customer Care"},
		"link in tagline": {Name: "Ada", Tagline: "order at https://evil.test"},
		"email in about":  {Name: "Ada", About: "write to ada@evil.test"},
		"long tagline":    {Name: "Ada", Tagline: strings.Repeat("a", 121)},
		"foreign phone":   {Name: "Ada", Phone: "+447700900123"},
		"bad template":    {Name: "Ada", Template: "fancy"},
		"bad accent":      {Name: "Ada", Accent: "#ff00ff"},
		"bad hours":       {Name: "Ada", Hours: Hours{Days: [7][]Span{{{Open: "8am", Close: "6pm"}}}}},
		"four openings":   {Name: "Ada", Hours: Hours{Days: [7][]Span{{{"08:00", "09:00"}, {"10:00", "11:00"}, {"12:00", "13:00"}, {"14:00", "15:00"}}}}},
	} {
		if err := c.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOpenNow(t *testing.T) {
	var h Hours
	if _, known := h.OpenNow(time.Now()); known {
		t.Fatal("unset hours are not known")
	}
	h.Days[0] = []Span{{"08:00", "18:00"}}   // Monday
	h.Days[4] = []Span{{"18:00", "02:00"}}   // Friday night into Saturday
	at := func(day, hhmm string) time.Time { // 2026-10-19 is a Monday
		d := map[string]string{"mon": "19", "fri": "23", "sat": "24", "sun": "25"}[day]
		tm, err := time.ParseInLocation("2006-01-02 15:04", "2026-10-"+d+" "+hhmm, Lagos)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	for _, c := range []struct {
		day, at string
		open    bool
	}{
		{"mon", "07:59", false}, {"mon", "08:00", true}, {"mon", "17:59", true}, {"mon", "18:00", false},
		{"fri", "17:00", false}, {"fri", "23:30", true}, {"sat", "01:30", true}, {"sat", "02:00", false}, {"sun", "12:00", false},
	} {
		open, known := h.OpenNow(at(c.day, c.at))
		if !known || open != c.open {
			t.Errorf("%s %s: open=%v known=%v", c.day, c.at, open, known)
		}
	}
	// The same instant seen from UTC is still Lagos time.
	if open, _ := h.OpenNow(at("mon", "08:30").UTC()); !open {
		t.Fatal("hours are in Lagos time")
	}
}

func TestColumnsCheck(t *testing.T) {
	types := map[string]string{"name": "text", "price": "number", "qty": "integer", "photo": "text", "ok": "boolean", "phone": "personal"}
	known := func(c string) (string, bool) { typ, ok := types[c]; return typ, ok }
	good := Columns{Name: "name", Price: "price", Photo: "photo", Available: "ok", Sort: "qty"}
	if err := good.Check(known); err != nil || good.PriceUnit != "naira" {
		t.Fatalf("%v %+v", err, good)
	}
	for name, m := range map[string]Columns{
		"no name":           {},
		"unknown column":    {Name: "nope"},
		"price is text":     {Name: "name", Price: "photo"},
		"available is text": {Name: "name", Available: "name"},
		"a personal column": {Name: "name", Description: "phone"},
		"bad unit":          {Name: "name", Price: "price", PriceUnit: "dollars"},
	} {
		if err := m.Check(known); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestFormatNaira(t *testing.T) {
	for k, want := range map[int64]string{0: "₦0", 150000: "₦1,500", 150050: "₦1,500.50", 123456789: "₦1,234,567.89", 99: "₦0.99"} {
		if got := FormatNaira(k); got != want {
			t.Errorf("%d: %q, want %q", k, got, want)
		}
	}
}

func TestItemsWithoutPhotos(t *testing.T) {
	rows := []map[string]any{
		{"id": float64(1), "name": "Jollof rice", "price": float64(1500), "cat": "Rice", "ok": true, "pos": float64(2)},
		{"id": float64(2), "name": "  ", "price": float64(100)}, // no name: left out
		{"id": float64(3), "name": "Small chops", "price": "2500.5", "cat": "Snacks", "ok": false, "pos": float64(1)},
		{"id": float64(4), "name": "Water"},
	}
	items, err := Items(context.Background(), nil, uuid.New(), rows, Columns{Name: "name", Price: "price", PriceUnit: "naira", Category: "cat", Available: "ok", Sort: "pos"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].Name != "Small chops" || items[1].Name != "Jollof rice" || items[2].Name != "Water" {
		t.Fatalf("order: %+v", items)
	}
	if *items[0].PriceKobo != 250050 || items[0].Available || *items[1].PriceKobo != 150000 || items[2].PriceKobo != nil || !items[2].Available {
		t.Fatalf("values: %+v", items)
	}
	if _, err := Items(context.Background(), nil, uuid.New(), make([]map[string]any, MaxItems+1), Columns{Name: "name"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("over the limit")
	}
}

func in(t *testing.T, d *dbtest.DB, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	t.Helper()
	return db.InTenantTx(context.Background(), d.App, []uuid.UUID{tenant}, fn)
}

func TestStorefrontInTheDatabase(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	t1, t2 := d.SeedTenant(t, nil).ID, d.SeedTenant(t, nil).ID
	card := Card{Name: "Ada's Kitchen", Tagline: "Home-cooked", Hours: Hours{Days: [7][]Span{{{"08:00", "18:00"}}}}}
	var sf Storefront
	if err := in(t, d, t1, func(tx pgx.Tx) error {
		var err error
		sf, err = Save(ctx, tx, t1, "Ada-Kitchen", card, nil, Columns{}, "user:ada")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if sf.Slug != "ada-kitchen" || sf.Status != "draft" || sf.PublishedVersion != 0 {
		t.Fatalf("%+v", sf)
	}
	if err := in(t, d, t2, func(tx pgx.Tx) error {
		_, err := Save(ctx, tx, t2, "ada-kitchen", card, nil, Columns{}, "user:bob")
		return err
	}); !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("another tenant took the slug: %v", err)
	}
	if err := in(t, d, t2, func(tx pgx.Tx) error {
		_, err := Save(ctx, tx, t2, "gtbank-help", card, nil, Columns{}, "user:bob")
		return err
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a protected slug: %v", err)
	}

	// A photo, a rejected photo, and the catalogue copy.
	img := media.Image{Hash: "abc", Width: 800, Height: 600, Type: "image/jpeg", URLs: map[int]string{320: "https://x/320.jpg", 800: "https://x/800.jpg"}, Paths: []string{"images/abc/800.jpg"}}
	bad := media.Image{Hash: "bad", Width: 10, Height: 10, Type: "image/jpeg", URLs: map[int]string{320: "https://x/bad.jpg"}, Paths: []string{"images/bad/320.jpg"}}
	var good, rejected Media
	if err := in(t, d, t1, func(tx pgx.Tx) error {
		var err error
		if good, err = AddMedia(ctx, tx, t1, img, "", "", "user:ada"); err != nil {
			return err
		}
		if again, err := AddMedia(ctx, tx, t1, img, "approved", "", "user:ada"); err != nil || again.ID != good.ID {
			t.Errorf("the same image twice: %v %v", again.ID, err)
		}
		rejected, err = AddMedia(ctx, tx, t1, bad, "rejected", "nudity", "user:ada")
		if err != nil {
			return err
		}
		// Once rejected, uploading it again cannot clear it.
		again, err := AddMedia(ctx, tx, t1, bad, "approved", "", "user:ada")
		if err == nil && again.Moderation != "rejected" {
			t.Error("a rejected image was cleared by uploading it again")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"id": float64(7), "name": "Jollof", "price": float64(1500), "photo": good.ID.String()},
		{"id": float64(8), "name": "Mystery", "photo": rejected.ID.String()},
		{"id": float64(9), "name": "Chops", "photo": uuid.NewString()},
	}
	if err := in(t, d, t1, func(tx pgx.Tx) error {
		items, err := Items(ctx, tx, t1, rows, Columns{Name: "name", Price: "price", Photo: "photo", PriceUnit: "naira"})
		if err != nil {
			return err
		}
		if items[0].Photo["800"] != "https://x/800.jpg" || items[1].Photo != nil || items[2].Photo != nil || len(items) != 3 {
			t.Errorf("photos: %+v", items)
		}
		return SyncCatalogue(ctx, tx, t1, items)
	}); err != nil {
		t.Fatal(err)
	}

	// Publishing needs a preview domain, then claims its host.
	pub := d.PublicPool(t, 1)
	if err := in(t, d, t1, func(tx pgx.Tx) error { _, err := Publish(ctx, tx, t1, "", "user:ada"); return err }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no preview domain: %v", err)
	}
	card.Logo = rejected.ID.String()
	if err := in(t, d, t1, func(tx pgx.Tx) error {
		if _, err := Save(ctx, tx, t1, "ada-kitchen", card, nil, Columns{}, "user:ada"); err != nil {
			return err
		}
		_, err := Publish(ctx, tx, t1, "shops.test", "user:ada")
		return err
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a rejected logo was published: %v", err)
	}
	card.Logo = good.ID.String()
	if err := in(t, d, t1, func(tx pgx.Tx) error {
		if _, err := Save(ctx, tx, t1, "ada-kitchen", card, nil, Columns{}, "user:ada"); err != nil {
			return err
		}
		var err error
		sf, err = Publish(ctx, tx, t1, "Shops.Test", "user:ada")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if sf.Status != "published" || sf.PublishedVersion != 1 {
		t.Fatalf("%+v", sf)
	}
	var kind string
	var site uuid.UUID
	if err := pub.QueryRow(ctx, `SELECT kind, site_id FROM taskiem_public_host('ada-kitchen.shops.test')`).Scan(&kind, &site); err != nil || site != sf.ID || kind != "storefront" {
		t.Fatalf("host: %s %s %v", kind, site, err)
	}
	var status string
	var model, catalogue []byte
	if err := pub.QueryRow(ctx, `SELECT status, model FROM taskiem_public_storefront($1)`, sf.ID).Scan(&status, &model); err != nil || status != "published" {
		t.Fatalf("%s %v", status, err)
	}
	var m Model
	_ = json.Unmarshal(model, &m)
	if m.Card.Name != "Ada's Kitchen" || m.Logo["320"] != "https://x/320.jpg" || m.Version != 1 {
		t.Fatalf("model %+v", m)
	}
	if err := pub.QueryRow(ctx, `SELECT taskiem_public_storefront_catalogue($1)`, sf.ID).Scan(&catalogue); err != nil || !strings.Contains(string(catalogue), "Jollof") {
		t.Fatalf("catalogue %s %v", catalogue, err)
	}

	// A new slug moves the preview host; the old one stops serving.
	if err := in(t, d, t1, func(tx pgx.Tx) error {
		if _, err := Save(ctx, tx, t1, "adas-kitchen", card, nil, Columns{}, "user:ada"); err != nil {
			return err
		}
		_, err := Publish(ctx, tx, t1, "shops.test", "user:ada")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pub.QueryRow(ctx, `SELECT count(*) FROM taskiem_public_host('ada-kitchen.shops.test')`).Scan(&n)
	if n != 0 {
		t.Fatal("the old preview host still serves")
	}
	_ = pub.QueryRow(ctx, `SELECT count(*) FROM taskiem_public_host('adas-kitchen.shops.test')`).Scan(&n)
	if n != 1 {
		t.Fatal("the new preview host does not serve")
	}

	// Closed: still served, with its message; a suspended tenant's is not.
	if err := in(t, d, t1, func(tx pgx.Tx) error { _, err := Close(ctx, tx, t1, "Back on Monday"); return err }); err != nil {
		t.Fatal(err)
	}
	var msg *string
	_ = pub.QueryRow(ctx, `SELECT status, closed_message FROM taskiem_public_storefront($1)`, sf.ID).Scan(&status, &msg)
	if status != "closed" || msg == nil || *msg != "Back on Monday" {
		t.Fatalf("closed: %s %v", status, msg)
	}
	if err := in(t, d, t1, func(tx pgx.Tx) error { _, err := Reopen(ctx, tx, t1); return err }); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET status = 'suspended' WHERE id = $1`, t1); err != nil {
		t.Fatal(err)
	}
	_ = pub.QueryRow(ctx, `SELECT count(*) FROM taskiem_public_storefront($1)`, sf.ID).Scan(&n)
	if n != 0 {
		t.Fatal("a suspended tenant's storefront is served")
	}

	// A draft cannot be closed, and another tenant cannot claim this host.
	if err := in(t, d, t2, func(tx pgx.Tx) error { _, err := Close(ctx, tx, t2, ""); return err }); !errors.Is(err, ErrNotPublished) {
		t.Fatalf("closing nothing: %v", err)
	}
	if err := in(t, d, t2, func(tx pgx.Tx) error {
		if _, err := Save(ctx, tx, t2, "bola-bakes", Card{Name: "Bola Bakes"}, nil, Columns{}, "user:bob"); err != nil {
			return err
		}
		var ok bool
		var other uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM storefronts`).Scan(&other); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT taskiem_storefront_claim_host($1, 'adas-kitchen.shops.test')`, other).Scan(&ok); err != nil {
			return err
		}
		if ok {
			t.Error("claimed another storefront's host")
		}
		// Nor claim a host for someone else's storefront (in a savepoint:
		// the refusal aborts it).
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := sp.Exec(ctx, `SELECT taskiem_storefront_claim_host($1, 'x.shops.test')`, sf.ID); err == nil {
			t.Error("claimed a host for another tenant's storefront")
		}
		return sp.Rollback(ctx)
	}); err != nil {
		t.Fatal(err)
	}
}
