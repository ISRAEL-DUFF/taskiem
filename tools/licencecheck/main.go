// Command licencecheck enforces the licence gate (decision 0007) for Go
// modules linked into this repository's packages, code copied into the
// repository itself (third_party/, the vendored CPython build, fonts), and,
// optionally, npm packages reported by `pnpm licenses list --json`.
//
//	go run ./tools/licencecheck                       # Go and vendored code
//	go run ./tools/licencecheck -npm npm-licences.json
//
// The policy (policy.json) has three parts. "allowed" is the allow-list: an
// entry is a licence (allowed for every ecosystem) or {"licence", "ecosystems",
// "reason"} to allow it only for some. "exceptions" lets one module, package
// or vendored directory use one more licence; each names its ecosystem, so an
// exception for an npm package never covers a Go module. "vendored" lists the
// directories holding code copied into the repository, each with the licences
// it is under that no licence file shows (as CPython's in the WebAssembly
// build). A licence file anywhere else in the tree fails the gate until its
// directory is listed, so copied code cannot enter unreviewed.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/licensecheck"
)

// The ecosystems a finding, an allow-list entry or an exception belongs to.
const (
	ecoGo       = "go"
	ecoNPM      = "npm"
	ecoVendored = "vendored"
)

var ecosystems = map[string]bool{ecoGo: true, ecoNPM: true, ecoVendored: true}

type allowEntry struct {
	Licence    string   `json:"licence"`
	Ecosystems []string `json:"ecosystems"`
	Reason     string   `json:"reason"`
}

// UnmarshalJSON accepts a bare licence (allowed everywhere) or an object.
func (a *allowEntry) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*a = allowEntry{Licence: s}
		return nil
	}
	type plain allowEntry
	return json.Unmarshal(b, (*plain)(a))
}

type exception struct {
	Ecosystem string `json:"ecosystem"`
	Licence   string `json:"licence"`
	Reason    string `json:"reason"`
}

type vendoredDir struct {
	// Licences the code is under that its licence files do not show.
	Licences []string `json:"licences"`
	Reason   string   `json:"reason"`
}

type policy struct {
	Allowed    []allowEntry           `json:"allowed"`
	Exceptions map[string]exception   `json:"exceptions"`
	Vendored   map[string]vendoredDir `json:"vendored"`
}

func parsePolicy(raw []byte) (*policy, error) {
	var p policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	for _, a := range p.Allowed {
		if a.Licence == "" {
			return nil, errors.New("an allowed entry has no licence")
		}
		for _, e := range a.Ecosystems {
			if !ecosystems[e] {
				return nil, fmt.Errorf("allowed %s: unknown ecosystem %q (go, npm or vendored)", a.Licence, e)
			}
		}
	}
	for name, ex := range p.Exceptions {
		if !ecosystems[ex.Ecosystem] {
			return nil, fmt.Errorf("exception %s: ecosystem %q, want go, npm or vendored", name, ex.Ecosystem)
		}
		if ex.Licence == "" || ex.Reason == "" {
			return nil, fmt.Errorf("exception %s: needs a licence and a reason", name)
		}
	}
	for dir, v := range p.Vendored {
		if filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "." || strings.HasPrefix(dir, "..") {
			return nil, fmt.Errorf("vendored %q: want a clean path relative to the repository root", dir)
		}
		if v.Reason == "" {
			return nil, fmt.Errorf("vendored %s: needs a reason", dir)
		}
	}
	return &p, nil
}

// allows reports whether licence l is on the allow-list for ecosystem eco.
func (p *policy) allows(eco, l string) bool {
	for _, a := range p.Allowed {
		if a.Licence != l {
			continue
		}
		if len(a.Ecosystems) == 0 {
			return true
		}
		for _, e := range a.Ecosystems {
			if e == eco {
				return true
			}
		}
	}
	return false
}

// excepted reports whether name, in ecosystem eco, has an exception for l
// ("" for any licence, used when none is detected).
func (p *policy) excepted(eco, name, l string) bool {
	ex, ok := p.Exceptions[name]
	return ok && ex.Ecosystem == eco && (l == "" || ex.Licence == l)
}

// check needs every licence of a Go module or vendored directory allowed.
func (p *policy) check(eco, name string, licences []string) string {
	if len(licences) == 0 {
		if p.excepted(eco, name, "") {
			return ""
		}
		return "no licence detected"
	}
	for _, l := range licences {
		if !p.allows(eco, l) && !p.excepted(eco, name, l) {
			return "licence not allowed: " + l
		}
	}
	return ""
}

// checkAny accepts an npm package if any one of its alternative licences is allowed.
func (p *policy) checkAny(eco, name string, alternatives []string) string {
	for _, l := range alternatives {
		for _, part := range strings.Split(strings.Trim(l, "()"), " OR ") {
			if p.allows(eco, strings.TrimSpace(part)) {
				return ""
			}
		}
		if p.excepted(eco, name, l) {
			return ""
		}
	}
	if len(alternatives) == 0 {
		return "no licence declared"
	}
	return "licence not allowed: " + strings.Join(alternatives, ", ")
}

type finding struct {
	ecosystem, name, licence, problem string
}

