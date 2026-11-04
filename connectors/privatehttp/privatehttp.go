// Package privatehttp is the connector for web addresses that exist only on
// a business's own network: a router's REST API, a RADIUS or billing panel,
// an internal service. Every connection goes through an SSH bastion the
// business runs (decision 0013); the worker never dials the private address
// itself.
package privatehttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

const (
	defaultMax     = 256 << 10
	maxRequestBody = 256 << 10
	callTimeout    = 25 * time.Second
)

// New returns the private-network HTTP connector.
func New() *connector.Connector {
	return &connector.Connector{Manifest: connector.MustParse(manifest), Actions: map[string]connector.Action{
		"get":  connector.ActionFunc(get),
		"send": connector.ActionFunc(send),
	}}
}

func fatalf(format string, args ...any) error {
	return fmt.Errorf("privatehttp: "+format+": %w", append(args, effects.ErrFatal)...)
}

// forbidden request headers: the connector sets these itself, or they would
// change where the request goes.
var forbidden = map[string]bool{
	"host": true, "authorization": true, "content-length": true, "connection": true,
	"transfer-encoding": true, "upgrade": true, "proxy-authorization": true,
}

func get(ctx context.Context, req connector.Request) (connector.Response, error) {
	return do(ctx, req, http.MethodGet, false)
}

func send(ctx context.Context, req connector.Request) (connector.Response, error) {
	m, _ := req.Input["method"].(string)
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return connector.Response{}, fatalf("method must be POST, PUT, PATCH or DELETE, not %q", m)
	}
	return do(ctx, req, m, true)
}

func target(c map[string]string, in map[string]any) (*url.URL, error) {
	host := strings.TrimSpace(c["host"])
	if host == "" {
		return nil, fatalf("the connection needs host")
	}
	if strings.ContainsAny(host, "/@?#\\ ") {
		return nil, fatalf("host %q must be a bare address, with no scheme or path", host)
	}
	scheme := strings.ToLower(strings.TrimSpace(c["scheme"]))
	switch scheme {
	case "":
		scheme = "https"
	case "http", "https":
	default:
		return nil, fatalf("scheme must be http or https, not %q", scheme)
	}
	port := strings.TrimSpace(c["port"])
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[scheme]
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, fatalf("port %q is not a port number", port)
	}
	path, _ := in["path"].(string)
	if path == "" {
		path = "/"
	}
	pu, err := url.Parse(path)
	if err != nil || pu.Scheme != "" || pu.Host != "" || pu.User != nil || !strings.HasPrefix(pu.Path, "/") || strings.HasPrefix(path, "//") {
		return nil, fatalf("path %q must start with a single / and name a path on the equipment, not another address", path)
	}
	q := pu.Query()
	if extra, ok := in["query"].(map[string]any); ok {
		for k, v := range extra {
			q.Set(k, fmt.Sprint(v))
		}
	}
	return &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port), Path: pu.Path, RawPath: pu.RawPath, RawQuery: q.Encode()}, nil
}

