package deploy

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// caddyAdapt runs `caddy adapt` on a Caddyfile and returns its JSON.
func caddyAdapt(t *testing.T, caddy, file string, env ...string) map[string]any {
	t.Helper()
	cmd := exec.Command(caddy, "adapt", "--adapter", "caddyfile", "--config", file)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
			stderr = ee.Stderr
		}
		t.Fatalf("caddy adapt %s: %v: %s", file, err, stderr)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// at walks a JSON value by keys.
func at(v any, keys ...string) any {
	for _, k := range keys {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

// routesOn is the routes of the server listening on addr, by host ("" for
// a route with no host matcher).
func routesOn(t *testing.T, cfg map[string]any, addr string) map[string]any {
	t.Helper()
	servers, _ := at(cfg, "apps", "http", "servers").(map[string]any)
	for _, s := range servers {
		if l, _ := at(s, "listen").([]any); len(l) == 1 && l[0] == addr {
			byHost := map[string]any{}
			routes, _ := at(s, "routes").([]any)
			for _, r := range routes {
				host := ""
				if m, _ := at(r, "match").([]any); len(m) > 0 {
					if h, _ := at(m[0], "host").([]any); len(h) > 0 {
						host, _ = h[0].(string)
					}
				}
				if _, seen := byHost[host]; !seen {
					byHost[host] = at(r, "handle")
				}
			}
			return byHost
		}
	}
	t.Fatalf("no server listens on %s", addr)
	return nil
}

// TestExternalProxySitesMatchTaskiemsOwnCaddy: in external-proxy mode
// (docs/vm.md#sharing-the-server-with-xenibase) Xenibase's Caddy serves
// Taskiem's sites from taskiem.caddy and taskiem.global, which taskiem-vps
// writes with the names spelled out. With a real Caddy (on PATH, or
// TASKIEM_TEST_CADDY), both are adapted inside a Caddyfile shaped like
// Xenibase's (deploy/compose/Caddyfile: global options importing *.global,
// its UI, *.caddy, its http:// catch-all), and must route the app's host
// and every other host exactly as the kit's own Caddyfile and public.caddy
// do, with the same on-demand certificate check. Without Caddy it is
// skipped (CI's Go jobs have none).
func TestExternalProxySitesMatchTaskiemsOwnCaddy(t *testing.T) {
	caddy := os.Getenv("TASKIEM_TEST_CADDY")
	if caddy == "" {
		var err error
		if caddy, err = exec.LookPath("caddy"); err != nil {
			t.Skip("caddy is not installed")
		}
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	d := t.TempDir()
	write := func(path, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Taskiem's own Caddy, public pages on.
	own, _ := os.ReadFile("vm/Caddyfile")
	pub, _ := os.ReadFile("vm/public.caddy")
	write(filepath.Join(d, "own", "caddy.d", "public.caddy"), string(pub))
	write(filepath.Join(d, "own", "Caddyfile"), strings.ReplaceAll(string(own), "/etc/caddy/caddy.d", filepath.Join(d, "own", "caddy.d")))
	ownCfg := caddyAdapt(t, caddy, filepath.Join(d, "own", "Caddyfile"), "TASKIEM_DOMAIN=app.example.com")

	// The two files taskiem-vps writes, by an update in external-proxy mode
	// (the stubbed server of vps_test.go).
	v := newVPS(t)
	xd := filepath.Join(d, "xenibase", "caddy.d")
	if err := os.MkdirAll(xd, 0o755); err != nil {
		t.Fatal(err)
	}
	v.write(filepath.Join(v.dir, "etc", "vps.conf"), "DOMAIN='App.Example.com'\nPUBLIC='yes'\nPREVIEW_DOMAIN='shops.example.net'\nPROXY='external'\nPROXY_DIR='"+xd+"'\nPROXY_CONTAINER='xenibase-caddy-1'\nPROXY_NETWORK='cohost'\n")
	if out, err := v.run("taskiem:good2", "update", "--now"); err != nil {
		t.Fatalf("update: %v: %s", err, out)
	}
	write(filepath.Join(d, "xenibase", "Caddyfile"), `{
	email {$XENIBASE_ACME_EMAIL}
	import `+xd+`/*.global
}

xeni.example.com {
	encode zstd gzip
	reverse_proxy xenibase-server:8080
}

import `+xd+`/*.caddy

http:// {
	handle /.well-known/acme-challenge/* {
		reverse_proxy xenibase-server:8080
	}
	handle {
		redir https://xeni.example.com{uri}
	}
}
`)
	coCfg := caddyAdapt(t, caddy, filepath.Join(d, "xenibase", "Caddyfile"), "XENIBASE_ACME_EMAIL=ops@example.com")

	ownRoutes, coRoutes := routesOn(t, ownCfg, ":443"), routesOn(t, coCfg, ":443")
	for _, host := range []string{"app.example.com", ""} {
		if !reflect.DeepEqual(ownRoutes[host], coRoutes[host]) {
			a, _ := json.Marshal(ownRoutes[host])
			b, _ := json.Marshal(coRoutes[host])
			t.Errorf("host %q: own Caddy %s, Xenibase's %s", host, a, b)
		}
	}
	if coRoutes["xeni.example.com"] == nil {
		t.Error("Xenibase's own site is gone")
	}
	if a, b := at(ownCfg, "apps", "tls", "automation", "on_demand"), at(coCfg, "apps", "tls", "automation", "on_demand"); a == nil || !reflect.DeepEqual(a, b) {
		t.Errorf("on-demand check: own Caddy %v, Xenibase's %v", a, b)
	}
	// Preview addresses over plain HTTP are sent to HTTPS, ahead of
	// Xenibase's catch-all (which would send them to its UI).
	plain := routesOn(t, coCfg, ":80")
	if b, _ := json.Marshal(plain["*.shops.example.net"]); !strings.Contains(string(b), `"Location":["https://{http.request.host}{http.request.uri}"]`) || !strings.Contains(string(b), `"status_code":308`) {
		t.Errorf("http://*.shops.example.net: %s", b)
	}
}
