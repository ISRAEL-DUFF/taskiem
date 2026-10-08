package flowcode_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/flowcode"
)

var ctx = context.Background()

func equalJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}

// Every committed .flow.ts compiles, in the binary, to the definition
// committed beside it; generating code from that definition gives the file.
func TestRepositoryFlowsCompileInTheBinary(t *testing.T) {
	files, _ := filepath.Glob("../../flows/*/*.flow.ts")
	if len(files) < 4 {
		t.Fatalf("found %d flow files", len(files))
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			got, err := flowcode.CompileFile(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(strings.TrimSuffix(f, ".flow.ts") + ".wd.json")
			if err != nil {
				t.Fatal(err)
			}
			if !equalJSON(t, got, want) {
				t.Errorf("compiled definition differs from the committed one:\n%s", got)
			}
			code, err := flowcode.Generate(ctx, want)
			if err != nil {
				t.Fatal(err)
			}
			src, _ := os.ReadFile(f)
			if code != string(src) {
				t.Errorf("generated code differs from %s", f)
			}
		})
	}
}

func TestCompileSource(t *testing.T) {
	def, err := flowcode.CompileSource(ctx, `import { workflow, manual, transform } from "@taskiem/sdk";
import { iswallet } from "@taskiem/connectors";
export default workflow({ id: "wf_src", name: "From source", trigger: manual() })
  .step("bal", iswallet.get_balance({ wallet_id: ({ trigger }) => trigger.body.wallet }))
  .next("out", transform(({ steps }) => steps.bal.output.balances));`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(def), `"wallet_id": "=trigger.body.wallet"`) || !strings.Contains(string(def), `"output": "=steps.bal.output.balances"`) {
		t.Errorf("definition:\n%s", def)
	}
	if !strings.HasPrefix(string(def), "{\n  \"schema\": \"wd/v1\",\n  \"id\": \"wf_src\"") {
		t.Errorf("key order or format lost:\n%s", def)
	}
}

func TestCompileErrorsAreTheAuthors(t *testing.T) {
	for src, want := range map[string]string{
		`import fs from "node:fs"; export default fs;`: `package "node:fs" is not available`,
		`import x from "./other"; export default x;`:   "imports of other files are not available",
		`export default 1;`:                            "default export must be a workflow",
		`import { workflow, manual, transform } from "@taskiem/sdk";
export default workflow({ id: "wf_e", name: "e", trigger: manual() }).step("a", transform(({ trigger }) => trigger.a ?? 0));`: `steps.a.config.output: ?? is not supported`,
		`export default {`: "Expected identifier but found end of file",
	} {
		_, err := flowcode.CompileSource(ctx, src)
		if !errors.Is(err, flowcode.ErrCompile) || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", src, err, want)
		}
	}
}
