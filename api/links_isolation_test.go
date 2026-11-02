package api_test

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/sender"
)

// scopeLog is the query log the isolation harness reads (business links
// §14): every statement the API runs, with the tenant scope its
// transaction set.
type scopeLog struct {
	mu    sync.Mutex
	scope map[*pgx.Conn]string
	seen  []loggedQuery
	// rescoped are the statements that set a scope after the door, in the
	// same transaction: the door's narrowing must hold to its end.
	rescoped []string
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
		if l.scope[conn] == entered {
			l.rescoped = append(l.rescoped, fmt.Sprintf("%s %v", sql, d.Args))
		}
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
// (migration 00279): a request names its target, and the door enters it.
var crossing = regexp.MustCompile(`\btaskiem_link_(request|enter|answer|by_code|offer_to|search)\(|\btaskiem_links_of\(`)

// TestLinkIsolation is the isolation harness of §14 for L0: two
// businesses search, ask, accept and enter through the API while every
// statement is logged with its transaction's scope, on every pool the
// server holds. No transaction holds both businesses; a transaction of
// one names the other only in a call to the link functions; no statement
// reads business_links itself; no transaction sets a scope again after
// the door; each side's people are told in a transaction of that side
// alone; and an acceptance goes through the door once, audited in both
// chains, with the account the seller issues its linked buyer.
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
	// Every pool the server runs statements on: the store's, the invoices'
	// (the account issued at acceptance, §6.3) and the reach book's.
	w.srv.Store = &runtime.Store{Pool: traced, Registry: w.env.Store.Registry, PII: w.env.Store.PII}
	hash := bytes.Repeat([]byte{8}, 32)
	w.srv.Invoices = &invoicing.Invoices{Pool: traced, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: hash}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.Reach = &reach.Book{Pool: traced, Vault: w.env.Vault, HashKey: hash}
	rails := &linkRails{}
	w.srv.AccountRails = rails

	ada := w.tenant(t, "Ada's Kitchen", "ada@isolation.test")
	dangote := w.tenant(t, "Dangote Stores", "d@isolation.test")
	const adaOn, dangoteOn = "+2348010000341", "+2348010000342"
	w.bind(t, ada, adaOn)
	w.bind(t, dangote, dangoteOn)
	adaID, dangoteID := tenantOf(t, ada), tenantOf(t, dangote)
	dangote.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	// Dangote's owner has written, so the request's buttons reach them.
	w.say(t, dangoteOn, "links")
	log.take()

	// Ada searches and asks, Dangote accepts (by chat too), Ada ends it.
	ada.must(200, "GET", "/v1/links/search?q=dangote", nil)
	ada.must(201, "POST", "/v1/links", map[string]any{"business": dangoteID, "role": "supplier"})
	w.srv.WaitBackground()
	l := dangote.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	told := w.graph.Sent(dangoteOn)
	if len(told) == 0 || len(told[len(told)-1].Buttons) != 3 {
		t.Fatalf("Dangote told: %+v", told)
	}
	asking := log.take()
	if r := w.press(t, dangoteOn, told[len(told)-1].Buttons[0].ID); len(r) != 1 || !strings.Contains(r[0].Text, "Linked with") {
		t.Fatalf("accept: %+v", r)
	}
	w.srv.WaitBackground()
	accepting := log.take()
	// The acceptance: through the door once (the event for both sides'
	// workflows), audited in both chains, and Dangote, whom Ada buys
	// from, issued her an account number.
	doors := 0
	for _, q := range accepting {
		if strings.Contains(q.sql, "taskiem_link_enter(") {
			doors++
		}
	}
	if doors != 1 {
		t.Errorf("an acceptance went through the door %d times", doors)
	}
	for _, action := range []string{"link.accept", "link.event"} {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(DISTINCT tenant_id) FROM audit_log WHERE action = $1 AND target = $2`, action, l["id"]).Scan(&n); err != nil || n != 2 {
			t.Errorf("%s audited in %d chains: %v", action, n, err)
		}
	}
	if rails.asked != 1 {
		t.Errorf("accounts issued at acceptance: %d", rails.asked)
	}
	ada.must(200, "GET", "/v1/links", nil)
	code := dangote.must(200, "GET", "/v1/link-settings", nil)["code"].(string)
	ada.must(200, "GET", "/v1/links/code/"+code, nil)
	// They talk (L3): a message in both, read with the double tick.
	ada.must(201, "POST", "/v1/link-chats/"+l["id"].(string)+"/messages", map[string]any{"text": "Hello from Ada"})
	dangote.must(200, "POST", "/v1/link-chats/"+l["id"].(string)+"/read", nil)
	w.srv.WaitBackground()
	ada.must(200, "POST", "/v1/links/"+l["id"].(string)+"/end", nil)
	w.srv.WaitBackground()

	seen := append(append(asking, accepting...), log.take()...)
	ofEach := checkIsolation(t, seen, adaID, dangoteID)
	// Both sides ran their own transactions: the requester's, and the
	// other's people told in its own.
	if ofEach[adaID] == 0 || ofEach[dangoteID] == 0 {
		t.Errorf("statements by business: %v", ofEach)
	}
	if len(log.rescoped) > 0 {
		t.Errorf("a transaction set its scope again after the door: %q", log.rescoped)
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

// count is how many statements logged so far say s.
func (l *scopeLog) count(s string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, q := range l.seen {
		if strings.Contains(q.sql, s) {
			n++
		}
	}
	return n
}

// TestLinkIsolationFiles is the harness for a file sent in the
// conversation (L3): our copy is stored before the message's transaction,
// and the other business's copy only once that transaction has entered
// the door (taskiem_link_enter: the scope narrowed, the crossing audited
// on both sides); no statement names the other side outside the link
// functions. The same for a forward.
func TestLinkIsolationFiles(t *testing.T) {
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
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	w.srv.Media.Scanner = chatScanner{}

	ada := w.tenant(t, "Ada's Kitchen", "ada@isolation-files.test")
	dangote := w.tenant(t, "Dangote Stores", "d@isolation-files.test")
	cherry := w.tenant(t, "Cherry Blocks", "c@isolation-files.test")
	adaID, dangoteID, cherryID := tenantOf(t, ada), tenantOf(t, dangote), tenantOf(t, cherry)
	pair := func(a, b *client, bID string) string {
		b.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
		a.must(201, "POST", "/v1/links", map[string]any{"business": bID, "role": "supplier"})
		l := b.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
		b.must(200, "POST", "/v1/links/"+l+"/accept", nil)
		w.srv.WaitBackground()
		return l
	}
	withDangote := pair(ada, dangote, dangoteID)
	withCherry := pair(ada, cherry, cherryID)

	// Each write to a business's storage, with how many doors had been
	// entered by then.
	type put struct {
		project string
		entered int
	}
	var puts []put
	store.onPut = func(project string) { puts = append(puts, put{project, log.count("taskiem_link_enter(")}) }
	log.take()

	code, out := ada.sendFile(t, withDangote, "bags.jpg", photo(t), map[string]string{"text": "The new bags"})
	if code != 201 {
		t.Fatalf("photo: %d %v", code, out)
	}
	w.srv.WaitBackground()
	seen := log.take()
	checkIsolation(t, seen, adaID, dangoteID)
	if len(puts) != 2 || puts[0].project != "prj_"+adaID || puts[1].project != "prj_"+dangoteID {
		t.Fatalf("the copies: %+v", puts)
	}
	if puts[0].entered != 0 || puts[1].entered != 1 {
		t.Errorf("the other side's copy is made after the door is entered: %+v", puts)
	}
	// The other side's copy is made in the message's transaction: the
	// door, the copy and the other side's row, with no commit between.
	entered := -1
	for i, q := range seen {
		switch {
		case strings.Contains(q.sql, "taskiem_link_enter("):
			entered = i
		case entered >= 0 && strings.EqualFold(strings.TrimSpace(q.sql), "commit"):
			entered = -2
		case entered == -2 && strings.Contains(q.sql, "INSERT INTO link_messages"):
			t.Error("the other side's row written after the door's transaction committed")
		}
	}

	// A forward to Cherry: Cherry's copy after the door into Cherry.
	puts = nil
	log.take()
	ada.must(201, "POST", "/v1/link-chats/"+withDangote+"/forward", map[string]any{"message": out["message"].(map[string]any)["id"], "to": []string{withCherry}})
	w.srv.WaitBackground()
	seen = log.take()
	checkIsolation(t, seen, adaID, cherryID)
	checkIsolation(t, seen, adaID, dangoteID)
	if len(puts) != 2 || puts[0].project != "prj_"+adaID || puts[1].project != "prj_"+cherryID || puts[0].entered != 0 || puts[1].entered != 1 {
		t.Errorf("the forwarded copies: %+v", puts)
	}
}
