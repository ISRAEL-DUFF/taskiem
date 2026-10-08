package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/israel-duff/taskiem/engine/catalogue"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/connpkg"
	"github.com/israel-duff/taskiem/engine/conntest"
	"github.com/israel-duff/taskiem/engine/wasmconn"
	"github.com/israel-duff/taskiem/examples"
)

const connectorUsage = `usage:
  taskiem connector init [--id ID | --publisher SLUG] [--name NAME] DIR
      start a connector from the example (handlers, manifest, fixtures, conformance cases)
  taskiem connector build [-o FILE] [DIR]
      compile it to WebAssembly (DIR/connector.wasm)
  taskiem connector validate [--publisher SLUG] MANIFEST [MODULE]
      lint the manifest strictly (classes, hosts, personal data) and check the module's imports and memory
  taskiem connector test [-v] [DIR]
      run the conformance cases (DIR/testdata/conformance) against DIR/connector.wasm in the sandbox
  taskiem connector check MANIFEST MODULE
      load the pair as an upload would
  taskiem connector push MANIFEST MODULE
      upload a version of your own (x_) connector to your tenant
  taskiem connector keygen [-o FILE]
      make a publisher signing key (Ed25519); register the public key with 'connector publisher'
  taskiem connector package --key FILE --licence SPDX --contact EMAIL --original [--source-url URL] [-o FILE] [DIR]
      validate, test, and write a signed package for the catalogue
  taskiem connector verify [--public-key KEY] PACKAGE
      check a package's digest and signature
  taskiem connector publisher [--slug SLUG --name NAME --public-key KEY]
      show your publisher namespace, or ask for one / rotate its key
  taskiem connector submit PACKAGE
      submit a package to the public catalogue (automated checks, then review)
  taskiem connector submissions
      list your submissions and their state
  taskiem connector publish|withdraw ID
  taskiem connector revoke ID --reason TEXT
      publish an approved version; withdraw one; revoke a published one (installing tenants are alerted)`

var slugish = regexp.MustCompile(`[^a-z0-9_]+`)

// connectorInit copies the example connector into dir under a new id.
func connectorInit(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("id", "", "connector id (x_<name>, or p_<publisher>_<name>)")
	publisher := fs.String("publisher", "", "publish under this namespace: the id is p_<publisher>_<name>")
	name := fs.String("name", "", "display name")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return errors.New(connectorUsage)
	}
	dir := fs.Arg(0)
	base := strings.Trim(slugish.ReplaceAllString(strings.ToLower(filepath.Base(dir)), "_"), "_")
	if base == "" || base[0] < 'a' || base[0] > 'z' {
		base = "connector"
	}
	switch {
	case *id != "":
	case *publisher != "":
		*id = connector.CataloguePrefix + *publisher + "_" + base
	default:
		*id = connector.TenantPrefix + base
	}
	if _, _, ok := catalogue.ParseID(*id); !ok && !regexp.MustCompile(`^x_[a-z0-9_]{1,61}$`).MatchString(*id) {
		return fmt.Errorf("connector init: %q is not a connector id: x_<name> for your own, p_<publisher>_<name> for the catalogue", *id)
	}
	if *name == "" {
		*name = strings.ToUpper(base[:1]) + strings.ReplaceAll(base[1:], "_", " ")
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("connector init: %s is not empty", dir)
	}
	const src = "wasm-connector"
	err := iofs.WalkDir(examples.WasmConnector, src, func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, src), "/")
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		b, err := examples.WasmConnector.ReadFile(p)
		if err != nil {
			return err
		}
		b = bytes.ReplaceAll(b, []byte(examples.WasmConnectorID), []byte(*id))
		if path.Base(p) == "manifest.yaml" {
			b = bytes.Replace(b, []byte("name: Example ledger"), []byte("name: "+*name), 1)
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		return fmt.Errorf("connector init: %w", err)
	}
	fmt.Fprintf(stdout, "created %s (%s) from the example ledger connector\n", dir, *id)
	if !insideModule(dir) {
		mod := "example.com/" + base
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+mod+"\n\ngo 1.26\n"), 0o600); err != nil {
			return err
		}
		ver := "latest"
		if version != "dev" {
			ver = version
		}
		fmt.Fprintf(stdout, "next: cd %s && go get github.com/israel-duff/taskiem/sdk/connectorsdk@%s && go mod tidy\n", dir, ver)
	}
	fmt.Fprintf(stdout, "then: edit manifest.yaml and main.go, record fixtures in testdata/, and run\n  taskiem connector build %s && taskiem connector test %s\n", dir, dir)
	return nil
}

