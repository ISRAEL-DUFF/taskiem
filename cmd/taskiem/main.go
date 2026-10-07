// Command taskiem is the single Taskiem binary: it migrates the database,
// validates contracts, runs any engine role (spec 15.4), and verifies audit
// exports.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/flowcode"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `taskiem — workflow automation engine

Usage:
  taskiem migrate [--dsn DSN]        apply database migrations (as the schema owner)
  taskiem validate FILE...           validate workflow code (*.flow.ts), definitions (*.wd.json) and connector manifests (*.yaml)
  taskiem build [--check] [PATH...]  compile workflow code (*.flow.ts) to definitions (*.wd.json) beside it
  taskiem codegen [--write] FILE...  print definitions (*.wd.json) as workflow code (*.flow.ts)
  taskiem test [-run RE] [-v] [PATH...]
                                     run workflow tests (*.test.json) with mocked steps, offline
  taskiem diff [PATH...]             show what deploying local *.wd.json files would change (default path: flows)
  taskiem deploy [--dry-run] [PATH...]
                                     save and publish changed workflows through the API
                                     (to every environment not gated on another)
  taskiem promote [--from staging] [--to prod] [PATH...]
                                     run in --to the version each workflow runs in --from
  taskiem dev [--flows DIR] [--dsn DSN]
                                     run a local engine with the web app; reload workflows and tests on change
  taskiem runs tail [--workflow ID] [RUN_ID]
                                     stream run events as they are recorded
  taskiem connector init [--id ID | --publisher SLUG] DIR
                                     start a WebAssembly connector from the example
  taskiem connector build [-o FILE] [DIR]
                                     compile a Go connector (sdk/connectorsdk) to WebAssembly
  taskiem connector validate MANIFEST [MODULE]
                                     lint a manifest strictly (classes, hosts, personal data) and check the module
  taskiem connector test [-v] [DIR]  run its conformance cases against the module in the sandbox, offline
  taskiem connector check MANIFEST MODULE
                                     load a connector as an upload would, offline
  taskiem connector push MANIFEST MODULE
                                     upload a connector version to your tenant
  taskiem connector keygen|package|verify|publisher|submit|submissions|publish|withdraw|revoke
                                     sign a package and take it through the public catalogue (taskiem connector, for details)
  taskiem catalogue reviewers|publishers|queue|show|review|revoke
                                     review catalogue submissions, verify publishers, revoke versions (operators; audited)
  taskiem serve [--role ROLE]        run an engine role: api, edge, orchestrator, scheduler, worker, all (default)
  taskiem bootstrap --tenant NAME --email EMAIL
                                     create the first tenant and its owner (password from $TASKIEM_BOOTSTRAP_PASSWORD)
  taskiem audit verify FILE          verify an audit export (GET /v1/audit/export) offline
  taskiem passkeys reset --email EMAIL
                                     remove someone's passkeys when no owner can (recovery; audited)
  taskiem tenants limits TENANT_ID [--set KEY=VALUE]...
                                     show a tenant's plan limits and usage, or change them (audited)
  taskiem tenants partner TENANT_ID [--max-subtenants N] [--subtenant-runs-per-day N] [--subtenant-runs-per-month N]
                          [--capabilities white_label,custom_domains] [--disable]
                                     make a tenant a partner (embedding), set its partner-wide caps (audited)
  taskiem billing plans [--file FILE] [--load]
                                     validate the plan catalogue (deploy/plans.yaml), or load it into the database
  taskiem billing grant TENANT_ID PLAN [--until YYYY-MM-DD]
                                     put a tenant on a plan without payment (design partners; audited)
  taskiem healthcheck                probe the local API (container health checks)
  taskiem version                    print the version

