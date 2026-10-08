package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestConnectorBuildCheckPush builds the example connector, checks it
// offline, uploads it, and is refused the same version twice.
func TestConnectorBuildCheckPush(t *testing.T) {
	srv := startServer(t)
	login := srv.call(t, "POST", "/v1/auth/login", "", map[string]any{"email": "admin@smoke.test", "password": "correct horse battery", "bearer": true})
	key := srv.call(t, "POST", "/v1/api-keys", login["token"].(string), map[string]any{"name": "ci", "permissions": []string{"connector.manage"}})["key"].(string)
	t.Setenv("TASKIEM_URL", srv.base)
	t.Setenv("TASKIEM_API_KEY", key)

	module := t.TempDir() + "/connector.wasm"
	manifest := "../../examples/wasm-connector/manifest.yaml"
	cli := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := run(args, &out, &out)
		return out.String(), err
	}
	if out, err := cli("connector", "build", "-o", module, "../../examples/wasm-connector"); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if out, err := cli("connector", "check", manifest, module); err != nil || !strings.Contains(out, "create_payment (reconcilable_write)") {
		t.Fatalf("check: %v\n%s", err, out)
	}
	if out, err := cli("connector", "check", manifest, manifest); err == nil {
		t.Errorf("a YAML file passed as a module: %s", out)
	}
	if out, err := cli("connector", "push", manifest, module); err != nil || !strings.Contains(out, "uploaded x_example_ledger@1 1.0.0") {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if _, err := cli("connector", "push", manifest, module); err == nil || !strings.Contains(err.Error(), "409") {
		t.Errorf("same version again: %v", err)
	}
}
