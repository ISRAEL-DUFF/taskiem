package flowcode_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/flowcode"
	"github.com/israel-duff/taskiem/engine/wd"
)

// The round trip (spec 10.2, gate G2): any valid definition, printed as flow
// code and compiled back in the binary, is the definition it came from. The
// corpus is every definition in the repository plus randomly generated ones
// that reach every step type, trigger, option and value shape the schema
// allows, with expressions drawn from the CEL the SDK can and cannot print
// as arrow functions.

var (
	roundTrips = flag.Int("roundtrip.n", 120, "random definitions in the round-trip test")
	roundSeed  = flag.Uint64("roundtrip.seed", 0, "seed for the round-trip test (0: fixed default)")
)

func TestRoundTripRepositoryDefinitions(t *testing.T) {
	var files []string
	for _, pattern := range []string{"../../flows/*/*.wd.json", "../../flows/*/*/*.wd.json", "../../examples/*/*.wd.json", "../../docs/**/*.wd.json"} {
		m, _ := filepath.Glob(pattern)
		files = append(files, m...)
	}
	if len(files) < 4 {
		t.Fatalf("found %d definitions", len(files))
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			doc, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, doc)
		})
	}
}

func TestRoundTripGeneratedDefinitions(t *testing.T) {
	seed := *roundSeed
	if seed == 0 {
		seed = 20261006
	}
	n := *roundTrips
	if testing.Short() {
		n = 20
	}
	r := rand.New(rand.NewPCG(seed, 1))
	valid, arrows, fallbacks, menus := 0, 0, 0, 0
	for i := 0; valid < n; i++ {
		if i > n*20 {
			t.Fatalf("only %d of %d generated definitions were valid", valid, i)
		}
		g := &gen{r: r}
		def := g.definition()
		doc, err := json.Marshal(def)
		if err != nil {
			t.Fatal(err)
		}
		if probs := wd.Validate(doc); len(probs) > 0 {
			if os.Getenv("ROUNDTRIP_DEBUG") != "" {
				t.Logf("invalid: %v", probs[0])
			}
			continue
		}
		valid++
		if strings.Contains(string(doc), `"type":"ussd","config"`) {
			menus++
		}
		t.Run(fmt.Sprintf("seed%d_%03d", seed, valid), func(t *testing.T) {
			code := roundTrip(t, doc)
			arrows += strings.Count(code, ") => ")
			fallbacks += strings.Count(code, `"=`)
		})
	}
	// Both ways of printing an expression are exercised: as an arrow
	// function, and kept as CEL text where code cannot say it exactly.
	t.Logf("expressions printed as functions: %d, kept as CEL: %d; USSD menus: %d", arrows, fallbacks, menus)
	if menus < n/40 {
		t.Errorf("only %d USSD menus in the corpus", menus)
	}
	if arrows < n || fallbacks < n/4 {
		t.Errorf("the corpus is too narrow: %d functions, %d CEL strings", arrows, fallbacks)
	}
}

// roundTrip checks definition → code → definition, and that code generated
// from the result is the same code (stable formatting).
func roundTrip(t *testing.T, doc []byte) string {
	t.Helper()
	code, err := flowcode.Generate(ctx, doc)
	if err != nil {
		t.Fatalf("generate: %v\n%s", err, doc)
	}
	back, err := flowcode.CompileSource(ctx, code)
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, code)
	}
	if !sameJSON(t, doc, back) {
		t.Fatalf("lossy round trip\n--- definition\n%s\n--- code\n%s\n--- compiled\n%s\n--- first difference: %s", indent(doc), code, back, firstDiff(t, doc, back))
	}
	again, err := flowcode.Generate(ctx, back)
	if err != nil {
		t.Fatal(err)
	}
	if again != code {
		t.Errorf("code is not stable under a second pass\n--- first\n%s\n--- second\n%s", code, again)
	}
	return code
}

// sameJSON compares numbers by their text, so a value that only survives as a
// float is a difference.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	return reflect.DeepEqual(decodeNumbers(t, a), decodeNumbers(t, b))
}

