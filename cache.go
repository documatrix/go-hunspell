package gohunspell

import (
	"container/list"
	"errors"
	"fmt"
	"sync"
)

// Loader returns the affix and dictionary file of a dictionary. The Cache
// calls it when a dictionary is used for the first time or has changed.
type Loader func() (aff, dic []byte, err error)

// CacheOption configures a Cache.
type CacheOption func(*Cache)

// WithMaxDictionaries bounds the number of dictionaries a Cache keeps
// loaded; the least recently used one is dropped when another one is
// loaded. The default is 16, a value below 1 keeps every dictionary.
func WithMaxDictionaries(n int) CacheOption {
	return func(c *Cache) { c.maxDicts = n }
}

// WithCopies sets how many loaded copies of one dictionary a Cache may hold.
// A Dictionary serializes its calls, so copies let goroutines check with the
// same dictionary in parallel. Copies are only loaded while all existing
// ones are busy; the default is 4.
func WithCopies(n int) CacheOption {
	return func(c *Cache) { c.copies = n }
}

// WithDictionaryOptions passes Options to every dictionary the Cache loads.
func WithDictionaryOptions(opts ...Option) CacheOption {
	return func(c *Cache) { c.dictOpts = opts }
}

// Cache keeps loaded dictionaries for repeated use. A dictionary is
// identified by a key of the caller and a version; it is loaded again when
// it is used with another version, so the caller does not have to track
// changes itself. Memory is bounded by WithMaxDictionaries and WithCopies:
// at most max dictionaries × copies are loaded at any time, stale copies
// are dropped as soon as the goroutines using them are done. A Cache is
// safe for concurrent use.
type Cache struct {
	maxDicts int
	copies   int
	dictOpts []Option

	mu      sync.Mutex
	entries map[string]*list.Element // *cacheEntry
	lru     *list.List               // most recently used in front
}

type cacheEntry struct {
	key     string
	version string
	aff     []byte
	dic     []byte
	stale   bool             // dropped from the cache, copies are not returned
	idle    chan *Dictionary // copies not in use, capacity copies
	created int              // copies loaded so far
	loading sync.Mutex       // serializes loading the copies of one entry
}

// NewCache creates an empty Cache.
func NewCache(opts ...CacheOption) *Cache {
	c := &Cache{
		maxDicts: 16,
		copies:   4,
		entries:  map[string]*list.Element{},
		lru:      list.New(),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.copies < 1 {
		c.copies = 1
	}
	return c
}

// Len returns the number of loaded dictionaries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

// Invalidate drops the dictionary with the key. Goroutines still using one
// of its copies finish with it, the copy is dropped afterwards.
func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.remove(el)
	}
}

// Clear drops every dictionary.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.lru.Len() > 0 {
		c.remove(c.lru.Back())
	}
}

// Use runs fn with the dictionary identified by key and version, loading
// it with load if it is not loaded in that version yet. The dictionary must
// not be used after fn returns.
func (c *Cache) Use(key, version string, load Loader, fn func(d *Dictionary) error) error {
	e, err := c.entry(key, version, load)
	if err != nil {
		return err
	}
	d, err := c.acquire(e)
	if err != nil {
		return err
	}
	defer c.release(e, d)
	return fn(d)
}

// Check spell checks text with the dictionary and the word lists, see
// Checker.CheckText.
func (c *Cache) Check(
	key, version string,
	load Loader,
	text string,
	lists []*WordList,
	opts ...TextOption,
) (ms []Misspelling, err error) {
	err = c.Use(key, version, load, func(d *Dictionary) error {
		ms = NewChecker(d, lists...).CheckText(text, opts...)
		return nil
	})
	return ms, err
}

// Suggest suggests corrections for word with the dictionary and the word
// lists, see Checker.Suggest.
func (c *Cache) Suggest(
	key, version string,
	load Loader,
	word string,
	lists []*WordList,
) (sugs []string, err error) {
	err = c.Use(key, version, load, func(d *Dictionary) error {
		sugs = NewChecker(d, lists...).Suggest(word)
		return nil
	})
	return sugs, err
}

// entry returns the current entry of key, loading the first copy of the
// dictionary if the key is unknown or has another version.
func (c *Cache) entry(key, version string, load Loader) (*cacheEntry, error) {
	c.mu.Lock()
	if el, ok := c.entries[key]; ok {
		e := el.Value.(*cacheEntry)
		if e.version == version {
			c.lru.MoveToFront(el)
			c.mu.Unlock()
			return e, nil
		}
		c.remove(el)
	}
	c.mu.Unlock()

	if load == nil {
		return nil, errors.New("gohunspell: no loader for dictionary " + key)
	}
	aff, dic, err := load()
	if err != nil {
		return nil, fmt.Errorf("gohunspell: loading dictionary %s: %w", key, err)
	}
	e := &cacheEntry{
		key:     key,
		version: version,
		aff:     aff,
		dic:     dic,
		idle:    make(chan *Dictionary, c.copies),
	}
	d, err := c.load(e)
	if err != nil {
		return nil, err
	}
	e.idle <- d

	c.mu.Lock()
	defer c.mu.Unlock()
	// another goroutine may have loaded the same version in the meantime,
	// the entry which is already in the cache wins
	if el, ok := c.entries[key]; ok {
		if cur := el.Value.(*cacheEntry); cur.version == version {
			c.lru.MoveToFront(el)
			return cur, nil
		}
		c.remove(el)
	}
	c.entries[key] = c.lru.PushFront(e)
	for c.maxDicts > 0 && c.lru.Len() > c.maxDicts {
		c.remove(c.lru.Back())
	}
	return e, nil
}

// load loads one more copy of the dictionary of e.
func (c *Cache) load(e *cacheEntry) (*Dictionary, error) {
	d, err := OpenBytes(e.aff, e.dic, c.dictOpts...)
	if err != nil {
		return nil, fmt.Errorf("gohunspell: dictionary %s: %w", e.key, err)
	}
	e.created++
	return d, nil
}

// acquire takes an idle copy of e, loads another one if all copies are busy
// and fewer than the allowed number exist, and waits for a copy otherwise.
func (c *Cache) acquire(e *cacheEntry) (*Dictionary, error) {
	select {
	case d := <-e.idle:
		return d, nil
	default:
	}
	e.loading.Lock()
	defer e.loading.Unlock()
	select {
	case d := <-e.idle:
		return d, nil
	default:
	}
	if e.created < c.copies {
		return c.load(e)
	}
	return <-e.idle, nil
}

// release returns a copy to e, or drops it if e was removed meanwhile.
func (c *Cache) release(e *cacheEntry, d *Dictionary) {
	c.mu.Lock()
	stale := e.stale
	c.mu.Unlock()
	if stale {
		return
	}
	select {
	case e.idle <- d:
	default:
		// cannot happen, there are never more copies than the channel holds
	}
}

// remove drops el from the cache, c.mu must be held.
func (c *Cache) remove(el *list.Element) {
	e := el.Value.(*cacheEntry)
	e.stale = true
	delete(c.entries, e.key)
	c.lru.Remove(el)
	// idle copies are dropped right away, busy ones when they are released
	for {
		select {
		case <-e.idle:
		default:
			return
		}
	}
}

// Words returns the distinct misspelled words in the order of their first
// occurrence.
func Words(ms []Misspelling) []string {
	seen := map[string]bool{}
	var words []string
	for _, m := range ms {
		if !seen[m.Word] {
			seen[m.Word] = true
			words = append(words, m.Word)
		}
	}
	return words
}
