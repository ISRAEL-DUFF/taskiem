package stock_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/templates"
)

func catalogueNamed(table string, names map[string]string) stock.OldCatalogue {
	c := stock.OldCatalogue{Table: table, Names: map[string]string{}}
	for _, k := range stock.OldColumns {
		c.Names[k] = k
		if n, ok := names[k]; ok {
			c.Names[k] = n
		}
	}
	return c
}

func flow(trigger string, steps ...string) []byte {
	if trigger == "" {
		trigger = `{"type":"manual"}`
	}
	return []byte(`{"schema":"wd/v1","id":"wf_x","version":1,"name":"x","trigger":` + trigger + `,"steps":[` + strings.Join(steps, ",") + `]}`)
}

func got(uses []stock.OldColumnUse) string {
	var s []string
	for _, u := range uses {
		s = append(s, fmt.Sprintf("%s %s %s(%s)", u.Step, u.Uses, u.Column, u.TableColumn))
	}
	return strings.Join(s, "; ")
}

// The steps of a workflow that read or set the catalogue's old stock
// columns, by the names its mapping gave them (what-a-business-buys
// §4.3); those on stock@1, the kept copy or another table are not.
func TestOldColumnsIn(t *testing.T) {
	c := catalogueNamed("products", map[string]string{"stock": "qty"})
	for _, tc := range []struct {
		name string
		doc  []byte
		want string
	}{
		{"a read selecting the count", flow("", `{"id":"read","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables",
			"input":{"table":"products","select":"name, qty, cost_price"}}`), "read reads stock(qty); read reads cost_price(cost_price)"},
		{"a filter on the reorder level", flow("", `{"id":"low","type":"connector","connector":"xenibase@1","action":"query_rows",
			"input":{"table":"public.products","filters":[{"any":[{"column":"reorder_level","op":"gt","value":0}]}],"order":[{"column":"name"}]}}`), "low reads reorder_level(reorder_level)"},
		{"setting the cost", flow("", `{"id":"price","type":"connector","connector":"xenibase@1","action":"update_rows","connection":"data_tables",
			"input":{"table":"products","key":"7","set":{"cost_price":"=trigger.body.cost"}}}`), "price sets cost_price(cost_price)"},
		{"inserting a row with its count, in a foreach", flow("", `{"id":"each","type":"foreach","config":{"items":"=trigger.body.rows","steps":[
			{"id":"add","type":"connector","connector":"xenibase@1","action":"insert_rows","input":{"table":"products","rows":[{"name":"=item.name","qty":"=item.n"}]}}]}}`), "add sets stock(qty)"},
		{"a whole row read, its count read off it further on", flow("",
			`{"id":"read","type":"connector","connector":"xenibase@1","action":"read_rows","input":{"table":"products"}}`,
			`{"id":"each","type":"foreach","needs":["read"],"config":{"items":"=steps.read.output.rows","steps":[
				{"id":"tell","type":"connector","connector":"notify@1","action":"text","when":"=item.qty < 5","input":{"to":"x","message":"=item['supplier']"}}]}}`),
			"read reads stock(qty); read reads supplier(supplier)"},
		{"a whole row read, nothing read off it", flow("",
			`{"id":"read","type":"connector","connector":"xenibase@1","action":"read_rows","input":{"table":"products"}}`,
			`{"id":"n","type":"transform","needs":["read"],"config":{"output":{"names":"=steps.read.output.rows.map(r, r.name)"}}}`), ""},
		{"a trigger watching the count", flow(`{"type":"connector_event","config":{"connector":"xenibase@1","trigger":"row_changed","connection":"data_tables",
			"options":{"tables":["products"],"columns":["qty"]}}}`,
			`{"id":"t","type":"connector","connector":"notify@1","action":"text","input":{"to":"x","message":"='Low: ' + trigger.body.record.unit"}}`),
			"trigger reads stock(qty); trigger reads unit(unit)"},
		{"stock@1 and the kept copy", flow("",
			`{"id":"item","type":"connector","connector":"stock@1","action":"get_item","input":{"id":"7"}}`,
			`{"id":"adj","type":"connector","connector":"stock@1","action":"adjust","input":{"id":"7","change":-1}}`,
			`{"id":"kept","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"stock_items","select":"name,count,cost_kobo"}}`,
			`{"id":"say","type":"transform","config":{"output":{"left":"=steps.item.output.stock","unit":"=steps.kept.output.rows[0].unit"}}}`), ""},
		{"another table, another project", flow("",
			`{"id":"a","type":"connector","connector":"xenibase@1","action":"read_rows","input":{"table":"customers","select":"stock"}}`,
			`{"id":"b","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"warehouse","input":{"table":"products","select":"qty"}}`), ""},
		{"the mapping's name, not the default", flow("", `{"id":"read","type":"connector","connector":"xenibase@1","action":"read_rows","input":{"table":"products","select":"stock"}}`), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uses := stock.OldColumnsIn(tc.doc, c)
			if g := got(uses); g != tc.want {
				t.Fatalf("got %q, want %q", g, tc.want)
			}
			for _, u := range uses {
				if u.Instead == "" {
					t.Errorf("%+v: no replacement", u)
				}
			}
		})
	}
}

