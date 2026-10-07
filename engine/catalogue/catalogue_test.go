package catalogue_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/catalogue"
	"github.com/israel-duff/taskiem/engine/catalogue/cataloguetest"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/connpkg"
	"github.com/israel-duff/taskiem/engine/wasmconn"
	"github.com/israel-duff/taskiem/engine/wasmconn/wasmtest"
)

func messages(fs []catalogue.Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.String() + "\n")
	}
	return b.String()
}

func TestParseID(t *testing.T) {
	for id, want := range map[string]string{"p_acme_ledger": "acme/ledger", "p_acme_ledger_v2": "acme/ledger_v2", "p_a_b": "", "p_acme": "", "x_acme_ledger": "",
		"p_Acme_ledger": "", "p_acme__ledger": "", "p_acme_" + strings.Repeat("a", 60): ""} {
		pub, name, ok := catalogue.ParseID(id)
		got := ""
		if ok {
			got = pub + "/" + name
		}
		if got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
}

func TestLintTheExample(t *testing.T) {
	manifest, _ := wasmtest.Example(t)
	if _, fs := catalogue.Lint(manifest, catalogue.LintOptions{}); len(catalogue.Errors(fs)) > 0 {
		t.Errorf("the example as a tenant connector:\n%s", messages(fs))
	}
	// In the catalogue it must be in the publisher's namespace.
	if _, fs := catalogue.Lint(manifest, catalogue.LintOptions{Publisher: "acme"}); !strings.Contains(messages(fs), "p_acme_<name>") {
		t.Errorf("namespace not enforced:\n%s", messages(fs))
	}
	p := cataloguetest.Package(t, "acme", "1.0.0", mustKey(), nil)
	if _, fs := catalogue.Lint([]byte(p.Manifest), catalogue.LintOptions{Publisher: "acme"}); len(catalogue.Errors(fs)) > 0 {
		t.Errorf("the example in the catalogue:\n%s", messages(fs))
	}
}

func mustKey() []byte {
	k, _ := cataloguetest.Key()
	return k
}

func TestLintCatchesDishonesty(t *testing.T) {
	base := cataloguetest.Package(t, "acme", "1.0.0", mustKey(), nil).Manifest
	cases := map[string]struct {
		edit func(string) string
		want string
	}{
		"a write declared read": {func(m string) string {
			return m + "  create_refund:\n    title: Refund a payment\n    class: read\n    input: { type: object, properties: {} }\n    output: { type: object, properties: {} }\n"
		}, "reads like a change"},
		"personal data undeclared": {func(m string) string {
			return strings.Replace(m, "    pii:\n      - { field: account_number, category: account_number }\n", "", 1)
		}, `"account_number" holds personal data`},
		"personal output undeclared": {func(m string) string {
			return strings.Replace(m, "        currency:  { type: string }", "        currency:  { type: string }\n        email:     { type: string }", 1)
		}, `"output.email" holds personal data`},
		"wildcard host": {func(m string) string {
			return strings.Replace(m, "base_url: https://ledger.example.com", "base_url: https://ledger.example.com\negress_hosts: ['*.example.com']", 1)
		}, "wildcards are not accepted"},
		"IP host": {func(m string) string {
			return strings.Replace(m, "base_url: https://ledger.example.com", "base_url: https://10.0.0.5", 1)
		}, "not an IP address"},
		"plain http": {func(m string) string {
			return strings.Replace(m, "base_url: https://ledger.example.com", "base_url: http://ledger.example.com", 1)
		}, "https"},
		"no description": {func(m string) string {
			return strings.Replace(m, "description: A minimal ledger API, to show how a WebAssembly connector is written.\n", "", 1)
		}, "/description"},
	}
	for name, c := range cases {
		_, fs := catalogue.Lint([]byte(c.edit(base)), catalogue.LintOptions{Publisher: "acme"})
		if !strings.Contains(messages(catalogue.Errors(fs)), c.want) {
			t.Errorf("%s: want an error with %q, got:\n%s", name, c.want, messages(fs))
		}
	}
	// An unsafe write is allowed, with a warning that says what it costs.
	unsafe := strings.Replace(base, "class: reconcilable_write", "class: unsafe_write", 1)
	unsafe = strings.Replace(unsafe, "    reconcile: get_payment\n", "", 1)
	if _, fs := catalogue.Lint([]byte(unsafe), catalogue.LintOptions{Publisher: "acme"}); len(catalogue.Errors(fs)) > 0 || !strings.Contains(messages(fs), "parks the run") {
		t.Errorf("unsafe write:\n%s", messages(fs))
	}
}

func parse(t *testing.T, src string) *connector.Manifest {
	t.Helper()
	m, probs := connector.Parse([]byte(src))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	return m
}

func TestDiffAndConsent(t *testing.T) {
	v1 := cataloguetest.Package(t, "acme", "1.0.0", mustKey(), nil).Manifest
	same := parse(t, strings.Replace(v1, "version: 1.0.0", "version: 1.0.1", 1))
	d := catalogue.Compare(parse(t, v1), same)
	if d.NeedsConsent || len(d.AddedHosts)+len(d.ClassChanges)+len(d.AddedActions) > 0 {
		t.Errorf("a patch with nothing new: %+v", d)
	}
	wider := parse(t, strings.Replace(strings.Replace(v1, "version: 1.0.0", "version: 1.1.0", 1),
		"base_url: https://ledger.example.com", "base_url: https://ledger.example.com\negress_hosts: [ledger.example.com, files.example.net]", 1))
	d = catalogue.Compare(parse(t, v1), wider)
	if !d.NeedsConsent || strings.Join(d.AddedHosts, ",") != "files.example.net" {
		t.Errorf("a new host: %+v", d)
	}
	reclassed := parse(t, strings.Replace(strings.Replace(strings.Replace(v1, "version: 1.0.0", "version: 2.0.0", 1), "class: reconcilable_write", "class: unsafe_write", 1), "    reconcile: get_payment\n", "", 1))
	d = catalogue.Compare(parse(t, v1), reclassed)
	if !d.NeedsConsent || len(d.ClassChanges) != 1 || d.ClassChanges[0].To != "unsafe_write" {
		t.Errorf("a class change: %+v", d)
	}
	// Semver: the class change is fine in a new major, not in a minor.
	pub := []catalogue.Published{{Version: "1.0.0", Manifest: parse(t, v1)}}
	if err := catalogue.Semver(reclassed, pub); err != nil {
		t.Errorf("new major: %v", err)
	}
	minor := parse(t, strings.Replace(strings.Replace(strings.Replace(v1, "version: 1.0.0", "version: 1.2.0", 1), "class: reconcilable_write", "class: unsafe_write", 1), "    reconcile: get_payment\n", "", 1))
	if err := catalogue.Semver(minor, pub); err == nil || !strings.Contains(err.Error(), "new major") {
		t.Errorf("class change in a minor: %v", err)
	}
	if err := catalogue.Semver(parse(t, v1), pub); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Errorf("same version again: %v", err)
	}
}

