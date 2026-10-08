package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// proc is one `taskiem serve --role R` process.
type proc struct {
	name, role string
	listen     string // api or edge address, if any
	metrics    string // metrics address
	env        []string
	cmd        *exec.Cmd
	logf       *os.File
	exited     chan struct{}
	mu         sync.Mutex
	restarts   int
}

// cluster is the server roles split across processes, a fake provider and
// the tenants set up through the API.
type cluster struct {
	bin, dsn, out string
	commonEnv     []string
	procs         []*proc
	fake          *fakeProvider
	fakeSrv       *httptest.Server
	api, edge     *proc
	tenants       []tenant
	pprof         bool
	// seq numbers every webhook body: equal bodies are deduplicated by
	// the edge (the same delivery twice), so each must differ.
	seq atomic.Int64
}

type tenant struct {
	id, token string
}

func freePort() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// newCluster creates tenants with the binary's own bootstrap command and
// starts the roles: api, edge, orchestrator, scheduler and workers.
func newCluster(ctx context.Context, bin, dsn, out string, workers, tenants int, providerLatency time.Duration, extraEnv []string, pprof bool) *cluster {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	c := &cluster{bin: bin, dsn: dsn, out: out, fake: newFakeProvider(providerLatency), pprof: pprof}
	c.fakeSrv = httptest.NewServer(c.fake)
	c.commonEnv = append([]string{
		"TASKIEM_DATABASE_URL=" + dsn,
		"TASKIEM_LOCAL_KMS_KEY=" + base64.StdEncoding.EncodeToString(key),
		"TASKIEM_SECURE_COOKIES=false",
		"TASKIEM_BOOTSTRAP_PASSWORD=correct horse battery",
		"TASKIEM_PAYSTACK_URL=" + c.fakeSrv.URL,
		"TASKIEM_TERMII_URL=" + c.fakeSrv.URL,
		"TASKIEM_ARCHIVE_DIR=" + filepath.Join(out, "archive"),
		// The load is many runs from a few tenants: lift the per-tenant
		// ingest limits (an operator's setting) so they do not shape it.
		"TASKIEM_DEFAULT_INGEST_RATE=100000", "TASKIEM_DEFAULT_INGEST_BURST=100000",
		"TASKIEM_DEFAULT_INGEST_CEILING=100000", "TASKIEM_DEFAULT_INGEST_CEILING_BURST=100000",
		"TASKIEM_SHUTDOWN_DELAY=0s",
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
	}, extraEnv...)
	if pprof {
		c.commonEnv = append(c.commonEnv, "TASKIEM_PPROF=true")
	}
	for i := 0; i < tenants; i++ {
		email := fmt.Sprintf("load%d@load.test", i)
		cmd := exec.CommandContext(ctx, bin, "bootstrap", "--tenant", "Load "+strconv.Itoa(i), "--email", email) //nolint:gosec // the binary under test, named by the operator
		cmd.Env = c.commonEnv
		if b, err := cmd.CombinedOutput(); err != nil {
			log.Fatalf("bootstrap: %v: %s", err, b)
		}
	}
	add := func(name, role string, listen bool) *proc {
		p := &proc{name: name, role: role, metrics: freePort()}
		p.env = append(append([]string{}, c.commonEnv...), "TASKIEM_METRICS_LISTEN="+p.metrics)
		if listen {
			p.listen = freePort()
			p.env = append(p.env, "TASKIEM_LISTEN="+p.listen, "TASKIEM_EDGE_LISTEN="+p.listen)
		}
		c.procs = append(c.procs, p)
		return p
	}
	c.api = add("api", "api", true)
	c.edge = add("edge", "edge", true)
	add("orchestrator", "orchestrator", false)
	add("scheduler", "scheduler", false)
	for i := 1; i <= workers; i++ {
		add("worker"+strconv.Itoa(i), "worker", false)
	}
	for _, p := range c.procs {
		c.start(p)
	}
	for _, p := range c.procs {
		c.waitReady(p)
	}
	for i := 0; i < tenants; i++ {
		c.tenants = append(c.tenants, c.setupTenant(fmt.Sprintf("load%d@load.test", i)))
	}
	return c
}

func (c *cluster) start(p *proc) {
	if p.logf == nil {
		f, err := os.Create(filepath.Join(c.out, p.name+".log"))
		if err != nil {
			log.Fatal(err)
		}
		p.logf = f
	}
	cmd := exec.Command(c.bin, "serve", "--role", p.role) //nolint:gosec // the binary under test
	cmd.Env = p.env
	cmd.Stdout, cmd.Stderr = p.logf, p.logf
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	p.mu.Lock()
	p.cmd, p.exited = cmd, make(chan struct{})
	ch := p.exited
	p.mu.Unlock()
	go func() { _ = cmd.Wait(); close(ch) }()
}

func (c *cluster) waitReady(p *proc) {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + p.metrics + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Fatalf("%s not ready; see %s", p.name, filepath.Join(c.out, p.name+".log")) //nolint:gosec // our own process names
}

// kill sends SIGKILL and waits for the process to go.
func (c *cluster) kill(p *proc) {
	p.mu.Lock()
	cmd, ch := p.cmd, p.exited
	p.mu.Unlock()
	_ = cmd.Process.Signal(syscall.SIGKILL)
	<-ch
}

// stopGracefully sends SIGTERM and waits.
func (c *cluster) stopGracefully(p *proc) {
	p.mu.Lock()
	cmd, ch := p.cmd, p.exited
	p.mu.Unlock()
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-ch:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		<-ch
	}
}

