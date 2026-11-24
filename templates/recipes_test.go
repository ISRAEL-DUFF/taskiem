package templates_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// Recipes are the eight templates of made tools §13.
var Recipes = []string{"tool-look-up", "tool-add-a-record", "tool-list-and-act", "tool-ask-for-approval", "tool-tell-the-team", "tool-call-a-system", "tool-daily-report", "tool-watch-and-act"}

// The eight recipes (made tools §13): each a template whose definition is
// a tool called by the command chosen when it is installed, written as a
// command whatever way it is typed; the daily report and watch and act
// install a schedule beside it whose button opens the tool.
func TestRecipes(t *testing.T) {
	lib := templates.Default()
	n := 0
	for _, tpl := range lib.List() {
		if tpl.IsRecipe() {
			n++
		}
	}
	if n != len(Recipes) {
		t.Errorf("%d recipes, want %d", n, len(Recipes))
	}
	for _, id := range Recipes {
		tpl, ok := lib.Get(id)
		if !ok || !tpl.IsRecipe() || tpl.Category != "tools" {
			t.Fatalf("%s: not a recipe", id)
		}
		vals := tpl.Examples()
		vals["command"] = "/My Word"
		doc, err := tpl.Instantiate(vals, "")
		if err != nil {
			t.Fatal(err)
		}
		def, err := wd.Load(doc)
		if err != nil || def.Tool == nil || def.Tool.Command != "my-word" {
			t.Fatalf("%s: %v %+v", id, err, def)
		}
		comp, err := tpl.InstantiateCompanion(vals)
		if err != nil {
			t.Fatal(err)
		}
		if has := comp != nil; has != (id == "tool-daily-report" || id == "tool-watch-and-act") {
			t.Errorf("%s: companion %v", id, has)
		}
		if comp != nil && !strings.Contains(string(comp), `"tool":"my-word"`) {
			t.Errorf("%s: the companion's button does not open the tool: %s", id, comp)
		}
	}
	p := templates.Param{Name: "command", Type: "command"}
	for _, bad := range []any{"", "a", "9lives", "this-is-far-too-long-for-a-command", 3} {
		if _, err := p.Coerce(bad); err == nil {
			t.Errorf("%v: taken as a command", bad)
		}
	}
	bad := []byte(`{"id":"x","title":"X","summary":"s","description":"d","category":"c","tags":[],"connectors":[],"params":[],
	 "companion":{"name":"Y","definition":{}},
	 "definition":{"schema":"wd/v1","id":"wf_x","version":1,"name":"X","trigger":{"type":"manual"},"steps":[{"id":"a","type":"transform","config":{"output":{}}}]}}`)
	if _, err := templates.Load(fstest.MapFS{"library/x.json": {Data: bad}}); err == nil || !strings.Contains(err.Error(), "companion") {
		t.Errorf("a companion without a recipe: %v", err)
	}
}
