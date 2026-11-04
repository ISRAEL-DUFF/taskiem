package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/israel-duff/taskiem/engine/telemetry"
)

// streamEvent is one event of the Chat page's live stream.
type streamEvent struct {
	ID    string
	Event string
	Data  map[string]any
}

// chatStream opens the stream as a client and sends what it hears, with
// the time each event was heard, until the test is done.
func chatStream(t *testing.T, c *client, lastID string) (<-chan streamEvent, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+"/v1/chat/stream", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("the stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	events := make(chan streamEvent, 200)
	go func() {
		defer func() { _ = resp.Body.Close() }()
		defer close(events)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var ev streamEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				ev.ID = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				ev.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.Data)
			case line == "" && ev.Event != "":
				events <- ev
				ev = streamEvent{}
			}
		}
	}()
	return events, cancel
}

// next waits for an event of a kind (others pass), up to d; nil if none came.
func next(events <-chan streamEvent, kind string, d time.Duration, ok func(streamEvent) bool) *streamEvent {
	timeout := time.After(d)
	for {
		select {
		case ev, open := <-events:
			if !open {
				return nil
			}
			if ev.Event == kind && (ok == nil || ok(ev)) {
				return &ev
			}
		case <-timeout:
			return nil
		}
	}
}

// The Chat page's live stream: a message reaches the other side's stream
// within a couple of seconds, a read mark and a reaction reach the sender's,
// a deletion and a change of settings reach the person's own other tabs,
// typing shows and stops; a client that reconnects with Last-Event-ID gets
// what it missed and nothing twice; another business hears none of it; a
// business may hold only so many streams open; and the stream needs a session.
func TestChatStream(t *testing.T) {
	w := newWAWorld(t)
	seller, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	other := w.tenant(t, "Someone Else", "else@stream.test")

	buyerEvents, closeBuyer := chatStream(t, buyer, "")
	defer closeBuyer()
	sellerEvents, closeSeller := chatStream(t, seller, "")
	defer closeSeller()
	otherEvents, closeOther := chatStream(t, other, "")
	defer closeOther()
	if next(buyerEvents, "ready", 3*time.Second, nil) == nil {
		t.Fatal("no ready event")
	}
	next(sellerEvents, "ready", 3*time.Second, nil)

	// A message arrives on the other side's stream within a couple of seconds.
	sent := time.Now()
	m := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Rice is back"})["message"].(map[string]any)
	ev := next(buyerEvents, "message", 4*time.Second, func(e streamEvent) bool {
		return e.Data["message"].(map[string]any)["id"] == m["id"]
	})
	if ev == nil {
		t.Fatal("the message did not reach the buyer's stream")
	}
	if took := time.Since(sent); took > 3*time.Second {
		t.Errorf("it took %v", took)
	}
	got := ev.Data["message"].(map[string]any)
	if got["body"] != "Rice is back" || got["mine"] == true || got["read_at"] != nil {
		t.Errorf("the message as the buyer hears it: %v", got)
	}
	if _, err := strconv.ParseInt(ev.ID, 10, 64); err != nil {
		t.Errorf("an event carries its time as its id: %q", ev.ID)
	}
	// The sender's own other tabs hear it too, as theirs.
	if own := next(sellerEvents, "message", 4*time.Second, func(e streamEvent) bool { return e.Data["message"].(map[string]any)["id"] == m["id"] }); own == nil || own.Data["message"].(map[string]any)["mine"] != true {
		t.Errorf("the seller's own stream: %v", own)
	}

	// Read on the buyer's side: the seller's stream hears its message read.
	buyer.must(200, "POST", "/v1/link-chats/"+link+"/read", nil)
	read := next(sellerEvents, "message", 4*time.Second, func(e streamEvent) bool {
		msg := e.Data["message"].(map[string]any)
		return msg["id"] == m["id"] && msg["read_at"] != nil
	})
	if read == nil {
		t.Error("the read mark did not reach the sender's stream")
	}

	// A reaction reaches the other side's stream, with the reactions on it.
	buyer.must(200, "PUT", "/v1/link-chats/"+link+"/messages/"+m["id"].(string)+"/reaction", map[string]any{"emoji": "👍"})
	if r := next(sellerEvents, "message", 4*time.Second, func(e streamEvent) bool {
		return e.Data["message"].(map[string]any)["reactions"] != nil
	}); r == nil {
		t.Error("the reaction did not reach the seller's stream")
	} else if rx := r.Data["message"].(map[string]any)["reactions"].([]any)[0].(map[string]any); rx["emoji"] != "👍" || rx["mine"] != nil {
		t.Errorf("as the seller hears it: %v", rx)
	}

	// Typing shows, and stops.
	seller.must(204, "POST", "/v1/link-chats/"+link+"/typing", nil)
	if ty := next(buyerEvents, "typing", 5*time.Second, func(e streamEvent) bool { return len(e.Data["typing"].([]any)) == 1 }); ty == nil || ty.Data["typing"].([]any)[0] != link {
		t.Errorf("typing did not show: %v", ty)
	}

	// Settings and deletion reach the person's own streams (here the buyer's).
	buyer.must(200, "PUT", "/v1/link-chats/"+link, map[string]any{"muted": true})
	if c := next(buyerEvents, "conversation", 4*time.Second, nil); c == nil || c.Data["muted"] != true || c.Data["link"] != link {
		t.Errorf("a change of settings: %v", c)
	}
	buyer.must(204, "POST", "/v1/link-chats/"+link+"/messages/"+m["id"].(string)+"/hide", nil)
	if g := next(buyerEvents, "gone", 4*time.Second, nil); g == nil || g.Data["id"] != m["id"] {
		t.Errorf("a deletion for me: %v", g)
	}

	// Typing stops (it lasts six seconds).
	if ty := next(buyerEvents, "typing", 10*time.Second, func(e streamEvent) bool { return len(e.Data["typing"].([]any)) == 0 }); ty == nil {
		t.Error("typing did not stop")
	}

	// Another business heard none of it.
	if e := next(otherEvents, "message", 500*time.Millisecond, nil); e != nil {
		t.Errorf("another business heard a message: %v", e.Data)
	}
	if e := next(otherEvents, "conversation", 100*time.Millisecond, nil); e != nil {
		t.Errorf("another business heard a change: %v", e.Data)
	}

	// Reconnecting after what it last heard: what it missed, once.
	last := ev.ID
	closeBuyer()
	m2 := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "On its way"})["message"].(map[string]any)
	again, closeAgain := chatStream(t, buyer, last)
	defer closeAgain()
	heard := map[string]int{}
	deadline := time.After(4 * time.Second)