func decodeNumbers(t *testing.T, doc []byte) any {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(string(doc)))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func firstDiff(t *testing.T, a, b []byte) string {
	var walk func(path string, x, y any) string
	walk = func(path string, x, y any) string {
		switch xv := x.(type) {
		case map[string]any:
			yv, ok := y.(map[string]any)
			if !ok {
				return path
			}
			keys := map[string]bool{}
			for k := range xv {
				keys[k] = true
			}
			for k := range yv {
				keys[k] = true
			}
			sorted := make([]string, 0, len(keys))
			for k := range keys {
				sorted = append(sorted, k)
			}
			sort.Strings(sorted)
			for _, k := range sorted {
				if d := walk(path+"/"+k, xv[k], yv[k]); d != "" {
					return d
				}
			}
			return ""
		case []any:
			yv, ok := y.([]any)
			if !ok || len(xv) != len(yv) {
				return path
			}
			for i := range xv {
				if d := walk(fmt.Sprintf("%s/%d", path, i), xv[i], yv[i]); d != "" {
					return d
				}
			}
			return ""
		}
		if !reflect.DeepEqual(x, y) {
			return fmt.Sprintf("%s: %#v != %#v", path, x, y)
		}
		return ""
	}
	return walk("", decodeNumbers(t, a), decodeNumbers(t, b))
}

func indent(doc []byte) []byte {
	out, err := flowcode.Indent(doc)
	if err != nil {
		return doc
	}
	return out
}

// gen builds random definitions. Map keys are kept in insertion order by
// obj so the definition's own key order varies too.
type gen struct {
	r      *rand.Rand
	nextID int
}

type kv struct {
	k string
	v any
}

// obj is an ordered JSON object.
type obj []kv

func (o obj) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(e.k)
		v, err := json.Marshal(e.v)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

func (o *obj) set(k string, v any) { *o = append(*o, kv{k, v}) }

func (g *gen) chance(p float64) bool { return g.r.Float64() < p }

func pick[T any](g *gen, xs ...T) T { return xs[g.r.IntN(len(xs))] }

func (g *gen) id(prefix string) string {
	g.nextID++
	return fmt.Sprintf("%s%d", prefix, g.nextID)
}

func (g *gen) duration() string {
	return pick(g, "30s", "5m", "1h", "2d", "1h30m", "250ms", "90s")
}

// scope says what an expression may read where it appears.
type scope struct {
	steps   []string // step ids settled before this step
	foreach bool
	secrets bool
}

func (g *gen) definition() obj {
	var d obj
	d.set("schema", "wd/v1")
	d.set("id", "wf_"+g.word(1+g.r.IntN(12)))
	d.set("version", pick(g, 1, 1, 2, 17))
	d.set("name", g.text())
	if g.chance(0.4) {
		d.set("description", g.text())
	}
	d.set("trigger", g.trigger())
	if g.chance(0.3) {
		d.set("inputs", obj{{"schema", obj{{"type", "object"}, {"properties", obj{{"amount", obj{{"type", "integer"}}}, {"note", obj{{"$ref", "#/types/Note"}}}}}, {"required", []string{"amount"}}}}})
		d.set("types", obj{{"Note", obj{{"type", "string"}, {"maxLength", 140}}}})
	}
	d.set("steps", g.steps(scope{}, 1+g.r.IntN(5), 0))
	if g.chance(0.5) {
		var s obj
		if g.chance(0.5) {
			s.set("timeout", g.duration())
		}
		if g.chance(0.5) {
			s.set("concurrency_key", "=trigger.body.customer_id")
			s.set("max_concurrency", 1+g.r.IntN(5))
		}
		if g.chance(0.5) {
			s.set("retention", "30d")
		}
		d.set("settings", s)
	} else {
		d.set("settings", obj{})
	}
	return d
}

