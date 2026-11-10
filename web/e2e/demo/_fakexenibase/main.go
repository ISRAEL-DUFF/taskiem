// Command _fakexenibase runs the fake Xenibase the Go tests use
// (connectors/xenibase/xenibasetest), plus file storage for photos, for
// the designer's demo world (web/e2e/demo). It is in a directory whose
// name starts with an underscore so that ./... never builds it.
//
// Taskiem refuses to reach private and loopback addresses for tables,
// stock and photos (engine/egress), so the fake is named by a public IP
// literal (-ip) and Taskiem is pointed at a CONNECT proxy
// (TASKIEM_EGRESS_PROXY) that this command also runs: the proxy sends that
// one address to the fake on loopback and refuses everything else.
//
// Everything is kept in memory: stop this process and the demo's stock,
// catalogue and photos are gone (serve-demo.sh leaves it running).
package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
)

// tb is just enough of testing.TB for the fake.
type tb struct{ testing.TB }

func (tb) Helper()                   {}
func (tb) Cleanup(func())            {}
func (tb) Errorf(f string, a ...any) { log.Printf("fake xenibase: "+f, a...) }
func (tb) Fatalf(f string, a ...any) { log.Fatalf("fake xenibase: "+f, a...) }
func (tb) Logf(f string, a ...any)   { log.Printf("fake xenibase: "+f, a...) }
func (tb) Error(a ...any)            { log.Print(a...) }
func (tb) Fatal(a ...any)            { log.Fatal(a...) }
func (tb) Log(a ...any)              { log.Print(a...) }
func (tb) Name() string              { return "demo" }
func (tb) Failed() bool              { return false }
func (tb) Skipf(string, ...any)      {}
func (tb) Setenv(string, string)     {}
func (tb) TempDir() string           { return os.TempDir() }
func (tb) Skipped() bool             { return false }
func (tb) Fail()                     {}
func (tb) FailNow()                  {}
func (tb) SkipNow()                  {}
func (tb) Skip(...any)               {}

// files is the projects' file storage: the paths media.storage calls.
type files struct {
	mu    sync.Mutex
	blobs map[string][]byte // "bucket/path"
	types map[string]string
}

