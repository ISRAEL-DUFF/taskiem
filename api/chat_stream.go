package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// The Chat page's live stream (business links, L3): GET /v1/chat/stream, as
// server-sent events, tells the page what changed in the person's
// conversations as it happens, so a message appears on the other side in a
// second or two and a tick turns blue when it is read. The page keeps its
// polling as a fallback and reconciles with it.
//
// How the server learns of a change is the design question the performance
// document raises: Postgres NOTIFY serialises commits, and the same
// database carries the runs. So each connection asks every second what
// changed since it last looked, from an index (migration 00306): a lookup
// when nothing has, a few rows when something has. The number of open
// streams is capped per business and per server.
//
// Events (each with an id, the change's time in microseconds, so a client
// that reconnects with Last-Event-ID misses nothing):
//
//	message       a message of a conversation, new or changed (its read mark, its
//	              reactions): {"message": {…}}, as the messages API gives it
//	gone          a message deleted for this side: {"id", "link"}
//	conversation  a conversation's settings changed (muted, archived, pinned): {"link", …}
//	typing        the conversations whose other side is typing now: {"typing": [links]}
//	taskiem       the person's own conversation with Taskiem, on every channel: {"messages": […]}
//
// then a ready event on connecting and a ping event at least every ten
// seconds, so a page can tell a quiet stream from one that has stalled.
const (
	chatStreamEvery    = time.Second
	chatStreamTypingEv = 2 * time.Second
	// A change that commits a little after it was written (its statement's
	// clock) is read again within this look-back; the events already sent are
	// remembered so it is sent once.
	chatStreamLookBack = 10 * time.Second
	// How far back a reconnecting client may be sent (Last-Event-ID): an old
	// id is not the business's whole history over again.
	chatStreamMaxReplay = time.Hour
	chatStreamMax       = 30 * time.Minute
	// The ping event's interval when the server does not set one.
	chatStreamPing = 10 * time.Second
	// How many streams one business may hold open (its people's tabs), and
	// how many this server holds in all.
	chatStreamsPerTenant = 12
	chatStreamsTotal     = 2000
	// How many changes one look takes up; a full batch is followed at once by another.
	chatStreamBatch = 200
)

type chatStreamGate struct {
	mu     sync.Mutex
	tenant map[uuid.UUID]int
	total  int
	// refused is which cap turned the last one away (business or server).
	refused string
}

// lastCap is which cap turned the last stream away.
func (g *chatStreamGate) lastCap() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refused
}

func (g *chatStreamGate) acquire(t uuid.UUID) (release func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tenant == nil {
		g.tenant = map[uuid.UUID]int{}
	}
	if g.tenant[t] >= chatStreamsPerTenant {
		g.refused = "business"
		return nil, false
	}
	if g.total >= chatStreamsTotal {
		g.refused = "server"
		return nil, false
	}
	g.tenant[t]++
	g.total++
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.tenant[t]--; g.tenant[t] <= 0 {
			delete(g.tenant, t)
		}
		g.total--
	}, true
}