// insideModule reports whether dir is inside a Go module (a go.mod above it).
func insideModule(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for d := filepath.Dir(abs); ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return true
		}
		if filepath.Dir(d) == d {
			return false
		}
	}
}

// lintFor lints a manifest for the catalogue when its id is in a
// namespace, otherwise as a tenant's own connector.
func lintFor(raw []byte, publisher string) (*connector.Manifest, []catalogue.Finding) {
	if publisher == "" {
		if m, probs := connector.Parse(raw); len(probs) == 0 {
			publisher, _, _ = catalogue.ParseID(m.ID)
		}
	}
	return catalogue.Lint(raw, catalogue.LintOptions{Publisher: publisher})
}

func connectorValidate(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	publisher := fs.String("publisher", "", "the namespace a catalogue manifest must be in (default: from its id)")
	if err := fs.Parse(args); err != nil || fs.NArg() < 1 || fs.NArg() > 2 {
		return errors.New(connectorUsage)
	}
	raw, err := os.ReadFile(fs.Arg(0)) //nolint:gosec // a path the user named
	if err != nil {
		return err
	}
	m, findings := lintFor(raw, *publisher)
	for _, f := range findings {
		fmt.Fprintln(stdout, f.String())
	}
	errs := catalogue.Errors(findings)
	if fs.NArg() == 2 && len(errs) == 0 {
		module, err := os.ReadFile(fs.Arg(1)) //nolint:gosec // a path the user named
		if err != nil {
			return err
		}
		rt, err := wasmconn.New(ctx, wasmconn.Limits{})
		if err != nil {
			return err
		}
		defer func() { _ = rt.Close(ctx) }()
		info, err := rt.Inspect(ctx, module)
		if err != nil {
			return fmt.Errorf("module: %w", err)
		}
		if _, err := loadAny(ctx, rt, raw, module); err != nil {
			return fmt.Errorf("module: %w", err)
		}
		fmt.Fprintf(stdout, "module: %d bytes, memory %d of %d pages, imports %s\n", info.Size, info.MemoryMinPages, info.LimitPages, strings.Join(info.Imports, ", "))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %d error(s)", fs.Arg(0), len(errs))
	}
	fmt.Fprintf(stdout, "%s %s is valid (%d warning(s))\n", m.ID, m.Version, len(findings))
	return nil
}

// testPaths resolves a connector directory's manifest, module and testdata.
func testPaths(dir string) (manifest, module, testdata string) {
	return filepath.Join(dir, "manifest.yaml"), filepath.Join(dir, "connector.wasm"), filepath.Join(dir, "testdata")
}

// runConformance loads a connector in the sandbox and runs its suite.
func runConformance(ctx context.Context, manifestPath, modulePath string, suite *conntest.Suite) (*connector.Connector, conntest.Report, error) {
	manifest, err := os.ReadFile(manifestPath) //nolint:gosec // a path the user named
	if err != nil {
		return nil, conntest.Report{}, err
	}
	module, err := os.ReadFile(modulePath) //nolint:gosec // a path the user named
	if errors.Is(err, os.ErrNotExist) {
		return nil, conntest.Report{}, fmt.Errorf("%s does not exist: run taskiem connector build first", modulePath)
	}
	if err != nil {
		return nil, conntest.Report{}, err
	}
	rt, err := wasmconn.New(ctx, wasmconn.Limits{})
	if err != nil {
		return nil, conntest.Report{}, err
	}
	defer func() { _ = rt.Close(ctx) }()
	c, err := loadAny(ctx, rt, manifest, module)
	if err != nil {
		return nil, conntest.Report{}, err
	}
	return c, conntest.Run(ctx, c, suite, conntest.Options{}), nil
}