func (s *files) serve(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if i := strings.Index(p, "/storage/v1/public/"); i >= 0 && r.Method == http.MethodGet {
		key := strings.TrimPrefix(p[i:], "/storage/v1/public/")
		s.mu.Lock()
		b, ok := s.blobs[key]
		ct := s.types[key]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return true
		}
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(b)
		return true
	}
	if !strings.HasPrefix(p, "/api/v1/projects/") || !strings.Contains(p, "/files") {
		return false
	}
	rest := p[strings.Index(p, "/files")+len("/files"):]
	switch {
	case r.Method == http.MethodGet && rest == "":
		_, _ = io.WriteString(w, `{"buckets":[]}`)
	case r.Method == http.MethodPost && rest == "/buckets":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	case r.Method == http.MethodPatch && strings.HasPrefix(rest, "/buckets/"):
		_, _ = io.WriteString(w, `{}`)
	case r.Method == http.MethodPut && strings.HasSuffix(rest, "/object"):
		bucket := strings.TrimSuffix(strings.TrimPrefix(rest, "/buckets/"), "/object")
		path := r.URL.Query().Get("path")
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.blobs[bucket+"/"+path] = body
		s.types[bucket+"/"+path] = r.Header.Get("Content-Type")
		s.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"path":%q,"size":%d,"mime_type":%q}`, path, len(body), r.Header.Get("Content-Type"))
	case r.Method == http.MethodPost && strings.HasSuffix(rest, "/sign"):
		_, _ = io.WriteString(w, `{"signed_url":"","url":""}`)
	case r.Method == http.MethodDelete && strings.HasSuffix(rest, "/objects"):
		_, _ = io.WriteString(w, `{}`)
	default:
		http.NotFound(w, r)
	}
	return true
}

func main() {
	ip := flag.String("ip", "45.33.0.1", "the public IP literal Taskiem is told the fake is at")
	port := flag.Int("port", 18093, "the fake's port, on loopback and in its URL")
	proxyAddr := flag.String("proxy", "127.0.0.1:18094", "where the CONNECT proxy listens")
	token := flag.String("token", "xb_demo_platform_token", "the platform token Taskiem sends")
	caOut := flag.String("ca", "", "where to write the PEM certificate Taskiem must trust (SSL_CERT_FILE)")
	pass := flag.String("pass", "", "loopback addresses (host:port, comma-separated) the proxy also tunnels to as they are: the how-to videos' stand-ins for WhatsApp and the model")
	flag.Parse()

	f := xenibasetest.New(tb{})
	f.AddToken(xenibasetest.Token{Secret: *token, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	f.Close() // its own loopback listener; the handler is served below
	// The connector wants https: a self-signed certificate for the IP,
	// which the demo server trusts through SSL_CERT_FILE.
	cert, certPEM := selfSigned(*ip)
	if *caOut != "" {
		if err := os.WriteFile(*caOut, certPEM, 0o644); err != nil { //nolint:gosec // a public certificate
			log.Fatal(err)
		}
	}
	public := fmt.Sprintf("https://%s:%d", *ip, *port)
	f.URL = public
	store := &files{blobs: map[string][]byte{}, types: map[string]string{}}
	inner := f.Config.Handler
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	// Xenibase serves each project's data API at a host of its own, with
	// no path (media.FetchPublic insists): here a port of its own on the
	// same IP for each project, which serves the fake's /edge/{ref}.
	ports := &projectPorts{ip: *ip, next: *port + 10, byRef: map[string]int{}, allowed: map[int]bool{*port: true}}
	for _, hp := range strings.Split(*pass, ",") {
		if hp = strings.TrimSpace(hp); hp != "" {
			ports.pass = append(ports.pass, hp)
		}
	}
	edge := regexp.MustCompile(regexp.QuoteMeta(public) + `/edge/(k[0-9a-f]{7})`)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store.serve(w, r) {
			return
		}
		rec := httptest.NewRecorder()
		inner.ServeHTTP(rec, r)
		body := edge.ReplaceAllFunc(rec.Body.Bytes(), func(m []byte) []byte {
			ref := string(edge.FindSubmatch(m)[1])
			return []byte(fmt.Sprintf("https://%s:%d", *ip, ports.serve(ref, inner, store, tlsCfg)))
		})
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
	})
	listen(fmt.Sprintf("127.0.0.1:%d", *port), h, tlsCfg)

	pl, err := net.Listen("tcp", *proxyAddr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("TASKIEM_XENIBASE_URL=%s TASKIEM_XENIBASE_TOKEN=%s TASKIEM_XENIBASE_ORG=%s TASKIEM_EGRESS_PROXY=http://%s\n", public, *token, f.OrgID, *proxyAddr)
	for {
		c, err := pl.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go tunnel(c, ports)
	}
}

func listen(addr string, h http.Handler, cfg *tls.Config) {
	srv := httptest.NewUnstartedServer(h)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	srv.Listener = ln
	srv.TLS = cfg
	srv.StartTLS()
}

// projectPorts are the projects' data API hosts: a port each.
type projectPorts struct {
	mu      sync.Mutex
	ip      string
	next    int
	byRef   map[string]int
	allowed map[int]bool
	// pass are loopback addresses tunnelled to as they are (-pass).
	pass []string
}

func (p *projectPorts) serve(ref string, inner http.Handler, store *files, cfg *tls.Config) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if port, ok := p.byRef[ref]; ok {
		return port
	}
	port := p.next
	p.next++
	listen(fmt.Sprintf("127.0.0.1:%d", port), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store.serve(w, r) {
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/edge/" + ref + r.URL.Path
		r2.URL.RawPath = ""
		inner.ServeHTTP(w, r2)
	}), cfg)
	p.byRef[ref] = port
	p.allowed[port] = true
	return port
}

func (p *projectPorts) ok(host string) (string, bool) {
	if slices.Contains(p.pass, host) {
		return host, true
	}
	h, ps, err := net.SplitHostPort(host)
	if err != nil || h != p.ip {
		return "", false
	}
	n, err := strconv.Atoi(ps)
	if err != nil {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return "127.0.0.1:" + ps, p.allowed[n]
}

// tunnel answers one CONNECT: to one of the fake's public addresses, a
// tunnel to it on loopback; anything else is refused.
func tunnel(c net.Conn, ports *projectPorts) {
	defer c.Close()
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil || req.Method != http.MethodConnect {
		return
	}
	local, ok := ports.ok(req.Host)
	if !ok {
		_, _ = io.WriteString(c, "HTTP/1.1 403 Forbidden\r\n\r\n")
		return
	}
	up, err := net.Dial("tcp", local)
	if err != nil {
		_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer up.Close()
	_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, br); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
}

// selfSigned is a certificate for ip that is its own root.
func selfSigned(ip string) (tls.Certificate, []byte) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "demo xenibase"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(5 * 365 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{net.ParseIP(ip)}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	kd, _ := x509.MarshalECPrivateKey(key)
	c, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}))
	if err != nil {
		log.Fatal(err)
	}
	return c, certPEM
}
