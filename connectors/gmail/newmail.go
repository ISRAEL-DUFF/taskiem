package gmail

import (
	"context"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/connectors/internal/google"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/mailtext"
)

// The new_mail trigger: Gmail's push notifications need a Cloud Pub/Sub
// topic in the business's own Google project, so Taskiem asks the mailbox
// itself every 5 minutes while a workflow listens (new_messages), as it
// does Zoho Mail's.

const (
	// maxNew bounds one poll: two pages of message ids, each message then
	// read on its own.
	maxNew   = 200
	pageSize = 100
	// fetchers read that many messages at once, so a full poll fits the
	// poller's 30 seconds without spending the mailbox's quota in a burst.
	fetchers = 4
	// maxLatest bounds the newest part of a message a delivery carries; a
	// workflow reads the rest with get_message.
	maxLatest = 32 << 10
)

// notNew are the labels of messages that are not mail someone sent the
// mailbox: its own (SENT, and DRAFT), Chat, and spam and trash, which a
// search leaves out anyway.
var notNew = []string{"SENT", "DRAFT", "CHAT", "SPAM", "TRASH"}

type fullMessage struct {
	msgRef
	Snippet      string `json:"snippet"`
	InternalDate string `json:"internalDate"`
	Payload      part   `json:"payload"`
}

// header is the first value of a header (names are case-insensitive).
func (m fullMessage) header(name string) string {
	for _, h := range m.Payload.Headers {
		if strings.EqualFold(h.Name, name) {
			return strings.TrimSpace(h.Value)
		}
	}
	return ""
}

func (m fullMessage) received() (time.Time, bool) {
	ms, err := strconv.ParseInt(m.InternalDate, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms).UTC(), true
}

// address is the bare address of a From header, lower case ("" when it
// names none).
func address(from string) string {
	if a, err := mail.ParseAddress(from); err == nil {
		return strings.ToLower(a.Address)
	}
	if i, j := strings.LastIndex(from, "<"), strings.LastIndex(from, ">"); i >= 0 && j > i {
		from = from[i+1 : j]
	}
	from = strings.ToLower(strings.TrimSpace(from))
	if !strings.Contains(from, "@") {
		return ""
	}
	return from
}

