package templates_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/templates"
)

// The recipes that read a table are for the roles chosen (owner and
// manager unless others are), look up needs two letters, a web address
// is https with no name or password in it, and a list answers at once
// while marking a row asks yes (review M6: 4, 7).
func TestRecipeGuards(t *testing.T) {
	lib := templates.Default()
	reg := registry(t)
	for _, id := range []string{"tool-look-up", "tool-list-and-act", "tool-daily-report", "tool-watch-and-act", "tool-add-a-record"} {
		tpl, _ := lib.Get(id)
		doc, err := tpl.Instantiate(tpl.Examples(), "")
		if err != nil {
			t.Fatal(err)
		}
		def, _ := wd.Load(doc)
		if !slices.Equal(def.Roles, []string{"owner", "manager"}) {
			t.Errorf("%s: roles %v", id, def.Roles)
		}
		vals := tpl.Examples()
		vals["roles"] = "staff"
		doc, _ = tpl.Instantiate(vals, "")
		if def, _ = wd.Load(doc); !slices.Equal(def.Roles, []string{"staff"}) {
			t.Errorf("%s: roles chosen %v", id, def.Roles)
		}
	}
	look, _ := lib.Get("tool-look-up")
	if !strings.Contains(string(look.Definition), `"minLength": 2`) {
		t.Errorf("look up takes one letter")
	}
	sys, _ := lib.Get("tool-call-a-system")
	addr, _ := sys.Param("address")
	for _, bad := range []string{"http://api.example.com/x", "https://user:pass@api.example.com/x", "ftp://x"} {
		if _, err := addr.Coerce(bad); err == nil {
			t.Errorf("%s: taken as an address", bad)
		}
	}
	if _, err := addr.Coerce("https://api.example.com/orders"); err != nil {
		t.Errorf("https: %v", err)
	}
	list, _ := lib.Get("tool-list-and-act")
	doc, _ := list.Instantiate(list.Examples(), "")
	care, err := wdcheck.ToolCare(doc, reg, nil)
	if err != nil || care.Level != wdcheck.CareYes || len(care.If) != 1 {
		t.Fatalf("care: %+v %v", care, err)
	}
	if got := care.For(map[string]any{}); got != wdcheck.CareAnswers {
		t.Errorf("a plain list: %s", got)
	}
	if got := care.For(map[string]any{"done": 3}); got != wdcheck.CareYes {
		t.Errorf("marking a row: %s", got)
	}
}

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
	// Who may use it (review M6, 4): role names, typed with commas too.
	r := templates.Param{Name: "roles", Type: "roles"}
	if v, err := r.Coerce("Owner, shift manager, owner"); err != nil || fmt.Sprint(v) != "[owner shift_manager]" {
		t.Errorf("roles typed: %v %v", v, err)
	}
	for _, bad := range []any{"", " , ", "9x", []any{1}} {
		if _, err := r.Coerce(bad); err == nil {
			t.Errorf("%v: taken as roles", bad)
		}
	}
	bad := []byte(`{"id":"x","title":"X","summary":"s","description":"d","category":"c","tags":[],"connectors":[],"params":[],
	 "companion":{"name":"Y","definition":{}},
	 "definition":{"schema":"wd/v1","id":"wf_x","version":1,"name":"X","trigger":{"type":"manual"},"steps":[{"id":"a","type":"transform","config":{"output":{}}}]}}`)
	if _, err := templates.Load(fstest.MapFS{"library/x.json": {Data: bad}}); err == nil || !strings.Contains(err.Error(), "companion") {
		t.Errorf("a companion without a recipe: %v", err)
	}
}