func main() {
	policyPath := flag.String("policy", "tools/licencecheck/policy.json", "policy file")
	npmPath := flag.String("npm", "", "output of `pnpm licenses list --json` to check as well")
	root := flag.String("root", ".", "repository root, searched for vendored code")
	verbose := flag.Bool("v", false, "list every dependency and its licence")
	flag.Parse()

	raw, err := os.ReadFile(*policyPath)
	if err != nil {
		fatal(err)
	}
	pol, err := parsePolicy(raw)
	if err != nil {
		fatal(fmt.Errorf("%s: %w", *policyPath, err))
	}

	var all []finding
	goMods, err := goModules()
	if err != nil {
		fatal(err)
	}
	for _, m := range goMods {
		ls := detect(m.dir)
		all = append(all, finding{ecoGo, m.path + "@" + m.version, strings.Join(ls, " AND "), pol.check(ecoGo, m.path, ls)})
	}
	vend, err := vendored(*root, pol)
	if err != nil {
		fatal(err)
	}
	all = append(all, vend...)
	if *npmPath != "" {
		npm, err := npmPackages(*npmPath)
		if err != nil {
			fatal(err)
		}
		for name, ls := range npm {
			all = append(all, finding{ecoNPM, name, strings.Join(ls, " OR "), pol.checkAny(ecoNPM, name, ls)})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ecosystem+all[i].name < all[j].ecosystem+all[j].name })

	bad := 0
	for _, f := range all {
		if f.problem != "" {
			bad++
			fmt.Printf("FAIL  %-8s %s: %s\n", f.ecosystem, f.name, f.problem)
		} else if *verbose {
			fmt.Printf("ok    %-8s %s: %s\n", f.ecosystem, f.name, f.licence)
		}
	}
	fmt.Printf("licencecheck: %d dependencies checked, %d failing\n", len(all), bad)
	if bad > 0 {
		os.Exit(1)
	}
}

// skipDirs are never searched for vendored code: installed packages, build
// output, and directories starting with a dot (.git, .claude).
var skipDirs = map[string]bool{"node_modules": true, "dist": true, "bin": true, "test-results": true, "playwright-report": true}

// vendored checks the code copied into the repository under root. Every
// directory holding a licence file must be listed in the policy's vendored
// map; each listed directory must hold one, and its licences (those its
// files show plus those the policy declares) must pass as ecosystem
// "vendored".
func vendored(root string, pol *policy) ([]finding, error) {
	listed := map[string][]string{} // vendored dir -> licences found under it
	var out []finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()] || rel == "site") {
				return filepath.SkipDir
			}
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." || !isLicenceFile(d.Name()) {
			return nil // the repository's own licence, or not a licence file
		}
		owner := ""
		for v := range pol.Vendored {
			if (dir == v || strings.HasPrefix(dir, v+"/")) && len(v) > len(owner) {
				owner = v
			}
		}
		if owner == "" {
			out = append(out, finding{ecoVendored, rel, "", "licence file outside the vendored directories policy.json lists: list its directory there, with a reason, or remove it"})
			return nil
		}
		listed[owner] = append(listed[owner], classify(path, d.Name())...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for dir, v := range pol.Vendored {
		found, ok := listed[dir]
		if !ok {
			out = append(out, finding{ecoVendored, dir, "", "no licence file in a vendored directory"})
			continue
		}
		ls := uniq(append(found, v.Licences...))
		out = append(out, finding{ecoVendored, dir, strings.Join(ls, " AND "), pol.check(ecoVendored, dir, ls)})
	}
	return out, nil
}

func isLicenceFile(name string) bool {
	n := strings.ToUpper(name)
	if !strings.HasPrefix(n, "LICENSE") && !strings.HasPrefix(n, "LICENCE") && !strings.HasPrefix(n, "COPYING") {
		return false
	}
	ext := filepath.Ext(n)
	return ext == "" || ext == ".TXT" || ext == ".MD" || strings.HasPrefix(n, "LICENSE-") || strings.HasPrefix(n, "LICENCE-")
}

// classify names the licences in one licence file; text matching no known
// licence well enough is UNKNOWN(name), which no policy allows.
func classify(path, name string) []string {
	text, err := os.ReadFile(path)
	if err != nil {
		return []string{"UNKNOWN(" + name + ")"}
	}
	cov := licensecheck.Scan(text)
	if cov.Percent < 75 {
		return []string{"UNKNOWN(" + name + ")"}
	}
	var ls []string
	for _, m := range cov.Match {
		ls = append(ls, m.ID)
	}
	return ls
}

func uniq(ls []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range ls {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

type goModule struct{ path, version, dir string }

// goModules lists the non-standard modules providing packages that this
// module's packages (including tests) import.
func goModules() ([]goModule, error) {
	out, err := exec.Command("go", "list", "-deps", "-test", "-f",
		`{{if and (not .Standard) .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}|{{if .Module.Replace}}{{.Module.Replace.Dir}}{{end}}{{end}}`,
		"./...").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("go list: %s", ee.Stderr)
		}
		return nil, err
	}
	self, _ := exec.Command("go", "list", "-m").Output()
	seen := map[string]bool{}
	var mods []goModule
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		p := strings.Split(line, "|")
		if p[0] == strings.TrimSpace(string(self)) || seen[p[0]] {
			continue
		}
		seen[p[0]] = true
		dir := p[2]
		if p[3] != "" {
			dir = p[3]
		}
		mods = append(mods, goModule{p[0], p[1], dir})
	}
	return mods, nil
}

// detect classifies every licence file at the module root.
func detect(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var ls []string
	for _, e := range entries {
		if e.IsDir() || !isLicenceFile(e.Name()) {
			continue
		}
		ls = append(ls, classify(filepath.Join(dir, e.Name()), e.Name())...)
	}
	return uniq(ls)
}

// npmPackages reads `pnpm licenses list --json`: {licence: [{name, versions}]}.
func npmPackages(path string) (map[string][]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var byLicence map[string][]struct {
		Name     string   `json:"name"`
		Versions []string `json:"versions"`
	}
	if err := json.Unmarshal(raw, &byLicence); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := map[string][]string{}
	for lic, pkgs := range byLicence {
		for _, p := range pkgs {
			out[p.Name] = append(out[p.Name], lic)
		}
	}
	return out, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "licencecheck:", err)
	os.Exit(2)
}