collect:
	for {
		select {
		case e := <-again:
			if e.Event == "message" {
				heard[e.Data["message"].(map[string]any)["id"].(string)]++
			}
			if heard[m2["id"].(string)] > 0 && len(heard) >= 1 {
				// wait a moment more for a duplicate that should not come
				time.Sleep(2500 * time.Millisecond)
				for len(again) > 0 {
					if e := <-again; e.Event == "message" {
						heard[e.Data["message"].(map[string]any)["id"].(string)]++
					}
				}
				break collect
			}
		case <-deadline:
			break collect
		}
	}
	if heard[m2["id"].(string)] != 1 {
		t.Errorf("the missed message, once: %v", heard)
	}
}

func TestChatStreamNeedsASessionAndIsCapped(t *testing.T) {
	w := newWAWorld(t)
	seller, _, _, _ := linkedPair(t, w)
	req, _ := http.NewRequest("GET", seller.base+"/v1/chat/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
	// A business holds twelve open, and the thirteenth is told to poll instead.
	var closers []func()
	for i := 0; i < 12; i++ {
		_, closeOne := chatStream(t, seller, "")
		closers = append(closers, closeOne)
	}
	req, _ = http.NewRequest("GET", seller.base+"/v1/chat/stream", nil)
	req.Header.Set("Authorization", "Bearer "+seller.token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Errorf("the thirteenth: %d", resp.StatusCode)
	}
	// One closed, another may open.
	closers[0]()
	time.Sleep(500 * time.Millisecond)
	_, again := chatStream(t, seller, "")
	again()
	for _, c := range closers[1:] {
		c()
	}
}

// TestChatStreamLoad is the load test the live stream is rolled out behind:
// many businesses, each with several streams open, a pair of them talking.
// It reports what the database is asked per second by the idle streams, and
// how long a message takes to reach the other side's stream. Run it with
// TASKIEM_STREAM_LOAD=<businesses> (it creates that many, with twelve
// streams each); it is not part of the usual run.
func TestChatStreamLoad(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("TASKIEM_STREAM_LOAD"))
	if n == 0 {
		t.Skip("set TASKIEM_STREAM_LOAD=<businesses> to run the live stream's load test")
	}
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.LoginBurst = 10000 // many sign-ins from one address
	w.srv.SignupPerAddress = -1
	seller, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	var closers []func()
	defer func() {
		for _, c := range closers {
			c()
		}
	}()
	for i := 0; i < n; i++ {
		c := w.tenant(t, fmt.Sprintf("Load %d", i), fmt.Sprintf("load%d@stream.test", i))
		for k := 0; k < 12; k++ {
			_, closeOne := chatStream(t, c, "")
			closers = append(closers, closeOne)
		}
	}
	events, closeBuyer := chatStream(t, buyer, "")
	defer closeBuyer()
	time.Sleep(3 * time.Second) // settled

	commits := func() (c, rolled int64) {
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT xact_commit, xact_rollback FROM pg_stat_database WHERE datname = current_database()`).Scan(&c, &rolled); err != nil {
			t.Fatal(err)
		}
		return
	}
	c0, _ := commits()
	start := time.Now()
	var took []time.Duration
	for i := 0; i < 20; i++ {
		sent := time.Now()
		m := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": fmt.Sprintf("Load %d", i)})["message"].(map[string]any)
		if next(events, "message", 5*time.Second, func(e streamEvent) bool { return e.Data["message"].(map[string]any)["id"] == m["id"] }) == nil {
			t.Fatalf("message %d never arrived", i)
		}
		took = append(took, time.Since(sent))
		time.Sleep(700 * time.Millisecond)
	}
	c1, _ := commits()
	secs := time.Since(start).Seconds()
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	t.Logf("%d businesses, %d streams open", n, n*12+2)
	t.Logf("transactions per second, with the streams open: %.0f (%.2f per stream)", float64(c1-c0)/secs, float64(c1-c0)/secs/float64(n*12+2))
	t.Logf("send to arrival: median %v, p95 %v, worst %v", took[len(took)/2], took[len(took)*95/100], took[len(took)-1])
	if took[len(took)/2] > 2*time.Second {
		t.Errorf("the median is over two seconds: %v", took[len(took)/2])
	}
}

// A bulk change (a conversation of hundreds read at once, or hundreds of
// messages arriving together) is sent in full: a batch that fills the
// look is followed at once by the next, not repeated.
func TestChatStreamBulkChange(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	_, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO link_messages (id, tenant_id, link_id, pair_id, mine, kind, body)
		SELECT gen_random_uuid(), $1, $2, gen_random_uuid(), false, 'text', 'bulk ' || n FROM generate_series(1, 450) AS n`, tenantID(t, buyer), link); err != nil {
		t.Fatal(err)
	}
	events, closeStream := chatStream(t, buyer, "")
	defer closeStream()
	got := map[string]bool{}
	deadline := time.After(15 * time.Second)
	for len(got) < 450 {
		select {
		case e := <-events:
			if e.Event == "message" {
				got[e.Data["message"].(map[string]any)["id"].(string)] = true
			}
		case <-deadline:
			t.Fatalf("heard %d of 450", len(got))
		}
	}
}

