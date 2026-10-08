package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/policy"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

// builtinRegistry is the connectors compiled into this binary, for offline
// checks and tests.
func builtinRegistry() (*connector.Registry, error) {
	reg := connector.NewRegistry()
	return reg, builtin.Register(reg, builtin.Options{})
}

// testCmd runs workflow tests (*.test.json) offline (spec 10.5).
func testCmd(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	runPattern := fs.String("run", "", "only cases whose name matches this regular expression")
	verbose := fs.Bool("v", false, "list every case, not only failures")
	policyDir := fs.String("policies", "policies", "directory of approval policies (<name>.policy.json) the workflows use, if it exists")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var only *regexp.Regexp
	if *runPattern != "" {
		var err error
		if only, err = regexp.Compile(*runPattern); err != nil {
			return fmt.Errorf("test: -run: %w", err)
		}
	}
	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}
	files, err := wdtest.Discover(paths)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("test: no *.test.json files under %s", strings.Join(paths, ", "))
	}
	reg, err := builtinRegistry()
	if err != nil {
		return err
	}
	policies, err := loadPolicies(*policyDir)
	if err != nil {
		return err
	}
	passed, failed := 0, 0
	for _, path := range files {
		f, err := wdtest.Load(path)
		if err != nil {
			return err
		}
		if only != nil {
			kept := f.Cases[:0]
			for _, c := range f.Cases {
				if only.MatchString(c.Name) {
					kept = append(kept, c)
				}
			}
			if f.Cases = kept; len(kept) == 0 {
				continue
			}
		}
		for name, doc := range policies {
			if _, own := f.Policies[name]; !own {
				if f.Policies == nil {
					f.Policies = map[string]json.RawMessage{}
				}
				f.Policies[name] = doc
			}
		}
		results, err := f.Run(reg)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for _, r := range results {
			if r.Passed() {
				passed++
				if *verbose {
					fmt.Fprintf(stdout, "ok    %s: %s (%s)\n", path, r.Case, r.Duration.Round(1e5))
				}
				continue
			}
			failed++
			fmt.Fprintf(stdout, "FAIL  %s: %s\n", path, r.Case)
			for _, msg := range r.Failures {
				fmt.Fprintf(stdout, "      %s\n", msg)
			}
			fmt.Fprintln(stdout, "      events:")
			for _, line := range r.Trace {
				fmt.Fprintf(stdout, "        %s\n", line)
			}
		}
	}
	fmt.Fprintf(stdout, "%d passed, %d failed\n", passed, failed)
	if failed > 0 {
		return errors.New("test: some cases failed")
	}
	return nil
}

// loadPolicies reads dir/<name>.policy.json, as a Git-led sync does. A
// missing directory is no policies.
func loadPolicies(dir string) (map[string]json.RawMessage, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.policy.json"))
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // the repository's own policy files
		if err != nil {
			return nil, err
		}
		if _, err := policy.Parse(raw); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out[strings.TrimSuffix(filepath.Base(f), ".policy.json")] = raw
	}
	return out, nil
}
