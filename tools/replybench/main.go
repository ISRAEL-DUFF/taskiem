// Command replybench measures how long a customer waits for a reply
// (decision 0062 V4, A10): it sends WhatsApp messages to a Taskiem the way
// Meta does (signed with the app secret, to the connection's callback
// address), finds the run each one started (by the sender's number, the
// trigger's correlation key) and reads, from the run's history, when the
// step that sends the reply completed. It reports, for text and for voice
// notes, the time from delivery to the run's start, from the start to the
// reply, and in all, at p50, p95 and the slowest.
//
// Run it against staging, whose workflow answers with real providers (the
// model, speech, Meta's Graph API to a test number, people item VP5), and
// record the results in docs/performance.md:
//
//	go run ./tools/replybench -url https://staging.taskiem.example -tenant <id> \
//	  -app-secret "$META_APP_SECRET" -phone-number-id 1065... -api-key "$TASKIEM_API_KEY" \
//	  -workflow <id> -reply-step reply -n 50 -concurrency 10
//
// Each message comes from its own made-up number (-from-prefix), so runs
// are told apart and many can be in flight: right for a workflow whose
// reply goes to a fake Graph API. A staging workflow that really sends can
// only reach numbers registered with Meta's test app: -from sends every
// message from that one number, one at a time, and tells the runs apart by
// their ids. -voice-media-id sends voice notes (the id of a voice note
// Meta holds for the test number) instead of text.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	base, tenant, env, connection string
	appSecret, phoneID, apiKey    string
	workflow, replyStep           string
	n, concurrency                int
	text, mediaID, fromPrefix     string
	from                          string
	timeout                       time.Duration
}

func main() {
	var c config
	flag.StringVar(&c.base, "url", "", "Taskiem's public URL (https://...)")
	flag.StringVar(&c.tenant, "tenant", "", "the organisation's id")
	flag.StringVar(&c.env, "env", "prod", "the environment")
	flag.StringVar(&c.connection, "connection", "", "the WhatsApp connection's name, when there are several")
	flag.StringVar(&c.appSecret, "app-secret", os.Getenv("REPLYBENCH_APP_SECRET"), "the Meta app secret the connection checks signatures with")
	flag.StringVar(&c.phoneID, "phone-number-id", "", "the connection's phone number id")
	flag.StringVar(&c.apiKey, "api-key", os.Getenv("REPLYBENCH_API_KEY"), "a Taskiem API key that reads runs")
	flag.StringVar(&c.workflow, "workflow", "", "the workflow's id")
	flag.StringVar(&c.replyStep, "reply-step", "reply", "the step that sends the reply")
	flag.IntVar(&c.n, "n", 20, "messages to send")
	flag.IntVar(&c.concurrency, "concurrency", 4, "messages in flight at once")
	flag.StringVar(&c.text, "text", "Good morning, do you have rice today?", "the text each message says")
	flag.StringVar(&c.mediaID, "voice-media-id", "", "send voice notes with this media id instead of text")
	flag.StringVar(&c.fromPrefix, "from-prefix", "2349990", "senders' numbers start with this; each message has its own")
	flag.StringVar(&c.from, "from", "", "send every message from this one number (a test number Meta's app may reply to), one at a time")
	flag.DurationVar(&c.timeout, "timeout", 2*time.Minute, "how long to wait for each reply")
	flag.Parse()
	if c.base == "" || c.tenant == "" || c.appSecret == "" || c.phoneID == "" || c.apiKey == "" || c.workflow == "" {
		flag.Usage()
		os.Exit(2)
	}
	if c.from != "" {
		c.concurrency = 1
	}
	res := run(context.Background(), c, http.DefaultClient)
	fmt.Print(report(res, c))
	for _, r := range res {
		if r.err != nil {
			os.Exit(1)
		}
	}
}

// result is one message's timings.
type result struct {
	from             string
	sent             time.Time
	toStart, toReply time.Duration
	err              error
}

func run(ctx context.Context, c config, hc *http.Client) []result {
	out := make([]result, c.n)
	sem := make(chan struct{}, max(1, c.concurrency))
	var wg sync.WaitGroup
	stamp := time.Now().Unix() % 100000
	for i := range c.n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			from := fmt.Sprintf("%s%05d%03d", c.fromPrefix, stamp, i)
			if c.from != "" {
				from = c.from
			}
			out[i] = one(ctx, c, hc, from, i)
		}()
	}
	wg.Wait()
	return out
}

