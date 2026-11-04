package runtime_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/privatehttp"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/tunnel/tunneltest"
)

// A connection that names a tunnel reaches its equipment through the
// customer's bastion: the worker dials the bastion, and the bastion opens
// the one connection to the equipment (decision 0013). The private key
// never appears in the run's history.
func TestConnectorRunsThroughATunnel(t *testing.T) {
	e := rt.New(t)
	var hits atomic.Int32
	equipment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"active":false}`)
	}))
	defer equipment.Close()
	addr := equipment.Listener.Addr().String()
	host, port, _ := net.SplitHostPort(addr)
	b, key := tunneltest.Start(t, addr)
	bh, bp := b.HostPort()
	if err := e.Registry.Register(privatehttp.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "privatehttp", "isp-radius", "custom", map[string]string{
		"host": host, "port": port, "scheme": "http",
		"tunnel_host": bh, "tunnel_port": bp, "tunnel_user": "taskiem", "tunnel_private_key": key, "tunnel_host_key": b.Pin(),
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, wfDoc(`{"id":"check","type":"connector","connector":"privatehttp@1","action":"get","connection":"isp-radius","input":{"path":"/radius/ada"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}
	if hits.Load() != 1 {
		t.Fatalf("the equipment saw %d requests", hits.Load())
	}
	if ch := b.Channels(); len(ch) != 1 || ch[0] != "direct-tcpip" {
		t.Fatalf("the bastion was asked for %v", ch)
	}
	// The session is closed once the call is over.
	for i := 0; i < 200 && b.Sessions.Load() != 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if b.Sessions.Load() != 0 {
		t.Fatal("the tunnel outlived the call")
	}
	var leaked int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE run_id = $1 AND payload::text LIKE '%PRIVATE KEY%'`, ref.ID).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal("the private key reached the run's history")
	}
}
