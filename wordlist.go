package gohunspell

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// LoadPersonal applies a personal dictionary in the format of the hunspell
// tool (-p): one word per line, "word/example" for a word that takes the
// affixes of a dictionary word, and "*word" to forbid a word. Like the
// hunspell tool, it applies the input conversion of the dictionary (ICONV)
// to each line and skips the lines that cannot be applied: words that
// cannot be written in the dictionary encoding are left out silently, and
// the "word/example" lines whose example is not a dictionary word with
// affixes are reported in the returned error after the rest of the file is
// applied.
func (d *Dictionary) LoadPersonal(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var errs []error
	for sc.Scan() {
		line := d.InputConversion(strings.TrimRight(sc.Text(), "\r"))
		if line == "" {
			continue
		}
		var err error
		if i := strings.IndexByte(line[1:], '/'); i >= 0 {
			word, example := line[:i+1], strings.TrimPrefix(line[i+2:], "/")
			err = d.AddWithAffix(word, example)
		} else if line[0] == '*' {
			err = d.Remove(line[1:])
		} else {
			err = d.Add(line)
		}
		if err != nil && err != errUnencodable {
			errs = append(errs, err)
		}
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// LoadPersonalFile applies the personal dictionary file at path.
func (d *Dictionary) LoadPersonalFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return d.LoadPersonal(f)
}

// WordList is an overlay of allowed and forbidden words that a Checker
// consults before the dictionary, without changing the dictionary.
type WordList struct {
	mu        sync.RWMutex
	allowed   map[string]struct{}
	forbidden map[string]struct{}
}

// NewWordList reads a word list: one word per line, lines starting with #
// are comments and lines starting with * forbid the word.
func NewWordList(r io.Reader) (*WordList, error) {
	wl := &WordList{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "*"):
			if w := strings.TrimSpace(line[1:]); w != "" {
				wl.Forbid(w)
			}
		default:
			wl.Add(line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return wl, nil
}

// NewWordListFromDic reads the words of a dictionary file (.dic) as a word
// list: the word count line is skipped, and the flags and morphological
// fields of the entries are left out. Use it for a supplement word list
// that has no affix file.
func NewWordListFromDic(r io.Reader) (*WordList, error) {
	wl := &WordList{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			line = strings.TrimPrefix(line, "\uFEFF")
			if _, err := strconv.Atoi(strings.Fields(line + " x")[0]); err == nil {
				continue
			}
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if w := dicWord(line); w != "" {
			wl.Add(w)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return wl, nil
}

// dicWord returns the word of a dictionary line: the part before the
// unescaped slash of the flags, a tab or the first morphological field.
func dicWord(line string) string {
	if i := strings.IndexByte(line, '\t'); i >= 0 {
		line = line[:i]
	}
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' && i+1 < len(line) && line[i+1] == '/' {
			b.WriteByte('/')
			i++
			continue
		}
		if c == '/' && i > 0 {
			break
		}
		if c == ' ' && isMorphField(line[i+1:]) {
			break
		}
		b.WriteByte(c)
	}
	return strings.TrimSpace(b.String())
}

// isMorphField reports whether s starts with a morphological field like
// "po:noun".
func isMorphField(s string) bool {
	s = strings.TrimLeft(s, " ")
	return len(s) >= 3 && s[2] == ':' && s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z'
}

// NewWordListFile reads the word list file at path.
func NewWordListFile(path string) (*WordList, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return NewWordList(f)
}

// Add allows a word.
func (wl *WordList) Add(word string) {
	wl.mu.Lock()
	defer wl.mu.Unlock()
	if wl.allowed == nil {
		wl.allowed = map[string]struct{}{}
	}
	wl.allowed[word] = struct{}{}
	delete(wl.forbidden, word)
}

// Forbid forbids a word.
func (wl *WordList) Forbid(word string) {
	wl.mu.Lock()
	defer wl.mu.Unlock()
	if wl.forbidden == nil {
		wl.forbidden = map[string]struct{}{}
	}
	wl.forbidden[word] = struct{}{}
	delete(wl.allowed, word)
}

// HasWord reports whether the word is allowed by the list.
func (wl *WordList) HasWord(word string) bool {
	if wl == nil {
		return false
	}
	wl.mu.RLock()
	defer wl.mu.RUnlock()
	_, ok := wl.allowed[word]
	return ok
}

// IsForbidden reports whether the word is forbidden by the list.
func (wl *WordList) IsForbidden(word string) bool {
	if wl == nil {
		return false
	}
	wl.mu.RLock()
	defer wl.mu.RUnlock()
	_, ok := wl.forbidden[word]
	return ok
}

// Words returns the allowed words of the list.
func (wl *WordList) Words() []string {
	wl.mu.RLock()
	defer wl.mu.RUnlock()
	res := make([]string, 0, len(wl.allowed))
	for w := range wl.allowed {
		res = append(res, w)
	}
	return res
}

// Checker combines a dictionary with word lists, for example a project
// vocabulary and the words of one document. The word lists can be added and
// removed at any time; the dictionary itself is not changed.
type Checker struct {
	dict  *Dictionary
	mu    sync.RWMutex
	lists []*WordList
}

// NewChecker returns a checker of the dictionary with the given word lists.
func NewChecker(dict *Dictionary, lists ...*WordList) *Checker {
	return &Checker{dict: dict, lists: lists}
}

// Dictionary returns the dictionary of the checker.
func (c *Checker) Dictionary() *Dictionary { return c.dict }

// AddWordList adds a word list.
func (c *Checker) AddWordList(wl *WordList) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lists = append(c.lists, wl)
}

// RemoveWordList removes a word list.
func (c *Checker) RemoveWordList(wl *WordList) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, l := range c.lists {
		if l == wl {
			c.lists = append(c.lists[:i], c.lists[i+1:]...)
			return
		}
	}
}

