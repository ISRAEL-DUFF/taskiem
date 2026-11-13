package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

// The phone app keeps the pages it read (Today, Money, Orders, the tools'
// lists: docs/spec/mobile-app-explore-and-offline.md K4) and asks again
// with If-None-Match: an answer that has not changed is a 304 with no
// body, so asking costs a request, not the page.
//
// etagJSON gives each signed-in GET answered with JSON and 200 an ETag (a
// hash of its body, so it is the same for the same answer to the same
// person, and never one person's for another: the body differs). Anything
// else (a stream, a file, an error, a write) passes through untouched,
// written as it is written.

// etagLimit is the most an answer is held to hash; a larger one passes
// through without an ETag.
const etagLimit = 4 << 20

func etagJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			next.ServeHTTP(w, r)
			return
		}
		ew := &etagWriter{ResponseWriter: w}
		next.ServeHTTP(ew, r)
		ew.finish(r)
	})
}

type etagWriter struct {
	http.ResponseWriter
	status  int
	decided bool
	holding bool
	buf     bytes.Buffer
}

// decide, at the head: a JSON success is held to hash; the rest goes on.
func (e *etagWriter) decide(status int) {
	if e.decided {
		return
	}
	e.decided = true
	e.status = status
	e.holding = status == http.StatusOK && strings.HasPrefix(e.Header().Get("Content-Type"), "application/json")
	if !e.holding {
		e.ResponseWriter.WriteHeader(status)
	}
}

func (e *etagWriter) WriteHeader(status int) { e.decide(status) }

func (e *etagWriter) Write(b []byte) (int, error) {
	e.decide(http.StatusOK)
	if !e.holding {
		return e.ResponseWriter.Write(b)
	}
	if e.buf.Len()+len(b) > etagLimit {
		// Too large to hold: written as it is, with no ETag.
		e.holding = false
		e.ResponseWriter.WriteHeader(e.status)
		if _, err := e.ResponseWriter.Write(e.buf.Bytes()); err != nil {
			return 0, err
		}
		e.buf.Reset()
		return e.ResponseWriter.Write(b)
	}
	return e.buf.Write(b)
}

// Flush passes a stream's flushes on (a handler that streams is never held).
func (e *etagWriter) Flush() {
	if e.holding {
		return
	}
	if f, ok := e.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (e *etagWriter) Unwrap() http.ResponseWriter { return e.ResponseWriter }

func (e *etagWriter) finish(r *http.Request) {
	if !e.decided || !e.holding {
		return
	}
	sum := sha256.Sum256(e.buf.Bytes())
	tag := `W/"` + base64.RawURLEncoding.EncodeToString(sum[:16]) + `"`
	h := e.Header()
	h.Set("ETag", tag)
	if matches(r.Header.Get("If-None-Match"), tag) {
		h.Del("Content-Length")
		h.Del("Content-Type")
		e.ResponseWriter.WriteHeader(http.StatusNotModified)
		return
	}
	e.ResponseWriter.WriteHeader(e.status)
	_, _ = e.ResponseWriter.Write(e.buf.Bytes())
}

// matches reads If-None-Match: a list of tags, or *.
func matches(header, tag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || t == tag || strings.TrimPrefix(t, "W/") == strings.TrimPrefix(tag, "W/") {
			return true
		}
	}
	return false
}
