package gohunspell

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"strings"
	"sync"

	"github.com/documatrix/go-hunspell/internal/charset"
	"github.com/documatrix/go-hunspell/internal/core"
)

// Dictionary is a loaded Hunspell dictionary: an affix file and one or more
// dictionary files.
//
// All words go in and come out as UTF-8, whatever the encoding of the
// dictionary files. A Dictionary is safe for concurrent use; calls are
// serialized, so use several Dictionary values for parallel checking.
type Dictionary struct {
	mu   sync.Mutex
	h    *core.Hunspell
	enc  *charset.Encoding // nil for an encoding without a known mapping
	utf8 bool
	aff  core.Source
	key  string
	diag []string
}

// Result is the detailed outcome of Check.
type Result struct {
	// Correct reports whether the word is spelled correctly.
	Correct bool
	// Compound is set for a word accepted as a compound word, or split at a
	// BREAK point.
	Compound bool
	// Forbidden is set for a word the dictionary explicitly forbids.
	Forbidden bool
	// Warn is set for a correct word that carries the WARN flag (a rare or
	// potentially mistaken word).
	Warn bool
	// Root is the dictionary stem of an affixed or compound word, if any.
	Root string
}

// Option configures how a dictionary is loaded.
type Option func(*options)

type options struct {
	key      string
	noLimits bool
}

// WithoutTimeLimits turns off the time limits of Hunspell: by default a
// suggestion search stops after about 250 ms (100 ms per group of
// suggestions, 50 ms for compound word checks), so the suggestions of a
// slow or busy machine can be fewer. Without the limits the results are
// always the same, but a pathological word can take much longer.
func WithoutTimeLimits() Option {
	return func(o *options) { o.noLimits = true }
}

// WithKey sets the password of encrypted .hz dictionary files.
func WithKey(key string) Option {
	return func(o *options) { o.key = key }
}

// LoadError reports a dictionary that could not be loaded.
type LoadError struct {
	Messages []string
}

func (e *LoadError) Error() string {
	return "gohunspell: " + strings.Join(e.Messages, "; ")
}

func applyOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func fileExists(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	_, err := os.Stat(path + ".hz")
	return err == nil
}

// Open loads the dictionary from an affix file and a dictionary file. Files
// compressed with hzip (name.dic.hz) are found and read, too.
func Open(affPath, dicPath string, opts ...Option) (*Dictionary, error) {
	for _, p := range []string{affPath, dicPath} {
		if p == "" || !fileExists(p) {
			return nil, fmt.Errorf("gohunspell: %s: %w", p, os.ErrNotExist)
		}
	}
	return newDictionary(core.Source{Path: affPath}, core.Source{Path: dicPath}, applyOptions(opts))
}

// OpenReader loads the dictionary from the contents of an affix file and a
// dictionary file (plain or hzip compressed).
func OpenReader(aff, dic io.Reader, opts ...Option) (*Dictionary, error) {
	affData, err := io.ReadAll(aff)
	if err != nil {
		return nil, err
	}
	dicData, err := io.ReadAll(dic)
	if err != nil {
		return nil, err
	}
	return newDictionary(core.Source{Data: nonNil(affData)}, core.Source{Data: nonNil(dicData)}, applyOptions(opts))
}

// OpenFS loads the dictionary from an affix file and a dictionary file of
// a file system, such as an embed.FS. Files compressed with hzip
// (name.dic.hz) are found and read, too.
func OpenFS(fsys fs.FS, affPath, dicPath string, opts ...Option) (*Dictionary, error) {
	var data [2][]byte
	for i, p := range []string{affPath, dicPath} {
		b, err := fs.ReadFile(fsys, p)
		if errors.Is(err, fs.ErrNotExist) {
			b, err = fs.ReadFile(fsys, p+".hz")
		}
		if err != nil {
			return nil, fmt.Errorf("gohunspell: %w", err)
		}
		data[i] = b
	}
	return OpenBytes(data[0], data[1], opts...)
}

// OpenBytes loads the dictionary from the contents of an affix file and a
// dictionary file.
func OpenBytes(aff, dic []byte, opts ...Option) (*Dictionary, error) {
	return OpenReader(bytes.NewReader(aff), bytes.NewReader(dic), opts...)
}

func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

func newDictionary(aff, dic core.Source, o options) (*Dictionary, error) {
	h := core.New(aff, dic, o.key)
	if o.noLimits {
		h.SetTimeLimits(0, 0, 0)
	}
	errs := messages(h.Errors())
	d := &Dictionary{h: h, aff: aff, key: o.key, diag: append(errs, messages(h.Warnings())...)}
	if err := d.loadError(errs); err != nil {
		return nil, err
	}
	d.utf8 = h.Encoding() == "UTF-8"
	d.enc = charset.Lookup(h.Encoding())
	return d, nil
}

