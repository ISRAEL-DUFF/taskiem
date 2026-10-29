package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mitText = `MIT License

Copyright (c) 2025 Someone

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`

func mustPolicy(t *testing.T, raw string) *policy {
	t.Helper()
	p, err := parsePolicy([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The repository's own policy: Python-2.0 is allowed only in the npm tree
// (decision 0007), and an npm package's exception never covers a Go module.
func TestPolicyEcosystems(t *testing.T) {
	raw, err := os.ReadFile("policy.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := parsePolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.checkAny(ecoNPM, "some-npm-package", []string{"Python-2.0"}); got != "" {
		t.Errorf("npm Python-2.0: %q, want allowed", got)
	}
	if got := p.check(ecoGo, "example.com/py", []string{"Python-2.0"}); got == "" {
		t.Error("a Go module under Python-2.0 passed; decision 0007 allows it for TypeScript's tree only")
	}
	if got := p.check(ecoGo, "example.com/x", []string{"MIT"}); got != "" {
		t.Errorf("Go MIT: %q", got)
	}
	if got := p.check(ecoGo, "axe-core", []string{"MPL-2.0"}); got == "" {
		t.Error("the npm axe-core exception covered a Go module of the same name")
	}
	if got := p.checkAny(ecoNPM, "axe-core", []string{"MPL-2.0"}); got != "" {
		t.Errorf("npm axe-core: %q, want its exception to apply", got)
	}
}

func TestPolicyRejectsExceptionWithoutEcosystem(t *testing.T) {
	for _, raw := range []string{
		`{"allowed":["MIT"],"exceptions":{"x":{"licence":"MPL-2.0","reason":"r"}}}`,
		`{"allowed":["MIT"],"exceptions":{"x":{"ecosystem":"pypi","licence":"MPL-2.0","reason":"r"}}}`,
		`{"allowed":[{"licence":"MIT","ecosystems":["cargo"]}]}`,
		`{"allowed":["MIT"],"vendored":{"../outside":{"reason":"r"}}}`,
		`{"allowed":["MIT"],"vendored":{"third_party/x":{}}}`,
	} {
		if _, err := parsePolicy([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func write(t *testing.T, root, rel, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func problems(fs []finding) map[string]string {
	m := map[string]string{}
	for _, f := range fs {
		m[f.name] = f.problem
	}
	return m
}

// Code copied into the repository is checked: its licence files must be in
// a listed vendored directory, and its licences (found or declared) pass.
func TestVendoredCode(t *testing.T) {
	p := mustPolicy(t, `{
	  "allowed": ["MIT", {"licence": "Python-2.0", "ecosystems": ["npm"]}],
	  "vendored": {
	    "third_party/ok": {"reason": "r"},
	    "third_party/unknown": {"reason": "r"},
	    "third_party/missing": {"reason": "r"},
	    "engine/py": {"licences": ["Python-2.0"], "reason": "r"},
	    "engine/pyex": {"licences": ["Python-2.0"], "reason": "r"}
	  },
	  "exceptions": {"engine/pyex": {"ecosystem": "vendored", "licence": "Python-2.0", "reason": "r"}}
	}`)
	root := t.TempDir()
	write(t, root, "LICENSE", "the repository's own licence is not vendored code")
	write(t, root, "third_party/ok/LICENSE", mitText)
	write(t, root, "third_party/ok/sub/COPYING", mitText)
	write(t, root, "third_party/unknown/LICENSE", "All rights reserved. Not for redistribution.")
	write(t, root, "third_party/missing/code.go", "package missing")
	write(t, root, "engine/py/LICENSE", mitText)
	write(t, root, "engine/pyex/LICENSE.txt", mitText)
	write(t, root, "engine/copied/LICENSE", mitText)
	write(t, root, "web/node_modules/pkg/LICENSE", "skipped")
	write(t, root, ".claude/worktrees/x/LICENSE", "skipped")
	write(t, root, "site/fonts/LICENSE", "skipped: the built site")

	fs, err := vendored(root, p)
	if err != nil {
		t.Fatal(err)
	}
	got := problems(fs)
	want := map[string]string{
		"third_party/ok":        "",
		"third_party/unknown":   "licence not allowed: UNKNOWN(LICENSE)",
		"third_party/missing":   "no licence file in a vendored directory",
		"engine/py":             "licence not allowed: Python-2.0",
		"engine/pyex":           "",
		"engine/copied/LICENSE": "licence file outside the vendored directories",
	}
	if len(got) != len(want) {
		t.Errorf("findings %v, want %v", got, want)
	}
	for name, w := range want {
		g, ok := got[name]
		switch {
		case !ok:
			t.Errorf("%s: no finding", name)
		case w == "" && g != "":
			t.Errorf("%s: %q, want it to pass", name, g)
		case w != "" && !strings.HasPrefix(g, w):
			t.Errorf("%s: %q, want %q", name, g, w)
		}
	}
}

// The repository as it is passes, and finds the vendored CPython build.
func TestRepositoryVendoredCode(t *testing.T) {
	raw, err := os.ReadFile("policy.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := parsePolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := vendored("../..", p)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, f := range fs {
		if f.problem != "" {
			t.Errorf("%s: %s", f.name, f.problem)
		}
		if f.name == "engine/sandbox/pywasm" {
			seen = true
			if !strings.Contains(f.licence, "Python-2.0") || !strings.Contains(f.licence, "Apache-2.0") {
				t.Errorf("pywasm licences: %s", f.licence)
			}
		}
	}
	if !seen {
		t.Error("engine/sandbox/pywasm was not checked")
	}
}