// g-old-cols: a column named inside an expression ("=…") is found as well:
// a select, a filter's column, filters and an order given whole, set, row
// and rows given as an expression, a column passed as a string; a value
// that merely holds the word is not.
func TestOldColumnsInExpressions(t *testing.T) {
	c := catalogueNamed("products", map[string]string{"stock": "qty"})
	step := func(action, input string) []byte {
		return flow("", `{"id":"s","type":"connector","connector":"xenibase@1","action":"`+action+`","connection":"data_tables","input":{"table":"products",`+input+`}}`)
	}
	for _, tc := range []struct {
		name string
		doc  []byte
		want string
	}{
		{"a select as an expression", step("read_rows", `"select":"='name, ' + (trigger.body.all ? 'qty, cost_price' : 'unit')"`),
			"s reads stock(qty); s reads unit(unit); s reads cost_price(cost_price)"},
		{"a select in double quotes", step("read_rows", `"select":"=\"name,qty\""`), "s reads stock(qty)"},
		{"a filter's column as an expression", step("query_rows", `"filters":[{"column":"=trigger.body.low ? 'reorder_level' : 'name'","op":"gt","value":0}]`),
			"s reads reorder_level(reorder_level)"},
		{"filters given whole", step("query_rows", `"filters":"=[{'column': 'qty', 'op': 'lt', 'value': trigger.body.n}]"`), "s reads stock(qty)"},
		{"one filter of the list as an expression", step("query_rows", `"filters":["={'column': 'target_level', 'op': 'gt', 'value': 0}"]`),
			"s reads target_level(target_level)"},
		{"an order as an expression", step("query_rows", `"order":"=[{\"column\": \"cost_price\"}]"`), "s reads cost_price(cost_price)"},
		{"a filter's value is not a column", step("query_rows", `"filters":[{"column":"name","op":"eq","value":"='qty'"}],"select":"name"`), ""},
		{"set as an expression", step("update_rows", `"key":"7","set":"={'qty': trigger.body.n, 'note': 'unit'}"`), "s sets stock(qty)"},
		{"set as a map built in a ternary", step("update_rows", `"key":"7","set":"=trigger.body.cost > 0 ? {\"cost_price\": trigger.body.cost} : {}"`),
			"s sets cost_price(cost_price)"},
		{"a row as an expression", step("insert_rows", `"row":"={'name': trigger.body.name, 'supplier': trigger.body.from}"`), "s sets supplier(supplier)"},
		{"rows as an expression", step("insert_rows", `"rows":"=trigger.body.items.map(i, {'name': i.name, 'qty': i.n})"`), "s sets stock(qty)"},
		{"one of the rows as an expression", step("upsert_rows", `"rows":["={'name': 'x', 'reorder_level': 3}"],"on_conflict":["=trigger.body.key ? 'name' : 'unit'"]`),
			"s reads unit(unit); s sets reorder_level(reorder_level)"},
		// H2 review fix 6: only a map literal's keys, never a ternary's
		// strings before its colon.
		{"a ternary inside a set's value", step("update_rows", `"key":"7","set":"={'note': trigger.body.low ? 'qty' : 'unit'}"`), ""},
		{"a ternary as a row, no map", step("insert_rows", `"row":"=trigger.body.a ? 'qty' : 'unit'"`), ""},
		{"a ternary in a list in a set's value", step("update_rows", `"key":"7","set":"={'tags': [x ? 'cost_price' : 'a']}"`), ""},
		{"a filter's column chosen by a ternary is not one string", step("query_rows", `"filters":"=[{'column': c ? 'qty' : 'name', 'op': 'eq'}]","select":"name"`), ""},
		{"a nested map's keys are", step("update_rows", `"key":"7","set":"={'note': 'x', 'qty': {'a': 1}['a']}"`), "s sets stock(qty)"},
		{"the kept copy by expression is not", flow("", `{"id":"s","type":"connector","connector":"xenibase@1","action":"read_rows","input":{"table":"stock_items","select":"='count, qty'"}}`), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if g := got(stock.OldColumnsIn(tc.doc, c)); g != tc.want {
				t.Fatalf("got %q, want %q", g, tc.want)
			}
		})
	}
}

// What replaces each use (§4.3).
func TestOldColumnsInstead(t *testing.T) {
	for _, tc := range []struct{ col, uses, has string }{
		{"stock", stock.Reads, "stock_items"},
		{"stock", stock.Sets, "stock@1 adjust"},
		{"cost_price", stock.Reads, "cost_kobo"},
		{"cost_price", stock.Sets, "cost_kobo"},
		{"reorder_level", stock.Reads, "low_level"},
		{"supplier", stock.Sets, "Stock page"},
	} {
		if s := stock.Instead(tc.col, tc.uses); !strings.Contains(s, tc.has) {
			t.Errorf("%s %s: %q", tc.uses, tc.col, s)
		}
	}
}

// PR review fix 4: the shop front's products table (templates/tables, made
// by the shop kind) makes none of the stock columns S1 took from the
// catalogue: how many are left is kept on stock items (decision 0070).
func TestShopTableHasNoOldColumns(t *testing.T) {
	kt, ok := templates.KnownTable("products")
	if !ok {
		t.Fatal("no products table")
	}
	for _, c := range kt.Columns {
		for _, old := range stock.OldColumns {
			if c.Name == old {
				t.Errorf("the products table still makes %s", old)
			}
		}
	}
	if strings.Contains(kt.Description, "stock kept") {
		t.Errorf("its description: %q", kt.Description)
	}
}
