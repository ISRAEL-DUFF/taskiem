package canary_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/canary"
	"github.com/israel-duff/taskiem/engine/wd"
)

func TestDefinitionIsValid(t *testing.T) {
	def, err := wd.Load([]byte(canary.Definition))
	if err != nil {
		t.Fatal(err)
	}
	if def.ID != canary.WorkflowID {
		t.Errorf("id %s", def.ID)
	}
}

// fake is the hooks endpoint and the runs API of a Taskiem.
type fake struct {
	mu      sync.Mutex
	secret  string
	status  int    // webhook answer; default 202
	finish  string // the run's final status; default completed
	wrong   bool   // complete with the wrong output
	polls   int
	pending int // polls answered "running" first
	n       float64
	records []canary.Result
}

func (f *fake) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/hooks/t1"+canary.HookPath:
			body, _ := io.ReadAll(r.Body)
			if r.Header.Get("X-Taskiem-Signature") != canary.Sign(f.secret, body) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var in struct {
				Probe string  `json:"probe"`
				N     float64 `json:"n"`
			}
			_ = json.Unmarshal(body, &in)
			if in.Probe == "" {
				t.Error("no probe id for deduplication")
			}
			f.n = in.N
			if f.status != 0 {
				w.WriteHeader(f.status)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"run_id":"r1"}`))
		case r.Method == "GET" && r.URL.Path == "/v1/runs/r1":
			if r.Header.Get("Authorization") != "Bearer key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.polls++
			st := "running"
			if f.polls > f.pending {
				st = f.finish
				if st == "" {
					st = "completed"
				}
			}
			out := f.n*2 + 1
			if f.wrong {
				out++
			}
			_, _ = fmt.Fprintf(w, `{"run":{"status":%q},"events":[{"type":"RunStarted"},{"type":"StepCompleted","step_id":"out","payload":{"output":%v}},{"type":"RunCompleted"}]}`, st, out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestProbe(t *testing.T) {
	cases := []struct {
		name         string
		f            fake
		secret       string
		ok           bool
		stage, check string
	}{
		{name: "ok", f: fake{pending: 2}, ok: true},
		{name: "bad signature", secret: "wrong", stage: "accept", check: "http_401"},
		{name: "ingest down", f: fake{status: 503}, stage: "accept", check: "http_503"},
		{name: "run failed", f: fake{finish: "failed"}, stage: "complete", check: "run_failed"},
		{name: "wrong output", f: fake{wrong: true}, stage: "output"},
		{name: "never finishes", f: fake{pending: 1 << 30}, stage: "complete", check: "timeout"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &c.f
			f.secret = "s3cret"
			srv := f.server(t)
			defer srv.Close()
			secret := c.secret
			if secret == "" {
				secret = f.secret
			}
			p := &canary.Prober{HookURL: srv.URL + "/hooks/t1" + canary.HookPath, Secret: secret, APIURL: srv.URL, APIKey: "key",
				Timeout: 500 * time.Millisecond, Poll: 10 * time.Millisecond,
				Record: func(_ context.Context, r canary.Result) error {
					f.mu.Lock()
					f.records = append(f.records, r)
					f.mu.Unlock()
					return nil
				}}
			r := p.Probe(context.Background())
			if r.OK != c.ok || r.Stage != c.stage || (c.check != "" && r.Detail != c.check) {
				t.Fatalf("got %+v", r)
			}
			if len(f.records) != 1 || f.records[0].Code() != r.Code() {
				t.Errorf("recorded %+v", f.records)
			}
			if c.ok && (r.Complete <= 0 || r.Accept <= 0 || r.Code() != "") {
				t.Errorf("timings %+v", r)
			}
			if !c.ok && !strings.HasPrefix(r.Code(), c.stage+":") {
				t.Errorf("code %q", r.Code())
			}
		})
	}
}