// A change that commits a few seconds after it was written (a long
// transaction) is still sent, once: the stream looks back over the last ten
// seconds and remembers what it has sent.
func TestChatStreamLateCommit(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	_, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	events, closeStream := chatStream(t, buyer, "")
	defer closeStream()
	next(events, "ready", 3*time.Second, nil)
	insert := `INSERT INTO link_messages (id, tenant_id, link_id, pair_id, mine, kind, body) VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(), false, 'text', $3)`
	// A transaction writes a message and stays open.
	slow, err := w.env.DB.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := slow.Exec(ctx, insert, tenantID(t, buyer), link, "written first, committed late"); err != nil {
		t.Fatal(err)
	}
	// Meanwhile another is written and committed: the stream moves on past it.
	if _, err := w.env.DB.Admin.Exec(ctx, insert, tenantID(t, buyer), link, "written second, committed at once"); err != nil {
		t.Fatal(err)
	}
	if next(events, "message", 4*time.Second, func(e streamEvent) bool {
		return e.Data["message"].(map[string]any)["body"] == "written second, committed at once"
	}) == nil {
		t.Fatal("the second did not arrive")
	}
	time.Sleep(3 * time.Second)
	if err := slow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	late := 0
	deadline := time.After(5 * time.Second)
	for late == 0 {
		select {
		case e := <-events:
			if e.Event == "message" && e.Data["message"].(map[string]any)["body"] == "written first, committed late" {
				late++
			}
		case <-deadline:
			t.Fatal("the late commit never arrived")
		}
	}
	// And only once.
	time.Sleep(3 * time.Second)
	for len(events) > 0 {
		if e := <-events; e.Event == "message" && e.Data["message"].(map[string]any)["body"] == "written first, committed late" {
			late++
		}
	}
	if late != 1 {
		t.Errorf("the late commit was sent %d times", late)
	}
}

