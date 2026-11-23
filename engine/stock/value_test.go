package stock_test

import (
	"math"
	"testing"

	"github.com/israel-duff/taskiem/engine/stock"
)

// PR review fix: stock value is held at the largest int64, never wrapped
// round to a negative, when counts at costs (each up to a trillion) pass
// it, alone or added together.
func TestStockValueDoesNotOverflow(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	big := []stock.Item{
		{StockItemID: "a", Stock: n(10_000_000_000), CostKobo: n(1_000_000_000_000)},
		{StockItemID: "b", Stock: n(3), CostKobo: n(100)},
	}
	if v := stock.ValueKobo(big); v != math.MaxInt64 {
		t.Fatalf("one past int64: %d", v)
	}
	two := []stock.Item{
		{StockItemID: "a", Stock: n(5_000_000), CostKobo: n(1_000_000_000_000)},
		{StockItemID: "b", Stock: n(5_000_000), CostKobo: n(1_000_000_000_000)},
	}
	if v := stock.ValueKobo(two); v != math.MaxInt64 {
		t.Fatalf("two adding past int64: %d", v)
	}
	if v := stock.ValueKobo(big[1:]); v != 300 {
		t.Fatalf("a small value: %d", v)
	}
}