func (g *gen) trigger() obj {
	switch g.r.IntN(8) {
	case 7:
		return obj{{"type", "ussd"}, {"config", g.ussd()}}
	case 0:
		c := obj{{"path", "/" + g.word(6) + "/v1.events"}, {"auth", pick(g, "hmac", "bearer", "mtls", "none")}}
		if g.chance(0.5) {
			c.set("dedup", "=trigger.headers['X-Event-Id']")
		}
		if g.chance(0.3) {
			c.set("respond", g.chance(0.5))
		}
		return obj{{"type", "webhook"}, {"config", c}}
	case 1:
		c := obj{{"cron", "*/15 * * * *"}}
		if g.chance(0.5) {
			c.set("timezone", "Africa/Lagos")
		}
		return obj{{"type", "schedule"}, {"config", c}}
	case 2:
		c := obj{{"connector", "paystack@1"}, {"trigger", "transfer_event"}}
		if g.chance(0.5) {
			c.set("events", []string{"transfer.success", "transfer.failed"})
		}
		if g.chance(0.5) {
			c.set("connection", "conn_"+g.word(4))
		}
		return obj{{"type", "connector_event"}, {"config", c}}
	case 3:
		return obj{{"type", "polling"}, {"config", obj{{"connector", "postgres@1"}, {"action", "query"}, {"input", obj{{"sql", "select 1"}}}, {"interval", "5m"}, {"item_id", "=item.id"}}}}
	case 4:
		if g.chance(0.5) {
			return obj{{"type", "manual"}, {"config", obj{}}}
		}
		return obj{{"type", "manual"}}
	case 5:
		return obj{{"type", pick(g, "subflow", "email", "whatsapp", "database_change")}}
	}
	return obj{{"type", "subflow"}, {"config", obj{{"anything", g.value(scope{}, 2)}}}}
}

func (g *gen) steps(sc scope, n, depth int) []obj {
	var out []obj
	var earlier []string
	for range n {
		s := g.step(sc, earlier, depth)
		out = append(out, s)
		earlier = append(earlier, s[0].v.(string))
	}
	return out
}

