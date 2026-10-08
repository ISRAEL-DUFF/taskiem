package lru

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestBoundEvictsLeastRecentlyUsed(t *testing.T) {
	c := New[string, int](3, 0)
	for i := range 3 {
		c.Put(strconv.Itoa(i), i)
	}
	c.Get("0") // 1 is now the least recently used
	c.Put("3", 3)
	if _, ok := c.Get("1"); ok {
		t.Error("the least recently used entry survived")
	}
	for _, k := range []string{"0", "2", "3"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("%s was evicted", k)
		}
	}
	for i := range 10_000 {
		c.Put(strconv.Itoa(i), i)
	}
	if n := c.Len(); n != 3 {
		t.Errorf("held %d entries, bound 3", n)
	}
}

func TestTTL(t *testing.T) {
	c := New[string, int](10, time.Minute)
	now := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return now }
	c.Put("a", 1)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatal("fresh entry missing")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get("a"); ok {
		t.Error("an expired entry was served")
	}
	if c.Len() != 0 {
		t.Error("an expired entry was kept after a miss")
	}
}

func TestZeroCache(t *testing.T) {
	var c Cache[int, int]
	for i := range DefaultSize + 10 {
		c.Put(i, i)
	}
	if c.Len() != DefaultSize {
		t.Errorf("held %d entries, bound %d", c.Len(), DefaultSize)
	}
	if v, ok := c.Get(DefaultSize + 9); !ok || v != DefaultSize+9 {
		t.Error("the newest entry is missing")
	}
}

func TestConcurrent(t *testing.T) {
	c := New[int, int](64, 0)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 2000 {
				c.Put(g*10_000+i, i)
				c.Get(i)
				if i%100 == 0 {
					c.Delete(i)
				}
			}
		})
	}
	wg.Wait()
	if c.Len() > 64 {
		t.Errorf("held %d entries, bound 64", c.Len())
	}
}
