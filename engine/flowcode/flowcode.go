// Package flowcode compiles workflow code (.flow.ts) to definitions and
// generates code from definitions (spec 10.1, 10.2), inside the binary: the
// TypeScript SDK is embedded, bundled with esbuild, and run in the QuickJS
// sandbox, so neither the CLI nor the API needs Node.
package flowcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/israel-duff/taskiem/engine/sandbox"
	"github.com/israel-duff/taskiem/sdk"
)

// ErrCompile is a problem in the workflow code itself.
var ErrCompile = errors.New("flow code")

// limits bound one compile or generation; the SDK is small.
var limits = sandbox.Limits{Memory: 128 << 20, Timeout: 20 * time.Second, MaxOutput: 8 << 20, MaxLogs: 16 << 10}

const sdkNS = "taskiem-sdk"

// sdkPlugin resolves @taskiem/sdk and @taskiem/connectors to the embedded
// SDK, and refuses other packages: flow code is declarative and runs with
// nothing but the SDK.
func sdkPlugin(allowFiles bool) api.Plugin {
	return api.Plugin{
		Name: "taskiem-sdk",
		Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: `.*`}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
				switch {
				case a.Path == "@taskiem/sdk":
					return api.OnResolveResult{Path: "src/index.ts", Namespace: sdkNS}, nil
				case a.Path == "@taskiem/connectors":
					return api.OnResolveResult{Path: "src/connectors.gen.ts", Namespace: sdkNS}, nil
				case a.Namespace == sdkNS:
					// Imports inside the SDK: "./builder.js" is src/builder.ts.
					p := path.Join(path.Dir(a.Importer), strings.TrimSuffix(a.Path, ".js")+".ts")
					return api.OnResolveResult{Path: p, Namespace: sdkNS}, nil
				case a.Kind == api.ResolveEntryPoint:
					return api.OnResolveResult{}, nil
				case strings.HasPrefix(a.Path, ".") || strings.HasPrefix(a.Path, "/"):
					if !allowFiles {
						return api.OnResolveResult{}, fmt.Errorf("imports of other files are not available here; only @taskiem/sdk and @taskiem/connectors")
					}
					return api.OnResolveResult{}, nil // esbuild resolves files on disk
				}
				return api.OnResolveResult{}, fmt.Errorf("package %q is not available to flow code; only @taskiem/sdk and @taskiem/connectors (put logic in a code step)", a.Path)
			})
			b.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: sdkNS}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
				src, err := fs.ReadFile(sdk.Source, a.Path)
				if err != nil {
					return api.OnLoadResult{}, err
				}
				s := string(src)
				return api.OnLoadResult{Contents: &s, Loader: api.LoaderTS}, nil
			})
		},
	}
}

// bundle builds an entry module into a script for the sandbox.
func bundle(entry, resolveDir string, allowFiles bool) (string, error) {
	res := api.Build(api.BuildOptions{
		Stdin:      &api.StdinOptions{Contents: entry, ResolveDir: resolveDir, Sourcefile: "entry.ts", Loader: api.LoaderTS},
		Bundle:     true,
		Write:      false,
		Format:     api.FormatIIFE,
		GlobalName: "__taskiem_module",
		Target:     api.ES2022,
		Platform:   api.PlatformNeutral,
		Plugins:    []api.Plugin{sdkPlugin(allowFiles)},
		LogLevel:   api.LogLevelSilent,
	})
	if len(res.Errors) > 0 {
		msgs := make([]string, len(res.Errors))
		for i, m := range res.Errors {
			loc := ""
			if m.Location != nil {
				loc = fmt.Sprintf("%s:%d:%d: ", m.Location.File, m.Location.Line, m.Location.Column)
			}
			msgs[i] = loc + m.Text
		}
		return "", fmt.Errorf("%w: %s", ErrCompile, strings.Join(msgs, "; "))
	}
	if len(res.OutputFiles) != 1 {
		return "", fmt.Errorf("%w: bundling produced %d files", ErrCompile, len(res.OutputFiles))
	}
	return string(res.OutputFiles[0].Contents), nil
}