func (g *gen) step(outer scope, earlier []string, depth int) obj {
	var s obj
	id := g.id(pick(g, "s", "step_", "pay", "x"))
	s.set("id", id)
	types := []string{"connector", "connector", "code", "container", "http", "wait", "signal", "approval", "subflow", "transform", "ai"}
	if depth < 2 {
		types = append(types, "branch", "parallel", "foreach")
	}
	typ := pick(g, types...)
	s.set("type", typ)
	sc := outer
	sc.steps = append([]string(nil), outer.steps...)
	var needs []string
	if len(earlier) > 0 && g.chance(0.8) {
		needs = append(needs, earlier[len(earlier)-1])
		if len(earlier) > 1 && g.chance(0.3) {
			needs = append(needs, earlier[0])
		}
		if g.chance(0.15) {
			needs = []string{earlier[g.r.IntN(len(earlier))]}
		}
		s.set("needs", needs)
		sc.steps = append(sc.steps, needs...)
	} else if len(earlier) == 0 && g.chance(0.05) {
		s.set("needs", []string{})
	}
	if g.chance(0.2) {
		s.set("name", g.text())
	}
	if g.chance(0.15) {
		s.set("description", g.text())
	}
	if g.chance(0.25) {
		s.set("when", g.expr(sc))
	}
	if g.chance(0.15) {
		r := obj{{"max", g.r.IntN(6)}}
		if g.chance(0.5) {
			r.set("backoff", pick(g, "fixed", "exponential"))
			r.set("initial", g.duration())
		}
		if g.chance(0.3) {
			r.set("max_delay", g.duration())
			r.set("max_duration", g.duration())
		}
		s.set("retry", r)
	}
	if g.chance(0.15) {
		s.set("timeout", g.duration())
	}
	inner := sc
	switch typ {
	case "connector":
		ra := pick(g, [2]string{"paystack@1", "transfer"}, [2]string{"iswallet@1", "get_balance"}, [2]string{"postgres@1", "query"},
			[2]string{"telegram@1", "send_message"}, [2]string{"acme_bank@3", "post_entry"}, [2]string{"acme_bank@3", "list"}, [2]string{"paystack@1", "constructor"}, [2]string{"paystack@1", "not_in_the_helper"})
		s.set("connector", ra[0])
		s.set("action", ra[1])
		if g.chance(0.85) {
			sec := sc
			sec.secrets = true
			s.set("input", g.values(sec, 3))
		}
		if g.chance(0.2) {
			s.set("connection", "conn_"+g.word(5))
		}
		if g.chance(0.2) {
			s.set("effect", obj{{"idempotency_seed", "=run.id + '/" + id + "'"}})
		}
		if g.chance(0.15) {
			c := obj{{"action", "reverse"}}
			if g.chance(0.7) {
				self := sc
				self.steps = append(self.steps, id)
				c.set("input", obj{{"reference", "=steps." + id + ".output.reference"}, {"why", g.text()}, {"extra", g.values(self, 1)}})
			}
			s.set("compensate", c)
		}
	case "code":
		c := obj{{"language", pick(g, "javascript", "typescript", "python")}, {"source", g.source()}}
		if g.chance(0.2) {
			c = obj{{"language", "wasm"}, {"module", "sha256:" + strings.Repeat("ab", 32)}}
		}
		if g.chance(0.3) {
			c.set("secrets", []string{"api_key", "webhook_secret"})
		}
		if g.chance(0.3) {
			c.set("limits", obj{{"memory_mb", 64}, {"cpu", "2s"}})
		}
		s.set("config", c)
		if g.chance(0.6) {
			s.set("input", g.values(sc, 2))
		}
	case "container":
		c := obj{{"image", "registry.example.com/tools/" + pick(g, "pdf", "ocr", "ml-score") + "@sha256:" + strings.Repeat("0f", 32)},
			{"command", pick(g, []string{"/bin/render"}, []string{"python", "/app/main.py"})}}
		if g.chance(0.4) {
			c.set("args", []string{"--fast", g.word(4)})
		}
		if g.chance(0.3) {
			c.set("input_mode", pick(g, "stdin", "file"))
		}
		if g.chance(0.3) {
			c.set("output_mode", pick(g, "stdout", "file"))
		}
		if g.chance(0.3) {
			c.set("secrets", []string{"api_key"})
			if g.chance(0.5) {
				c.set("secrets_mode", pick(g, "env", "file"))
			}
		}
		if g.chance(0.5) {
			c.set("class", pick(g, "read", "idempotent_write", "unsafe_write"))
		}
		if g.chance(0.3) {
			c.set("network", "egress")
			c.set("hosts", []string{"api.example.com", "*.example.org"})
		} else if g.chance(0.2) {
			c.set("network", "none")
		}
		if g.chance(0.4) {
			c.set("limits", obj{{"cpu", pick(g, "500m", "1", "1.5")}, {"memory_mb", 1024}, {"timeout", "10m"}, {"output_bytes", 65536}})
		}
		s.set("config", c)
		if g.chance(0.6) {
			sec := sc
			sec.secrets = true
			s.set("input", g.values(sec, 2))
		}
		if g.chance(0.2) {
			s.set("effect", obj{{"idempotency_seed", "=run.id + '/" + id + "'"}})
		}
	case "http":
		m := pick(g, "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE")
		c := obj{{"method", m}, {"url", pick(g, "https://api.example.com/v1/things", "=env.base_url + '/v1/things/' + string(trigger.body.id)")}}
		if g.chance(0.5) {
			sec := sc
			sec.secrets = true
			c.set("headers", obj{{"Authorization", "=secrets.api_token"}, {"X-Request-Id", "=run.id"}, {"Content-Type", "application/json"}})
			_ = sec
		}
		if g.chance(0.4) {
			c.set("query", g.values(sc, 1))
		}
		if m != "GET" && m != "HEAD" {
			c.set("class", pick(g, "unsafe_write", "idempotent_write"))
			if g.chance(0.5) {
				c.set("body", g.value(sc, 3))
			}
			if g.chance(0.3) {
				c.set("idempotency_header", "Idempotency-Key")
			}
		} else if g.chance(0.3) {
			c.set("class", "read")
		}
		s.set("config", c)
		if g.chance(0.2) {
			s.set("effect", obj{{"idempotency_seed", "=run.id"}})
		}
	case "wait":
		if g.chance(0.5) {
			s.set("config", obj{{"duration", g.duration()}})
		} else {
			s.set("config", obj{{"until", "=timestamp(trigger.body.due_at)"}})
		}
	case "signal":
		c := obj{{"event", pick(g, "payout.settled", "paystack@1:transfer_event.transfer.success")}, {"correlation", g.expr(sc)}}
		if g.chance(0.5) {
			c.set("timeout", g.duration())
		}
		s.set("config", c)
	case "approval":
		var c obj
		if g.chance(0.6) {
			c.set("policy", pick(g, "single", "maker_checker", "high_value"))
		}
		if len(c) == 0 || g.chance(0.4) {
			c.set("role", "credit_officer")
		}
		if g.chance(0.4) {
			c.set("count", 1+g.r.IntN(3))
		}
		if g.chance(0.4) {
			c.set("timeout", g.duration())
			c.set("on_timeout", pick(g, "reject", "fail", "escalate:head_of_ops"))
		}
		if g.chance(0.6) {
			c.set("subject", g.values(sc, 2))
		}
		s.set("config", c)
	case "subflow":
		c := obj{{"workflow", "wf_" + g.word(8)}, {"version", 1 + g.r.IntN(4)}}
		if g.chance(0.4) {
			c.set("wait", g.chance(0.5))
		}
		s.set("config", c)
		if g.chance(0.6) {
			s.set("input", g.values(sc, 2))
		}
	case "transform":
		s.set("config", obj{{"output", g.value(sc, 3)}})
	case "ai":
		c := obj{{"prompt", g.text() + "\n" + g.source()}, {"output_schema", obj{{"type", "object"}, {"properties", obj{{"risk", obj{{"enum", []string{"low", "high"}}}}}}}}}
		if g.chance(0.5) {
			c.set("model", "default")
		}
		s.set("config", c)
		if g.chance(0.6) {
			s.set("input", g.values(sc, 2))
		}
	case "branch":
		var paths []obj
		for range 1 + g.r.IntN(3) {
			paths = append(paths, obj{{"name", g.id("path")}, {"when", g.expr(sc)}, {"steps", g.steps(inner, 1+g.r.IntN(3), depth+1)}})
		}
		c := obj{{"paths", paths}}
		if g.chance(0.6) {
			c.set("default", obj{{"steps", g.steps(inner, 1+g.r.IntN(2), depth+1)}})
		}
		s.set("config", c)
	case "parallel":
		var branches []obj
		for range 2 + g.r.IntN(2) {
			branches = append(branches, obj{{"name", g.id("lane")}, {"steps", g.steps(inner, 1+g.r.IntN(3), depth+1)}})
		}
		c := obj{{"branches", branches}}
		if g.chance(0.5) {
			c.set("join", pick(g, "all", "any"))
		}
		if g.chance(0.3) {
			c.set("max_concurrency", 1+g.r.IntN(4))
		}
		s.set("config", c)
	case "foreach":
		body := inner
		body.foreach = true
		c := obj{{"items", pick(g, "=trigger.body.items", "=trigger.body.transfers.filter(t, t.amount > 0)")}}
		if g.chance(0.4) {
			c.set("max_concurrency", 1+g.r.IntN(20))
		}
		if g.chance(0.3) {
			c.set("max_items", 500)
		}
		c.set("steps", g.steps(body, 1+g.r.IntN(3), depth+1))
		s.set("config", c)
	}
	if depth < 2 && g.chance(0.1) {
		s.set("on_error", obj{{"steps", g.steps(sc, 1+g.r.IntN(2), depth+1)}})
	}
	return s
}

