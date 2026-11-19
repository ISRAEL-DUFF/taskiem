package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// countTx counts the queries a live connection's basket look makes; each
// answers no rows.
type countTx struct {
	pgx.Tx
	queries []string
}

type noRows struct{}

func (noRows) Close()                                       {}
func (noRows) Err() error                                   { return nil }
func (noRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (noRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (noRows) Next() bool                                   { return false }
func (noRows) Scan(...any) error                            { return nil }
func (noRows) Values() ([]any, error)                       { return nil, nil }
func (noRows) RawValues() [][]byte                          { return nil }
func (noRows) Conn() *pgx.Conn                              { return nil }
func (noRows) TypeMap() *pgtype.Map                         { return pgtype.NewMap() }

type nowRow struct{}

func (nowRow) Scan(dest ...any) error {
	*(dest[0].(*time.Time)) = time.Now()
	return nil
}

func (c *countTx) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	c.queries = append(c.queries, sql)
	return noRows{}, nil
}

func (c *countTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	c.queries = append(c.queries, sql)
	return nowRow{}
}

// A connection whose person has no placed basket asks once, when it
// starts, which placed baskets to follow, and never again every five
// seconds (review fix 7; the load test's 1,496 scans in 20 seconds).
func TestBasketsLiveNoPlacedNoFollow(t *testing.T) {
	bl := newBasketLive(time.Now())
	tx := &countTx{}
	for range 3 {
		if _, _, err := bl.idsTx(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
		bl.followAt = time.Time{} // as if five seconds went by
	}
	moved := 0
	for _, q := range tx.queries {
		if strings.Contains(q, "taskiem_baskets_moved") {
			moved++
		}
		if strings.Contains(q, "FROM people_baskets") {
			t.Fatalf("read every basket: %s", q)
		}
	}
	if moved != 1 {
		t.Fatalf("looked for moved orders %d times, want once: %v", moved, tx.queries)
	}
	// A placed basket sent on it: followed from then on.
	bl.placed[uuid.New()] = struct{}{}
	if _, _, err := bl.idsTx(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tx.queries[len(tx.queries)-1], "taskiem_baskets_moved") {
		t.Fatalf("a placed basket is not followed: %v", tx.queries)
	}
}

// The placer with no items of their own has a share, carrying the kobo of
// the delivery left over and any difference the order came to (fix 4).
func TestBasketSharesPlacerWithNoItems(t *testing.T) {
	placer, a, b := uuid.New(), uuid.New(), uuid.New()
	v := basketOut{DeliveryKobo: 100001, People: []basketPersonOut{{PersonID: a, ItemsKobo: 1000}, {PersonID: b, ItemsKobo: 2000}}}
	out := basketShares(v, placer, 3000+100001+50)
	if len(out) != 3 || out[2].Person != placer {
		t.Fatalf("shares: %+v", out)
	}
	if out[0].Total != 1000+50000 || out[1].Total != 2000+50000 || out[2].Delivery != 1 || out[2].Total != 1+50 {
		t.Fatalf("split: %+v", out)
	}
}