diff, deploy, promote, runs and connector push use $TASKIEM_URL (default http://localhost:8080) and an
API key in $TASKIEM_API_KEY, or --url and --key.
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
	case "test":
		return testCmd(args[1:], stdout)
	case "build":
		return buildCmd(args[1:], stdout)
	case "codegen":
		return codegenCmd(args[1:], stdout)
	case "diff":
		return diffCmd(args[1:], stdout)
	case "deploy":
		return deployCmd(args[1:], stdout)
	case "promote":
		return promoteCmd(args[1:], stdout)
	case "dev":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return devCmd(ctx, args[1:], stdout)
	case "runs":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runsCmd(ctx, args[1:], stdout)
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return serve(ctx, args[1:])
	case "bootstrap":
		return bootstrap(args[1:], stdout)
	case "audit":
		return auditCmd(args[1:], stdout)
	case "passkeys":
		return passkeysCmd(args[1:], stdout)
	case "tenants":
		return tenantsCmd(context.Background(), args[1:], stdout)
	case "billing":
		return billingCmd(context.Background(), args[1:], stdout)
	case "connector":
		return connectorCmd(context.Background(), args[1:], stdout)
	case "catalogue":
		return catalogueCmd(context.Background(), args[1:], stdout)
	case "healthcheck":
		return healthcheck()
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
	reg, err := builtinRegistry()
	if err != nil {
		return err
	}
	failed := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var probs []string
		if strings.HasSuffix(f, ".flow.ts") {
			def, err := flowcode.CompileFile(context.Background(), f)
			if err != nil {
				probs = append(probs, err.Error())
			} else {
				for _, p := range wdcheck.Check(def, reg) {
					probs = append(probs, p.String())
				}
			}
		}
		switch {
		case strings.HasSuffix(f, ".flow.ts"):
		case strings.HasSuffix(f, ".wd.json"):
			// The contract, plus this binary's connectors, code compilation
			// and trigger checks: what publishing would check.
			for _, p := range wdcheck.Check(src, reg) {
				probs = append(probs, p.String())
			}
		case strings.HasSuffix(f, ".yaml"), strings.HasSuffix(f, ".yml"):
			_, probs = connector.Parse(src)
		default:
			return fmt.Errorf("validate: %s: expected *.flow.ts, *.wd.json or a connector manifest (*.yaml)", filepath.Base(f))
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

func bootstrap(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	tenant := fs.String("tenant", "", "tenant name")
	email := fs.String("email", "", "owner's email")
	name := fs.String("name", "", "owner's name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pw := os.Getenv("TASKIEM_BOOTSTRAP_PASSWORD")
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	ctx := context.Background()
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	defer pool.Close()
	t, u, err := api.CreateTenant(ctx, pool, *tenant, *email, *name, pw)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	fmt.Fprintf(stdout, "tenant %s\nowner  %s (%s)\n", t, u, *email)
	return nil
}

// passkeysCmd recovers someone who lost every passkey, when no other owner
// can reset theirs from the web app.
func passkeysCmd(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "reset" {
		return errors.New("usage: taskiem passkeys reset --email EMAIL")
	}
	fs := flag.NewFlagSet("passkeys reset", flag.ContinueOnError)
	email := fs.String("email", "", "the person's email")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("usage: taskiem passkeys reset --email EMAIL")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	operator := "cli:" + env("USER", "operator")
	n, err := api.ResetPasskeys(ctx, pool, *email, operator)
	if err != nil {
		return fmt.Errorf("passkeys reset: %w", err)
	}
	fmt.Fprintf(stdout, "removed %d passkey(s) of %s and signed them out; they sign in with their password and enrol a new one\n", n, *email)
	return nil
}

func auditCmd(args []string, stdout io.Writer) error {
	usage := errors.New("usage: taskiem audit verify [--anchors FILE [--key BASE64]] FILE (an export from GET /v1/audit/export; - for stdin)")
	if len(args) < 1 || args[0] != "verify" {
		return usage
	}
	fs := flag.NewFlagSet("audit verify", flag.ContinueOnError)
	anchorsFile := fs.String("anchors", "", "anchors to check the export against: the JSON lines delivered outside the database, or GET /v1/audit/anchors")
	key := fs.String("key", "", "the anchor public key (base64), from GET /v1/audit/anchors")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usage
	}
	var r io.Reader = os.Stdin
	if fs.Arg(0) != "-" {
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	if *anchorsFile == "" {
		res, err := audit.Verify(r)
		if err != nil {
			return err
		}
		if res.FirstBroken != 0 {
			return fmt.Errorf("audit chain BROKEN at entry %d: %s (%d entries verified before it)", res.FirstBroken, res.Reason, res.Entries)
		}
		fmt.Fprintf(stdout, "audit chain intact: %d entries, every hash and link recomputed\n", res.Entries)
		return nil
	}
	raw, err := os.ReadFile(*anchorsFile)
	if err != nil {
		return err
	}
	anchors, err := readAnchorsFile(raw)
	if err != nil {
		return err
	}
	var pub ed25519.PublicKey
	if *key != "" {
		b, err := base64.StdEncoding.DecodeString(*key)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return errors.New("audit verify: --key must be a base64 Ed25519 public key")
		}
		pub = b
	}
	res, err := audit.VerifyWithAnchors(r, anchors, pub)
	if err != nil {
		return err
	}
	if res.FirstBroken != 0 {
		return fmt.Errorf("audit chain BROKEN at entry %d: %s", res.FirstBroken, res.Reason)
	}
	signed := "signatures not checked (no --key)"
	if pub != nil {
		signed = "every signature checked"
	}
	fmt.Fprintf(stdout, "audit chain intact: %d entries, and it matches all %d anchors (%s)\n", res.Entries, res.Anchors, signed)
	return nil
}

// readAnchorsFile accepts anchors as JSON lines, or the API's response.
func readAnchorsFile(raw []byte) ([]audit.Anchor, error) {
	var resp struct {
		Anchors []audit.Anchor `json:"anchors"`
	}
	if json.Unmarshal(raw, &resp) == nil && resp.Anchors != nil {
		return resp.Anchors, nil
	}
	return audit.ReadAnchors(bytes.NewReader(raw))
}

// healthcheck probes the local API's /readyz, for container health checks
// in images without a shell.
func healthcheck() error {
	addr := env("TASKIEM_LISTEN", ":8080")
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/readyz")
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("readyz: %s", resp.Status)
	}
	return nil
}