func (c *Checker) snapshot() []*WordList {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]*WordList(nil), c.lists...)
}

// listVerdict reports whether a word list decides the word: forbidden words
// lose, then allowed words win. The capitalized and upper case forms of an
// allowed lower case word are allowed, too.
func listVerdict(lists []*WordList, word string) (correct, decided bool) {
	for _, wl := range lists {
		if wl.IsForbidden(word) {
			return false, true
		}
	}
	for _, form := range caseForms(word) {
		for _, wl := range lists {
			if wl.HasWord(form) {
				return true, true
			}
		}
	}
	return false, false
}

// caseForms returns the word and the forms of it a word list entry may
// have: the word with the first letter lowered (Sentence start) and the
// lower case and capitalized forms of an all upper case word.
func caseForms(word string) []string {
	forms := []string{word}
	lower := strings.ToLower(word)
	if lower == word {
		return forms
	}
	if word == strings.ToUpper(word) {
		forms = append(forms, lower, capitalize(lower))
	} else if capitalize(lower) == word {
		forms = append(forms, lower)
	}
	return forms
}

func capitalize(s string) string {
	_, n := utf8.DecodeRuneInString(s)
	return strings.ToUpper(s[:n]) + s[n:]
}

// Spell reports whether a word is correct: forbidden by a word list, allowed
// by a word list, or else as the dictionary decides.
func (c *Checker) Spell(word string) bool {
	if ok, decided := listVerdict(c.snapshot(), word); decided {
		return ok
	}
	return c.dict.Spell(word)
}

// Suggest returns the dictionary's suggestions without the forbidden words,
// followed by the closest allowed words of the word lists.
func (c *Checker) Suggest(word string) []string {
	lists := c.snapshot()
	var res []string
	seen := map[string]bool{word: true}
	for _, s := range c.dict.Suggest(word) {
		if forbidden(lists, s) || seen[s] {
			continue
		}
		seen[s] = true
		res = append(res, s)
	}
	type cand struct {
		word string
		dist int
	}
	var extra []cand
	for _, wl := range lists {
		for _, w := range wl.Words() {
			if seen[w] || forbidden(lists, w) {
				continue
			}
			seen[w] = true
			if d := editDistance(strings.ToLower(word), strings.ToLower(w)); d <= maxListDistance(word) {
				extra = append(extra, cand{w, d})
			}
		}
	}
	sortStable(extra, func(a, b cand) bool { return a.dist < b.dist || (a.dist == b.dist && a.word < b.word) })
	for _, e := range extra {
		res = append(res, e.word)
	}
	return res
}

func forbidden(lists []*WordList, w string) bool {
	for _, wl := range lists {
		if wl.IsForbidden(w) {
			return true
		}
	}
	return false
}

func maxListDistance(word string) int {
	n := len([]rune(word))
	switch {
	case n <= 4:
		return 1
	case n <= 8:
		return 2
	}
	return 3
}

func sortStable[T any](s []T, less func(a, b T) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// editDistance is the optimal string alignment distance of a and b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(rb)]
}

// CheckText spell checks a text with the dictionary and the word lists.
func (c *Checker) CheckText(text string, opts ...TextOption) []Misspelling {
	var o textOptions
	for _, opt := range opts {
		opt(&o)
	}
	lists := c.snapshot()
	d := c.dict
	var res []Misspelling
	d.tokenize(text, o, func(t Token) {
		ok, decided := listVerdict(lists, t.Word)
		if !decided {
			d.mu.Lock()
			ok = d.checkToken(t.Word)
			d.mu.Unlock()
		}
		if ok {
			return
		}
		m := Misspelling{Token: t}
		if o.suggest {
			m.Suggestions = c.Suggest(t.Word)
		}
		res = append(res, m)
	})
	return res
}
