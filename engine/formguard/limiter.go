package formguard

import (
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/lru"
)

// Bucket is a token bucket's rate and burst.
type Bucket struct {
	Rate  rate.Limit
	Burst int
}

// Defaults per IP hash. They are generous on purpose: Nigerian mobile
// networks put thousands of phones behind one address (carrier-grade NAT),
// so a per-IP limit tight enough to stop one bot would also stop a market's
// worth of real customers. They only blunt floods; the token, fill time,
// honeypot and proof-of-work stop bots, and per-page and per-phone limits
// sit on top (storefronts.md 7.5).
var (
	// PageViewsPerIP: 300 a minute, bursts of 100.
	PageViewsPerIP = Bucket{Rate: rate.Every(time.Minute / 300), Burst: 100}
	// SubmissionsPerIP: 20 every 10 minutes, bursts of 10.
	SubmissionsPerIP = Bucket{Rate: rate.Every(10 * time.Minute / 20), Burst: 10}
)

// Limiter is a token bucket per key (an IP hash, a page, a phone hash),
// held in a bounded cache so a flood of distinct keys costs no lasting
// memory (S34). A key idle for the TTL is forgotten, as is the least
// recently used one when the cache is full; either way it starts again
// with a full bucket, so pick a TTL of at least Burst/Rate. Limiters are
// per process: with several replicas the rates apply to each.
type Limiter struct {
	rate  rate.Limit
	burst int

	mu      sync.Mutex // makes the lookup and the insert one step
	buckets *lru.Cache[string, *rate.Limiter]
}

// NewLimiter makes a Limiter of at most size keys, each allowed r a second
// with bursts of burst, forgetting keys idle for ttl (0: only by size).
func NewLimiter(r rate.Limit, burst int, size int, ttl time.Duration) *Limiter {
	return &Limiter{rate: r, burst: burst, buckets: lru.New[string, *rate.Limiter](size, ttl)}
}

// Allow takes a token from key's bucket, reporting whether there was one.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	b, ok := l.buckets.Get(key)
	if !ok {
		b = rate.NewLimiter(l.rate, l.burst)
	}
	l.buckets.Put(key, b) // refreshes the entry's age: the TTL is idle time
	l.mu.Unlock()
	return b.Allow()
}

// Len is the number of keys held.
func (l *Limiter) Len() int { return l.buckets.Len() }
