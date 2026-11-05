package expr

import (
	"math"
	"strconv"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

// The list blocks. CEL has map, filter and exists but no way to group a
// list or to match two lists on a key, and a workflow that totals sales by
// area or pairs each customer with their latest order needs both. They are
// pure functions of their arguments, so a run's history still replays to
// the same decisions.
//
//	groupBy(rows, "area")                    [{key, count, items}], in order of first appearance
//	uniqueBy(rows, "phone")                  the first row for each phone
//	joinBy(customers, orders, "id", "cid")   [{left, right}] for every matching pair
//	leftJoinBy(customers, orders, "id", "cid")  every left row; right is null when nothing matches
//
// A key matches another when both are the same text, or the same number
// (1 and 1.0 match; "1" and 1 do not). A missing or null key never matches
// in a join, and is one group of its own in groupBy and uniqueBy.

// MaxListBlock bounds the lists the blocks take and the rows a join makes,
// so one expression cannot build an unbounded result.
const MaxListBlock = 10_000

var listBlocks = []cel.EnvOption{
	cel.Function("groupBy", cel.Overload("groupBy_list_string", []*cel.Type{cel.ListType(cel.DynType), cel.StringType}, cel.DynType,
		cel.BinaryBinding(groupBy))),
	cel.Function("uniqueBy", cel.Overload("uniqueBy_list_string", []*cel.Type{cel.ListType(cel.DynType), cel.StringType}, cel.DynType,
		cel.BinaryBinding(uniqueBy))),
	cel.Function("joinBy", cel.Overload("joinBy_list_list_string_string", []*cel.Type{cel.ListType(cel.DynType), cel.ListType(cel.DynType), cel.StringType, cel.StringType}, cel.DynType,
		cel.FunctionBinding(func(a ...ref.Val) ref.Val { return joinBy("joinBy", a, false) }))),
	cel.Function("leftJoinBy", cel.Overload("leftJoinBy_list_list_string_string", []*cel.Type{cel.ListType(cel.DynType), cel.ListType(cel.DynType), cel.StringType, cel.StringType}, cel.DynType,
		cel.FunctionBinding(func(a ...ref.Val) ref.Val { return joinBy("leftJoinBy", a, true) }))),
}

func rows(fn string, v ref.Val) ([]ref.Val, ref.Val) {
	l, ok := v.(traits.Lister)
	if !ok {
		return nil, types.NewErr("%s: not a list", fn)
	}
	n, ok := l.Size().(types.Int)
	if !ok || n > MaxListBlock {
		return nil, types.NewErr("%s: a list of more than %d items", fn, MaxListBlock)
	}
	out := make([]ref.Val, 0, int(n))
	for it := l.Iterator(); it.HasNext() == types.True; {
		out = append(out, it.Next())
	}
	return out, nil
}

// field reads a key from a row: Null when the row is not a map or has none.
func field(row ref.Val, name string) ref.Val {
	m, ok := row.(traits.Mapper)
	if !ok {
		return types.NullValue
	}
	if v, found := m.Find(types.String(name)); found && v != nil {
		return v
	}
	return types.NullValue
}

// keyOf is a key's identity: text and numbers by value, nothing for null.
func keyOf(v ref.Val) (string, bool) {
	switch x := v.(type) {
	case types.String:
		return "s:" + string(x), true
	case types.Int:
		return "n:" + strconv.FormatInt(int64(x), 10), true
	case types.Uint:
		return "n:" + strconv.FormatUint(uint64(x), 10), true
	case types.Double:
		f := float64(x)
		if f == math.Trunc(f) && math.Abs(f) < 1e15 {
			return "n:" + strconv.FormatInt(int64(f), 10), true
		}
		return "n:" + strconv.FormatFloat(f, 'g', -1, 64), true
	case types.Bool:
		return "b:" + strconv.FormatBool(bool(x)), true
	}
	return "", false
}

func groupBy(list, name ref.Val) ref.Val {
	n, ok := name.(types.String)
	if !ok {
		return types.NewErr("groupBy: the field is not a string")
	}
	rs, bad := rows("groupBy", list)
	if bad != nil {
		return bad
	}
	type group struct {
		key   ref.Val
		items []ref.Val
	}
	var order []*group
	at := map[string]*group{}
	for _, r := range rs {
		k := field(r, string(n))
		id, ok := keyOf(k)
		if !ok {
			id = "null"
		}
		g := at[id]
		if g == nil {
			g = &group{key: k}
			at[id] = g
			order = append(order, g)
		}
		g.items = append(g.items, r)
	}
	out := make([]ref.Val, len(order))
	for i, g := range order {
		out[i] = types.NewRefValMap(types.DefaultTypeAdapter, map[ref.Val]ref.Val{
			types.String("key"):   g.key,
			types.String("count"): types.Int(len(g.items)),
			types.String("items"): types.NewRefValList(types.DefaultTypeAdapter, g.items),
		})
	}
	return types.NewRefValList(types.DefaultTypeAdapter, out)
}

func uniqueBy(list, name ref.Val) ref.Val {
	n, ok := name.(types.String)
	if !ok {
		return types.NewErr("uniqueBy: the field is not a string")
	}
	rs, bad := rows("uniqueBy", list)
	if bad != nil {
		return bad
	}
	seen := map[string]bool{}
	var out []ref.Val
	for _, r := range rs {
		id, ok := keyOf(field(r, string(n)))
		if !ok {
			id = "null"
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, r)
	}
	return types.NewRefValList(types.DefaultTypeAdapter, out)
}

func joinBy(fn string, a []ref.Val, keepLeft bool) ref.Val {
	lk, ok1 := a[2].(types.String)
	rk, ok2 := a[3].(types.String)
	if !ok1 || !ok2 {
		return types.NewErr("%s: the fields are not strings", fn)
	}
	left, bad := rows(fn, a[0])
	if bad != nil {
		return bad
	}
	right, bad := rows(fn, a[1])
	if bad != nil {
		return bad
	}
	byKey := map[string][]ref.Val{}
	for _, r := range right {
		if id, ok := keyOf(field(r, string(rk))); ok {
			byKey[id] = append(byKey[id], r)
		}
	}
	var out []ref.Val
	pair := func(l, r ref.Val) ref.Val {
		return types.NewRefValMap(types.DefaultTypeAdapter, map[ref.Val]ref.Val{types.String("left"): l, types.String("right"): r})
	}
	for _, l := range left {
		var matches []ref.Val
		if id, ok := keyOf(field(l, string(lk))); ok {
			matches = byKey[id]
		}
		if len(matches) == 0 && keepLeft {
			out = append(out, pair(l, types.NullValue))
		}
		for _, r := range matches {
			if len(out) >= MaxListBlock {
				return types.NewErr("%s: more than %d rows come out", fn, MaxListBlock)
			}
			out = append(out, pair(l, r))
		}
	}
	return types.NewRefValList(types.DefaultTypeAdapter, out)
}