func run(ctx context.Context, script string, input any) (string, error) {
	res, err := sandbox.Run(ctx, script, input, sandbox.Host{}, limits)
	if err != nil {
		msg := err.Error()
		// Keep the author-facing part of a script error.
		msg = strings.TrimSuffix(strings.TrimPrefix(msg, sandbox.ErrScript.Error()+": "), ": fatal")
		return "", fmt.Errorf("%w: %s", ErrCompile, msg)
	}
	return res.JSON, nil
}

// buildEntry imports a flow module and builds its default export.
const buildEntry = `import wf from %q;
export default () => {
  if (!wf || typeof wf.build !== "function") throw new Error("the module's default export must be a workflow(...)");
  return wf.build();
};`

// CompileFile compiles a .flow.ts file (which may import other local files)
// to its definition, as indented JSON in the builder's key order.
func CompileFile(ctx context.Context, file string) ([]byte, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	script, err := bundle(fmt.Sprintf(buildEntry, abs), filepath.Dir(abs), true)
	if err != nil {
		return nil, err
	}
	return finish(run(ctx, script, nil))
}

// CompileSource compiles flow code given as text (from the web app or the
// API). It may import only the SDK.
func CompileSource(ctx context.Context, source string) ([]byte, error) {
	entry := strings.Replace(buildEntry, "import wf from %q;", "import wf from \"flow:source\";", 1)
	script, err := bundleWithSource(entry, source)
	if err != nil {
		return nil, err
	}
	return finish(run(ctx, script, nil))
}

func bundleWithSource(entry, source string) (string, error) {
	plugin := api.Plugin{
		Name: "flow-source",
		Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: `^flow:source$`}, func(api.OnResolveArgs) (api.OnResolveResult, error) {
				return api.OnResolveResult{Path: "flow.ts", Namespace: "flow-source"}, nil
			})
			b.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: "flow-source"}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
				return api.OnLoadResult{Contents: &source, Loader: api.LoaderTS}, nil
			})
		},
	}
	res := api.Build(api.BuildOptions{
		Stdin:      &api.StdinOptions{Contents: entry, Sourcefile: "entry.ts", Loader: api.LoaderTS},
		Bundle:     true,
		Format:     api.FormatIIFE,
		GlobalName: "__taskiem_module",
		Target:     api.ES2022,
		Platform:   api.PlatformNeutral,
		Plugins:    []api.Plugin{plugin, sdkPlugin(false)},
		LogLevel:   api.LogLevelSilent,
	})
	if len(res.Errors) > 0 {
		msgs := make([]string, len(res.Errors))
		for i, m := range res.Errors {
			loc := ""
			if m.Location != nil && m.Location.File != "entry.ts" {
				loc = fmt.Sprintf("%d:%d: ", m.Location.Line, m.Location.Column)
			}
			msgs[i] = loc + m.Text
		}
		return "", fmt.Errorf("%w: %s", ErrCompile, strings.Join(msgs, "; "))
	}
	return string(res.OutputFiles[0].Contents), nil
}

func finish(out string, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &v); err != nil || v["schema"] == nil {
		return nil, fmt.Errorf("%w: the workflow did not build to a definition", ErrCompile)
	}
	return Indent([]byte(out))
}

// Indent formats a definition the way files in a repository hold it: two
// spaces, a trailing newline, key order kept.
func Indent(doc []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, doc, "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

const generateEntry = `import { generate } from "@taskiem/sdk";
import { CONNECTOR_HELPERS } from "@taskiem/connectors";
export default (def) => generate(def, { connectors: CONNECTOR_HELPERS });`

// Generate prints a definition as flow code.
func Generate(ctx context.Context, doc []byte) (string, error) {
	if !json.Valid(doc) {
		return "", fmt.Errorf("%w: definition is not JSON", ErrCompile)
	}
	def := json.RawMessage(doc) // key order matters to the generated code
	script, err := bundle(generateEntry, "", false)
	if err != nil {
		return "", err
	}
	out, err := run(ctx, script, def)
	if err != nil {
		return "", err
	}
	var code string
	if err := json.Unmarshal([]byte(out), &code); err != nil {
		return "", fmt.Errorf("%w: generation returned %T", ErrCompile, out)
	}
	return code, nil
}
