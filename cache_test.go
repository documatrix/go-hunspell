package gohunspell

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

func testLoader(t *testing.T, calls *atomic.Int32) Loader {
	t.Helper()
	aff, err := os.ReadFile("testdata/en_US/en_US.aff")
	if err != nil {
		t.Fatal(err)
	}
	dic, err := os.ReadFile("testdata/en_US/en_US.dic")
	if err != nil {
		t.Fatal(err)
	}
	return func() ([]byte, []byte, error) {
		calls.Add(1)
		return aff, dic, nil
	}
}

func TestCacheLoadsOncePerVersion(t *testing.T) {
	var calls atomic.Int32
	load := testLoader(t, &calls)
	c := NewCache()

	for i := 0; i < 3; i++ {
		ms, err := c.Check("en", "1", load, "a speling mistake", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := Words(ms); len(got) != 1 || got[0] != "speling" {
			t.Fatalf("misspellings = %v", got)
		}
	}
	if calls.Load() != 1 || c.Len() != 1 {
		t.Fatalf("loader calls = %d, len = %d, want 1 and 1", calls.Load(), c.Len())
	}

	// a new version loads again and replaces the old one
	if _, err := c.Check("en", "2", load, "ok", nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || c.Len() != 1 {
		t.Fatalf("loader calls = %d, len = %d, want 2 and 1", calls.Load(), c.Len())
	}
	// the old version is gone, using it loads a third time
	if _, err := c.Check("en", "1", load, "ok", nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("loader calls = %d, want 3", calls.Load())
	}
}

func TestCacheSuggestAndWordLists(t *testing.T) {
	var calls atomic.Int32
	c := NewCache()
	wl := &WordList{}
	wl.Add("gohunspell")
	wl.Forbid("colour")

	ms, err := c.Check("en", "1", testLoader(t, &calls), "gohunspell colour color", []*WordList{wl})
	if err != nil {
		t.Fatal(err)
	}
	if got := Words(ms); len(got) != 1 || got[0] != "colour" {
		t.Fatalf("misspellings = %v", got)
	}
	sugs, err := c.Suggest("en", "1", nil, "gohunspel", []*WordList{wl})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range sugs {
		if s == "colour" {
			t.Fatalf("forbidden word suggested: %v", sugs)
		}
		found = found || s == "gohunspell"
	}
	if !found {
		t.Fatalf("allowed word not suggested: %v", sugs)
	}
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1", calls.Load())
	}
}

func TestCacheErrors(t *testing.T) {
	c := NewCache()
	if _, err := c.Check("x", "1", nil, "word", nil); err == nil {
		t.Fatal("expected an error without a loader")
	}
	boom := errors.New("boom")
	_, err := c.Check("x", "1", func() ([]byte, []byte, error) { return nil, nil, boom }, "word", nil)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	_, err = c.Check("x", "1", func() ([]byte, []byte, error) {
		return []byte("SET UTF-8\n"), []byte("not a number\n"), nil
	}, "word", nil)
	if err == nil {
		t.Fatal("expected an error for an invalid dictionary")
	}
	if c.Len() != 0 {
		t.Fatalf("len = %d, want 0 after failed loads", c.Len())
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	var calls atomic.Int32
	load := testLoader(t, &calls)
	c := NewCache(WithMaxDictionaries(2))

	for _, key := range []string{"a", "b", "a", "c"} {
		if _, err := c.Check(key, "1", load, "ok", nil); err != nil {
			t.Fatal(err)
		}
	}
	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2", c.Len())
	}
	// "b" was used least recently and is gone, "a" is still loaded
	if _, err := c.Check("a", "1", load, "ok", nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("loader calls = %d, want 3", calls.Load())
	}
	if _, err := c.Check("b", "1", load, "ok", nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatalf("loader calls = %d, want 4", calls.Load())
	}

	c.Invalidate("a")
	c.Invalidate("unknown")
	if c.Len() != 1 {
		t.Fatalf("len = %d, want 1 after Invalidate", c.Len())
	}
	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("len = %d, want 0 after Clear", c.Len())
	}
}

func TestCacheCopies(t *testing.T) {
	var calls atomic.Int32
	load := testLoader(t, &calls)
	c := NewCache(WithCopies(2))

	// hold one copy, a second user gets its own copy, a third one waits
	var wg sync.WaitGroup
	hold := make(chan struct{})
	first := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = c.Use("en", "1", load, func(*Dictionary) error {
			close(first)
			<-hold
			return nil
		})
	}()
	<-first

	second := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = c.Use("en", "1", load, func(*Dictionary) error {
			close(second)
			<-hold
			return nil
		})
	}()
	<-second

	e := c.entries["en"].Value.(*cacheEntry)
	if e.created != 2 {
		t.Fatalf("copies = %d, want 2", e.created)
	}

	done := make(chan struct{})
	go func() {
		_ = c.Use("en", "1", load, func(*Dictionary) error { return nil })
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("third user did not wait for a copy")
	default:
	}
	close(hold)
	<-done
	wg.Wait()
	if e.created != 2 {
		t.Fatalf("copies = %d, want still 2", e.created)
	}
	if len(e.idle) != 2 {
		t.Fatalf("idle copies = %d, want 2", len(e.idle))
	}
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1 (copies reuse the loaded files)", calls.Load())
	}
}

func TestCacheStaleCopiesAreDropped(t *testing.T) {
	var calls atomic.Int32
	load := testLoader(t, &calls)
	c := NewCache()

	hold := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = c.Use("en", "1", load, func(*Dictionary) error {
			close(started)
			<-hold
			return nil
		})
		close(done)
	}()
	<-started
	old := c.entries["en"].Value.(*cacheEntry)

	// the dictionary changes while the copy is in use
	if _, err := c.Check("en", "2", load, "ok", nil); err != nil {
		t.Fatal(err)
	}
	close(hold)
	<-done
	if !old.stale || len(old.idle) != 0 {
		t.Fatalf("stale = %v, idle copies = %d; the copy must not return to the dropped entry", old.stale, len(old.idle))
	}
	if c.Len() != 1 || c.entries["en"].Value.(*cacheEntry).version != "2" {
		t.Fatal("new version is not the current entry")
	}
}

func TestCacheConcurrentUse(t *testing.T) {
	var calls atomic.Int32
	load := testLoader(t, &calls)
	c := NewCache(WithCopies(3))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key, version := "a", "1"
			if i%5 == 0 {
				key = "b"
			}
			if i%7 == 0 {
				version = "2"
			}
			if _, err := c.Check(key, version, load, "speling and mistaek", nil); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if c.Len() > 2 {
		t.Fatalf("len = %d, want at most 2", c.Len())
	}
	for _, el := range c.entries {
		e := el.Value.(*cacheEntry)
		if e.created > 3 || len(e.idle) != e.created {
			t.Fatalf("entry %s: created %d, idle %d", e.key, e.created, len(e.idle))
		}
	}
}

func TestWordsHelper(t *testing.T) {
	ms := []Misspelling{{Token: Token{Word: "b"}}, {Token: Token{Word: "a"}}, {Token: Token{Word: "b"}}}
	if got := Words(ms); len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("Words = %v", got)
	}
	if got := Words(nil); got != nil {
		t.Fatalf("Words(nil) = %v", got)
	}
}
