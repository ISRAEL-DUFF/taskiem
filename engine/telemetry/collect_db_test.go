package telemetry_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// TestScrapesDoNotHoldTheAppPool: the queue and pool collectors read the
// database on each scrape, through the pool that serves requests. While
// the database cannot answer (here: tasks locked, as by a migration), a
// scrape gives up after a short deadline, and scrapes arriving together
// (anyone who can reach :9090) take one connection between them, not one
// each until the pool is gone.
func TestScrapesDoNotHoldTheAppPool(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	pool := d.AppPool(t, 4)
	reg := prometheus.NewRegistry()
	reg.MustRegister(telemetry.QueueCollector{Pool: pool}, telemetry.PoolCollector{Pool: pool})

	tx, err := d.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "LOCK TABLE tasks IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	var wg sync.WaitGroup
	var most int32
	var mu sync.Mutex
	for range 8 {
		wg.Go(func() { _, _ = reg.Gather() })
	}
	watch, stop := context.WithCancel(ctx)
	go func() {
		for watch.Err() == nil {
			n := pool.Stat().AcquiredConns()
			mu.Lock()
			most = max(most, n)
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		_ = tx.Rollback(ctx) // let the scrapes finish before the test ends
		<-done
		t.Error("scrapes waited for the locked table for more than 6s")
	}
	stop()
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("scrapes took %s", took)
	}
	mu.Lock()
	defer mu.Unlock()
	if most > 2 {
		t.Errorf("concurrent scrapes held %d of the pool's 4 connections", most)
	}
	// A cancelled query's connection is closed in the background.
	for i := 0; pool.Stat().AcquiredConns() != 0; i++ {
		if i == 100 {
			t.Fatalf("%d connections still held a second after the scrapes", pool.Stat().AcquiredConns())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