func printReport(w io.Writer, rep conntest.Report, verbose bool) {
	for _, r := range rep.Results {
		status := "ok  "
		if !r.Pass {
			status = "FAIL"
		}
		if verbose || !r.Pass {
			fmt.Fprintf(w, "%s %s (%s)\n", status, r.Case, r.Action)
		}
		for _, p := range r.Problems {
			fmt.Fprintf(w, "       %s\n", p)
		}
	}
	if len(rep.Uncovered) > 0 {
		fmt.Fprintf(w, "no case for: %s (the catalogue needs one per action)\n", strings.Join(rep.Uncovered, ", "))
	}
	if len(rep.KeyNotSent) > 0 {
		fmt.Fprintf(w, "the idempotency key never reached the provider in: %s (an idempotent write must send it)\n", strings.Join(rep.KeyNotSent, ", "))
	}
	if len(rep.ReadWrites) > 0 {
		fmt.Fprintf(w, "note: read action(s) sent something other than GET or HEAD: %s (reviewers will check they change nothing)\n", strings.Join(rep.ReadWrites, ", "))
	}
}

func connectorTest(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	verbose := fs.Bool("v", false, "list every case")
	if err := fs.Parse(args); err != nil || fs.NArg() > 1 {
		return errors.New(connectorUsage)
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	manifest, module, testdata := testPaths(dir)
	suite, err := conntest.LoadDir(testdata)
	if err != nil {
		return err
	}
	_, rep, err := runConformance(ctx, manifest, module, suite)
	if err != nil {
		return err
	}
	printReport(stdout, rep, *verbose)
	if !rep.Passed() {
		return fmt.Errorf("%d of %d conformance case(s) failed", rep.Failed(), len(rep.Results))
	}
	fmt.Fprintf(stdout, "%d conformance case(s) passed in the sandbox\n", len(rep.Results))
	return nil
}

func connectorKeygen(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector keygen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := fs.String("o", "publisher.key", "where to write the private key (keep it secret)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return errors.New(connectorUsage)
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s exists; not overwriting a key", *out)
	}
	seed, pub, err := connpkg.GenerateKey()
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(seed+"\n"), 0o600); err != nil {
		return err
	}
	key, _ := connpkg.ParsePublicKey(pub)
	fmt.Fprintf(stdout, "wrote the private key to %s (keep it secret; anyone with it can sign as you)\npublic key: %s\nkey id:     %s\n", *out, pub, connpkg.KeyID(key))
	return nil
}

