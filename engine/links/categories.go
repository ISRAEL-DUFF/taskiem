package links

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Categories a business picks (one or two) to be found by in Explore
// (docs/spec/mobile-app-explore-and-offline.md §2.1, migration 00427). The
// keys are the database's; the names are what people read.
type Category struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// N is how many businesses we may find in it (Explore's count).
	N int `json:"n"`
}

// MaxCategories is how many a business picks at most.
const MaxCategories = 2

// Categories is the fixed list, in the order Explore shows it.
var Categories = []Category{
	{Key: "food", Name: "Food and drinks"},
	{Key: "building", Name: "Building materials"},
	{Key: "fashion", Name: "Fashion"},
	{Key: "phones", Name: "Phones and gadgets"},
	{Key: "electrical", Name: "Electrical and electronics"},
	{Key: "beauty", Name: "Beauty and personal care"},
	{Key: "farm", Name: "Farm produce"},
	{Key: "health", Name: "Health and pharmacy"},
	{Key: "home", Name: "Home and furniture"},
	{Key: "auto", Name: "Cars and spare parts"},
	{Key: "logistics", Name: "Delivery and logistics"},
	{Key: "wholesale", Name: "Wholesale and distribution"},
	{Key: "services", Name: "Services"},
}

// IsCategory says whether key is one of the list.
func IsCategory(key string) bool {
	return slices.ContainsFunc(Categories, func(c Category) bool { return c.Key == key })
}

// cleanCategories keeps the known keys, once each, at most two.
func cleanCategories(in []string) ([]string, error) {
	out := []string{}
	for _, k := range in {
		if !IsCategory(k) {
			return nil, fmt.Errorf("%w: %q is not a category", ErrInvalid, k)
		}
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	if len(out) > MaxCategories {
		return nil, fmt.Errorf("%w: pick at most %d categories", ErrInvalid, MaxCategories)
	}
	return out, nil
}

// ExploreCategoriesTx is the list with each category's count.
func ExploreCategoriesTx(ctx context.Context, tx pgx.Tx) ([]Category, error) {
	rows, err := tx.Query(ctx, `SELECT category, n FROM taskiem_link_explore_categories()`)
	if err != nil {
		return nil, pgErr(err)
	}
	counts := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return nil, pgErr(err)
		}
		counts[k] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, pgErr(err)
	}
	out := make([]Category, len(Categories))
	for i, c := range Categories {
		c.N = counts[c.Key]
		out[i] = c
	}
	return out, nil
}

// exploreCategoriesOfTx fills in each card's categories, in one query.
func exploreCategoriesOfTx(ctx context.Context, tx pgx.Tx, cards []ExploreCard) error {
	ids := make([]uuid.UUID, 0, len(cards))
	for i, c := range cards {
		if cards[i].Categories == nil {
			cards[i].Categories = []string{}
		}
		ids = append(ids, c.Business)
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT business, categories FROM taskiem_link_categories_of($1)`, ids)
	if err != nil {
		return pgErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var b uuid.UUID
		var cats []string
		if err := rows.Scan(&b, &cats); err != nil {
			return pgErr(err)
		}
		for i := range cards {
			if cards[i].Business == b {
				cards[i].Categories = cats
			}
		}
	}
	return pgErr(rows.Err())
}
