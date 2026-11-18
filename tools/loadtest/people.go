package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// -mode people: the stage 5b load test (people-chatting §13, the 5b
// checklist B "Load test before 5b ships"). It starts the built binary's
// api role (one or more processes on one database), makes people, groups
// of up to 256 and pairs of friends, opens their live connections
// (GET /v1/me/live), and then sends group and one-to-one messages at set
// rates, with some of those who get them reading them. It measures:
//
//   - send to delivery on the live connections (each recipient's first
//     sight of the message, matched by its words), p50, p95, p99;
//
//   - the HTTP time of the sends and reads;
//
//   - what the database is asked: transactions a second, idle and loaded,
//     backends, what active backends run and wait on (pg_stat_activity
//     samples), sequential scans and row writes per table;
//
//   - storage: bytes and rows per message of each table a message writes
//     (the group's one copy, members' marks and read times, the keyed
//     search index; one-to-one's two copies and their index).
//
//     go build -o /tmp/taskiem ./cmd/taskiem
//     go run ./tools/loadtest -mode people -bin /tmp/taskiem -people 3000 -live 3000 \
//     -groups 12 -group-size 256 -pairs 200 -group-rate 2 -pair-rate 5 -duration 60s
type peopleOpts struct {
	people, live, groups, groupSize, pairs, apis, pool *int
	groupRate, pairRate, readFrac                      *float64
	duration, settle                                   *time.Duration
}

func peopleFlags() peopleOpts {
	return peopleOpts{
		people:    flag.Int("people", 1000, "people: accounts made"),
		live:      flag.Int("live", 1000, "people: live connections held open (one per person, the first N)"),
		groups:    flag.Int("groups", 4, "people: groups"),
		groupSize: flag.Int("group-size", 256, "people: members of each group (at most 256)"),
		pairs:     flag.Int("pairs", 100, "people: pairs of friends chatting one to one"),
		apis:      flag.Int("apis", 1, "people: api processes (each holds at most 2,000 live connections); connections go round them"),
		pool:      flag.Int("api-pool", 0, "people: TASKIEM_DATABASE_POOL of each api process (0: the default)"),
		groupRate: flag.Float64("group-rate", 1, "people: group messages a second, in all"),
		pairRate:  flag.Float64("pair-rate", 2, "people: one-to-one messages a second, in all"),
		readFrac:  flag.Float64("read-frac", 0.2, "people: share of recipients who read a message when it arrives (POST …/read)"),
		settle:    flag.Duration("settle", 15*time.Second, "people: how long the open connections are measured idle before sending"),
	}
}

type lperson struct {
	id    uuid.UUID
	token string
	api   string // base URL its live connection and calls use
}

type lgroup struct {
	id      string
	members []int // indexes into people
}

type lpair struct {
	a, b         int
	aChat, bChat string
}

// sentAt is when each message (by its words) was sent, and how many it
// should reach on live connections.
type peopleRec struct {
	mu        sync.Mutex
	sent      map[string]time.Time
	want      map[string]int
	got       map[string]int
	groupLat  []time.Duration
	pairLat   []time.Duration
	sendLat   map[string][]time.Duration // by what
	errs      map[string]int
	events    atomic.Int64
	connsOpen atomic.Int64
	connFail  atomic.Int64
}

func (r *peopleRec) err(what string) {
	r.mu.Lock()
	r.errs[what]++
	r.mu.Unlock()
}

func (r *peopleRec) took(what string, d time.Duration) {
	r.mu.Lock()
	r.sendLat[what] = append(r.sendLat[what], d)
	r.mu.Unlock()
}

type lclient struct {
	hc *http.Client
}

func (c *lclient) do(ctx context.Context, p lperson, method, path string, body any) (int, map[string]any, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.api+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, nil
}

func (c *lclient) must(ctx context.Context, p lperson, want int, method, path string, body any) map[string]any {
	code, out, err := c.do(ctx, p, method, path, body)
	if err != nil || code != want {
		log.Fatalf("%s %s: %d %v %v", method, path, code, err, out)
	}
	return out
}