// A stream says something at least every ping interval, so a page can tell a
// quiet stream from a stalled one.
func TestChatStreamPings(t *testing.T) {
	w := newWAWorld(t)
	w.srv.ChatStreamPing = 700 * time.Millisecond
	seller, _, _, _ := linkedPair(t, w)
	events, closeStream := chatStream(t, seller, "")
	defer closeStream()
	if next(events, "ready", 3*time.Second, nil) == nil {
		t.Fatal("no ready event")
	}
	for i := 0; i < 3; i++ {
		if next(events, "ping", 3*time.Second, nil) == nil {
			t.Fatalf("ping %d did not come", i+1)
		}
	}
}

// Reconnecting after an old Last-Event-ID is not the whole history again:
// only the last hour is sent.
func TestChatStreamReplayIsBounded(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	_, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	insert := `INSERT INTO link_messages (id, tenant_id, link_id, pair_id, mine, kind, body, updated_at)
		VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(), false, 'text', $3, now() - $4::interval)`
	for body, ago := range map[string]string{"two hours ago": "2 hours", "yesterday": "26 hours", "ten minutes ago": "10 minutes"} {
		if _, err := w.env.DB.Admin.Exec(ctx, insert, tenantID(t, buyer), link, body, ago); err != nil {
			t.Fatal(err)
		}
	}
	// An id from the beginning of time, and one from the future.
	for _, id := range []string{"1", strconv.FormatInt(time.Now().Add(24*time.Hour).UnixMicro(), 10)} {
		events, closeStream := chatStream(t, buyer, id)
		heard := map[string]bool{}
		deadline := time.After(3 * time.Second)
	collect:
		for {
			select {
			case e := <-events:
				if e.Event == "message" {
					heard[e.Data["message"].(map[string]any)["body"].(string)] = true
				}
			case <-deadline:
				break collect
			}
		}
		closeStream()
		if id == "1" && (!heard["ten minutes ago"] || heard["two hours ago"] || heard["yesterday"]) {
			t.Errorf("an old id replays only the last hour: %v", heard)
		}
		if id != "1" && len(heard) != 0 {
			t.Errorf("an id from the future is not trusted: %v", heard)
		}
	}
}