func tlsConfig(c map[string]string, host string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	if strings.EqualFold(strings.TrimSpace(c["skip_tls_verify"]), "yes") {
		// The way to the equipment is the SSH tunnel; its certificate is
		// usually self-signed. #nosec G402
		cfg.InsecureSkipVerify = true
		return cfg, nil
	}
	if ca := strings.TrimSpace(c["ca_cert"]); ca != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(strings.ReplaceAll(ca, `\n`, "\n"))) {
			return nil, fatalf("ca_cert has no certificate in PEM format")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

func do(ctx context.Context, req connector.Request, method string, write bool) (connector.Response, error) {
	c := req.Credentials
	if strings.TrimSpace(c["tunnel_host"]) == "" {
		return connector.Response{}, fatalf("this connection has no tunnel: its address is on a private network, which only a tunnel can reach (set the bastion fields)")
	}
	if req.Dial == nil {
		return connector.Response{}, fatalf("no dialer")
	}
	u, err := target(c, req.Input)
	if err != nil {
		return connector.Response{}, err
	}
	var body io.Reader
	contentType := ""
	if write {
		switch b := req.Input["body"].(type) {
		case nil:
		case string:
			body, contentType = strings.NewReader(b), "text/plain; charset=utf-8"
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				return connector.Response{}, fatalf("body cannot be sent as JSON (%v)", err)
			}
			body, contentType = bytes.NewReader(raw), "application/json"
		}
		if r, ok := body.(interface{ Len() int }); ok && r.Len() > maxRequestBody {
			return connector.Response{}, fatalf("body is larger than %d bytes", maxRequestBody)
		}
	}
	tc, err := tlsConfig(c, strings.TrimSpace(c["host"]))
	if err != nil {
		return connector.Response{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	hr, err := http.NewRequestWithContext(cctx, method, u.String(), body)
	if err != nil {
		return connector.Response{}, fatalf("request: %v", err)
	}
	if contentType != "" {
		hr.Header.Set("Content-Type", contentType)
	}
	hr.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.5")
	hr.Header.Set("User-Agent", "Taskiem")
	if h, ok := req.Input["headers"].(map[string]any); ok {
		for k, v := range h {
			if forbidden[strings.ToLower(k)] || strings.ContainsAny(k, "\r\n:") {
				return connector.Response{}, fatalf("header %q cannot be set", k)
			}
			s := fmt.Sprint(v)
			if strings.ContainsAny(s, "\r\n") {
				return connector.Response{}, fatalf("header %q has a line break", k)
			}
			hr.Header.Set(k, s)
		}
	}
	switch {
	case c["token"] != "":
		hr.Header.Set("Authorization", "Bearer "+c["token"])
	case c["username"] != "":
		hr.SetBasicAuth(c["username"], c["password"])
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           req.Dial,
		TLSClientConfig:       tc,
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: callTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
	}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.Do(hr)
	if err != nil {
		return connector.Response{}, transportError(err, write)
	}
	defer resp.Body.Close()
	max := defaultMax
	if n, ok := asInt(req.Input["max_bytes"]); ok && n > 0 && n <= 1<<20 {
		max = n
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(max)+1))
	if err != nil {
		return connector.Response{}, transportError(err, write)
	}
	truncated := len(raw) > max
	if truncated {
		raw = raw[:max]
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout:
		// Refused before it was handled: safe to send again.
		return connector.Response{}, fmt.Errorf("privatehttp: the equipment answered %d: %w", resp.StatusCode, effects.ErrNotSent)
	case resp.StatusCode >= 500:
		if write {
			return connector.Response{}, fmt.Errorf("privatehttp: the equipment answered %d, so it may or may not have applied the change: %w", resp.StatusCode, effects.ErrUnknownOutcome)
		}
		return connector.Response{}, fmt.Errorf("privatehttp: the equipment answered %d: %w", resp.StatusCode, effects.ErrRetryable)
	}
	hdr := map[string]any{}
	for _, k := range []string{"Content-Type", "Location", "Etag", "Last-Modified"} {
		if v := resp.Header.Get(k); v != "" {
			hdr[strings.ToLower(k)] = v
		}
	}
	var out any = string(raw)
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "json") && !truncated {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			out = v
		}
	}
	return connector.Response{Output: map[string]any{
		"status": resp.StatusCode, "ok": resp.StatusCode >= 200 && resp.StatusCode < 300,
		"headers": hdr, "body": out, "truncated": truncated,
	}}, nil
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case json.Number:
		n, err := t.Int64()
		return int(n), err == nil
	}
	return 0, false
}

// transportError says what is known: a failure before the request left (the
// tunnel or the connection to the equipment) is safe to retry; after it, a
// write's outcome is unknown.
func transportError(err error, write bool) error {
	if errors.Is(err, effects.ErrFatal) || errors.Is(err, effects.ErrNotSent) {
		return fmt.Errorf("privatehttp: %w", err)
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return fmt.Errorf("privatehttp: %w: %w", err, effects.ErrNotSent)
	}
	if !write {
		return fmt.Errorf("privatehttp: %w: %w", err, effects.ErrRetryable)
	}
	return fmt.Errorf("privatehttp: %w: %w", err, effects.ErrUnknownOutcome)
}
