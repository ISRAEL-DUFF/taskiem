package spike

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Recorder collects durations for percentile reporting.
type Recorder struct {
	mu sync.Mutex
	d  []time.Duration
}

func (r *Recorder) Add(d time.Duration) {
	r.mu.Lock()
	r.d = append(r.d, d)
	r.mu.Unlock()
}

// Percentiles returns the requested percentiles (0-100) and the count.
func (r *Recorder) Percentiles(ps ...float64) ([]time.Duration, int) {
	r.mu.Lock()
	d := append([]time.Duration(nil), r.d...)
	r.mu.Unlock()
	if len(d) == 0 {
		return make([]time.Duration, len(ps)), 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	out := make([]time.Duration, len(ps))
	for i, p := range ps {
		idx := int(p / 100 * float64(len(d)-1))
		out[i] = d[idx]
	}
	return out, len(d)
}

func (r *Recorder) Reset() {
	r.mu.Lock()
	r.d = r.d[:0]
	r.mu.Unlock()
}

type Counter struct{ n atomic.Int64 }

func (c *Counter) Inc()        { c.n.Add(1) }
func (c *Counter) Load() int64 { return c.n.Load() }