// values is an object of literals and expressions.
func (g *gen) values(sc scope, depth int) obj {
	var o obj
	for range g.r.IntN(5) {
		o.set(g.key(), g.value(sc, depth-1))
	}
	if sc.secrets && g.chance(0.2) {
		o.set("token", "=secrets.provider_token")
	}
	return o
}

func (g *gen) key() string {
	return pick(g, "amount", "account_number", "narration", "bank_code", "meta", "x", "items",
		"Content-Type", "with space", "123", "$ref", "a.b", "__proto__", "constructor", "toString", "", "ñame", "key\"quote", "valueOf")
}

func (g *gen) value(sc scope, depth int) any {
	n := 9
	if depth <= 0 {
		n = 7
	}
	switch g.r.IntN(n) {
	case 0, 1:
		return g.expr(sc)
	case 2:
		return g.text()
	case 3:
		return pick[any](g, 0, 1, -1, 42, 150000, 2.5, -0.125, 1e-7, 123456789012, 9007199254740991, -9007199254740991)
	case 4:
		return pick[any](g, true, false, nil)
	case 5:
		return pick(g, "${run.id}", "{{ handlebars }}", "x=1", "`tick`", "line\nbreak", "\u2028sep", "tab\there", "back\\slash", "")
	case 6:
		return g.expr(sc)
	case 7:
		var xs []any
		for range g.r.IntN(4) {
			xs = append(xs, g.value(sc, depth-1))
		}
		if xs == nil {
			return []any{}
		}
		return xs
	}
	return g.values(sc, depth-1)
}