// messages returns the messages of Hunspell without their line ends.
func messages(list []string) []string {
	res := make([]string, len(list))
	for i, m := range list {
		res[i] = strings.TrimRight(m, "\r\n")
	}
	return res
}

// loadError turns the messages of a failed dictionary file into an error.
func (d *Dictionary) loadError(msgs []string) error {
	for _, m := range msgs {
		if strings.HasPrefix(m, "Hash Manager Error") {
			return &LoadError{Messages: msgs}
		}
	}
	return nil
}

// Diagnostics returns the messages Hunspell reported while loading the
// dictionary files: first the error messages of the C++ library (such as a
// failure to load the affix file), then the warnings its debug builds print
// about the affix and dictionary files (such as "error: line 3: missing
// data"). The messages have no line ends.
func (d *Dictionary) Diagnostics() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.diag...)
}

// AddDictionary loads an extra dictionary file that uses the affix rules of
// this dictionary (for example a medical or technical word list with flags).
func (d *Dictionary) AddDictionary(dicPath string) error {
	if !fileExists(dicPath) {
		return fmt.Errorf("gohunspell: %s: %w", dicPath, os.ErrNotExist)
	}
	return d.addDic(core.Source{Path: dicPath})
}

// AddDictionaryReader loads an extra dictionary file from r.
func (d *Dictionary) AddDictionaryReader(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return d.addDic(core.Source{Data: nonNil(data)})
}

func (d *Dictionary) addDic(src core.Source) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	n, nw := len(d.h.Errors()), len(d.h.Warnings())
	d.h.AddDic(src, d.key)
	msgs := messages(d.h.Errors()[n:])
	d.diag = append(d.diag, msgs...)
	d.diag = append(d.diag, messages(d.h.Warnings()[nw:])...)
	return d.loadError(msgs)
}

// in converts an UTF-8 string to the dictionary encoding.
func (d *Dictionary) in(s string) (string, bool) {
	if d.utf8 || d.enc == nil {
		return s, true
	}
	return d.enc.FromUTF8(s)
}

// out converts a string of the dictionary to UTF-8.
func (d *Dictionary) out(s string) string {
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	if d.utf8 || d.enc == nil {
		return s
	}
	return d.enc.ToUTF8Lossy(s)
}

func (d *Dictionary) outAll(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	res := make([]string, len(list))
	for i, s := range list {
		res[i] = d.out(s)
	}
	return res
}

// Spell reports whether word is spelled correctly.
func (d *Dictionary) Spell(word string) bool {
	return d.Check(word).Correct
}

