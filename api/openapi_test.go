package api_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/api/openapi"
)

// notInSpec are the routes outside the public API document, and why. A
// route is excluded when its path starts with one of these prefixes, or
// equals one of the exact entries ("METHOD /path").
var notInSpec = []struct{ route, why string }{
	{"/healthz", "liveness probe for load balancers and orchestrators (docs/operations.md)"},
	{"/readyz", "readiness probe (docs/operations.md)"},
	{"/hooks/", "inbound webhooks (role edge): each trigger's URL and auth are shown with the workflow (GET /v1/workflows/{wf}/triggers)"},
	{"/channels/", "provider callbacks for WhatsApp and USSD (role edge), signed by the provider (docs/whatsapp.md, docs/ussd.md)"},
	{"/git-hooks/", "push notifications from Git hosts, signed per connection (docs/git.md)"},
	{"/scim/v2/", "the standard SCIM 2.0 protocol (RFC 7644) for identity providers, not Taskiem's own API (docs/governance.md)"},
	{"/embed/", "the embedded builder's script and frame pages, not JSON (docs/embedding.md)"},
	{"POST /v1/billing/webhooks/{provider}", "payment providers' callbacks, signed by them (docs/billing.md)"},
	{"/v1/status/admin/", "the status page's operator API, with operator tokens rather than tenant credentials (docs/reliability.md#status-page)"},
	{"/v1/ops/", "the operator console's API: Taskiem's own operators, with operator sessions rather than tenant credentials (docs/operator-console.md)"},
	{"/*", "the web app's static files"},
}

func excluded(method, path string) bool {
	return slices.ContainsFunc(notInSpec, func(x struct{ route, why string }) bool { return excludes(x.route, method, path) })
}

func excludes(entry, method, path string) bool {
	return entry == method+" "+path || entry == path || (strings.HasSuffix(entry, "/") && strings.HasPrefix(path, entry))
}

// fullServer turns on every optional route.
func fullServer() *api.Server {
	return &api.Server{AllowSignup: true, Ingest: http.NotFoundHandler(), Static: http.NotFoundHandler()}
}

