// Command taskiem is the single Taskiem binary. In Phase 0 it migrates the
// database and validates contracts; the engine roles arrive in Phase 1.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/wd"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `taskiem — workflow automation engine

Usage:
  taskiem migrate [--dsn DSN]        apply database migrations (as the schema owner)
  taskiem validate FILE...           validate workflow definitions (*.wd.json) and connector manifests (*.yaml)
  taskiem serve --role ROLE          run an engine role: api, edge, orchestrator, scheduler, worker, all
  taskiem version                    print the version
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "taskiem:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("missing command")
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "migrate":
		return migrate(args[1:], stdout)
	case "validate":
		return validate(args[1:], stdout)
	case "serve":
		return serve(args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	}
	fmt.Fprint(stderr, usage)
	return fmt.Errorf("unknown command %q", args[0])
}

func migrate(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dsn := fs.String("dsn", os.Getenv("TASKIEM_DATABASE_URL"), "Postgres DSN (default $TASKIEM_DATABASE_URL)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dsn == "" {
		return errors.New("migrate: --dsn or TASKIEM_DATABASE_URL is required")
	}
	results, err := db.Migrate(context.Background(), *dsn)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Fprintln(stdout, "migrate: up to date")
	}
	for _, r := range results {
		fmt.Fprintf(stdout, "migrate: applied %s (%s)\n", r.Source.Path, r.Duration.Round(1e6))
	}
	return nil
}

func validate(files []string, stdout io.Writer) error {
	if len(files) == 0 {
		return errors.New("validate: no files given")
	}
	failed := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var probs []string
		switch {
		case strings.HasSuffix(f, ".wd.json"):
			for _, p := range wd.Validate(src) {
				probs = append(probs, p.String())
			}
		case strings.HasSuffix(f, ".yaml"), strings.HasSuffix(f, ".yml"):
			_, probs = connector.Parse(src)
		default:
			return fmt.Errorf("validate: %s: expected *.wd.json or a connector manifest (*.yaml)", filepath.Base(f))
		}
		if len(probs) == 0 {
			fmt.Fprintf(stdout, "ok    %s\n", f)
			continue
		}
		failed++
		fmt.Fprintf(stdout, "FAIL  %s\n", f)
		for _, p := range probs {
			fmt.Fprintf(stdout, "      %s\n", p)
		}
	}
	if failed > 0 {
		return fmt.Errorf("validate: %d of %d files invalid", failed, len(files))
	}
	return nil
}

var roles = map[string]bool{"api": true, "edge": true, "orchestrator": true, "scheduler": true, "worker": true, "all": true}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	role := fs.String("role", "all", "api, edge, orchestrator, scheduler, worker, or all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !roles[*role] {
		return fmt.Errorf("serve: unknown role %q", *role)
	}
	return fmt.Errorf("serve: role %q is built in Phase 1; Phase 0 provides migrate and validate", *role)
}