// Check spell checks word and reports the details of the decision.
func (d *Dictionary) Check(word string) Result {
	w, ok := d.in(word)
	if !ok {
		return Result{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.check(w)
}

func (d *Dictionary) check(w string) Result {
	var info int
	var root string
	ok := d.h.Spell(w, &info, &root)
	return Result{
		Correct:   ok,
		Compound:  info&core.SpellCompound != 0,
		Forbidden: info&core.SpellForbidden != 0,
		Warn:      info&core.SpellWarn != 0,
		Root:      d.out(root),
	}
}

// Suggest returns spelling suggestions for word, best first. It returns at
// most 15 suggestions, like Hunspell.
func (d *Dictionary) Suggest(word string) []string {
	w, ok := d.in(word)
	if !ok {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outAll(d.h.Suggest(w))
}

// Analyze returns the morphological analyses of word, one per line of the
// form " st:stem po:pos is:suffix ...", using the fields of the dictionary.
func (d *Dictionary) Analyze(word string) []string {
	w, ok := d.in(word)
	if !ok {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outAll(d.h.Analyze(w))
}

// Stem returns the stems of word.
func (d *Dictionary) Stem(word string) []string {
	w, ok := d.in(word)
	if !ok {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outAll(d.h.Stem(w))
}

// StemAnalyses returns the stems of the given morphological analyses, as
// returned by Analyze.
func (d *Dictionary) StemAnalyses(analyses []string) []string {
	in, ok := d.inAll(analyses)
	if !ok {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outAll(d.h.StemMorph(in))
}

func (d *Dictionary) inAll(list []string) ([]string, bool) {
	res := make([]string, len(list))
	for i, s := range list {
		v, ok := d.in(s)
		if !ok {
			return nil, false
		}
		res[i] = v
	}
	return res, true
}

// Generate returns the forms of word that are inflected like example, for
// example Generate("drink", "eats") returns "drinks".
func (d *Dictionary) Generate(word, example string) []string {
	w, ok1 := d.in(word)
	e, ok2 := d.in(example)
	if !ok1 || !ok2 {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outAll(d.h.Generate(w, e))
}

// GenerateMorph returns the forms of word that have the given morphological
// descriptions, for example GenerateMorph("drink", []string{"is:past_1"}).
func (d *Dictionary) GenerateMorph(word string, morph []string) []string {
	w, ok := d.in(word)
	m, ok2 := d.inAll(morph)
	if !ok || !ok2 {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outAll(d.h.GenerateMorph(w, m))
}

// SuffixSuggest returns the forms the suffix rules of a dictionary word make.
func (d *Dictionary) SuffixSuggest(root string) []string {
	w, ok := d.in(root)
	if !ok {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outAll(d.h.SuffixSuggest(w))
}

// Expand returns a dictionary word and its forms with one prefix, one
// suffix or both (like the unmunch tool), for each homonym of the word.
// Forms that need further affixes (NEEDAFFIX, CIRCUMFIX, ONLYINCOMPOUND
// continuation flags) are left out.
func (d *Dictionary) Expand(word string) []string {
	w, ok := d.in(word)
	if !ok {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outAll(d.h.Expand(w))
}

var errUnencodable = errors.New("gohunspell: the word cannot be written in the dictionary encoding")

// Add adds a word to the dictionary for the lifetime of this Dictionary.
// Words added with a capital or in upper case are handled like the
// dictionary's own: an all-uppercase form is accepted, too.
func (d *Dictionary) Add(word string) error {
	w, ok := d.in(word)
	if !ok {
		return errUnencodable
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.h.Add(w)
	return nil
}

// AddWithFlags adds a word with affix flags (in the FLAG syntax of the
// dictionary) and an optional morphological description.
func (d *Dictionary) AddWithFlags(word, flags, morph string) error {
	w, ok1 := d.in(word)
	f, ok2 := d.in(flags)
	m, ok3 := d.in(morph)
	if !ok1 || !ok2 || !ok3 {
		return errUnencodable
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.h.AddWithFlags(w, f, m)
	return nil
}

// AddWithAffix adds a word that takes the affixes of example, a word of the
// dictionary: AddWithAffix("tweet", "beat") also accepts "tweets".
func (d *Dictionary) AddWithAffix(word, example string) error {
	w, ok1 := d.in(word)
	e, ok2 := d.in(example)
	if !ok1 || !ok2 {
		return errUnencodable
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.h.AddWithAffix(w, e) != 0 {
		return fmt.Errorf("gohunspell: %q is not a dictionary word with affixes", example)
	}
	return nil
}

// Remove forbids a word of the dictionary for the lifetime of this
// Dictionary.
func (d *Dictionary) Remove(word string) error {
	w, ok := d.in(word)
	if !ok {
		return errUnencodable
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.h.Remove(w)
	return nil
}

// HasWord reports whether word is an entry of the dictionary files (or was
// added with Add) exactly as written: no affixes, compounds or case
// variants. Forbidden and removed words are not entries.
func (d *Dictionary) HasWord(word string) bool {
	w, ok := d.in(word)
	if !ok {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.h.HasWord(w)
}

// Words returns the entries of the dictionary (see HasWord), each once. The
// sequence is a snapshot taken when the iteration starts.
func (d *Dictionary) Words() iter.Seq[string] {
	return func(yield func(string) bool) {
		d.mu.Lock()
		var words []string
		d.h.ForEachWord(func(w string) bool {
			words = append(words, d.out(w))
			return true
		})
		d.mu.Unlock()
		for _, w := range words {
			if !yield(w) {
				return
			}
		}
	}
}

// WordCount returns the number of entries of the dictionary (see HasWord).
func (d *Dictionary) WordCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	d.h.ForEachWord(func(string) bool { n++; return true })
	return n
}

// InputConversion applies the input conversion table of the dictionary
// (the ICONV option), which Spell, Suggest and the other methods apply
// themselves.
func (d *Dictionary) InputConversion(word string) string {
	w, ok := d.in(word)
	if !ok {
		return word
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, changed := d.h.InputConv(w); changed {
		return d.out(c)
	}
	return word
}

// Trace spell checks word and reports each decision Hunspell takes to fn:
// the lookups, the affix rules tried, the compound splits and the flags
// tested, as the --trace option of the hunspell tool prints them. depth is
// the nesting level of the record.
func (d *Dictionary) Trace(word string, fn func(depth int, line string)) bool {
	w, ok := d.in(word)
	if !ok {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.h.SetTrace(func(depth int, line string) { fn(depth, d.out(line)) })
	defer d.h.SetTrace(nil)
	return d.h.Spell(w, nil, nil)
}

// Encoding returns the character encoding of the dictionary files (the SET
// option of the affix file).
func (d *Dictionary) Encoding() string { return d.h.Encoding() }

// Lang returns the language of the dictionary (the LANG option), if set.
func (d *Dictionary) Lang() string { return d.h.Lang() }

// Version returns the VERSION line of the affix file, if any.
func (d *Dictionary) Version() string { return d.h.Version() }

// WordChars returns the characters that belong to words besides the
// letters (the WORDCHARS option as written in the affix file; for 8-bit
// dictionaries the letters of the encoding are included).
func (d *Dictionary) WordChars() string {
	return d.out(d.h.WordChars())
}