func runPeople(ctx context.Context, dsn string, o peopleOpts, co clusterOpts, duration *time.Duration) {
	o.duration = duration
	if *co.bin == "" {
		log.Fatal("-bin required: go build -o /tmp/taskiem ./cmd/taskiem")
	}
	if *o.groupSize > 256 || *o.groupSize < 2 {
		log.Fatal("-group-size is 2 to 256")
	}
	if *o.live > *o.people {
		*o.live = *o.people
	}
	out := *co.out
	if out == "" {
		out, _ = os.MkdirTemp("", "people-load-")
	}
	_ = os.MkdirAll(out, 0o750)
	u, _ := url.Parse(dsn)
	dbName := strings.TrimPrefix(u.Path, "/")
	admin := pool(ctx, dsn, "", 8)
	defer admin.Close()

	// The api processes.
	key, hash := make([]byte, 32), make([]byte, 32)
	_, _ = rand.Read(key)
	_, _ = rand.Read(hash)
	c := &cluster{bin: *co.bin, dsn: dsn, out: out}
	c.commonEnv = []string{
		"TASKIEM_DATABASE_URL=" + dsn,
		"TASKIEM_LOCAL_KMS_KEY=" + base64.StdEncoding.EncodeToString(key),
		"TASKIEM_MESSAGE_HASH_KEY=" + base64.StdEncoding.EncodeToString(hash),
		"TASKIEM_SECURE_COOKIES=false",
		"TASKIEM_SHUTDOWN_DELAY=0s",
		"TASKIEM_LOG_LEVEL=warn",
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
	}
	if *o.pool > 0 {
		c.commonEnv = append(c.commonEnv, "TASKIEM_DATABASE_POOL="+strconv.Itoa(*o.pool))
	}
	if *co.extraEnv != "" {
		c.commonEnv = append(c.commonEnv, strings.Split(*co.extraEnv, ",")...)
	}
	var apis []*proc
	for i := 0; i < *o.apis; i++ {
		p := &proc{name: fmt.Sprintf("api%d", i+1), role: "api", metrics: freePort(), listen: freePort()}
		p.env = append(append([]string{}, c.commonEnv...), "TASKIEM_METRICS_LISTEN="+p.metrics, "TASKIEM_LISTEN="+p.listen,
			"TASKIEM_PUBLIC_URL=http://localhost"+p.listen[strings.LastIndex(p.listen, ":"):])
		apis = append(apis, p)
		c.procs = append(c.procs, p)
		c.start(p)
	}
	for _, p := range apis {
		c.waitReady(p)
	}
	defer func() {
		for _, p := range apis {
			c.stopGracefully(p)
			_ = p.logf.Close()
		}
	}()

	hc := &http.Client{Transport: &http.Transport{MaxIdleConns: 4096, MaxIdleConnsPerHost: 1024, IdleConnTimeout: time.Minute}}
	cl := &lclient{hc: hc}

	// People: accounts older than their first week (so the first week's
	// limits do not shape the load), each with a session.
	t0 := time.Now()
	people := make([]lperson, *o.people)
	ids := make([]uuid.UUID, len(people))
	toks := make([]string, len(people))
	hashes := make([][]byte, len(people))
	for i := range people {
		ids[i] = uuid.Must(uuid.NewV7())
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		toks[i] = hex.EncodeToString(b)
		h := sha256.Sum256([]byte(toks[i]))
		hashes[i] = h[:]
		people[i] = lperson{id: ids[i], token: toks[i], api: "http://" + apis[i%len(apis)].listen}
	}
	for lo := 0; lo < len(people); lo += 1000 {
		hi := min(lo+1000, len(people))
		if _, err := admin.Exec(ctx, `WITH u AS (
				INSERT INTO users (id, email, name, created_at)
				SELECT id, id::text || '@phone.taskiem.invalid', 'Person ' || n, now() - interval '30 days'
				FROM unnest($1::uuid[]) WITH ORDINALITY AS x(id, n) RETURNING id)
			INSERT INTO user_phones (user_id, number) SELECT id, '+23480' || lpad((row_number() OVER () + $2)::text, 8, '0') FROM u`,
			ids[lo:hi], lo); err != nil {
			log.Fatal("people: ", err)
		}
		for i := lo; i < hi; i++ {
			var ok bool
			if err := admin.QueryRow(ctx, `SELECT taskiem_auth_personal_start($1, $2, 'phone', '12 hours'::interval, '127.0.0.1', NULL, NULL, NULL)`,
				hashes[i], ids[i]).Scan(&ok); err != nil || !ok {
				log.Fatalf("session: %v %v", ok, err)
			}
		}
	}
	log.Printf("%d people with sessions in %s", len(people), time.Since(t0).Round(time.Millisecond))

	// Groups: the admin makes it through the API (its key), the members
	// are written in as TestGroupsFull does (adding 255 people through the
	// API would need 255 contacts each).
	t0 = time.Now()
	var groups []lgroup
	for g := 0; g < *o.groups; g++ {
		start := (g * *o.groupSize / 2) % len(people) // groups overlap by half
		var members []int
		for k := 0; k < *o.groupSize && k < len(people); k++ {
			members = append(members, (start+k)%len(people))
		}
		adminP := people[members[0]]
		gid := cl.must(ctx, adminP, 201, "POST", "/v1/me/groups", map[string]any{"name": fmt.Sprintf("Load %d", g)})["group"].(map[string]any)["id"].(string)
		var rest []uuid.UUID
		for _, m := range members[1:] {
			rest = append(rest, people[m].id)
		}
		if _, err := admin.Exec(ctx, `INSERT INTO group_members (group_id, person_id, how, visible)
			SELECT $1, id, 'added', tstzmultirange(tstzrange(now(), NULL)) FROM unnest($2::uuid[]) AS id`, gid, rest); err != nil {
			log.Fatal("members: ", err)
		}
		groups = append(groups, lgroup{id: gid, members: members})
	}
	log.Printf("%d groups of %d in %s", len(groups), *o.groupSize, time.Since(t0).Round(time.Millisecond))

	// Pairs of friends: a writes, b accepts and writes back.
	t0 = time.Now()
	var pairs []lpair
	for k := 0; k < *o.pairs && 2*k+1 < len(people); k++ {
		a, b := len(people)-1-2*k, len(people)-2-2*k
		aChat := cl.must(ctx, people[a], 201, "POST", "/v1/me/people", map[string]any{"person_id": people[b].id})["id"].(string)
		cl.must(ctx, people[a], 201, "POST", "/v1/me/people/"+aChat+"/messages", map[string]any{"text": "Hi"})
		reqs := cl.must(ctx, people[b], 200, "GET", "/v1/me/people/requests", nil)["requests"].([]any)
		if len(reqs) != 1 {
			log.Fatalf("requests: %v", reqs)
		}
		bChat := reqs[0].(map[string]any)["id"].(string)
		cl.must(ctx, people[b], 200, "POST", "/v1/me/people/"+bChat+"/accept", nil)
		pairs = append(pairs, lpair{a, b, aChat, bChat})
	}
	log.Printf("%d pairs in %s", len(pairs), time.Since(t0).Round(time.Millisecond))

	rec := &peopleRec{sent: map[string]time.Time{}, want: map[string]int{}, got: map[string]int{}, sendLat: map[string][]time.Duration{}, errs: map[string]int{}}
	live := map[int]bool{}
	for i := 0; i < *o.live; i++ {
		live[i] = true
	}
	// Reads go through a bounded set of workers, so a burst of 255
	// deliveries does not open 255 requests at once.
	reads := make(chan func(), 10000)
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	var rw sync.WaitGroup
	for i := 0; i < 32; i++ {
		rw.Add(1)
		go func() {
			defer rw.Done()
			for {
				select {
				case f := <-reads:
					f()
				case <-runCtx.Done():
					return
				}
			}
		}()
	}

	// Live connections.
	t0 = time.Now()
	var lw sync.WaitGroup
	sem := make(chan struct{}, 64)
	for i := 0; i < *o.live; i++ {
		sem <- struct{}{}
		lw.Add(1)
		ready := make(chan struct{})
		go func(i int) {
			defer lw.Done()
			livePerson(runCtx, hc, people[i], rec, ready, func(kind, chat string) {
				if rndFloat() >= *o.readFrac {
					return
				}
				path := "/v1/me/groups/" + chat + "/read"
				if kind == "pair" {
					path = "/v1/me/people/" + chat + "/read"
				}
				select {
				case reads <- func() {
					s := time.Now()
					code, _, err := cl.do(runCtx, people[i], "POST", path, nil)
					if err != nil || code != 204 {
						if runCtx.Err() == nil {
							rec.err(fmt.Sprintf("read %s %d", kind, code))
						}
						return
					}
					rec.took("read "+kind, time.Since(s))
				}:
				default:
					rec.err("read queue full")
				}
			})
		}(i)
		go func() {
			select {
			case <-ready:
			case <-time.After(30 * time.Second):
			}
			<-sem
		}()
	}
	for len(sem) > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	opened, failed := rec.connsOpen.Load(), rec.connFail.Load()
	log.Printf("%d live connections open (%d refused or failed) in %s", opened, failed, time.Since(t0).Round(time.Millisecond))

	// Idle: what the open connections alone ask of the database.
	time.Sleep(3 * time.Second)
	idle := measureDB(ctx, admin, dbName, apis, c, *o.settle)
	log.Printf("idle: %s", idle.line())

	// Load.
	before := storage(ctx, admin)
	tsBefore := readTableStats(ctx, admin)
	var seq atomic.Int64
	var sw sync.WaitGroup
	sendLoop := func(rate float64, one func(n int64)) {
		if rate <= 0 {
			return
		}
		sw.Add(1)
		go func() {
			defer sw.Done()
			every := time.Duration(float64(time.Second) / rate)
			t := time.NewTicker(every)
			defer t.Stop()
			end := time.Now().Add(*o.duration)
			for time.Now().Before(end) {
				<-t.C
				n := seq.Add(1)
				sw.Add(1)
				go func() { defer sw.Done(); one(n) }()
			}
		}()
	}
	words := strings.Fields("rice beans market church weekend football wedding school phone money bus rain jollof party meeting")
	text := func(n int64) string {
		return fmt.Sprintf("lt%d %s %s %s", n, words[rnd(len(words))], words[rnd(len(words))], words[rnd(len(words))])
	}
	var groupMsgs, pairMsgs atomic.Int64
	sendLoop(*o.groupRate, func(n int64) {
		g := groups[rnd(len(groups))]
		from := g.members[rnd(len(g.members))]
		tx := text(n)
		want := 0
		for _, m := range g.members {
			if m != from && live[m] {
				want++
			}
		}
		tag := strings.Fields(tx)[0]
		rec.mu.Lock()
		rec.sent[tag], rec.want[tag] = time.Now(), want
		rec.mu.Unlock()
		s := time.Now()
		code, _, err := cl.do(runCtx, people[from], "POST", "/v1/me/groups/"+g.id+"/messages", map[string]any{"text": tx})
		if err != nil || code != 201 {
			rec.err(fmt.Sprintf("group send %d %v", code, err))
			rec.mu.Lock()
			delete(rec.want, tag)
			rec.mu.Unlock()
			return
		}
		groupMsgs.Add(1)
		rec.took("group send", time.Since(s))
	})
	sendLoop(*o.pairRate, func(n int64) {
		p := pairs[rnd(len(pairs))]
		from, to, chat := p.a, p.b, p.aChat
		if rnd(2) == 0 {
			from, to, chat = p.b, p.a, p.bChat
		}
		tx := text(n)
		tag := strings.Fields(tx)[0]
		want := 0
		if live[to] {
			want = 1
		}
		rec.mu.Lock()
		rec.sent[tag], rec.want[tag] = time.Now(), want
		rec.mu.Unlock()
		s := time.Now()
		code, _, err := cl.do(runCtx, people[from], "POST", "/v1/me/people/"+chat+"/messages", map[string]any{"text": tx})
		if err != nil || code != 201 {
			rec.err(fmt.Sprintf("pair send %d %v", code, err))
			rec.mu.Lock()
			delete(rec.want, tag)
			rec.mu.Unlock()
			return
		}
		pairMsgs.Add(1)
		rec.took("pair send", time.Since(s))
	})
	loaded := measureDB(ctx, admin, dbName, apis, c, *o.duration)
	sw.Wait()
	time.Sleep(5 * time.Second) // the last deliveries
	after := storage(ctx, admin)
	tsAfter := readTableStats(ctx, admin)
	log.Printf("loaded: %s", loaded.line())
	stop()
	rw.Wait()

	// The report.
	var b strings.Builder
	fmt.Fprintf(&b, "# People load test (%s)\n\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "people=%d live=%d (open %d, failed %d) apis=%d groups=%d x %d pairs=%d group-rate=%.1f/s pair-rate=%.1f/s read-frac=%.2f duration=%s\n",
		len(people), *o.live, opened, failed, len(apis), len(groups), *o.groupSize, len(pairs), *o.groupRate, *o.pairRate, *o.readFrac, *o.duration)
	fmt.Fprintf(&b, "sent: group %d, one to one %d\n\n", groupMsgs.Load(), pairMsgs.Load())
	rec.mu.Lock()
	for _, l := range [][]time.Duration{rec.groupLat, rec.pairLat} {
		sort.Slice(l, func(i, j int) bool { return l[i] < l[j] })
	}
	fmt.Fprintf(&b, "send to delivery, group (each live member): n=%d p50=%s p95=%s p99=%s max=%s\n", len(rec.groupLat), pct(rec.groupLat, 50), pct(rec.groupLat, 95), pct(rec.groupLat, 99), pct(rec.groupLat, 100))
	fmt.Fprintf(&b, "send to delivery, one to one:               n=%d p50=%s p95=%s p99=%s max=%s\n", len(rec.pairLat), pct(rec.pairLat, 50), pct(rec.pairLat, 95), pct(rec.pairLat, 99), pct(rec.pairLat, 100))
	var wantAll, gotAll int
	for k, w := range rec.want {
		wantAll += w
		gotAll += min(rec.got[k], w)
	}
	fmt.Fprintf(&b, "deliveries: %d of %d expected (%.2f%%)\n", gotAll, wantAll, 100*float64(gotAll)/math.Max(1, float64(wantAll)))
	var whats []string
	for k := range rec.sendLat {
		whats = append(whats, k)
	}
	sort.Strings(whats)
	for _, k := range whats {
		l := rec.sendLat[k]
		sort.Slice(l, func(i, j int) bool { return l[i] < l[j] })
		fmt.Fprintf(&b, "HTTP %-14s n=%d p50=%s p95=%s p99=%s max=%s\n", k, len(l), pct(l, 50), pct(l, 95), pct(l, 99), pct(l, 100))
	}
	if len(rec.errs) > 0 {
		fmt.Fprintf(&b, "errors: %v\n", rec.errs)
	}
	rec.mu.Unlock()
	fmt.Fprintf(&b, "\nidle (connections open, nothing sent):\n  %s\n%s", idle.line(), idle.act.report(8))
	fmt.Fprintf(&b, "\nloaded:\n  %s\n%s", loaded.line(), loaded.act.report(12))
	fmt.Fprintf(&b, "\n%s", seqScanReport(tsBefore, tsAfter, 14))
	fmt.Fprintf(&b, "\nstorage (growth during the load):\n%s", storageReport(before, after, groupMsgs.Load(), pairMsgs.Load()))
	fmt.Print(b.String())
	_ = os.WriteFile(filepath.Join(out, "summary.md"), []byte(b.String()), 0o600)
	log.Printf("logs and summary in %s", out)
}