func connectorPackage(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector package", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keyFile := fs.String("key", "", "the publisher's private key (connector keygen)")
	licence := fs.String("licence", "", "SPDX licence identifier, or LicenseRef-Proprietary")
	contact := fs.String("contact", "", "an email address reviewers can reach about this version")
	original := fs.Bool("original", false, "attest that this is original work you may publish, copying no other product's connector")
	source := fs.String("source-url", "", "where the source can be read (optional)")
	out := fs.String("o", "", "output file (default DIR/<id>-<version>.tcpkg)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 1 || *keyFile == "" || *licence == "" {
		return errors.New(connectorUsage)
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	seed, err := os.ReadFile(*keyFile) //nolint:gosec // a path the user named
	if err != nil {
		return err
	}
	key, err := connpkg.ParsePrivateKey(string(seed))
	if err != nil {
		return err
	}
	manifestPath, modulePath, testdata := testPaths(dir)
	raw, err := os.ReadFile(manifestPath) //nolint:gosec // a path the user named
	if err != nil {
		return err
	}
	m, findings := lintFor(raw, "")
	if errs := catalogue.Errors(findings); len(errs) > 0 {
		for _, f := range errs {
			fmt.Fprintln(stdout, f.String())
		}
		return fmt.Errorf("the manifest has %d error(s); fix them before packaging", len(errs))
	}
	if _, _, ok := catalogue.ParseID(m.ID); !ok {
		return fmt.Errorf("%s is not a catalogue id: catalogue connectors are p_<publisher>_<name> (connector init --publisher)", m.ID)
	}
	if !catalogue.Licences[*licence] {
		return fmt.Errorf("licence %q is not accepted in the catalogue (docs/connector-submissions.md)", *licence)
	}
	suite, err := conntest.LoadDir(testdata)
	if err != nil {
		return err
	}
	_, rep, err := runConformance(ctx, manifestPath, modulePath, suite)
	if err != nil {
		return err
	}
	if !rep.Passed() || len(rep.Uncovered) > 0 || len(rep.KeyNotSent) > 0 {
		printReport(stdout, rep, false)
		return errors.New("the conformance suite must pass, cover every action, and see every idempotent write send its key")
	}
	module, err := os.ReadFile(modulePath) //nolint:gosec // a path the user named
	if err != nil {
		return err
	}
	p := &connpkg.Package{Format: connpkg.Format, ID: m.ID, Version: m.Version, Manifest: string(raw), Module: module, Licence: *licence, SourceURL: *source,
		Conformance: suite.Inline(), Attestation: connpkg.Attestation{OriginalWork: *original, Contact: *contact}, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	p.Sign(key)
	if *out == "" {
		*out = filepath.Join(dir, m.ID+"-"+m.Version+".tcpkg")
	}
	var buf bytes.Buffer
	if err := connpkg.Write(&buf, p); err != nil {
		return err
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "packaged %s %s: %s\n  digest %s\n  signed with key %s\n", m.ID, m.Version, *out, p.Digest, p.KeyID)
	if !*original || !strings.Contains(*contact, "@") {
		fmt.Fprintln(stdout, "note: submissions need --original and a --contact email address; this package will fail the attestation check")
	}
	return nil
}

func readPackage(path string) (*connpkg.Package, error) {
	f, err := os.Open(path) //nolint:gosec // a path the user named
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return connpkg.Read(f)
}

func connectorVerify(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pubKey := fs.String("public-key", "", "the publisher's public key (base64); without it only the digest is checked")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return errors.New(connectorUsage)
	}
	p, err := readPackage(fs.Arg(0))
	if err != nil {
		return err
	}
	if p.Digest != p.ComputeDigest() {
		return connpkg.ErrDigest
	}
	signed := "signature not checked (no --public-key)"
	if *pubKey != "" {
		key, err := connpkg.ParsePublicKey(*pubKey)
		if err != nil {
			return err
		}
		if err := p.Verify(key); err != nil {
			return err
		}
		signed = "signature verifies with key " + p.KeyID
	}
	cases := 0
	if p.Conformance != nil {
		cases = len(p.Conformance.Cases)
	}
	fmt.Fprintf(stdout, "%s %s: digest %s matches; %s\n  licence %s; module %d bytes (sha256 %s); %d conformance case(s)\n",
		p.ID, p.Version, p.Digest, signed, p.Licence, len(p.Module), p.ModuleDigest(), cases)
	return nil
}

func connectorPublisher(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector publisher", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	remote := remoteFlags(fs)
	slug := fs.String("slug", "", "the namespace to ask for")
	name := fs.String("name", "", "the publisher's display name")
	pub := fs.String("public-key", "", "the signing key's public half (connector keygen)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return errors.New(connectorUsage)
	}
	c, err := remote()
	if err != nil {
		return err
	}
	var out struct {
		Slug   string `json:"slug"`
		Name   string `json:"name"`
		Status string `json:"status"`
		KeyID  string `json:"key_id"`
	}
	if *name == "" && *pub == "" {
		err = c.do(ctx, "GET", "/v1/catalogue/publisher", nil, &out)
	} else {
		err = c.do(ctx, "PUT", "/v1/catalogue/publisher", map[string]string{"slug": *slug, "name": *name, "public_key": *pub}, &out)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "publisher %s (%s): %s; signing key %s\n", out.Slug, out.Name, out.Status, out.KeyID)
	if out.Status == "pending" {
		fmt.Fprintln(stdout, "Taskiem verifies publishers before they can submit; you will hear from us at your contact address")
	}
	return nil
}

func connectorSubmit(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector submit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	remote := remoteFlags(fs)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return errors.New(connectorUsage)
	}
	// Read it as a package first: a file that is not one never leaves.
	p, err := readPackage(fs.Arg(0))
	if err != nil {
		return err
	}
	if p.Digest != p.ComputeDigest() {
		return connpkg.ErrDigest
	}
	c, err := remote()
	if err != nil {
		return err
	}
	c.http.Timeout = 5 * time.Minute
	var out struct {
		ID, Connector, Version, State string
		Checks                        catalogue.Report
	}
	if err := c.do(ctx, "POST", "/v1/catalogue/submissions", p, &out); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "submitted %s %s as %s: %s\n", out.Connector, out.Version, out.ID, out.State)
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	for _, ch := range out.Checks.Checks {
		mark := "pass"
		if !ch.Pass {
			mark = "FAIL"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", mark, ch.Name, ch.Detail)
	}
	_ = tw.Flush()
	if out.State == "checks_failed" {
		return errors.New("automated checks failed; fix them and submit again")
	}
	fmt.Fprintln(stdout, "a reviewer outside your organisation will look at it next")
	return nil
}