// routerRoutes walks the router: "METHOD /path" with chi's patterns.
func routerRoutes(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := chi.Walk(fullServer().Handler().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if route != "/" {
			route = strings.TrimSuffix(route, "/")
		}
		out[method+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func loadSpec(t *testing.T) *openapi.Document {
	t.Helper()
	doc, err := openapi.Load()
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestOpenAPICoversRoutes fails when a public route is missing from
// api/openapi/openapi.yaml, or when the document names a route the router
// does not have.
func TestOpenAPICoversRoutes(t *testing.T) {
	doc := loadSpec(t)
	routes := routerRoutes(t)
	inSpec := map[string]bool{}
	for _, op := range doc.Operations() {
		inSpec[op.Method+" "+op.Path] = true
	}
	var missing, stale []string
	for r := range routes {
		method, path, _ := strings.Cut(r, " ")
		if excluded(method, path) {
			continue
		}
		if !inSpec[r] {
			missing = append(missing, r)
		}
	}
	for r := range inSpec {
		method, path, _ := strings.Cut(r, " ")
		if !routes[r] {
			stale = append(stale, r)
		} else if excluded(method, path) {
			stale = append(stale, r+" (excluded as not public)")
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	for _, r := range missing {
		t.Errorf("route %s is not in api/openapi/openapi.yaml: add it, or list it in notInSpec with the reason", r)
	}
	for _, r := range stale {
		t.Errorf("api/openapi/openapi.yaml names %s, which the router does not serve publicly", r)
	}
	// Every exclusion still matches something, so the list stays honest.
	for _, x := range notInSpec {
		hit := false
		for r := range routes {
			method, path, _ := strings.Cut(r, " ")
			hit = hit || excludes(x.route, method, path)
		}
		if !hit {
			t.Errorf("notInSpec entry %q matches no route", x.route)
		}
	}
}

// TestOpenAPIMatchesSource checks what the document says about each
// operation against the router's source: authentication, the permission,
// plan features, and whether keys limited to one environment are refused.
func TestOpenAPIMatchesSource(t *testing.T) {
	doc := loadSpec(t)
	src := routeSource(t)
	ids := map[string]string{}
	tags := map[string]bool{}
	for _, tag := range doc.Tags {
		tags[tag.Name] = true
		if tag.Guide == "" {
			t.Errorf("tag %s names no guide (x-taskiem-guide)", tag.Name)
		} else if _, err := os.Stat("../docs/" + tag.Guide + ".md"); err != nil {
			t.Errorf("tag %s: guide docs/%s.md does not exist", tag.Name, tag.Guide)
		}
	}
	for _, op := range doc.Operations() {
		key := op.Method + " " + op.Path
		meta, ok := src[key]
		if !ok {
			continue // TestOpenAPICoversRoutes reports it
		}
		if op.Summary == "" {
			t.Errorf("%s: no summary", key)
		}
		if op.OperationID == "" {
			t.Errorf("%s: no operationId", key)
		} else if prev, dup := ids[op.OperationID]; dup {
			t.Errorf("%s: operationId %s is also %s's", key, op.OperationID, prev)
		}
		ids[op.OperationID] = key
		if len(op.Tags) != 1 || !tags[op.Tags[0]] {
			t.Errorf("%s: wants exactly one declared tag, has %v", key, op.Tags)
		}
		if op.Responses["default"] == nil {
			t.Errorf("%s: no default (error) response", key)
		}
		perm := strings.Join(meta.Permissions, ",")
		if op.Permission != perm {
			t.Errorf("%s (%s): x-taskiem-permission is %q, the code needs %q", key, meta.Handler, op.Permission, perm)
		}
		want := slices.Sorted(slices.Values(meta.Features))
		if got := slices.Sorted(slices.Values(op.Features)); !slices.Equal(got, want) {
			t.Errorf("%s (%s): x-taskiem-plan-feature is %v, the code needs %v", key, meta.Handler, got, want)
		}
		if op.AllEnvironments != meta.TenantWide {
			t.Errorf("%s (%s): x-taskiem-all-environments is %v, the code says %v", key, meta.Handler, op.AllEnvironments, meta.TenantWide)
		}
		if got, want := securityOf(op.Security), securityFor(meta.Auth, op.Method); got != want {
			t.Errorf("%s (%s): security is %s, want %s", key, meta.Handler, got, want)
		}
		for _, sch := range op.Security {
			for name := range sch {
				if _, ok := doc.Components.SecuritySchemes[name]; !ok {
					t.Errorf("%s: unknown security scheme %s", key, name)
				}
			}
		}
	}
}

func securityOf(reqs []map[string][]string) string {
	var alts []string
	for _, req := range reqs {
		names := make([]string, 0, len(req))
		for n := range req {
			names = append(names, n)
		}
		sort.Strings(names)
		alts = append(alts, strings.Join(names, "+"))
	}
	sort.Strings(alts)
	return "[" + strings.Join(alts, " | ") + "]"
}

// securityFor is the security an operation should declare for how the
// router authenticates it.
func securityFor(auth, method string) string {
	switch auth {
	case "none":
		return "[]"
	case "embed":
		return "[endUserToken]"
	case "partner":
		return "[apiKey]"
	}
	cookie := "csrfHeader+sessionCookie"
	if method == "GET" {
		cookie = "sessionCookie"
	}
	alts := []string{"apiKey", cookie, "sessionToken"}
	sort.Strings(alts)
	return "[" + strings.Join(alts, " | ") + "]"
}

var (
	specOnce      sync.Once
	specValidator *openapi.Validator
	specErr       error
)

// specCheck wraps the API so every answer the tests receive is checked
// against the document's schema for its route and status. Failures are
// reported when the test ends.
func specCheck(t *testing.T, h http.Handler) http.Handler {
	specOnce.Do(func() { specValidator, specErr = openapi.NewValidator() })
	if specErr != nil {
		t.Fatal(specErr)
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	var failures []string
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, f := range failures {
			t.Errorf("answer does not match api/openapi/openapi.yaml: %s", f)
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.NewRouteContext()
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
		rec := &recorder{ResponseWriter: w}
		h.ServeHTTP(rec, r)
		pattern := rctx.RoutePattern()
		if pattern != "/" {
			pattern = strings.TrimSuffix(pattern, "/")
		}
		// A pattern ending in * matched no route (chi's 404 or 405).
		if !strings.HasPrefix(pattern, "/v1/") || strings.HasSuffix(pattern, "*") || excluded(r.Method, pattern) ||
			r.Method == http.MethodOptions || r.Method == http.MethodHead {
			return
		}
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		if err := specValidator.ValidateResponse(r.Method, pattern, status, rec.Header().Get("Content-Type"), rec.body.Bytes()); err != nil {
			msg := err.Error()
			mu.Lock()
			if k := fmt.Sprintf("%s %s %d", r.Method, pattern, status); !seen[k] {
				seen[k] = true
				failures = append(failures, msg)
			}
			mu.Unlock()
		}
	})
}

// recorder passes an answer through while keeping a copy of it.
type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.body.Len() < 1<<20 {
		r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