// The person's own conversation with Taskiem, on every channel, comes on the
// stream: what they send from the web and what Taskiem answers, and a message
// that came by WhatsApp; the ids are the history's, so a page that has the
// answer already does not show it twice.
func TestChatStreamTaskiemConversation(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	_, buyer, _, _ := linkedPair(t, w)
	other := w.tenant(t, "Someone Else", "else@stream.test")
	events, closeStream := chatStream(t, buyer, "")
	defer closeStream()
	otherEvents, closeOther := chatStream(t, other, "")
	defer closeOther()
	next(events, "ready", 3*time.Second, nil)

	sent := buyer.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "status"})["messages"].([]any)
	if len(sent) == 0 {
		t.Fatal("no answer")
	}
	heard := map[string]map[string]any{}
	deadline := time.After(5 * time.Second)
	for len(heard) < len(sent) { // the response has what was said and the answer
		select {
		case e := <-events:
			if e.Event == "taskiem" {
				for _, m := range e.Data["messages"].([]any) {
					heard[m.(map[string]any)["id"].(string)] = m.(map[string]any)
				}
			}
		case <-deadline:
			t.Fatalf("heard %d of %d: %v", len(heard), len(sent), heard)
		}
	}
	for _, m := range sent {
		if got := heard[m.(map[string]any)["id"].(string)]; got == nil || got["text"] != m.(map[string]any)["text"] {
			t.Errorf("an answer the page has, as the stream tells it: %v vs %v", m, got)
		}
	}
	var inbound int
	for _, m := range heard {
		if m["inbound"] == true && m["text"] == "status" && m["channel"] == "web" {
			inbound++
		}
	}
	if inbound != 1 {
		t.Errorf("what the person sent, once: %d in %v", inbound, heard)
	}
	// Another person's stream hears none of it, nor does one for another business.
	if e := next(otherEvents, "taskiem", 500*time.Millisecond, nil); e != nil {
		t.Errorf("another business heard the conversation: %v", e.Data)
	}
	// A message that came by WhatsApp (a row of the history, another channel).
	var user, tenant string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT user_id::text, tenant_id::text FROM chat_history ORDER BY created_at DESC LIMIT 1`).Scan(&user, &tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO chat_history (id, tenant_id, user_id, channel, inbound, kind, body, buttons, expires_at)
		SELECT gen_random_uuid(), tenant_id, user_id, 'whatsapp', true, kind, body, buttons, expires_at FROM chat_history WHERE tenant_id = $1 AND user_id = $2 AND inbound ORDER BY created_at DESC LIMIT 1`, tenant, user); err != nil {
		t.Fatal(err)
	}
	if e := next(events, "taskiem", 4*time.Second, func(e streamEvent) bool {
		ms := e.Data["messages"].([]any)
		return len(ms) == 1 && ms[0].(map[string]any)["channel"] == "whatsapp"
	}); e == nil {
		t.Error("a message by another channel did not come")
	}
}

// What the streams cost and send is counted, for the people who run it.
func TestChatStreamMetrics(t *testing.T) {
	w := newWAWorld(t)
	seller, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	open0 := metricValue(telemetry.ChatStreamsOpen)
	polls0 := metricValue(telemetry.ChatStreamPolls)
	msgs0 := metricValue(telemetry.ChatStreamEvents.WithLabelValues("message"))
	events, closeBuyer := chatStream(t, buyer, "")
	_, closeSeller := chatStream(t, seller, "")
	next(events, "ready", 3*time.Second, nil)
	if got := metricValue(telemetry.ChatStreamsOpen) - open0; got != 2 {
		t.Errorf("open streams: %v more", got)
	}
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Counted"})
	next(events, "message", 4*time.Second, nil)
	time.Sleep(1500 * time.Millisecond)
	if metricValue(telemetry.ChatStreamPolls)-polls0 < 3 {
		t.Errorf("polls: %v", metricValue(telemetry.ChatStreamPolls)-polls0)
	}
	if got := metricValue(telemetry.ChatStreamEvents.WithLabelValues("message")) - msgs0; got < 2 {
		t.Errorf("message events counted: %v", got) // one to each side
	}
	closeBuyer()
	closeSeller()
	time.Sleep(500 * time.Millisecond)
	if got := metricValue(telemetry.ChatStreamsOpen) - open0; got != 0 {
		t.Errorf("after closing: %v still open", got)
	}
	// Turned away for the cap, by which cap.
	refused0 := metricValue(telemetry.ChatStreamRefused.WithLabelValues("business"))
	var closers []func()
	for i := 0; i < 12; i++ {
		_, c := chatStream(t, seller, "")
		closers = append(closers, c)
	}
	req, _ := http.NewRequest("GET", seller.base+"/v1/chat/stream", nil)
	req.Header.Set("Authorization", "Bearer "+seller.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := metricValue(telemetry.ChatStreamRefused.WithLabelValues("business")) - refused0; got != 1 {
		t.Errorf("refused for the business's cap: %v", got)
	}
	for _, c := range closers {
		c()
	}
}

// metricValue is a counter's or a gauge's value now.
func metricValue(m prometheus.Metric) float64 {
	var out dto.Metric
	if err := m.Write(&out); err != nil {
		return 0
	}
	if out.Gauge != nil {
		return out.Gauge.GetValue()
	}
	return out.Counter.GetValue()
}
