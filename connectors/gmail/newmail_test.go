package gmail

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// fakeMessage is a message as users.messages.get?format=full answers it.
func fakeMessage(id, thread string, at time.Time, labels []string, headers map[string]string, mime, body string) map[string]any {
	var hs []map[string]string
	for _, k := range []string{"From", "To", "Cc", "Subject", "Date", "Message-ID", "In-Reply-To", "References"} {
		if v, ok := headers[k]; ok {
			hs = append(hs, map[string]string{"name": k, "value": v})
		}
	}
	return map[string]any{"id": id, "threadId": thread, "labelIds": labels, "snippet": strings.SplitN(body, "\n", 2)[0],
		"internalDate": itoa(at.UnixMilli()), "payload": map[string]any{
			"mimeType": "multipart/alternative", "headers": hs, "body": map[string]any{"size": 0},
			"parts": []any{map[string]any{"mimeType": mime, "headers": []any{}, "body": map[string]any{"size": len(body), "data": base64.RawURLEncoding.EncodeToString([]byte(body))}}},
		}}
}

// fakeGmail serves a mailbox: the profile, a search listing ids in pages,
// and each message in full. It records the searches it was asked.
type fakeGmail struct {
	*httptest.Server
	mu       sync.Mutex
	queries  []string
	pages    [][]string
	messages map[string]map[string]any
	gets     int
	read     map[string]int
}

