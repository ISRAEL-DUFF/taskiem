package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/runfiles"
	"github.com/israel-duff/taskiem/engine/runfiles/runfilestest"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/voice"
)

// A run kept "conversations" long (decision 0062 V5) is kept as long as
// the business keeps its conversations, and its files end with it.
func TestRunKeptWithConversations(t *testing.T) {
	e := rt.New(t)
	e.RunFiles = &runfiles.Service{Pool: e.DB.App, Store: runfilestest.New()}
	conn := &connector.Connector{Manifest: connector.MustParse([]byte(fetcherManifest)), Actions: map[string]connector.Action{
		"fetch": connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			ref, err := req.Files.Put(ctx, "voice.ogg", voice.TestOgg(2, 8), false)
			return connector.Response{Output: map[string]any{"file": ref}}, err
		}),
		"size": connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
			return connector.Response{Output: map[string]any{}}, nil
		}),
	}}
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Admin.Exec(context.Background(), `INSERT INTO tenant_channel_settings (tenant_id, languages, default_language, voice_notes, conversation_days, updated_by)
		VALUES ($1, '{}', 'en', false, 7, 'test')`, e.Tenant); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, `{"schema":"wd/v1","id":"wf_keep","version":1,"name":"keep","trigger":{"type":"manual"},"settings":{"retention":"conversations"},
	  "steps":[{"id":"fetch","type":"connector","connector":"fetcher@1","action":"fetch","input":{}}]}`)
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s", st)
	}
	var keep, file time.Time
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT r.retain_until, f.expires_at FROM runs r JOIN run_files f ON f.run_id = r.id WHERE r.id = $1`, ref.ID).Scan(&keep, &file); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(keep); d < 6*24*time.Hour || d > 8*24*time.Hour || !file.Equal(keep) {
		t.Fatalf("run kept until %v (in %v), its file until %v", keep, d, file)
	}
}
