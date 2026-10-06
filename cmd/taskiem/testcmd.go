package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/connector"
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
