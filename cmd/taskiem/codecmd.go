package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/flowcode"
)

// findFiles lists files with suffix under paths (default: flows).
func findFiles(paths []string, suffix string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"flows"}
	}
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "node_modules" || (strings.HasPrefix(d.Name(), ".") && path != p)) {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), suffix) {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// sameJSON compares two documents as JSON values.
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// buildFlow compiles one .flow.ts and writes its .wd.json when the
// definition changed (formatting alone never rewrites a file). It reports
// whether the definition changed.
func buildFlow(ctx context.Context, file string, write bool) (bool, error) {
	def, err := flowcode.CompileFile(ctx, file)
	if err != nil {
		return false, err
	}
	target := strings.TrimSuffix(file, ".flow.ts") + ".wd.json"
	old, err := os.ReadFile(target)
	if err == nil && sameJSON(old, def) {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if write {
		if err := os.WriteFile(target, def, 0o644); err != nil { //nolint:gosec // a source file
			return false, err
		}
	}
	return true, nil
}

// buildCmd compiles workflow code (*.flow.ts) to definitions (*.wd.json).
func buildCmd(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	check := fs.Bool("check", false, "write nothing; fail if a definition is out of date (for CI)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files, err := findFiles(fs.Args(), ".flow.ts")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("build: no *.flow.ts files found")
	}
	ctx := context.Background()
	failed, stale := 0, 0
	for _, f := range files {
		changed, err := buildFlow(ctx, f, !*check)
		target := strings.TrimSuffix(f, ".flow.ts") + ".wd.json"
		switch {
		case err != nil:
			failed++
			fmt.Fprintf(stdout, "FAIL  %s\n      %v\n", f, err)
		case changed && *check:
			stale++
			fmt.Fprintf(stdout, "STALE %s does not match %s\n", target, f)
		case changed:
			fmt.Fprintf(stdout, "wrote %s\n", target)
		default:
			fmt.Fprintf(stdout, "ok    %s\n", f)
		}
	}
	if failed > 0 {
		return fmt.Errorf("build: %d of %d files failed", failed, len(files))
	}
	if stale > 0 {
		return fmt.Errorf("build: %d definitions are out of date; run taskiem build", stale)
	}
	return nil
}

// codegenCmd prints definitions as workflow code.
func codegenCmd(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("codegen", flag.ContinueOnError)
	write := fs.Bool("write", false, "write FILE.flow.ts next to each FILE.wd.json instead of printing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("codegen: give one or more *.wd.json files")
	}
	ctx := context.Background()
	for _, f := range fs.Args() {
		doc, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		code, err := flowcode.Generate(ctx, doc)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		if !*write {
			fmt.Fprint(stdout, code)
			continue
		}
		target := strings.TrimSuffix(f, ".wd.json") + ".flow.ts"
		if old, err := os.ReadFile(target); err == nil && bytes.Equal(old, []byte(code)) {
			fmt.Fprintf(stdout, "ok    %s\n", target)
			continue
		}
		if err := os.WriteFile(target, []byte(code), 0o644); err != nil { //nolint:gosec // a source file
			return err
		}
		fmt.Fprintf(stdout, "wrote %s\n", target)
	}
	return nil
}
