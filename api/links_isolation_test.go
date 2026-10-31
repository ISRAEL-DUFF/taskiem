package api_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/runtime"
)

// scopeLog is the query log the isolation harness reads (business links
// §14): every statement the API runs, with the tenant scope its
// transaction set.
type scopeLog struct {
	mu    sync.Mutex
	scope map[*pgx.Conn]string
	seen  []loggedQuery
}

type loggedQuery struct {
	scope string
	sql   string
	args  []any
}

func (l *scopeLog) TraceQueryStart(ctx context.Context, conn *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	sql := strings.TrimSpace(d.SQL)
	switch {
	case strings.EqualFold(sql, "begin") || strings.HasPrefix(strings.ToLower(sql), "begin "):
		l.scope[conn] = ""
	case strings.Contains(sql, "set_config('app.tenant_scope'") && len(d.Args) > 0:
		l.scope[conn] = fmt.Sprint(d.Args[0])
	}
	l.seen = append(l.seen, loggedQuery{scope: l.scope[conn], sql: sql, args: d.Args})
	// The door narrows the transaction to the other business, inside the
	// function: what follows is that business's, which only the database
	// knows (its policies hold it to that scope).
	if strings.Contains(sql, "taskiem_link_enter(") {
		l.scope[conn] = entered
	}
	return ctx
}

// entered is the scope the log gives a transaction after the door.
const entered = "entered"

func (l *scopeLog) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (l *scopeLog) take() []loggedQuery {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.seen
	l.seen = nil
	return out
}

// crossing are the functions through which one business may name another
// (migration 00275): a request names its target, and the door enters it.
var crossing = regexp.MustCompile(`\btaskiem_link_(request|enter|answer|by_code|offer_to|search)\(|\btaskiem_links_of\(`)

// TestLinkIsolation is the isolation harness of §14 for L0: two
// businesses search, ask, accept and enter through the API while every
// statement is logged with its transaction's scope. No transaction holds
// both businesses; a transaction of one names the other only in a call to
// the link functions; no statement reads business_links itself; and each
// side's people are told in a transaction of that side alone.
func TestLinkIsolation(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	log := &scopeLog{scope: map[*pgx.Conn]string{}}
	cfg := w.env.Store.Pool.Config()
	cfg.ConnConfig.Tracer = log
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	w.srv.Store = &runtime.Store{Pool: traced, Registry: w.env.Store.Registry, PII: w.env.Store.PII}

	ada := w.tenant(t, "Ada's Kitchen", "ada@isolation.test")
	dangote := w.tenant(t, "Dangote Stores", "d@isolation.test")
	const adaOn, dangoteOn = "+2348010000341", "+2348010000342"
	w.bind(t, ada, adaOn)
	w.bind(t, dangote, dangoteOn)
	adaID, dangoteID := tenantOf(t, ada), tenantOf(t, dangote)
	dangote.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	log.take()

	// Ada searches and asks, Dangote accepts (by chat too), Ada ends it.
	ada.must(200, "GET", "/v1/links/search?q=dangote", nil)
	ada.must(201, "POST", "/v1/links", map[string]any{"business": dangoteID, "role": "supplier"})
	w.srv.WaitBackground()
	l := dangote.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	if r := w.press(t, dangoteOn, "lk:accept:"+l["id"].(string)); len(r) != 1 || !strings.Contains(r[0].Text, "Linked with") {
		t.Fatalf("accept: %+v", r)
	}
	w.srv.WaitBackground()
	ada.must(200, "GET", "/v1/links", nil)
	code := dangote.must(200, "GET", "/v1/link-settings", nil)["code"].(string)
	ada.must(200, "GET", "/v1/links/code/"+code, nil)
	// They talk (L3): a message in both, read with the double tick.
	ada.must(201, "POST", "/v1/link-chats/"+l["id"].(string)+"/messages", map[string]any{"text": "Hello from Ada"})
	dangote.must(200, "POST", "/v1/link-chats/"+l["id"].(string)+"/read", nil)
	w.srv.WaitBackground()
	ada.must(200, "POST", "/v1/links/"+l["id"].(string)+"/end", nil)
	w.srv.WaitBackground()

	ofEach := checkIsolation(t, log.take(), adaID, dangoteID)
	// Both sides ran their own transactions: the requester's, and the
	// other's people told in its own.
	if ofEach[adaID] == 0 || ofEach[dangoteID] == 0 {
		t.Errorf("statements by business: %v", ofEach)
	}
}

// checkIsolation reads the statements logged while two businesses traded
// through their link: no statement reads business_links; no transaction
// holds both; a transaction of one names the other only in a call to the
// link functions, as its own customer key link:<the other> (§6.1), or
// after the door, in the other's scope. It answers how
// many statements each ran in its own scope.
func checkIsolation(t *testing.T, seen []loggedQuery, a, b string) map[string]int {
	t.Helper()
	names := func(q loggedQuery, id string) bool {
		if strings.Contains(q.sql, id) {
			return true
		}
		for _, arg := range q.args {
			switch v := arg.(type) {
			case uuid.UUID:
				if v.String() == id {
					return true
				}
			case string:
				// A seller's own key for its linked customer, link:<the
				// buyer> (§6.1), is its record, in its own scope.
				if strings.Contains(strings.ReplaceAll(v, "link:"+id, ""), id) {
					return true
				}
			case []uuid.UUID:
				for _, u := range v {
					if u.String() == id {
						return true
					}
				}
			}
		}
		return false
	}
	if len(seen) < 20 {
		t.Fatalf("only %d statements logged", len(seen))
	}
	ofEach := map[string]int{}
	for _, q := range seen {
		if strings.Contains(q.sql, "business_links") {
			t.Errorf("a statement reads business_links itself: %s", q.sql)
		}
		if strings.Contains(q.scope, ",") {
			t.Errorf("a transaction holds more than one business (%s): %s", q.scope, q.sql)
		}
		for _, pair := range [][2]string{{a, b}, {b, a}} {
			mine, other := pair[0], pair[1]
			if !strings.Contains(q.scope, mine) {
				continue
			}
			ofEach[mine]++
			if names(q, other) && !crossing.MatchString(q.sql) {
				t.Errorf("a transaction of one business names the other outside the link functions: %s %v", q.sql, q.args)
			}
		}
	}
	return ofEach
}
