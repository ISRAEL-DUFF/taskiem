package runtime_test

import (
	"context"
	"strings"
	"testing"

	filesconn "github.com/israel-duff/taskiem/connectors/files"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/runfiles"
	"github.com/israel-duff/taskiem/engine/runfiles/runfilestest"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

const fetcherManifest = `
manifest: connector/v1
id: fetcher
version: 1.0.0
name: Fetcher
category: other
auth: { type: none }
actions:
  fetch:
    title: Fetch a voice note
    class: read
    input: { type: object, properties: { name: { type: string } } }
  size:
    title: Read a file's size
    class: read
    input: { type: object, required: [file], properties: { file: {} } }
`

// A connector keeps a file it fetched through the run's file store and
// answers its reference; a later step reads it back by that reference,
// and files@1 links to it. The bytes never enter the run's history.
func TestConnectorsKeepAndReadRunFiles(t *testing.T) {
	e := rt.New(t)
	store := runfilestest.New()
	e.RunFiles = &runfiles.Service{Pool: e.DB.App, Store: store}
	voice := "OggS" + strings.Repeat("v", 40)
	conn := &connector.Connector{Manifest: connector.MustParse([]byte(fetcherManifest)), Actions: map[string]connector.Action{
		"fetch": connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			if req.Files == nil {
				t.Fatal("no file store in a run")
			}
			ref, err := req.Files.Put(ctx, "voice.ogg", []byte(voice), true)
			return connector.Response{Output: map[string]any{"file": ref}}, err
		}),
		"size": connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			b, ref, err := req.Files.Read(ctx, req.Input["file"])
			if err != nil {
				return connector.Response{}, err
			}
			return connector.Response{Output: map[string]any{"bytes": len(b), "name": ref["name"]}}, nil
		}),
	}}
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	if err := e.Registry.Register(filesconn.New(e.RunFiles)); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, wfDoc(`{"id":"fetch","type":"connector","connector":"fetcher@1","action":"fetch","input":{}},
		{"id":"size","type":"connector","connector":"fetcher@1","action":"size","needs":["fetch"],"input":{"file":"=steps.fetch.output.file"}},
		{"id":"link","type":"connector","connector":"files@1","action":"link","needs":["fetch"],"input":{"file":"=steps.fetch.output.file","ttl_minutes":5}},
		{"id":"uses","type":"transform","needs":["link"],"config":{"output":"=steps.link.output.url.startsWith('https://storage.test/')"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}
	if store.Count() != 1 || store.Scanned() != 1 {
		t.Fatalf("%d objects, %d scanned: want one kept and scanned", store.Count(), store.Scanned())
	}
	var leaked int
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM run_events WHERE run_id = $1 AND payload::text LIKE '%vvvvvvvvvv%'`, ref.ID).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal("a file's bytes reached the run's history")
	}
	var size, link string
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT payload::text FROM run_events WHERE run_id = $1 AND step_id = 'size' AND type = 'StepCompleted'`, ref.ID).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(size, `"bytes": 44`) && !strings.Contains(size, `"bytes":44`) {
		t.Fatalf("size step read %s", size)
	}
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT payload::text FROM run_events WHERE run_id = $1 AND step_id = 'link' AND type = 'StepCompleted'`, ref.ID).Scan(&link); err != nil {
		t.Fatal(err)
	}
	// The link opens a customer's file: sealed in history, still read by
	// the run's own steps.
	if strings.Contains(link, "https://storage.test/") || !strings.Contains(link, `"$pii"`) {
		t.Fatalf("the link is in history in the clear: %s", link)
	}
	var uses string
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT payload::text FROM run_events WHERE run_id = $1 AND step_id = 'uses' AND type = 'StepCompleted'`, ref.ID).Scan(&uses); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uses, "true") {
		t.Fatalf("a later step could not read the link: %s", uses)
	}
}
