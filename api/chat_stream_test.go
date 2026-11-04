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