// livePerson holds one person's live connection open, reconnecting until
// ctx ends, and records each message it is sent.
func livePerson(ctx context.Context, hc *http.Client, p lperson, rec *peopleRec, ready chan struct{}, read func(kind, chat string)) {
	once := sync.Once{}
	signal := func() { once.Do(func() { close(ready) }) }
	defer signal()
	counted := false
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, "GET", p.api+"/v1/me/live", nil)
		req.Header.Set("Authorization", "Bearer "+p.token)
		resp, err := hc.Do(req)
		if err != nil || resp.StatusCode != 200 {
			if resp != nil {
				_ = resp.Body.Close()
			}
			if ctx.Err() == nil && !counted {
				rec.connFail.Add(1)
				counted = true
				signal()
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if counted {
			rec.connFail.Add(-1)
			counted = false
		}
		rec.connsOpen.Add(1)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		var event string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = line[7:]
				if event == "ready" {
					signal()
				}
			case strings.HasPrefix(line, "data: "):
				rec.events.Add(1)
				if event == "group_message" || event == "message" {
					seen(rec, event, []byte(line[6:]), read)
				}
			}
		}
		_ = resp.Body.Close()
		rec.connsOpen.Add(-1)
	}
}

func seen(rec *peopleRec, event string, data []byte, read func(kind, chat string)) {
	now := time.Now()
	var e struct {
		GroupID string `json:"group_id"`
		ChatID  string `json:"chat_id"`
		Message struct {
			Mine bool   `json:"mine"`
			Body string `json:"body"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &e) != nil || e.Message.Mine || !strings.HasPrefix(e.Message.Body, "lt") {
		return
	}
	tag, _, _ := strings.Cut(e.Message.Body, " ")
	rec.mu.Lock()
	at, ok := rec.sent[tag]
	first := ok && rec.got[tag] < rec.want[tag]
	if first {
		rec.got[tag]++
		if event == "group_message" {
			rec.groupLat = append(rec.groupLat, now.Sub(at))
		} else {
			rec.pairLat = append(rec.pairLat, now.Sub(at))
		}
	}
	rec.mu.Unlock()
	// A message changed later (a read tick) comes again: read once.
	if first {
		if event == "group_message" {
			read("group", e.GroupID)
		} else {
			read("pair", e.ChatID)
		}
	}
}

// dbWindow is what the database did over a window.
type dbWindow struct {
	secs                float64
	commits, rollbacks  int64
	backends, activeMax int
	hostBusy            float64
	apiCores            float64
	pgCores             float64
	load                string
	act                 *activity
}

func (d dbWindow) line() string {
	return fmt.Sprintf("%.0f commits/s, %.1f rollbacks/s, backends %d (active at most %d), api processes %.2f cores, postgres backends %.2f cores, host busy %.0f%%, load %s",
		float64(d.commits)/d.secs, float64(d.rollbacks)/d.secs, d.backends, d.activeMax, d.apiCores, d.pgCores, d.hostBusy*100, d.load)
}

func measureDB(ctx context.Context, admin *pgxpool.Pool, dbName string, apis []*proc, c *cluster, window time.Duration) dbWindow {
	xacts := func() (int64, int64) {
		var a, r int64
		_ = admin.QueryRow(ctx, `SELECT xact_commit, xact_rollback FROM pg_stat_database WHERE datname = $1`, dbName).Scan(&a, &r)
		return a, r
	}
	act := newActivity()
	actCtx, stopAct := context.WithCancel(ctx)
	go act.run(actCtx, admin, dbName, 200*time.Millisecond)
	pgPIDs := backendPIDs(ctx, admin, dbName)
	ticks := func() (api, pg uint64) {
		for _, p := range apis {
			api += cpuTicks(c.pid(p))
		}
		for _, pid := range pgPIDs {
			pg += cpuTicks(pid)
		}
		return
	}
	a0, r0 := xacts()
	api0, pg0 := ticks()
	cpu0 := cpuSample()
	start := time.Now()
	activeMax := 0
	for time.Since(start) < window {
		time.Sleep(time.Second)
		var n int
		_ = admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = $1 AND state = 'active' AND application_name <> 'loadtest'`, dbName).Scan(&n)
		activeMax = max(activeMax, n)
	}
	stopAct()
	a1, r1 := xacts()
	api1, pg1 := ticks()
	cpu1 := cpuSample()
	busy, _ := cpu1.busyFraction(cpu0)
	secs := time.Since(start).Seconds()
	var backends int
	_ = admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = $1`, dbName).Scan(&backends)
	return dbWindow{secs: secs, commits: a1 - a0, rollbacks: r1 - r0, backends: backends, activeMax: activeMax,
		hostBusy: busy, apiCores: float64(api1-api0) / 100 / secs, pgCores: float64(pg1-pg0) / 100 / secs, load: loadavg(), act: act}
}

// storageTables are the tables a people message writes.
var storageTables = []string{"group_messages", "group_members", "group_reads", "group_search", "group_marks",
	"people_messages", "people_chats", "people_search", "person_live", "audit_log"}

type tableSize struct{ bytes, rows int64 }

func storage(ctx context.Context, admin *pgxpool.Pool) map[string]tableSize {
	out := map[string]tableSize{}
	for _, t := range storageTables {
		var s tableSize
		if err := admin.QueryRow(ctx, `SELECT pg_total_relation_size($1::regclass), (SELECT count(*) FROM `+t+`)`, t).Scan(&s.bytes, &s.rows); err != nil {
			continue
		}
		out[t] = s
	}
	return out
}

func storageReport(before, after map[string]tableSize, group, pair int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "    %-16s %12s %10s %14s %14s\n", "table", "bytes added", "rows added", "rows/message", "bytes/message")
	for _, t := range storageTables {
		a, ok := after[t]
		if !ok {
			continue
		}
		bf := before[t]
		n := group + pair
		switch {
		case strings.HasPrefix(t, "group_"):
			n = group
		case strings.HasPrefix(t, "people_"):
			n = pair
		}
		fmt.Fprintf(&b, "    %-16s %12d %10d %14.2f %14.0f\n", t, a.bytes-bf.bytes, a.rows-bf.rows,
			float64(a.rows-bf.rows)/math.Max(1, float64(n)), float64(a.bytes-bf.bytes)/math.Max(1, float64(n)))
	}
	return b.String()
}

// rnd and rndFloat shape the load; nothing secret depends on them.
func rnd(n int) int { return mrand.IntN(n) } //nolint:gosec // the load's shape, not a secret

func rndFloat() float64 { return mrand.Float64() } //nolint:gosec // the load's shape, not a secret
