package storefront_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/publicweb"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// PR review fix 9: a front listing the catalogue's own table shows its
// products sold out, as the home page does (the public page, through
// taskiem_product_left), and an order from it is checked against the
// counts (countedFrom); another table's rows are no products.
func TestFrontListingTheCatalogueShowsSoldOut(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	newShelf(w, map[string]int64{"1": 0, "2": 3})
	table := uuid.New()
	w.in(func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO data_tables (id, tenant_id, name, created_by) VALUES ($1, $2, 'products', 'test')`, table, w.env.Tenant); err != nil {
			return err
		}
		if _, err := storefront.Save(ctx, tx, w.env.Tenant, "ada-kitchen", storefront.Card{Name: "Ada Kitchen"}, &table, storefront.Columns{Name: "name"}, "user:ada"); err != nil {
			return err
		}
		if err := storefront.SyncCatalogue(ctx, tx, w.env.Tenant, []storefront.Item{{ID: "1", Name: "Zobo", Available: true}, {ID: "2", Name: "Malt", Available: true}}); err != nil {
			return err
		}
		f, err := storefront.CreateFront(ctx, tx, w.env.Tenant, storefront.NewFront{Name: "Market", Kind: "shop", Table: &table, TableName: "products"})
		if err != nil {
			return err
		}
		on := true
		if _, err := storefront.UpdateFront(ctx, tx, w.env.Tenant, f.ID, storefront.FrontPatch{Enabled: &on}); err != nil {
			return err
		}
		w.sf, err = storefront.Publish(ctx, tx, w.env.Tenant, "shops.test", "user:ada", false)
		return err
	})
	p, ok, err := publicweb.DBStorefronts(w.env.DB.PublicPool(t, 1))(ctx, w.env.Tenant, w.sf.ID)
	if err != nil || !ok {
		t.Fatalf("the public page: %v %v", ok, err)
	}
	list := p.Lists["products"]
	left := map[string]*int64{}
	for _, it := range list {
		left[it.ID] = it.Stock
	}
	if len(list) != 2 || left["1"] == nil || *left["1"] != 0 || left["2"] == nil || *left["2"] != 3 {
		t.Fatalf("the market front's list: %+v", list)
	}

	other := uuid.New()
	for _, c := range []struct {
		from storefront.Source
		want bool
	}{
		{storefront.Source{}, true},
		{storefront.Source{Table: &table}, true},
		{storefront.Source{Table: &other}, false},
		{storefront.Source{Services: true}, false},
	} {
		if got := storefront.CountedFrom(c.from, &table); got != c.want {
			t.Errorf("counted from %+v: %v", c.from, got)
		}
	}
}
