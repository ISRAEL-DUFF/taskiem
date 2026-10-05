// Command licencecheck enforces the licence gate (decision 0007) for Go
// modules linked into this repository's packages and, optionally, npm
// packages reported by `pnpm licenses list --json`.
//
//	go run ./tools/licencecheck                       # Go only
//	go run ./tools/licencecheck -npm npm-licences.json
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/licensecheck"
)

type policy struct {
	Allowed    []string `json:"allowed"`
	Exceptions map[string]struct {
		Licence string `json:"licence"`
		Reason  string `json:"reason"`
	} `json:"exceptions"`
}

type finding struct {
	ecosystem, name, licence, problem string
}

func main() {
	policyPath := flag.String("policy", "tools/licencecheck/policy.json", "policy file")
	npmPath := flag.String("npm", "", "output of `pnpm licenses list --json` to check as well")
	verbose := flag.Bool("v", false, "list every dependency and its licence")
	flag.Parse()

	var pol policy
	raw, err := os.ReadFile(*policyPath)
	if err != nil {
		fatal(err)
	}
	if err := json.Unmarshal(raw, &pol); err != nil {
		fatal(err)
	}
	allowed := map[string]bool{}
	for _, l := range pol.Allowed {
		allowed[l] = true
	}
	ok := func(name string, licences []string) string {
		if len(licences) == 0 {
			if _, ex := pol.Exceptions[name]; ex {
				return ""
			}
			return "no licence detected"
		}
		for _, l := range licences {
			if !allowed[l] {
				if ex, found := pol.Exceptions[name]; found && ex.Licence == l {
					continue
				}
				return "licence not allowed: " + l
			}
		}
		return ""
	}

	var all []finding
	goMods, err := goModules()
	if err != nil {
		fatal(err)
	}
	for _, m := range goMods {
		ls := detect(m.dir)
		all = append(all, finding{"go", m.path + "@" + m.version, strings.Join(ls, " AND "), ok(m.path, ls)})
	}
	if *npmPath != "" {
		npm, err := npmPackages(*npmPath)
		if err != nil {
			fatal(err)
		}
		for name, ls := range npm {
			all = append(all, finding{"npm", name, strings.Join(ls, " OR "), okAny(allowed, pol, name, ls)})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ecosystem+all[i].name < all[j].ecosystem+all[j].name })

	bad := 0
	for _, f := range all {
		if f.problem != "" {
			bad++
			fmt.Printf("FAIL  %-4s %s: %s\n", f.ecosystem, f.name, f.problem)
		} else if *verbose {
			fmt.Printf("ok    %-4s %s: %s\n", f.ecosystem, f.name, f.licence)
		}
	}
	fmt.Printf("licencecheck: %d dependencies checked, %d failing\n", len(all), bad)
	if bad > 0 {
		os.Exit(1)
	}
}

// okAny accepts an npm package if any one of its alternative licences is allowed.
func okAny(allowed map[string]bool, pol policy, name string, alternatives []string) string {
	for _, l := range alternatives {
		for _, part := range strings.Split(strings.Trim(l, "()"), " OR ") {
			if allowed[strings.TrimSpace(part)] {
				return ""
			}
		}
		if ex, found := pol.Exceptions[name]; found && ex.Licence == l {
			return ""
		}
	}
	if len(alternatives) == 0 {
		return "no licence declared"
	}
	return "licence not allowed: " + strings.Join(alternatives, ", ")
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
	found := map[string]bool{}
	for _, e := range entries {
		n := strings.ToUpper(e.Name())
		isLicence := strings.HasPrefix(n, "LICENSE") || strings.HasPrefix(n, "LICENCE") || strings.HasPrefix(n, "COPYING")
		if e.IsDir() || !isLicence {
			continue
		}
		if ext := filepath.Ext(n); ext != "" && ext != ".TXT" && ext != ".MD" && !strings.HasPrefix(n, "LICENSE-") && !strings.HasPrefix(n, "LICENCE-") {
			continue
		}
		text, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		cov := licensecheck.Scan(text)
		if cov.Percent < 75 {
			found["UNKNOWN("+e.Name()+")"] = true
			continue
		}
		for _, m := range cov.Match {
			found[m.ID] = true
		}
	}
	var ls []string
	for l := range found {
		ls = append(ls, l)
	}
	sort.Strings(ls)
	return ls
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
