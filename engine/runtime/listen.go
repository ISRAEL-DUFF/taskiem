package runtime

import (
	"context"
	"sync"
	"time"
)

// listen holds one connection on LISTEN channel and signals wake on each
// notification. One listener per process (decision 0002 amendment).
func listen(ctx context.Context, s *Store, channel string) (<-chan struct{}, func(), error) {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		conn.Release()
		return nil, nil, err
	}
	wake := make(chan struct{}, 1)
	lctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if _, err := conn.Conn().WaitForNotification(lctx); err != nil {
				return
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}()
	return wake, func() {
		cancel()
		wg.Wait()
		// The connection may be mid-wait; discard it rather than reuse it.
		_ = conn.Hijack().Close(context.Background())
	}, nil
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
