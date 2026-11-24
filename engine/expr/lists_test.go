package expr

import (
	"reflect"
	"strings"
	"testing"
)

var le = MustNewWithRoots("sales", "customers", "orders", "big", "same")

func listData() map[string]any {
	d, _ := DecodeJSON([]byte(`{
	  "sales": [
	    {"area":"Yaba","amount":5000,"name":"Ada","phone":"0801"},
	    {"area":"Ikeja","amount":3000,"name":"Bola","phone":"0802"},
	    {"area":"Yaba","amount":2500.5,"name":"Chidi","phone":"0801"},
	    {"amount":100,"name":"Dayo"}
	  ],
	  "customers": [{"id":1,"name":"Ada"},{"id":2,"name":"Bola"},{"id":"3","name":"Chidi"},{"name":"No id"}],
	  "orders": [{"cid":1,"total":10},{"cid":1.0,"total":20},{"cid":2,"total":30},{"cid":3,"total":40},{"total":50}]
	}`))
	return d.(map[string]any)
}

func TestListBlocks(t *testing.T) {
	cases := map[string]any{
		// totals by area: group, then add each group up
		"=groupBy(sales, 'area').map(g, {'area': g.key, 'n': g.count, 'total': sum(g.items.map(s, s.amount))})": []any{
			map[string]any{"area": "Yaba", "n": int64(2), "total": 7500.5},
			map[string]any{"area": "Ikeja", "n": int64(1), "total": int64(3000)},
			map[string]any{"area": nil, "n": int64(1), "total": int64(100)},
		},
		"=size(groupBy([], 'area'))":                                                                int64(0),
		"=uniqueBy(sales, 'phone').map(s, s.name)":                                                  []any{"Ada", "Bola", "Dayo"},
		"=joinBy(customers, orders, 'id', 'cid').size()":                                            int64(4), // Ada twice (1 and 1.0), Bola, and the Chidi whose id is the text "3" does not match the number 3
		"=joinBy(customers, orders, 'id', 'cid').map(p, p.left.name + ':' + string(p.right.total))": []any{"Ada:10", "Ada:20", "Bola:30"},
		"=leftJoinBy(customers, orders, 'id', 'cid').map(p, p.left.name + ':' + (p.right == null ? 'none' : string(p.right.total)))": []any{"Ada:10", "Ada:20", "Bola:30", "Chidi:none", "No id:none"},
		"=leftJoinBy(customers, [], 'id', 'cid').size()": int64(4),
		"=joinBy([], orders, 'id', 'cid')":               []any{},
	}
	// the text "3" does not match the number 3: four pairs would be wrong
	cases["=joinBy(customers, orders, 'id', 'cid').size()"] = int64(3)
	for src, want := range cases {
		got, err := le.Eval(src, listData())
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s\n got  %#v\n want %#v", src, got, want)
		}
	}
}

func TestListBlockErrors(t *testing.T) {
	big := make([]any, MaxListBlock+1)
	for i := range big {
		big[i] = map[string]any{"k": int64(i)}
	}
	same := make([]any, 101)
	for i := range same {
		same[i] = map[string]any{"k": int64(1)}
	}
	for src, vars := range map[string]map[string]any{
		"=groupBy(5, 'a')":              nil,
		"=groupBy([1, 2], 'a').size()":  nil, // rows that are not maps group under null: not an error
		"=groupBy(big, 'k')":            {"big": big},
		"=uniqueBy(big, 'k')":           {"big": big},
		"=joinBy(same, same, 'k', 'k')": {"same": same, "big": big}, // 101 x 101 pairs is more than the cap
	} {
		_, err := le.Eval(src, vars)
		wantErr := !strings.Contains(src, "[1, 2]")
		if wantErr && err == nil {
			t.Errorf("%s: expected an error", src)
		}
		if !wantErr && err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
}

// The starters the builder offers (web/src/lib/listblocks.ts) work as they
// stand on a list in the step's input.
func TestBuilderStarters(t *testing.T) {
	body, _ := DecodeJSON([]byte(`{"rows":[{"area":"Yaba","amount":5},{"area":"Yaba","amount":7},{"area":"Ikeja","amount":3}],
	  "customers":[{"id":1,"name":"Ada"},{"id":2,"name":"Bola"}],"orders":[{"customer_id":1,"n":"A"}]}`))
	vars := map[string]any{"trigger": map[string]any{"body": body}}
	for src, want := range map[string]any{
		"=size(trigger.body.rows)":                 int64(3),
		"=sum(trigger.body.rows.map(r, r.amount))": int64(15),
		"=groupBy(trigger.body.rows, 'area').map(g, {'name': g.key, 'count': g.count, 'total': sum(g.items.map(r, r.amount))})": []any{
			map[string]any{"name": "Yaba", "count": int64(2), "total": int64(12)},
			map[string]any{"name": "Ikeja", "count": int64(1), "total": int64(3)},
		},
		"=leftJoinBy(trigger.body.customers, trigger.body.orders, 'id', 'customer_id').map(p, p.left.name)": []any{"Ada", "Bola"},
	} {
		got, err := e.Eval(src, vars)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, %v; want %#v", src, got, err, want)
		}
	}
}
