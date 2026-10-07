package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wasmconn"
)

// connectorCmd builds, checks and uploads a tenant's own WebAssembly
// connector (docs/connector-sdk.md).
func connectorCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(connectorUsage)
	}
	switch args[0] {
	case "init":
		return connectorInit(args[1:], stdout)
	case "validate":
		return connectorValidate(ctx, args[1:], stdout)
	case "test":
		return connectorTest(ctx, args[1:], stdout)
	case "keygen":
		return connectorKeygen(args[1:], stdout)
	case "package":
		return connectorPackage(ctx, args[1:], stdout)
	case "verify":
		return connectorVerify(args[1:], stdout)
	case "publisher":
		return connectorPublisher(ctx, args[1:], stdout)
	case "submit":
		return connectorSubmit(ctx, args[1:], stdout)
	case "submissions":
		return connectorSubmissions(ctx, args[1:], stdout)
	case "publish", "withdraw", "revoke":
		return connectorMove(ctx, args[0], args[1:], stdout)
	case "build":
		return connectorBuild(args[1:], stdout)
	case "check":
		fs := flag.NewFlagSet("connector check", flag.ContinueOnError)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 2 {
			return errors.New("usage: taskiem connector check MANIFEST MODULE")
		}
		c, err := checkConnector(ctx, fs.Arg(0), fs.Arg(1))
		if err != nil {
			return err
		}
		describeConnector(stdout, c)
		return nil
	case "push":
		return connectorPush(ctx, args[1:], stdout)
	}
	return fmt.Errorf("unknown connector command %q\n%s", args[0], connectorUsage)
}

func connectorBuild(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector build", flag.ContinueOnError)
	out := fs.String("o", "", "output file (default DIR/connector.wasm)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if *out == "" {
		*out = filepath.Join(dir, "connector.wasm")
	}
	abs, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-trimpath", "-o", abs, ".") //nolint:gosec // the author's own package
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, stdout, os.Stderr
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	fmt.Fprintf(stdout, "built %s\n", *out)
	return nil
}

// checkConnector loads the pair as the platform would on upload: a
// tenant's own connector (x_), or a catalogue version (p_).
func checkConnector(ctx context.Context, manifestPath, modulePath string) (*connector.Connector, error) {
	manifest, err := os.ReadFile(manifestPath) //nolint:gosec // a path the user named
	if err != nil {
		return nil, err
	}
	module, err := os.ReadFile(modulePath) //nolint:gosec // a path the user named
	if err != nil {
		return nil, err
	}
	rt, err := wasmconn.New(ctx, wasmconn.Limits{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close(ctx) }()
	return loadAny(ctx, rt, manifest, module)
}

func loadAny(ctx context.Context, rt *wasmconn.Runtime, manifest, module []byte) (*connector.Connector, error) {
	if m, probs := connector.Parse(manifest); len(probs) == 0 && strings.HasPrefix(m.ID, connector.CataloguePrefix) {
		return rt.LoadPublished(ctx, manifest, module)
	}
	return rt.Load(ctx, manifest, module)
}

func describeConnector(w io.Writer, c *connector.Connector) {
	names := make([]string, 0, len(c.Manifest.Actions))
	for n := range c.Manifest.Actions {
		names = append(names, fmt.Sprintf("%s (%s)", n, c.Manifest.Actions[n].Class))
	}
	sort.Strings(names)
	fmt.Fprintf(w, "%s %s\n  actions: %s\n  hosts:   %s\n", c.Ref(), c.Manifest.Version, strings.Join(names, ", "), strings.Join(c.Manifest.Hosts(), ", "))
}

func connectorPush(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connector push", flag.ContinueOnError)
	remote := remoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: taskiem connector push MANIFEST MODULE")
	}
	if _, err := checkConnector(ctx, fs.Arg(0), fs.Arg(1)); err != nil {
		return err
	}
	c, err := remote()
	if err != nil {
		return err
	}
	c.http.Timeout = 2 * time.Minute
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range []struct{ field, path string }{{"manifest", fs.Arg(0)}, {"module", fs.Arg(1)}} {
		b, err := os.ReadFile(f.path)
		if err != nil {
			return err
		}
		fw, err := mw.CreateFormFile(f.field, filepath.Base(f.path))
		if err != nil {
			return err
		}
		if _, err := fw.Write(b); err != nil {
			return err
		}
	}
	if err := mw.Close(); err != nil {
		return err
	}
	var out struct {
		Ref, Version, Digest string
	}
	if err := c.send(ctx, "POST", "/v1/tenant-connectors", mw.FormDataContentType(), &buf, &out); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "uploaded %s %s (module sha256 %s)\nnew runs use it; runs already started keep the version they started with\n", out.Ref, out.Version, out.Digest)
	return nil
}