// ussd is a USSD menu (docs/ussd.md) that fits any generated inputs
// schema: it collects amount (an integer) and sometimes note (text).
func (g *gen) ussd() obj {
	c := obj{{"service_code", pick(g, "*384*123#", "*1#", "*920*7*1#")}}
	if g.chance(0.3) {
		c.set("start", "main")
	}
	if g.chance(0.3) {
		c.set("max_chars", pick(g, 160, 182))
	}
	note, help := g.chance(0.5), g.chance(0.5)
	opts := []any{obj{{"label", "Pay"}, {"next", "amount"}}}
	if note {
		o := obj{{"label", "Pay with a note"}, {"next", "note"}}
		if g.chance(0.5) {
			o.set("value", pick[any](g, "noted", 2, true, 1.5))
		}
		if g.chance(0.5) {
			o.set("when", pick(g, "=size(trigger.body) == 0", "=!has(trigger.body.amount)"))
		}
		opts = append(opts, o)
	}
	if help {
		opts = append(opts, obj{{"label", "Help"}, {"next", "help"}})
	}
	screens := []any{obj{{"id", "main"}, {"type", "menu"}, {"text", pick(g, "Welcome", "Line one\nline two", "Quote \" and back\\slash")}, {"options", opts}}}
	v := obj{{"type", "integer"}, {"min", 1}, {"max", pick(g, 5000, 100000)}}
	if g.chance(0.5) {
		v.set("when", pick(g, "=trigger.body.amount % 2 == 0", "=trigger.body.amount > 10 && trigger.body.amount < 90000"))
	}
	amount := obj{{"id", "amount"}, {"type", "input"}, {"text", "Amount"}, {"input", "amount"}, {"validate", v}, {"next", "ok"}}
	if g.chance(0.5) {
		amount.set("error", "Whole naira, please.")
	}
	screens = append(screens, amount)
	if note {
		screens = append(screens, obj{{"id", "note"}, {"type", "input"}, {"text", "Note"}, {"input", "note"},
			{"validate", obj{{"pattern", "[A-Za-z ]{1,20}"}, {"min_length", 1}, {"max_length", 20}}}, {"next", "amount"}})
	}
	confirm := obj{{"id", "ok"}, {"type", "confirm"}, {"text", pick(g, "Pay {{amount}}?", "Pay {{ amount }} now?")}}
	if g.chance(0.5) {
		confirm.set("confirm_label", "Pay")
		confirm.set("cancel_label", "No")
	}
	if g.chance(0.5) {
		confirm.set("done", "Done. Ref {{reference}}")
	}
	screens = append(screens, confirm)
	if help {
		screens = append(screens, obj{{"id", "help"}, {"type", "end"}, {"text", "Call us."}})
	}
	c.set("screens", screens)
	if g.chance(0.5) {
		n := obj{{"sms", true}}
		if g.chance(0.5) {
			n.set("connection", "at_"+g.word(3))
			n.set("completed", "Paid {{amount}}. Ref {{reference}}")
		}
		c.set("notify", n)
	}
	return c
}