func one(ctx context.Context, c config, hc *http.Client, from string, i int) result {
	r := result{from: from}
	// The run this sender started last, so a new one is told from it.
	prev, _, _, _, err := lookup(ctx, c, hc, from)
	if err != nil {
		r.err = err
		return r
	}
	body := delivery(c, from, i)
	u := strings.TrimRight(c.base, "/") + "/hooks/" + url.PathEscape(c.tenant) + "/connectors/whatsapp@1/messages?env=" + url.QueryEscape(c.env)
	if c.connection != "" {
		u += "&connection=" + url.QueryEscape(c.connection)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", Sign(c.appSecret, body))
	r.sent = time.Now()
	resp, err := hc.Do(req)
	if err != nil {
		r.err = err
		return r
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		r.err = fmt.Errorf("the delivery was answered %s", resp.Status)
		return r
	}
	deadline := time.Now().Add(c.timeout)
	for time.Now().Before(deadline) {
		id, started, replied, done, err := lookup(ctx, c, hc, from)
		if err != nil {
			r.err = err
			return r
		}
		if id == "" || id == prev {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if !replied.IsZero() {
			r.toStart, r.toReply = started.Sub(r.sent), replied.Sub(started)
			return r
		}
		if done {
			r.err = fmt.Errorf("the run ended without completing step %s", c.replyStep)
			return r
		}
		time.Sleep(250 * time.Millisecond)
	}
	r.err = errors.New("no reply in time")
	return r
}

// delivery is a Cloud API webhook body with one message, as Meta sends it.
func delivery(c config, from string, i int) []byte {
	msg := map[string]any{"from": from, "id": fmt.Sprintf("wamid.replybench.%s.%d", from, i), "timestamp": strconv.FormatInt(time.Now().Unix(), 10)}
	if c.mediaID != "" {
		msg["type"] = "audio"
		msg["audio"] = map[string]any{"id": c.mediaID, "mime_type": "audio/ogg; codecs=opus", "voice": true}
	} else {
		msg["type"] = "text"
		msg["text"] = map[string]any{"body": c.text}
	}
	b, _ := json.Marshal(map[string]any{"object": "whatsapp_business_account", "entry": []any{map[string]any{
		"id": "replybench", "changes": []any{map[string]any{"field": "messages", "value": map[string]any{
			"messaging_product": "whatsapp",
			"metadata":          map[string]any{"display_phone_number": "replybench", "phone_number_id": c.phoneID},
			"contacts":          []any{map[string]any{"profile": map[string]any{"name": "Replybench"}, "wa_id": from}},
			"messages":          []any{msg},
		}}},
	}}})
	return b
}

// Sign is Meta's X-Hub-Signature-256 for a body.
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// lookup finds the run a sender's message started and reads when it
// started and when the reply step completed.
func lookup(ctx context.Context, c config, hc *http.Client, from string) (id string, started, replied time.Time, done bool, err error) {
	var list struct {
		Runs []struct {
			ID        string    `json:"id"`
			StartedAt time.Time `json:"started_at"`
		} `json:"runs"`
	}
	q := url.Values{"workflow": {c.workflow}, "correlation_key": {from}, "environment": {c.env}, "limit": {"1"}}
	if err := get(ctx, c, hc, "/v1/runs?"+q.Encode(), &list); err != nil {
		return "", started, replied, false, err
	}
	if len(list.Runs) == 0 {
		return "", started, replied, false, nil
	}
	var detail struct {
		Run struct {
			Status string `json:"status"`
		} `json:"run"`
		Events []struct {
			Type       string    `json:"type"`
			StepID     string    `json:"step_id"`
			RecordedAt time.Time `json:"recorded_at"`
		} `json:"events"`
	}
	if err := get(ctx, c, hc, "/v1/runs/"+url.PathEscape(list.Runs[0].ID), &detail); err != nil {
		return "", started, replied, false, err
	}
	started = list.Runs[0].StartedAt
	for _, e := range detail.Events {
		if e.Type == "StepCompleted" && e.StepID == c.replyStep {
			replied = e.RecordedAt
		}
	}
	switch detail.Run.Status {
	case "completed", "failed", "cancelled":
		done = true
	}
	return list.Runs[0].ID, started, replied, done, nil
}

func get(ctx context.Context, c config, hc *http.Client, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.base, "/")+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s answered %s", strings.SplitN(path, "?", 2)[0], resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// report sums the timings up.
func report(res []result, c config) string {
	kind := "text"
	if c.mediaID != "" {
		kind = "voice"
	}
	var start, reply, total []time.Duration
	failed := 0
	var b strings.Builder
	for _, r := range res {
		if r.err != nil {
			failed++
			fmt.Fprintf(&b, "  %s: %v\n", r.from, r.err)
			continue
		}
		start, reply, total = append(start, r.toStart), append(reply, r.toReply), append(total, r.toStart+r.toReply)
	}
	head := fmt.Sprintf("%d %s messages, %d at once: %d answered, %d not\n", len(res), kind, c.concurrency, len(total), failed)
	line := func(name string, ds []time.Duration) string {
		if len(ds) == 0 {
			return ""
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return fmt.Sprintf("  %-22s p50 %8s  p95 %8s  max %8s\n", name, pct(ds, 50), pct(ds, 95), ds[len(ds)-1].Round(time.Millisecond))
	}
	return head + line("delivery to run start", start) + line("run start to reply", reply) + line("delivery to reply", total) + b.String()
}

func pct(sorted []time.Duration, p int) time.Duration {
	i := (len(sorted)*p+99)/100 - 1
	return sorted[max(0, min(i, len(sorted)-1))].Round(time.Millisecond)
}

func init() { log.SetFlags(0) }
