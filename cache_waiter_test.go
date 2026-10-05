package gohunspell

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestCacheWaiterWhenEntryIsDropped covers a request which waits for a copy of a busy dictionary
// while the cache entry of that dictionary is dropped: by a new version, by Invalidate or by the
// LRU eviction. The waiting request has to finish once the busy copy is released.
func TestCacheWaiterWhenEntryIsDropped(t *testing.T) {
	tests := []struct {
		name string
		drop func(c *Cache, load Loader) error
	}{
		{
			name: "new version",
			drop: func(c *Cache, load Loader) error {
				_, err := c.Check("en", "2", load, "ok", nil)
				return err
			},
		},
		{
			name: "invalidate",
			drop: func(c *Cache, _ Loader) error {
				c.Invalidate("en")
				return nil
			},
		},
		{
			name: "clear",
			drop: func(c *Cache, _ Loader) error {
				c.Clear()
				return nil
			},
		},
		{
			name: "lru eviction",
			drop: func(c *Cache, load Loader) error {
				_, err := c.Check("other", "1", load, "ok", nil)
				return err
			},
		},
		{
			// without a drop the waiter gets the released copy, this case passes already
			name: "control: entry stays",
			drop: func(*Cache, Loader) error { return nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			load := testLoader(t, &calls)
			// one copy and one dictionary, so the second request has to wait and a second
			// dictionary evicts the first one
			c := NewCache(WithCopies(1), WithMaxDictionaries(1))

			// the first request holds the only copy
			hold := make(chan struct{})
			holding := make(chan struct{})
			holderDone := make(chan error, 1)
			go func() {
				holderDone <- c.Use("en", "1", load, func(*Dictionary) error {
					close(holding)
					<-hold
					return nil
				})
			}()
			<-holding

			// the second request waits for that copy
			waiterDone := make(chan error, 1)
			go func() {
				_, err := c.Check("en", "1", load, "ok", nil)
				waiterDone <- err
			}()
			waitUntilWaiting(t, c, "en")

			// the entry is dropped while the second request waits, then the copy is released
			if err := tt.drop(c, load); err != nil {
				t.Fatal(err)
			}
			close(hold)

			if err := <-holderDone; err != nil {
				t.Fatal(err)
			}

			select {
			case err := <-waiterDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the waiting request did not finish after the busy copy was released")
			}
		})
	}
}

// waitUntilWaiting waits until a request blocks on the busy copies of the entry with the key,
// which holds the loading lock of the entry while it waits.
func waitUntilWaiting(t *testing.T, c *Cache, key string) {
	t.Helper()

	c.mu.Lock()
	e := c.entries[key].Value.(*cacheEntry)
	c.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for e.loading.TryLock() {
		e.loading.Unlock()

		if time.Now().After(deadline) {
			t.Fatal("the second request did not start waiting for a copy")
		}

		time.Sleep(time.Millisecond)
	}
}