func TestAutomatedChecks(t *testing.T) {
	key, pubB64 := cataloguetest.Key()
	pub, _ := connpkg.ParsePublicKey(pubB64)
	ck := &catalogue.Checker{Resolver: cataloguetest.Public}
	ctx := context.Background()

	good := cataloguetest.Package(t, "acme", "1.0.0", key, nil)
	rep := ck.Run(ctx, good, "acme", pub, nil)
	if !rep.Passed {
		raw, _ := json.MarshalIndent(rep, "", " ")
		t.Fatalf("the example fails its checks:\n%s", raw)
	}
	if rep.Module == nil || len(rep.Module.Imports) == 0 || rep.Conformance == nil || len(rep.Conformance.Results) != 6 {
		t.Errorf("report: %+v", rep)
	}

	failed := func(name string, p *connpkg.Package, slug string, res catalogue.Checker, want string) {
		t.Helper()
		if res.Resolver == nil {
			res.Resolver = cataloguetest.Public
		}
		rep := res.Run(ctx, p, slug, pub, nil)
		raw, _ := json.Marshal(rep.Checks)
		if rep.Passed || !strings.Contains(strings.Join(rep.Failed(), ","), want) {
			t.Errorf("%s: want %s to fail: %s", name, want, raw)
		}
	}
	// Another publisher's key, or a package changed after signing.
	tampered := *good
	tampered.Licence = "MIT"
	failed("tampered", &tampered, "acme", catalogue.Checker{}, "signature")
	// Someone else's namespace.
	failed("namespace", good, "globex", catalogue.Checker{}, "manifest")
	// A host on a private address (DNS pointing inside).
	failed("private host", good, "acme", catalogue.Checker{Resolver: cataloguetest.Resolver{Addr: "10.1.2.3"}}, "hosts")
	failed("metadata host", good, "acme", catalogue.Checker{Resolver: cataloguetest.Resolver{Addr: "169.254.169.254"}}, "hosts")
	// A copyleft licence, or no attestation.
	gpl := cataloguetest.Package(t, "acme", "1.0.0", key, nil)
	gpl.Licence = "GPL-3.0-only"
	gpl.Sign(key)
	failed("licence", gpl, "acme", catalogue.Checker{}, "licence")
	noAttest := cataloguetest.Package(t, "acme", "1.0.0", key, nil)
	noAttest.Attestation.OriginalWork = false
	noAttest.Sign(key)
	failed("attestation", noAttest, "acme", catalogue.Checker{}, "attestation")
	// A case dropped: an action uncovered.
	short := cataloguetest.Package(t, "acme", "1.0.0", key, nil)
	cases := short.Conformance.Cases[:0]
	for _, c := range short.Conformance.Cases {
		if c.Action != "get_balance" {
			cases = append(cases, c)
		}
	}
	short.Conformance.Cases = cases
	short.Sign(key)
	failed("coverage", short, "acme", catalogue.Checker{}, "conformance")
	// A module that is not WebAssembly.
	junk := cataloguetest.Package(t, "acme", "1.0.0", key, nil)
	junk.Module = []byte("not wasm")
	junk.Sign(key)
	failed("module", junk, "acme", catalogue.Checker{}, "module")
	// A module over the size limit.
	failed("size", good, "acme", catalogue.Checker{Limits: wasmconn.Limits{MaxModule: 1024}}, "module")
}