func connectorSubmissions(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector submissions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	remote := remoteFlags(fs)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return errors.New(connectorUsage)
	}
	c, err := remote()
	if err != nil {
		return err
	}
	var out struct {
		Submissions []struct {
			ID, Connector, Version, State string
			SubmittedAt                   time.Time `json:"submitted_at"`
			ReviewNote                    *string   `json:"review_note"`
		}
	}
	if err := c.do(ctx, "GET", "/v1/catalogue/submissions", nil, &out); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCONNECTOR\tVERSION\tSTATE\tSUBMITTED\tREVIEW NOTE")
	for _, s := range out.Submissions {
		note := ""
		if s.ReviewNote != nil {
			note = *s.ReviewNote
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Connector, s.Version, s.State, s.SubmittedAt.Format(time.DateTime), note)
	}
	return tw.Flush()
}

func connectorMove(ctx context.Context, verb string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	remote := remoteFlags(fs)
	reason := fs.String("reason", "", "why (revoke: installing tenants are told)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return errors.New(connectorUsage)
	}
	c, err := remote()
	if err != nil {
		return err
	}
	var body any
	if *reason != "" {
		body = map[string]string{"reason": *reason}
	}
	var out map[string]any
	// Find the submission among the publisher's own: an id or a unique
	// prefix of one, or CONNECTOR@VERSION.
	var list struct {
		Submissions []struct {
			ID, Connector, Version, State string
		}
	}
	if err := c.do(ctx, "GET", "/v1/catalogue/submissions", nil, &list); err != nil {
		return err
	}
	id := ""
	for _, s := range list.Submissions {
		if strings.HasPrefix(s.ID, fs.Arg(0)) || s.Connector+"@"+s.Version == fs.Arg(0) {
			if id != "" && id != s.ID && s.State != "withdrawn" && s.State != "rejected" && s.State != "checks_failed" {
				return fmt.Errorf("connector %s: %q matches more than one submission; give the full id", verb, fs.Arg(0))
			}
			if id == "" || (s.State != "withdrawn" && s.State != "rejected" && s.State != "checks_failed") {
				id = s.ID
			}
		}
	}
	if id == "" {
		return fmt.Errorf("connector %s: no submission %q (taskiem connector submissions lists them)", verb, fs.Arg(0))
	}
	action := map[string]string{"publish": "/publish", "withdraw": "/withdraw", "revoke": "/revoke"}[verb]
	if err := c.do(ctx, "POST", "/v1/catalogue/submissions/"+id+action, body, &out); err != nil {
		return err
	}
	b, _ := json.Marshal(out)
	fmt.Fprintf(stdout, "%s\n", b)
	return nil
}
