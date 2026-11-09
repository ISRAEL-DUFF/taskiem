package xenibase

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// The one translator renders filters both as the data API's where
// parameters (for an update or delete by filter) and as the query body's
// where (for reads).
func TestFilterTranslator(t *testing.T) {
	filters := []any{
		map[string]any{"column": "status", "op": "eq", "value": "new"},
		map[string]any{"column": "total", "op": "gte", "value": float64(2000)},
		map[string]any{"column": "customer", "op": "in", "values": []any{"Ada", "Bayo, Jr", `O"Neil`}},
		map[string]any{"column": "phone", "op": "is_null"},
		map[string]any{"column": "paid", "op": "is", "value": true},
		map[string]any{"column": "notes", "op": "ilike", "value": "%urgent%"},
		map[string]any{"column": "ref", "op": "eq", "value": "a:b,c"},
		map[string]any{"column": "meta->tag", "op": "eq", "value": "work"},
		map[string]any{"column": "tags", "op": "contains", "value": []any{"vip"}},
	}
	fs, err := parseDataFilters(filters)
	if err != nil {
		t.Fatal(err)
	}
	where, err := whereParams(fs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"status:eq:new", "total:gte:2000", `customer:in:Ada,"Bayo, Jr","O\"Neil"`, "phone:is:null", "paid:is:true", "notes:ilike:%urgent%", "ref:eq:a:b,c", "meta->tag:eq:work", `tags:contains:["vip"]`}
	if !slices.Equal(where, want) {
		t.Errorf("where %q\nwant  %q", where, want)
	}
	// In the URL the parameters are percent-encoded whole, so a colon or a
	// comma in a value cannot be read as part of the grammar.
	q := url.Values{"where": where}
	enc := q.Encode()
	if !strings.Contains(enc, "where=ref%3Aeq%3Aa%3Ab%2Cc") || strings.Contains(enc, "a:b") {
		t.Errorf("encoded %s", enc)
	}
	body := queryWhere(fs)
	raw, _ := json.Marshal(body)
	and := body.(map[string]any)["and"].([]any)
	if len(and) != len(filters) || !strings.Contains(string(raw), `{"column":"phone","op":"is","value":null}`) ||
		!strings.Contains(string(raw), `{"column":"customer","op":"in","value":["Ada","Bayo, Jr","O\"Neil"]}`) {
		t.Errorf("query body %s", raw)
	}
	// One filter is the condition itself; none is nil.
	one, _ := parseDataFilters(filters[:1])
	if m, ok := queryWhere(one).(map[string]any); !ok || m["column"] != "status" {
		t.Errorf("one filter: %v", queryWhere(one))
	}
	if queryWhere(nil) != nil {
		t.Error("no filters is a where")
	}
	// not_null reads through the body's not, and has no where form.
	nn, err := parseDataFilters([]any{map[string]any{"column": "phone", "op": "not_null"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(queryWhere(nn))
	if string(raw) != `{"not":{"column":"phone","op":"is","value":null}}` {
		t.Errorf("not_null in the body: %s", raw)
	}
	if _, err := whereParams(nn); err == nil || !strings.Contains(err.Error(), "not_null is not supported for writes") {
		t.Errorf("not_null in where: %v", err)
	}
	// Refused shapes.
	for _, bad := range []any{
		map[string]any{"column": "id", "op": "like"},
		map[string]any{"column": "id", "op": "in", "value": "1"},
		map[string]any{"column": "id", "op": "in", "values": []any{}},
		map[string]any{"column": "id", "op": "is", "value": "null"},
		map[string]any{"column": "id", "op": "between", "value": 1},
		map[string]any{"column": "id:eq:1", "op": "eq", "value": 1},
		map[string]any{"column": "id,name", "op": "eq", "value": 1},
		map[string]any{"column": "", "op": "eq", "value": 1},
		"id:eq:1",
	} {
		if _, err := parseDataFilters([]any{bad}); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if o, err := orderText([]any{map[string]any{"column": "total", "desc": true}, map[string]any{"column": "id"}}); err != nil || o != "total:desc,id:asc" {
		t.Errorf("order %q %v", o, err)
	}
	if _, err := orderText([]any{map[string]any{"column": "total:desc"}}); err == nil {
		t.Error("an order column with a colon accepted")
	}
}

func TestValueText(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{"x", "x"}, {float64(7), "7"}, {float64(2.5), "2.5"}, {float64(1000000), "1000000"}, {true, "true"}, {nil, "null"},
		{json.Number("12345678901234567"), "12345678901234567"}, {map[string]any{"a": 1}, `{"a":1}`},
	} {
		if got, err := valueText(tc.in); err != nil || got != tc.want {
			t.Errorf("%v: %q %v", tc.in, got, err)
		}
	}
}

// An any group is the query body's or, for reads only (the Tables tool's
// search across columns).
func TestAnyGroup(t *testing.T) {
	fs, err := parseDataFilters([]any{
		map[string]any{"column": "paid", "op": "is", "value": false},
		map[string]any{"any": []any{
			map[string]any{"column": "name", "op": "ilike", "value": "%ada%"},
			map[string]any{"column": "phone", "op": "ilike", "value": "%ada%"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(queryWhere(fs))
	want := `{"and":[{"column":"paid","op":"is","value":false},{"or":[{"column":"name","op":"ilike","value":"%ada%"},{"column":"phone","op":"ilike","value":"%ada%"}]}]}`
	if string(got) != want {
		t.Errorf("where %s, want %s", got, want)
	}
	one, _ := parseDataFilters([]any{map[string]any{"any": []any{map[string]any{"column": "name", "op": "eq", "value": "Ada"}}}})
	if got, _ := json.Marshal(queryWhere(one)); string(got) != `{"column":"name","op":"eq","value":"Ada"}` {
		t.Errorf("a group of one: %s", got)
	}
	if _, err := whereParams(fs); err == nil {
		t.Error("a write by an any group was allowed")
	}
	for _, bad := range []any{
		map[string]any{"any": []any{}},
		map[string]any{"any": "name"},
		map[string]any{"any": []any{map[string]any{"any": []any{map[string]any{"column": "name", "op": "eq", "value": 1}}}}},
		map[string]any{"any": []any{map[string]any{"column": "name", "op": "nope"}}},
	} {
		if _, err := parseDataFilters([]any{bad}); err == nil {
			t.Errorf("%v was taken", bad)
		}
	}
}