// chatStream is GET /v1/chat/stream.
func (s *Server) chatStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	release, ok := s.chatGate.acquire(p.TenantID)
	if !ok {
		telemetry.ChatStreamRefused.WithLabelValues(s.chatGate.lastCap()).Inc()
		w.Header().Set("Retry-After", "30")
		writeErr(w, http.StatusTooManyRequests, "too many open chat streams: close another tab, or the page will poll instead")
		return
	}
	defer release()
	telemetry.ChatStreamsOpen.Inc()
	defer telemetry.ChatStreamsOpen.Dec()
	viewer := viewerOf(p)
	ping := s.ChatStreamPing
	if ping <= 0 {
		ping = chatStreamPing
	}

	// Where to start: after what the client has (Last-Event-ID, in
	// microseconds), or from a moment ago for a first connection (the page
	// fetched its messages just before; what it already has is merged).
	since := time.Now().Add(-chatStreamLookBack)
	if id, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil && id > 0 {
		// Only as far back as an hour (an older id, or one from the future, is
		// not trusted to be the history over again).
		if at := time.UnixMicro(id); at.Before(time.Now().Add(time.Minute)) {
			since = at.Add(-chatStreamLookBack)
		}
		if floor := time.Now().Add(-chatStreamMaxReplay); since.Before(floor) {
			since = floor
		}
	}
	sinceTaskiem := since
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // proxies must not buffer the stream
	w.WriteHeader(http.StatusOK)
	write := func(format string, args ...any) error {
		_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return err
		}
		return rc.Flush()
	}
	// Each event sent is counted, by its kind.
	sent := func(event string, err error) error {
		if err == nil {
			telemetry.ChatStreamEvents.WithLabelValues(event).Inc()
		}
		return err
	}
	if write("retry: 2000\n\nevent: ready\ndata: {}\n\n") != nil {
		return
	}

	type seenKey struct {
		pair uuid.UUID
		at   int64
	}
	seen := map[seenKey]struct{}{}
	seenConv := map[seenKey]struct{}{}
	seenTaskiem := map[seenKey]struct{}{}
	var activeLinks []uuid.UUID
	var linksAt, typingAt time.Time
	var typing []uuid.UUID
	deadline := time.Now().Add(chatStreamMax)
	lastWrite := time.Now()
	tick := time.NewTicker(chatStreamEvery)
	defer tick.Stop()
	for time.Now().Before(deadline) {
		var sends []func() error
		more := false
		polled := time.Now()
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			refs, err := links.ChangedRefsTx(ctx, tx, p.TenantID, since, chatStreamBatch)
			if err != nil {
				return err
			}
			var fresh []links.ChangeRef
			var pairs []uuid.UUID
			for _, c := range refs {
				k := seenKey{c.Pair, c.At.UnixMicro()}
				if _, done := seen[k]; done {
					continue
				}
				seen[k] = struct{}{}
				fresh = append(fresh, c)
				if !c.Hidden {
					pairs = append(pairs, c.Pair)
				}
			}
			if n := len(refs); n > 0 {
				if n == chatStreamBatch {
					// A full batch: more may be waiting, so go on from the
					// last of them (a bulk change, a conversation read at
					// once) rather than from the look-back, which could
					// not get past a batch that fills it.
					more = true
					if at := refs[n-1].At; at.After(since) {
						since = at.Add(-time.Microsecond)
					}
				} else if edge := refs[n-1].At.Add(-chatStreamLookBack); edge.After(since) {
					// The window slides to just before the newest change.
					since = edge
				}
			}
			if len(fresh) > 0 {
				msgs, err := links.MessagesByPairTx(ctx, tx, p.TenantID, pairs)
				if err != nil {
					return err
				}
				if err := links.DecorateTx(ctx, tx, p.TenantID, viewer, msgs); err != nil {
					return err
				}
				byPair := map[uuid.UUID]links.Message{}
				for _, m := range msgs {
					byPair[m.PairID] = m
				}
				for _, c := range fresh {
					id := c.At.UnixMicro()
					switch m, ok := byPair[c.Pair]; {
					case c.Hidden:
						b, _ := json.Marshal(map[string]any{"id": c.Pair, "link": c.Link})
						sends = append(sends, func() error { return sent("gone", write("id: %d\nevent: gone\ndata: %s\n\n", id, b)) })
					case ok:
						b, _ := json.Marshal(map[string]any{"message": m})
						sends = append(sends, func() error { return sent("message", write("id: %d\nevent: message\ndata: %s\n\n", id, b)) })
					}
				}
			}
			convs, err := links.ConversationChangesTx(ctx, tx, p.TenantID, since)
			if err != nil {
				return err
			}
			for _, c := range convs {
				k := seenKey{c.Link, c.At.UnixMicro()}
				if _, done := seenConv[k]; done {
					continue
				}
				seenConv[k] = struct{}{}
				id, b := c.At.UnixMicro(), mustJSON(c)
				sends = append(sends, func() error { return sent("conversation", write("id: %d\nevent: conversation\ndata: %s\n\n", id, b)) })
			}
			// Who is typing: the links come from the business's links, now
			// and then; the typing, every couple of seconds.
			if time.Since(linksAt) > 30*time.Second {
				if activeLinks, err = links.ActiveLinksTx(ctx, tx); err != nil {
					return err
				}
				linksAt = time.Now()
			}
			if time.Since(typingAt) >= chatStreamTypingEv {
				now, err := links.TypingNowTx(ctx, tx, activeLinks)
				if err != nil {
					return err
				}
				typingAt = time.Now()
				if !slices.Equal(now, typing) {
					typing = now
					b, _ := json.Marshal(map[string]any{"typing": append([]uuid.UUID{}, now...)})
					sends = append(sends, func() error { return sent("typing", write("event: typing\ndata: %s\n\n", b)) })
				}
			}
			// The person's own conversation with Taskiem (every channel).
			if p.UserID != uuid.Nil {
				rows, err := s.taskiemSince(ctx, tx, p, sinceTaskiem, chatStreamBatch)
				if err != nil {
					return err
				}
				var fresh []TaskiemMessage
				for _, m := range rows {
					k := seenKey{m.ID, m.CreatedAt.UnixMicro()}
					if _, done := seenTaskiem[k]; done && m.ID != uuid.Nil {
						continue
					}
					seenTaskiem[k] = struct{}{}
					fresh = append(fresh, m)
				}
				if n := len(rows); n > 0 {
					if n == chatStreamBatch {
						more = true
						if at := rows[n-1].CreatedAt.Add(-time.Microsecond); at.After(sinceTaskiem) {
							sinceTaskiem = at
						}
					} else if edge := rows[n-1].CreatedAt.Add(-chatStreamLookBack); edge.After(sinceTaskiem) {
						sinceTaskiem = edge
					}
				}
				if len(fresh) > 0 {
					id, b := fresh[len(fresh)-1].CreatedAt.UnixMicro(), mustJSON(map[string]any{"messages": fresh})
					sends = append(sends, func() error { return sent("taskiem", write("id: %d\nevent: taskiem\ndata: %s\n\n", id, b)) })
				}
			}
			return nil
		})
		telemetry.ChatStreamPolls.Inc()
		telemetry.ChatStreamPollSeconds.Observe(time.Since(polled).Seconds())
		if err != nil {
			if ctx.Err() == nil {
				s.Logger.Error("chat stream", "tenant", p.TenantID, "err", err)
			}
			return
		}
		for _, send := range sends {
			if send() != nil {
				return
			}
			lastWrite = time.Now()
		}
		// What was seen long ago cannot come again: forget it.
		for k := range seen {
			if time.UnixMicro(k.at).Before(since) {
				delete(seen, k)
			}
		}
		for k := range seenConv {
			if time.UnixMicro(k.at).Before(since) {
				delete(seenConv, k)
			}
		}
		for k := range seenTaskiem {
			if time.UnixMicro(k.at).Before(sinceTaskiem) {
				delete(seenTaskiem, k)
			}
		}
		if time.Since(lastWrite) >= ping {
			if sent("ping", write("event: ping\ndata: {}\n\n")) != nil {
				return
			}
			lastWrite = time.Now()
		}
		if more {
			continue // a full batch: the next straight away
		}
		select {
		case <-ctx.Done():
			return
		case <-s.done():
			return
		case <-tick.C:
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