func (g *gen) text() string {
	return pick(g, "Disburse approved loan", "Pay salaries", "₦ payout — retry", "Line one\nline two", "Quote \" and backslash \\", "Tab\tand `tick` and ${x}", "日本語", "emoji 🚀", "a")
}

func (g *gen) word(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[g.r.IntN(len(letters))]
	}
	return string(b)
}

func (g *gen) source() string {
	return pick(g,
		"export default function main(input) {\n  return { ok: true };\n}\n",
		"const s = `template ${1 + 1}`;\nexport default () => s + '\\n';\n",
		"def main(input, host):\n    return {\"total\": sum(x[\"amount\"] for x in input[\"items\"])}\n",
		"// backslash \\ and backtick ` and ${notInterpolated}\r\nexport default () => 1;",
		"x",
	)
}

// expr returns a CEL expression valid where sc says.
func (g *gen) expr(sc scope) string {
	ref := g.ref(sc)
	if sc.secrets && g.chance(0.1) {
		return "=secrets.provider_token"
	}
	forms := []string{
		"%s",
		"%s + 1",
		"%s * 2 - 3",
		"-%s",
		"!%s",
		"%s == 'approved'",
		"%s != null",
		"%s > 10 && %s < 20 || %s == 0",
		"%s ? 'yes' : 'no'",
		"has(%s.field)",
		"size(%s) > 0",
		"%s.map(x, x * 2)",
		"%s.filter(x, x.amount > 100000)",
		"%s.exists(x, x == 'a')",
		"%s.all(x, x > 0)",
		"'a' in %s",
		"%s in ['a', 'b']",
		"string(%s)",
		"int(%s) / 100",
		"double(%s) * 1.5",
		"%s.startsWith('NG')",
		"%s.contains(\"q\\\"uote\")",
		"%s.matches('^[0-9]{10}$')",
		"%s['key with space']",
		"%s[0]",
		"{'a': %s, 'b': [1, 2.5, true, null]}",
		"[%s, 'it\\'s', \"tab\\t\"]",
		"%s + 'ñ — 🚀'",
		"(%s + 1) * (%s - 1)",
		"%s % 7 == 3",
		"%s.size()",
		"%s.lowerAscii()",
		"timestamp(%s) > timestamp('2026-01-01T00:00:00Z')",
		"duration('1h') > duration('30m') ? %s : null",
		"uint(%s) + 1u",
		"%s.exists_one(y, y == 1)",
		"type(%s) == string",
	}
	f := pick(g, forms...)
	return "=" + strings.ReplaceAll(f, "%s", ref)
}

func (g *gen) ref(sc scope) string {
	var refs []string
	refs = append(refs, "trigger.body.amount", "trigger.body.items", "trigger.headers['X-Signature']", "run.id", "env.region", "trigger.body.recipient.account_number")
	for _, id := range sc.steps {
		refs = append(refs, "steps."+id+".output.status", "steps."+id+".output")
	}
	if sc.foreach {
		refs = append(refs, "item", "item.amount", "index")
	}
	return pick(g, refs...)
}

// Numbers code cannot carry exactly are refused, not changed.
func TestGenerateRefusesInexactNumbers(t *testing.T) {
	for _, n := range []string{"2.0", "1e5", "-3.0E2", "9007199254740993", "-9007199254740993"} {
		doc := `{"schema":"wd/v1","id":"wf_n","version":1,"name":"n","trigger":{"type":"manual"},"steps":[{"id":"t","type":"transform","config":{"output":{"rate":` + n + `}}}],"settings":{}}`
		if _, err := flowcode.Generate(ctx, []byte(doc)); err == nil || !strings.Contains(err.Error(), "/steps/0/config/output/rate") {
			t.Errorf("%s: %v", n, err)
		}
	}
	for _, n := range []string{"2", "2.5", "1e-7", "9007199254740992", "0.1"} {
		doc := `{"schema":"wd/v1","id":"wf_n","version":1,"name":"n","trigger":{"type":"manual"},"steps":[{"id":"t","type":"transform","config":{"output":{"rate":` + n + `}}}],"settings":{}}`
		roundTrip(t, []byte(doc))
	}
}
