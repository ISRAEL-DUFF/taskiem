package formguard

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

var (
	key1 = bytes.Repeat([]byte{1}, KeySize)
	key2 = bytes.Repeat([]byte{2}, KeySize)
	t0   = time.Unix(1_800_000_000, 0)
)

const form = "sf:0b8e7c6a-1d2e-4f00-9a1b-2c3d4e5f6a7b/order"

func guard(t *testing.T, key []byte, opts ...Option) *Guard {
	t.Helper()
	g, err := New(key, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func issue(t *testing.T, g *Guard, f string, at time.Time, pow int) string {
	t.Helper()
	tok, err := g.Issue(f, at, pow)
	if err != nil {
		t.Fatal(err)
	}
	return string(tok)
}

func TestNew(t *testing.T) {
	cases := []struct {
		name string
		key  []byte
		opts []Option
		ok   bool
	}{
		{"good", key1, nil, true},
		{"short", key1[:31], nil, false},
		{"long", append(bytes.Clone(key1), 0), nil, false},
		{"zero", make([]byte, KeySize), nil, false},
		{"bad previous", key1, []Option{WithPreviousKeys([]byte("short"))}, false},
		{"min fill over max age", key1, []Option{WithMinFill(time.Hour), WithMaxAge(time.Minute)}, false},
		{"negative min fill", key1, []Option{WithMinFill(-time.Second)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.key, c.opts...)
			if (err == nil) != c.ok {
				t.Fatalf("err = %v, want ok %v", err, c.ok)
			}
		})
	}
}

func TestIssueRejects(t *testing.T) {
	g := guard(t, key1)
	for _, c := range []struct {
		form string
		pow  int
	}{{"", 0}, {strings.Repeat("x", MaxForm+1), 0}, {form, -1}, {form, MaxPoW + 1}} {
		if _, err := g.Issue(c.form, t0, c.pow); err == nil {
			t.Errorf("Issue(%d-byte form, %d) succeeded", len(c.form), c.pow)
		}
	}
}

func TestIssueVerify(t *testing.T) {
	g := guard(t, key1)
	long := strings.Repeat("f", MaxForm)
	cases := []struct {
		name  string
		form  string // issued for
		check string // verified against
		age   time.Duration
		want  error
	}{
		{"ok", form, form, 10 * time.Second, nil},
		{"ok at min fill", form, form, DefaultMinFill, nil},
		{"ok at max age", form, form, DefaultMaxAge, nil},
		{"longest form", long, long, time.Minute, nil},
		{"wrong form", form, "sf:other/order", time.Minute, ErrInvalid},
		{"form prefix", form, form[:10], time.Minute, ErrInvalid},
		{"too fast", form, form, time.Second, ErrTooFast},
		{"from the future", form, form, -time.Minute, ErrTooFast},
		{"expired", form, form, DefaultMaxAge + time.Second, ErrExpired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok := issue(t, g, c.form, t0, 0)
			if strings.ContainsAny(tok, "+/=") {
				t.Fatalf("token %q is not unpadded base64url", tok)
			}
			cl, err := g.Verify(tok, c.check, t0.Add(c.age))
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if err != nil {
				return
			}
			if cl.Form != c.form || !cl.IssuedAt.Equal(t0) || cl.PoW != 0 || cl.Nonce == [16]byte{} {
				t.Fatalf("claims = %+v", cl)
			}
		})
	}
}

func TestClockAndOptions(t *testing.T) {
	now := t0
	g := guard(t, key1, WithClock(func() time.Time { return now }), WithMinFill(time.Minute), WithMaxAge(time.Hour))
	tok := issue(t, g, form, time.Time{}, 0)
	now = t0.Add(30 * time.Second)
	if _, err := g.Verify(tok, form, time.Time{}); !errors.Is(err, ErrTooFast) {
		t.Fatalf("at 30s: %v, want ErrTooFast", err)
	}
	now = t0.Add(2 * time.Minute)
	if _, err := g.Verify(tok, form, time.Time{}); err != nil {
		t.Fatalf("at 2m: %v", err)
	}
	now = t0.Add(61 * time.Minute)
	if _, err := g.Verify(tok, form, time.Time{}); !errors.Is(err, ErrExpired) {
		t.Fatalf("at 61m: %v, want ErrExpired", err)
	}
}

