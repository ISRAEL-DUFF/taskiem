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