// search answers a listing as Gmail does when no pages are set: the
// messages whose time is after: and before: the query's seconds, newest
// first, in pages of maxResults.
func (f *fakeGmail) search(q, max string) [][]string {
	var after, before int64 = 0, 1 << 62
	for _, w := range strings.Fields(q) {
		if v, ok := strings.CutPrefix(w, "after:"); ok {
			after, _ = strconv.ParseInt(v, 10, 64)
		}
		if v, ok := strings.CutPrefix(w, "before:"); ok {
			before, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	type at struct {
		id string
		ms int64
	}
	var list []at
	for id, m := range f.messages {
		ms, _ := strconv.ParseInt(m["internalDate"].(string), 10, 64)
		if ms/1000 > after && ms/1000 < before {
			list = append(list, at{id, ms})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].ms != list[j].ms {
			return list[i].ms > list[j].ms
		}
		return list[i].id > list[j].id
	})
	size, _ := strconv.Atoi(max)
	var pages [][]string
	for i, x := range list {
		if i%size == 0 {
			pages = append(pages, nil)
		}
		pages[len(pages)-1] = append(pages[len(pages)-1], x.id)
	}
	return pages
}

func newFakeGmail(t *testing.T, self string) *fakeGmail {
	f := &fakeGmail{messages: map[string]map[string]any{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer ya29.test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch p := r.URL.Path; {
		case p == "/users/me/profile":
			_ = json.NewEncoder(w).Encode(map[string]any{"emailAddress": self})
		case p == "/users/me/messages":
			f.queries = append(f.queries, r.URL.Query().Get("q"))
			pages := f.pages
			if pages == nil {
				pages = f.search(r.URL.Query().Get("q"), r.URL.Query().Get("maxResults"))
			}
			page := 0
			if tok := r.URL.Query().Get("pageToken"); tok != "" {
				page, _ = strconv.Atoi(tok)
			}
			var refs []any
			if page < len(pages) {
				for _, id := range pages[page] {
					refs = append(refs, map[string]any{"id": id, "threadId": "t"})
				}
			}
			out := map[string]any{"messages": refs}
			if page+1 < len(pages) {
				out["nextPageToken"] = strconv.Itoa(page + 1)
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(p, "/users/me/messages/"):
			f.gets++
			if f.read == nil {
				f.read = map[string]int{}
			}
			f.read[strings.TrimPrefix(p, "/users/me/messages/")]++
			m, ok := f.messages[strings.TrimPrefix(p, "/users/me/messages/")]
			if !ok || r.URL.Query().Get("format") != "full" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(m)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGmail) add(id, thread string, at time.Time, labels []string, headers map[string]string, mime, body string) {
	f.messages[id] = fakeMessage(id, thread, at, labels, headers, mime, body)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func newMail(t *testing.T, f *fakeGmail, since time.Time) ([]map[string]any, error) {
	t.Helper()
	ts := newTokenServer(t)
	out, err := execute(t, f.URL, ts.URL, f.Client(), "new_messages", nil, map[string]any{"since": since.Format(time.RFC3339)})
	var list []map[string]any
	if out != nil {
		for _, x := range out["messages"].([]any) {
			list = append(list, x.(map[string]any))
		}
	}
	return list, err
}

// Polling the mailbox: mail that arrived since the time, oldest first,
// with the reply's newest text only; never the business's own mail
// (sent, a draft, or from its own address), nor what came before.
func TestNewMessages(t *testing.T) {
	since := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	f := newFakeGmail(t, "Help@AdaCakes.ng")
	reply := "Thanks, a refund is fine.\r\n\r\nOn Fri, 6 Nov 2026 at 16:00, Ada Cakes <help@adacakes.ng> wrote:\r\n> We are sorry the cake was late.\r\n"
	f.add("m2", "t1", since.Add(3*time.Minute), []string{"INBOX", "UNREAD", "CATEGORY_PERSONAL"}, map[string]string{
		"From": "Ada Obi <Ada@Example.com>", "To": "help@adacakes.ng", "Subject": "Re: Order 42", "Date": "Sat, 7 Nov 2026 10:03:00 +0100",
		"Message-ID": "<CAF.2@mail.gmail.com>", "In-Reply-To": "<taskiem.t-42@adacakes.ng>", "References": "<CAF.1@mail.gmail.com> <taskiem.t-42@adacakes.ng>"}, "text/plain", reply)
	f.add("m1", "t9", since.Add(time.Minute), []string{"INBOX"}, map[string]string{
		"From": "bayo@example.org", "Subject": "Hello", "Message-ID": "<b@example.org>"}, "text/html", "<p>Is the shop open <b>today</b>?</p>")
	f.add("sent", "t1", since.Add(2*time.Minute), []string{"SENT"}, map[string]string{"From": "help@adacakes.ng"}, "text/plain", "We are sorry")
	f.add("self", "t3", since.Add(2*time.Minute), []string{"INBOX"}, map[string]string{"From": "Ada Cakes <help@adacakes.ng>"}, "text/plain", "a note to self")
	f.add("draft", "t4", since.Add(2*time.Minute), []string{"DRAFT"}, map[string]string{"From": "x@example.com"}, "text/plain", "draft")
	f.add("old", "t5", since.Add(-time.Second), []string{"INBOX"}, map[string]string{"From": "x@example.com"}, "text/plain", "before")
	// Two pages; "gone" was deleted between the listing and the read.
	f.pages = [][]string{{"m2", "sent", "self", "gone"}, {"draft", "old", "m1"}}

	got, err := newMail(t, f, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0]["id"] != "m1" || got[1]["id"] != "m2" {
		t.Fatalf("messages %v", got)
	}
	if len(f.queries) != 2 || f.queries[0] != "after:1794041999 -in:sent" {
		t.Errorf("searches %q", f.queries)
	}
	m := got[1]
	for k, want := range map[string]any{"thread_id": "t1", "message_id": "<CAF.2@mail.gmail.com>", "from": "Ada Obi <Ada@Example.com>",
		"from_address": "ada@example.com", "to": "help@adacakes.ng", "subject": "Re: Order 42", "date": "Sat, 7 Nov 2026 10:03:00 +0100",
		"received_at": "2026-11-07T09:03:00Z", "latest": "Thanks, a refund is fine.", "in_reply_to": "<taskiem.t-42@adacakes.ng>",
		"references": "<CAF.1@mail.gmail.com> <taskiem.t-42@adacakes.ng>", "unread": true, "has_attachments": false} {
		if m[k] != want {
			t.Errorf("%s = %#v, want %#v", k, m[k], want)
		}
	}
	if l, _ := m["label_ids"].([]any); len(l) != 3 || l[0] != "INBOX" {
		t.Errorf("label_ids %v", m["label_ids"])
	}
	if _, cut := m["truncated"]; cut {
		t.Error("a short message is not truncated")
	}
	// An HTML-only message is read as text.
	if got[0]["latest"] != "Is the shop open today?" || got[0]["unread"] != false {
		t.Errorf("html message %v", got[0])
	}
}

func newMailSeen(t *testing.T, f *fakeGmail, since time.Time, seen []string) (map[string]any, error) {
	t.Helper()
	ts := newTokenServer(t)
	list := []any{}
	for _, id := range seen {
		list = append(list, id)
	}
	return execute(t, f.URL, ts.URL, f.Client(), "new_messages", nil, map[string]any{"since": since.Format(time.RFC3339), "seen": list})
}

// A poll reads at most 200 messages, the oldest, and says how far it
// got; the next one, told what was read, reads the rest and nothing
// twice. A long text is cut.
func TestNewMessagesBounds(t *testing.T) {
	since := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	f := newFakeGmail(t, "help@adacakes.ng")
	const total = maxNew + 50
	for i := range total {
		id := fmt.Sprintf("m%03d", i)
		f.add(id, id, since.Add(time.Duration(i)*time.Second), []string{"INBOX"}, map[string]string{"From": "x@example.com"}, "text/plain", "hi")
	}
	f.messages["m000"] = fakeMessage("m000", "m000", since, []string{"INBOX"}, map[string]string{"From": "x@example.com"}, "text/plain", strings.Repeat("é", maxLatest))

	var got []string
	var seen []string
	from := since
	for poll := 0; poll < 3; poll++ {
		out, err := newMailSeen(t, f, from, seen)
		if err != nil {
			t.Fatal(err)
		}
		msgs := out["messages"].([]any)
		for _, m := range msgs {
			got = append(got, m.(map[string]any)["id"].(string))
		}
		for _, k := range out["keys"].([]any) {
			seen = append(seen, k.(string))
		}
		if poll == 0 {
			if len(msgs) != maxNew || f.gets != maxNew {
				t.Fatalf("first poll: %d messages, %d reads", len(msgs), f.gets)
			}
			if l := msgs[0].(map[string]any)["latest"].(string); msgs[0].(map[string]any)["truncated"] != true || len(l) > maxLatest || !strings.HasSuffix(l, "é") {
				t.Errorf("long text: %d bytes, truncated %v", len(l), msgs[0].(map[string]any)["truncated"])
			}
			// It got to the newest it read: the next poll starts there.
			if out["until"] != since.Add((maxNew-1)*time.Second).Format(time.RFC3339) {
				t.Fatalf("until %v", out["until"])
			}
		}
		if out["until"] == "" {
			break
		}
		// The poller looks back over the overlap, which seen covers.
		at, _ := time.Parse(time.RFC3339, out["until"].(string))
		from = at.Add(-10 * time.Minute)
	}
	if len(got) != total {
		t.Fatalf("%d messages over the polls, want %d", len(got), total)
	}
	for i, id := range got {
		if id != fmt.Sprintf("m%03d", i) {
			t.Fatalf("message %d is %s: not each once, oldest first", i, id)
		}
	}
	for id, n := range f.read {
		if n != 1 {
			t.Errorf("%s read %d times", id, n)
		}
	}
}

// A poll looking back over mail it read already reads none of it again.
func TestNewMessagesSkipsSeen(t *testing.T) {
	since := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	f := newFakeGmail(t, "help@adacakes.ng")
	for i := range 5 {
		id := fmt.Sprintf("m%d", i)
		f.add(id, id, since.Add(time.Duration(i)*time.Minute), []string{"INBOX"}, map[string]string{"From": "x@example.com"}, "text/plain", "hi")
	}
	f.add("own", "own", since.Add(time.Minute), []string{"INBOX"}, map[string]string{"From": "help@adacakes.ng"}, "text/plain", "note")
	first, err := newMailSeen(t, f, since, nil)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, k := range first["keys"].([]any) {
		seen = append(seen, k.(string))
	}
	if len(first["messages"].([]any)) != 5 || len(seen) != 6 || first["until"] != "" {
		t.Fatalf("first poll: %v", first)
	}
	f.add("m5", "m5", since.Add(6*time.Minute), []string{"INBOX"}, map[string]string{"From": "x@example.com"}, "text/plain", "new")
	reads := f.gets
	second, err := newMailSeen(t, f, since, seen)
	if err != nil {
		t.Fatal(err)
	}
	if msgs := second["messages"].([]any); len(msgs) != 1 || msgs[0].(map[string]any)["id"] != "m5" || f.gets-reads != 1 || f.read["m0"] != 1 || f.read["own"] != 1 {
		t.Fatalf("the overlapping poll read %d messages again: %v (reads %v)", f.gets-reads, msgs, f.read)
	}
}

// A listing too long for one call is narrowed until it fits, so the
// oldest messages are still read first.
func TestNewMessagesNarrowsABusyWindow(t *testing.T) {
	since := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	f := newFakeGmail(t, "help@adacakes.ng")
	for i := range maxListed + 100 {
		id := fmt.Sprintf("m%04d", i)
		f.add(id, id, since.Add(time.Duration(i)*time.Second), []string{"INBOX"}, map[string]string{"From": "x@example.com"}, "text/plain", "hi")
	}
	out, err := newMailSeen(t, f, since, nil)
	if err != nil {
		t.Fatal(err)
	}
	msgs := out["messages"].([]any)
	if len(msgs) != maxNew || msgs[0].(map[string]any)["id"] != "m0000" || msgs[maxNew-1].(map[string]any)["id"] != fmt.Sprintf("m%04d", maxNew-1) {
		t.Fatalf("%d messages, from %v", len(msgs), msgs[0])
	}
	if !strings.Contains(f.queries[len(f.queries)-1], " before:") {
		t.Errorf("searches %q", f.queries)
	}
}

func TestNewMessagesRefusals(t *testing.T) {
	f := newFakeGmail(t, "help@adacakes.ng")
	ts := newTokenServer(t)
	if _, err := execute(t, f.URL, ts.URL, f.Client(), "new_messages", nil, map[string]any{"since": "yesterday"}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("a bad since: %v", err)
	}
	// A message Gmail fails to read fails the poll, which is asked again.
	since := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	f.pages = [][]string{{"m1"}}
	f.add("m1", "t1", since, []string{"INBOX"}, nil, "text/plain", "x")
	f.messages["m1"]["payload"] = "not a part"
	if _, err := newMail(t, f, since); err == nil {
		t.Error("an unreadable message")
	}
}

// Each workflow's options choose from what one poll found: the inbox (by
// default), labels, senders.
func TestNewMailFilter(t *testing.T) {
	body := func(from string, labels ...any) any {
		return map[string]any{"event": "message.received", "data": map[string]any{"from_address": from, "label_ids": labels}}
	}
	inbox := body("ada@example.com", "INBOX", "Label_7")
	archived := body("ada@example.com", "Label_7")
	for name, c := range map[string]struct {
		opts map[string]any
		body any
		want bool
	}{
		"no options: the inbox":       {nil, inbox, true},
		"no options: not archived":    {nil, archived, false},
		"archived too":                {map[string]any{"inbox_only": false}, archived, true},
		"label":                       {map[string]any{"label_ids": []any{"Label_7"}}, inbox, true},
		"every label":                 {map[string]any{"label_ids": []any{"Label_7", "IMPORTANT"}}, inbox, false},
		"sender address":              {map[string]any{"senders": []any{"Ada@Example.com"}}, inbox, true},
		"sender @domain":              {map[string]any{"senders": []any{"@example.com"}}, inbox, true},
		"sender domain":               {map[string]any{"senders": []any{"bayo@example.org", "example.com"}}, inbox, true},
		"another sender":              {map[string]any{"senders": []any{"bayo@example.com", "example.org"}}, inbox, false},
		"a subdomain is not a domain": {map[string]any{"senders": []any{"mail.example.com"}}, inbox, false},
		"no sender address":           {map[string]any{"senders": []any{"example.com"}}, body("", "INBOX"), false},
		"no body":                     {nil, nil, false},
	} {
		if got := filterNewMail(c.opts, c.body, nil); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// The trigger is polled with new_messages, filtered per workflow, and its
// options are checked as the manifest describes them.
func TestNewMailTrigger(t *testing.T) {
	c := New(Options{})
	if err := connector.NewRegistry().Register(c); err != nil {
		t.Fatal(err)
	}
	spec := c.Manifest.Triggers["new_mail"]
	if spec.Poll == nil || spec.Poll.Action != "new_messages" || spec.Poll.Interval() != 5*time.Minute || c.Filters["new_mail"] == nil {
		t.Fatalf("new_mail %+v", spec)
	}
	if spec.Verify == nil || spec.Verify.SecretField != "refresh_token" || len(spec.PII) == 0 || spec.Example == nil {
		t.Errorf("verify %+v, pii %v", spec.Verify, spec.PII)
	}
	var opts map[string]any
	if err := json.Unmarshal(spec.Options, &opts); err != nil || opts["additionalProperties"] != false {
		t.Errorf("options %s: %v", spec.Options, err)
	}
}