func (c *cluster) restart(p *proc) {
	p.restarts++
	c.start(p)
	c.waitReady(p)
}

func (c *cluster) close() {
	for _, p := range c.procs {
		p.mu.Lock()
		cmd := p.cmd
		p.mu.Unlock()
		if cmd != nil && cmd.ProcessState == nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
	}
	for _, p := range c.procs {
		select {
		case <-p.exited:
		case <-time.After(45 * time.Second):
			_ = p.cmd.Process.Kill()
		}
		_ = p.logf.Close()
	}
	c.fakeSrv.Close()
}

func (c *cluster) pid(p *proc) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd.Process.Pid
}

// call makes an API request and decodes JSON; status >= 300 is an error.
func call(method, url, token string, body any) (map[string]any, int, error) {
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, url, r) //nolint:gosec // the cluster this tool started, on loopback
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // as above
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if resp.StatusCode >= 300 {
		return m, resp.StatusCode, fmt.Errorf("%s %s: %d %s", method, url, resp.StatusCode, b)
	}
	return m, resp.StatusCode, nil
}

func must(m map[string]any, _ int, err error) map[string]any {
	if err != nil {
		log.Fatal(err)
	}
	return m
}

// The load workflow: a webhook starts it; a sandbox code step, a read and
// an idempotent write on the fake Paystack, then a transform. Four steps,
// three of them on workers.
const loadWorkflow = `{"schema":"wd/v1","id":"wf_load","version":1,"name":"load",
 "trigger":{"type":"webhook","config":{"path":"/load","auth":"none"}},
 "steps":[
  {"id":"price","type":"code","config":{"language":"typescript","source":"export default (input: {n: number}) => ({ amount: 10000 + (input.n % 1000) })"},"input":{"n":"=trigger.body.n"}},
  {"id":"balance","type":"connector","connector":"paystack@1","action":"check_balance","needs":["price"],"input":{}},
  {"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","needs":["balance"],
   "input":{"amount":"=steps.price.output.amount","recipient":"RCP_load","reason":"=run.id"}},
  {"id":"out","type":"transform","needs":["pay"],"config":{"output":"=steps.pay.output.reference"}}]}`

// The unsafe workflow: one SMS, an unsafe write (no idempotency key, no
// lookup), with the run id as its text.
const smsWorkflow = `{"schema":"wd/v1","id":"wf_sms","version":1,"name":"sms",
 "trigger":{"type":"webhook","config":{"path":"/sms","auth":"none"}},
 "steps":[{"id":"sms","type":"connector","connector":"termii@1","action":"send_sms","input":{"to":"2348000000000","sms":"=run.id"}}]}`

func (c *cluster) setupTenant(email string) tenant {
	base := "http://" + c.api.listen
	login := must(call("POST", base+"/v1/auth/login", "", map[string]any{"email": email, "password": "correct horse battery", "bearer": true}))
	t := tenant{id: login["tenant_id"].(string), token: login["token"].(string)}
	must(call("POST", base+"/v1/connections", t.token, map[string]any{"environment": "prod", "connector": "paystack@1", "credentials": map[string]string{"secret_key": "sk_test_load"}}))
	must(call("POST", base+"/v1/connections", t.token, map[string]any{"environment": "prod", "connector": "termii@1", "credentials": map[string]string{"api_key": "tm_load", "sender_id": "Load"}}))
	for _, def := range []string{loadWorkflow, smsWorkflow} {
		wf := must(call("POST", base+"/v1/workflows", t.token, map[string]any{"name": "load", "definition": json.RawMessage(def)}))["id"].(string)
		must(call("POST", base+"/v1/workflows/"+wf+"/versions/1/publish", t.token, nil))
	}
	return t
}

// hook delivers one webhook to the edge and returns the run id.
func (c *cluster) hook(client *http.Client, t tenant, path string, body map[string]any) (string, int, time.Duration, error) {
	raw, _ := json.Marshal(body)
	start := time.Now()
	resp, err := client.Post("http://"+c.edge.listen+"/hooks/"+t.id+path, "application/json", bytes.NewReader(raw)) //nolint:gosec // the edge this tool started, on loopback
	took := time.Since(start)
	if err != nil {
		return "", 0, took, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	id, _ := m["run_id"].(string)
	if resp.StatusCode >= 300 {
		return "", resp.StatusCode, took, fmt.Errorf("%d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return id, resp.StatusCode, took, nil
}

// scrapeAll reads every process's metrics.
func (c *cluster) scrapeAll(ctx context.Context) snapshot {
	var all snapshot
	for _, p := range c.procs {
		s, err := scrape(ctx, "http://"+p.metrics+"/metrics")
		if err != nil {
			log.Printf("scrape %s: %v", p.name, err) //nolint:gosec // our own process names
			continue
		}
		for i := range s {
			s[i].labels["process"] = p.name
		}
		all = append(all, s...)
	}
	return all
}

// profile fetches a CPU profile (seconds long) or a heap profile from a
// process's metrics port, where TASKIEM_PPROF=true serves them.
func (c *cluster) profile(p *proc, kind string, seconds int, file string) error {
	u := "http://" + p.metrics + "/debug/pprof/" + kind
	if kind == "profile" {
		u += "?seconds=" + strconv.Itoa(seconds)
	}
	client := &http.Client{Timeout: time.Duration(seconds+30) * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %d", u, resp.StatusCode)
	}
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, resp.Body)
	return err
}
