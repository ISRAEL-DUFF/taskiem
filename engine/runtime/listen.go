package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// listen holds one connection on LISTEN channel and signals wake on each
// notification. One listener per process (decision 0002 amendment). When
// the connection drops (a database restart or failover), it reconnects
// with backoff and signals wake once, since notifications sent meanwhile
// were lost; callers also poll, so nothing waits on it alone. When want is
// set, a notification wakes the caller only if want accepts its payload
// (a worker skips tasks for other queues instead of claiming for nothing).
func listen(ctx context.Context, s *Store, channel string, want func(payload string) bool) (<-chan struct{}, func(), error) {
	conn, err := subscribe(ctx, s.Pool, channel)
	if err != nil {
		return nil, nil, err
	}
	wake := make(chan struct{}, 1)
	notify := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	lctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var mu sync.Mutex
	current := conn
	wg.Add(1)
	go func() {
		defer wg.Done()
		backoff := 100 * time.Millisecond
		for {
			mu.Lock()
			c := current
			mu.Unlock()
			if c != nil {
				if n, err := c.Conn().WaitForNotification(lctx); err == nil {
					backoff = 100 * time.Millisecond
					if want == nil || want(n.Payload) {
						notify()
					}
					continue
				}
				if lctx.Err() != nil {
					return
				}
				discard(c) // broken: reconnect below
				mu.Lock()
				current = nil
				mu.Unlock()
			}
			select {
			case <-lctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, 5*time.Second)
			if c, err := subscribe(lctx, s.Pool, channel); err == nil {
				mu.Lock()
				current = c
				mu.Unlock()
				notify()
			}
		}
	}()
	return wake, func() {
		cancel()
		wg.Wait()
		mu.Lock()
		defer mu.Unlock()
		if current != nil {
			discard(current) // it may be mid-wait: never return it to the pool
		}
	}, nil
}

// discard closes a listening connection instead of returning it to the pool.
func discard(c *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = c.Hijack().Close(ctx)
}

func subscribe(ctx context.Context, pool *pgxpool.Pool, channel string) (*pgxpool.Conn, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		conn.Release()
		return nil, err
	}
	return conn, nil
}

func waitFor(ctx context.Context, wake <-chan struct{}, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-wake:
	case <-t.C:
	case <-ctx.Done():
	}
}