// clipText cuts s to at most n bytes, on a character boundary.
func clipText(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// item is a message as the trigger delivers it.
func (m fullMessage) item() map[string]any {
	var text, html string
	atts := []any{}
	walk(m.Payload, &text, &html, &atts)
	if strings.TrimSpace(text) == "" && html != "" {
		text = mailtext.TextFromHTML(html)
	}
	latest, cut := clipText(mailtext.Latest(text), maxLatest)
	from := m.header("From")
	out := map[string]any{
		"id": m.ID, "thread_id": m.ThreadID, "message_id": m.header("Message-ID"),
		"from": from, "from_address": address(from), "to": m.header("To"), "cc": m.header("Cc"),
		"subject": m.header("Subject"), "date": m.header("Date"), "snippet": m.Snippet, "latest": latest,
		"in_reply_to": m.header("In-Reply-To"), "references": m.header("References"),
		"label_ids": labels(m.LabelIDs), "unread": slices.Contains(m.LabelIDs, "UNREAD"), "has_attachments": len(atts) > 0,
	}
	if cut {
		out["truncated"] = true
	}
	if at, ok := m.received(); ok {
		out["received_at"] = at.Format(time.RFC3339)
	}
	return out
}

// own reports whether the mailbox sent m itself: Gmail labels what it
// sends SENT (a message to oneself too), and a message from its own
// address that came in some other way is still its own.
func own(m fullMessage, self string) bool {
	if slices.ContainsFunc(m.LabelIDs, func(l string) bool { return slices.Contains(notNew, l) }) {
		return true
	}
	return self != "" && address(m.header("From")) == self
}

func (c *client) newMessages(ctx context.Context, req connector.Request) (connector.Response, error) {
	since, err := time.Parse(time.RFC3339, strings.TrimSpace(str(req.Input, "since")))
	if err != nil {
		return connector.Response{}, fatal("new_messages", fmt.Errorf("since must be a date and time (RFC 3339)"))
	}
	// The mailbox's own address, so its own mail is never news to it.
	var profile struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/profile", nil, nil, &profile); err != nil {
		return connector.Response{}, err
	}
	self := strings.ToLower(strings.TrimSpace(profile.EmailAddress))

	// after: takes seconds since the epoch; it is read to the second, and
	// each message's own time is checked again below.
	q := url.Values{"q": {"after:" + strconv.FormatInt(since.Unix()-1, 10) + " -in:sent"}, "maxResults": {strconv.Itoa(pageSize)}}
	var refs []msgRef
	for len(refs) < maxNew {
		var page struct {
			Messages      []msgRef `json:"messages"`
			NextPageToken string   `json:"nextPageToken"`
		}
		if err := c.do(ctx, req, http.MethodGet, "/messages", q, nil, &page); err != nil {
			return connector.Response{}, err
		}
		refs = append(refs, page.Messages...)
		if page.NextPageToken == "" {
			break
		}
		q.Set("pageToken", page.NextPageToken)
	}
	if len(refs) > maxNew {
		refs = refs[:maxNew]
	}

	msgs, err := c.fetchAll(ctx, req, refs)
	if err != nil {
		return connector.Response{}, err
	}
	var keep []fullMessage
	for _, m := range msgs {
		if m.ID == "" || own(m, self) {
			continue
		}
		if at, ok := m.received(); !ok || at.Before(since) {
			continue
		}
		keep = append(keep, m)
	}
	// Oldest first: a run waiting on a thread hears its replies in order.
	slices.SortStableFunc(keep, func(a, b fullMessage) int {
		x, _ := a.received()
		y, _ := b.received()
		if c := x.Compare(y); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	out := make([]any, 0, len(keep))
	for _, m := range keep {
		out = append(out, m.item())
	}
	return connector.Response{Output: map[string]any{"messages": out}}, nil
}

// fetchAll reads each listed message in full, a few at a time, in the
// listed order. A message deleted since it was listed is left out; any
// other failure fails the poll, which is asked again next time.
func (c *client) fetchAll(ctx context.Context, req connector.Request, refs []msgRef) ([]fullMessage, error) {
	out := make([]fullMessage, len(refs))
	errs := make([]error, len(refs))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(fetchers, len(refs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				var m fullMessage
				err := c.do(ctx, req, http.MethodGet, "/messages/"+url.PathEscape(refs[i].ID), url.Values{"format": {"full"}}, nil, &m)
				if google.IsNotFound(err) {
					continue
				}
				out[i], errs[i] = m, err
			}
		}()
	}
	for i := range refs {
		if ctx.Err() != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// filterNewMail is new_mail's filter: whether a workflow whose trigger
// options are opts takes the message. Gmail is asked once per connection,
// so each workflow's options are applied to what came.
func filterNewMail(opts map[string]any, body, _ any) bool {
	b, _ := body.(map[string]any)
	d, _ := b["data"].(map[string]any)
	have := strs(d, "label_ids")
	inboxOnly := true
	if v, ok := opts["inbox_only"].(bool); ok {
		inboxOnly = v
	}
	if inboxOnly && !slices.Contains(have, "INBOX") {
		return false
	}
	for _, l := range strs(opts, "label_ids") {
		if !slices.Contains(have, l) {
			return false
		}
	}
	senders := strs(opts, "senders")
	if len(senders) == 0 {
		return true
	}
	from := strings.ToLower(str(d, "from_address"))
	_, domain, _ := strings.Cut(from, "@")
	for _, s := range senders {
		s = strings.ToLower(strings.TrimSpace(s))
		switch {
		case s == "":
		case strings.Contains(strings.TrimPrefix(s, "@"), "@"):
			if s == from {
				return true
			}
		case domain != "" && strings.TrimPrefix(s, "@") == domain:
			return true
		}
	}
	return false
}