func TestTamper(t *testing.T) {
	g := guard(t, key1)
	tok := issue(t, g, form, t0, 0)
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatal(err)
	}
	n := len(form)
	flip := func(i int) func([]byte) []byte {
		return func(b []byte) []byte { b[i] ^= 1; return b }
	}
	cases := []struct {
		name string
		edit func([]byte) []byte
	}{
		{"version", func(b []byte) []byte { b[0] = 2; return b }},
		{"issued at", flip(8)},
		{"nonce", flip(9)},
		{"difficulty", func(b []byte) []byte { b[9+nonceSize] = 8; return b }},
		{"form length", flip(10 + nonceSize)},
		{"form", flip(headerSize)},
		{"tag", flip(len(raw) - 1)},
		{"truncated", func(b []byte) []byte { return b[:len(b)-1] }},
		{"extended", func(b []byte) []byte { return append(b, 0) }},
		{"tag only", func(b []byte) []byte { return b[headerSize+n:] }},
		{"empty", func([]byte) []byte { return nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := c.edit(bytes.Clone(raw))
			bad := base64.RawURLEncoding.EncodeToString(b)
			if _, err := g.Verify(bad, form, t0.Add(time.Minute)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
	for _, bad := range []string{tok + "=", "!" + tok[1:], strings.Repeat("A", 1000)} {
		if _, err := g.Verify(bad, form, t0.Add(time.Minute)); !errors.Is(err, ErrInvalid) {
			t.Errorf("Verify(%.20q…) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestKeyRotation(t *testing.T) {
	old := guard(t, key1)
	rotated := guard(t, key2, WithPreviousKeys(key1))
	other := guard(t, key2)
	at := t0.Add(time.Minute)

	oldTok := issue(t, old, form, t0, 0)
	c1, err := old.Verify(oldTok, form, at)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := rotated.Verify(oldTok, form, at)
	if err != nil {
		t.Fatalf("previous key: %v", err)
	}
	if c1.IdempotencyKey() != c2.IdempotencyKey() {
		t.Fatal("idempotency key changed across rotation")
	}
	if _, err := other.Verify(oldTok, form, at); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dropped key: %v, want ErrInvalid", err)
	}
	newTok := issue(t, rotated, form, t0, 0)
	if _, err := other.Verify(newTok, form, at); err != nil {
		t.Fatalf("new token not signed with the current key: %v", err)
	}
	if _, err := old.Verify(newTok, form, at); !errors.Is(err, ErrInvalid) {
		t.Fatalf("old guard took a new token: %v", err)
	}
}

func TestIdempotencyKey(t *testing.T) {
	g := guard(t, key1)
	tok := issue(t, g, form, t0, 0)
	a, _ := g.Verify(tok, form, t0.Add(time.Minute))
	b, _ := g.Verify(tok, form, t0.Add(time.Hour))
	if a.IdempotencyKey() != b.IdempotencyKey() || a.IdempotencyKey() == [32]byte{} {
		t.Fatal("same token, different or empty keys")
	}
	c, _ := g.Verify(issue(t, g, form, t0, 0), form, t0.Add(time.Minute))
	if a.IdempotencyKey() == c.IdempotencyKey() {
		t.Fatal("different tokens, same key")
	}
}

func TestHoneypot(t *testing.T) {
	cases := []struct {
		name string
		v    url.Values
		want bool
	}{
		{"absent", url.Values{"name": {"Ada"}}, false},
		{"empty", url.Values{HoneypotField: {""}}, false},
		{"blank", url.Values{HoneypotField: {"  "}}, false},
		{"filled", url.Values{HoneypotField: {"http://spam.example"}}, true},
		{"second value", url.Values{HoneypotField: {"", "x"}}, true},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := HoneypotTripped(c.v); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// solve is pow.js in Go.
func solve(t *testing.T, tok string, bits int) string {
	t.Helper()
	for c := uint64(0); c < 1<<32; c++ {
		s := strconv.FormatUint(c, 10)
		if leadingZeros(sha256.Sum256([]byte(tok+":"+s))) >= bits {
			return s
		}
	}
	t.Fatal("no solution")
	return ""
}

func TestPoW(t *testing.T) {
	g := guard(t, key1)
	tok := issue(t, g, form, t0, 8)
	cl, err := g.Verify(tok, form, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if cl.PoW != 8 {
		t.Fatalf("difficulty %d, want 8", cl.PoW)
	}
	good := solve(t, tok, 8)
	n, _ := strconv.ParseUint(good, 10, 32)
	wrong := strconv.FormatUint(n+1, 10)
	for leadingZeros(sha256.Sum256([]byte(tok+":"+wrong))) >= 8 {
		n++
		wrong = strconv.FormatUint(n+1, 10)
	}
	other := issue(t, g, form, t0, 8)
	cases := []struct {
		name, token, counter string
		ok                   bool
	}{
		{"solved", tok, good, true},
		{"wrong counter", tok, wrong, false},
		{"empty", tok, "", false},
		{"leading zero", tok, "0" + good, false},
		{"plus sign", tok, "+" + good, false},
		{"over uint32", tok, "4294967296", false},
		{"not a number", tok, "abc", false},
		{"another token", other, good, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := VerifyPoW(c.token, cl, c.counter)
			if c.ok != (err == nil) || (err != nil && !errors.Is(err, ErrPoW)) {
				t.Fatalf("err = %v, want ok %v", err, c.ok)
			}
		})
	}

	off := issue(t, g, form, t0, 0)
	clOff, _ := g.Verify(off, form, t0.Add(time.Minute))
	if err := VerifyPoW(off, clOff, ""); err != nil {
		t.Fatalf("difficulty 0: %v", err)
	}
}

func TestLeadingZeros(t *testing.T) {
	var h [32]byte
	if leadingZeros(h) != 256 {
		t.Fatal("all zero")
	}
	h[1] = 0x10
	if got := leadingZeros(h); got != 11 {
		t.Fatalf("got %d, want 11", got)
	}
}

func TestScript(t *testing.T) {
	s := Script()
	if len(s) == 0 || len(s) >= 1536 {
		t.Fatalf("pow.js is %d bytes, want under 1.5 KB", len(s))
	}
	for _, bad := range []string{"eval", "innerHTML", "Function("} {
		if bytes.Contains(s, []byte(bad)) {
			t.Errorf("pow.js contains %q", bad)
		}
	}
	for _, need := range []string{`input[name=` + PoWField + `]`, `"` + PoWField + `"`, "form[data-pow]", "data-pow-token", "crypto.subtle.digest"} {
		if !bytes.Contains(s, []byte(need)) {
			t.Errorf("pow.js lacks %q", need)
		}
	}
	sum := sha256.Sum256(s)
	if ScriptHash() != hex.EncodeToString(sum[:]) {
		t.Fatal("ScriptHash does not match Script")
	}
}

func TestHashIP(t *testing.T) {
	key := key1
	day := time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)
	later := day.Add(20 * time.Hour)
	next := day.Add(24 * time.Hour)
	h := func(ip string, d time.Time) [16]byte { return HashIP(key, ip, d) }

	same := []struct{ name, a, b string }{
		{"stable", "102.89.1.2", "102.89.1.2"},
		{"spaces", "102.89.1.2", " 102.89.1.2 "},
		{"with port", "102.89.1.2", "102.89.1.2:4431"},
		{"mapped IPv4", "102.89.1.2", "::ffff:102.89.1.2"},
		{"same /64", "2c0f:f5c0:1:2:aaaa::1", "2c0f:f5c0:1:2:ffff:1:2:3"},
		{"same /64, zone", "fe80::1", "fe80::2%eth0"},
		{"IPv6 with port", "2c0f:f5c0:1:2::1", "[2c0f:f5c0:1:2::9]:443"},
	}
	for _, c := range same {
		if h(c.a, day) != h(c.b, later) {
			t.Errorf("%s: %s and %s differ", c.name, c.a, c.b)
		}
	}
	differ := []struct{ name, a, b string }{
		{"IPv4 neighbour", "102.89.1.2", "102.89.1.3"},
		{"other /64", "2c0f:f5c0:1:2::1", "2c0f:f5c0:1:3::1"},
		{"not an address", "102.89.1.2", "garbage"},
	}
	for _, c := range differ {
		if h(c.a, day) == h(c.b, day) {
			t.Errorf("%s: %s and %s match", c.name, c.a, c.b)
		}
	}
	if h("102.89.1.2", day) == h("102.89.1.2", next) {
		t.Error("hash did not change across days")
	}
	if HashIP(key2, "102.89.1.2", day) == h("102.89.1.2", day) {
		t.Error("hash does not depend on the key")
	}
	lagos := time.FixedZone("WAT", 3600)
	if h("102.89.1.2", time.Date(2026, 10, 20, 0, 30, 0, 0, lagos)) != h("102.89.1.2", day) {
		t.Error("day is not taken in UTC")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(rate.Every(time.Hour), 3, 10, time.Hour)
	for i := range 3 {
		if !l.Allow("a") {
			t.Fatalf("request %d refused within the burst", i)
		}
	}
	if l.Allow("a") {
		t.Fatal("request past the burst allowed")
	}
	if !l.Allow("b") {
		t.Fatal("another key refused")
	}
}

func TestLimiterBounded(t *testing.T) {
	const size = 100
	l := NewLimiter(SubmissionsPerIP.Rate, SubmissionsPerIP.Burst, size, time.Hour)
	for i := range 10 * size {
		l.Allow("k" + strconv.Itoa(i))
		if n := l.Len(); n > size {
			t.Fatalf("after %d keys the cache holds %d, over %d", i+1, n, size)
		}
	}
	if l.Len() != size {
		t.Fatalf("holds %d, want %d", l.Len(), size)
	}
}

func TestDefaults(t *testing.T) {
	if got := float64(PageViewsPerIP.Rate) * 60; got < 299 || got > 301 || PageViewsPerIP.Burst != 100 {
		t.Errorf("PageViewsPerIP = %v a minute, burst %d", got, PageViewsPerIP.Burst)
	}
	if got := float64(SubmissionsPerIP.Rate) * 600; got < 19.9 || got > 20.1 || SubmissionsPerIP.Burst != 10 {
		t.Errorf("SubmissionsPerIP = %v per 10 minutes, burst %d", got, SubmissionsPerIP.Burst)
	}
}
