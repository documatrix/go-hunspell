package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The tests of this file cover the parts of the package that have no
// counterpart in the C++ library: the word iteration, the expansion of a
// word and the time limits of each instance.

func extraDict(t *testing.T, name string) *Hunspell {
	t.Helper()
	base := filepath.Join("..", "..", "testdata", "golden", "extra", name)
	h := New(Source{Path: base + ".aff"}, Source{Path: base + ".dic"}, "")
	h.SetTimeLimits(0, 0, 0)
	return h
}

func TestForEachWordDics(t *testing.T) {
	h := extraDict(t, "capmisc")
	h.AddDic(Source{Path: filepath.Join("..", "..", "testdata", "golden", "extra", "capmisc2.dic")}, "")
	var words []string
	h.ForEachWord(func(w string) bool {
		words = append(words, w)
		return true
	})
	slices.Sort(words)
	if want := []string{"ABC.", "extra", "foo", "kis", "sant'Elia"}; !slices.Equal(words, want) {
		t.Errorf("ForEachWord: got %q, want %q", words, want)
	}
	n := 0
	h.ForEachWord(func(string) bool {
		n++
		return false
	})
	if n != 1 {
		t.Errorf("ForEachWord did not stop: %d calls", n)
	}
}

func TestExpandHidden(t *testing.T) {
	h := extraDict(t, "misc")
	// the dictionary word XFOO/BC has a hidden homonym Xfoo for the
	// capitalized forms, which is not a word of its own
	if got := h.Expand("Xfoo"); got != nil {
		t.Errorf("Expand of a hidden homonym: %q", got)
	}
	if got := h.Expand("XFOO"); !slices.Equal(got, []string{"XFOO"}) {
		t.Errorf("Expand(XFOO) = %q", got)
	}
	h = extraDict(t, "cpdmore")
	if got := h.Expand("foobar"); got != nil {
		t.Errorf("Expand of a forbidden word: %q", got)
	}
}

// TestTimeLimitsExpire runs the searches with time limits that run out
// at once or part of the way through them; the results then depend on where
// the clock runs out, so only the return of the calls is checked.
func TestTimeLimitsExpire(t *testing.T) {
	var limits []time.Duration
	for d := time.Nanosecond; d < 2*time.Millisecond; d *= 3 {
		limits = append(limits, d)
	}
	words := []string{"foobarbar", "Foobarbaz", "FOOBARBAZ", "fooBarbaz", "FooBarbaz", "foo.", "FOO.", "Foo.",
		"almaházfa", "Almaház", "ALMAHÁZ", "almaHáz", "bar-foo", "xyzzy", "Xyzzy", "XYZZY", "fooqqbarbar", "aabbcc"}
	for _, name := range []string{"cpdmore", "hu", "misc", "phonet", "cpdsimple"} {
		h := extraDict(t, name)
		for _, d := range limits {
			for _, l := range [][3]time.Duration{{d, time.Hour, time.Hour}, {time.Hour, d, time.Hour}, {time.Hour, time.Hour, d}} {
				h.SetTimeLimits(l[0], l[1], l[2])
				for _, w := range words {
					h.Spell(w, nil, nil)
					h.Suggest(w)
					an := h.Analyze(w)
					h.Generate(w, "foos")
					h.GenerateMorph(w, an)
					h.StemMorph(an)
				}
			}
		}
	}
}

// TestHunzipRoundTrip reads files made by the hzip tool of Hunspell and
// compares the lines with the compressed files. The C++ reader is no
// reference here: it cannot read files that decompress to less than 64 KB
// and loses the end of the others.
func TestHunzipRoundTrip(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "golden", "extra", "hz")
	for _, c := range []struct{ hz, orig, key string }{
		{"big.dic.hz", "big.dic.gz", ""},
		{"small.dic.hz", "small.dic", "secret"},
	} {
		data := readFile(t, filepath.Join(dir, c.hz))
		orig := readFile(t, filepath.Join(dir, c.orig))
		if strings.HasSuffix(c.orig, ".gz") {
			orig = readGzip(t, filepath.Join(dir, c.orig))
		}
		var errs []string
		r := Source{Data: data}.open(c.key, &errs)
		var lines []string
		for {
			l, ok := r.getline()
			if !ok {
				break
			}
			lines = append(lines, l)
		}
		// the lines keep their newline, like those of Hunzip::getline
		want := strings.SplitAfter(strings.TrimSuffix(string(orig), "\n"), "\n")
		want[len(want)-1] += "\n"
		if len(errs) != 0 || len(lines) != len(want) {
			t.Errorf("%s: %d lines, want %d, errors %q", c.hz, len(lines), len(want), errs)
		}
		for i := range min(len(lines), len(want)) {
			if lines[i] != want[i] {
				t.Errorf("%s: line %d is %q, want %q", c.hz, i+1, lines[i], want[i])
				break
			}
		}
		if r.getlinenum() != len(want) {
			t.Errorf("%s: line number %d, want %d", c.hz, r.getlinenum(), len(want))
		}
	}
	// a wrong password
	var errs []string
	r := Source{Data: readFile(t, filepath.Join(dir, "small.dic.hz"))}.open("wrong", &errs)
	if _, ok := r.getline(); ok || len(errs) != 1 {
		t.Errorf("wrong password: errors %q", errs)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestAddTooLong adds a word over the length limit of the dictionary
// entries (64 KB, as in HashMgr::add_word).
func TestAddTooLong(t *testing.T) {
	h := extraDict(t, "misc")
	w := strings.Repeat("x", 65536)
	if h.Add(w) != 1 || h.AddWithFlags(w, "A", "") != 1 || h.AddWithAffix(w, "kis") != 1 || h.Spell(w[:100], nil, nil) {
		t.Error("a word over the length limit was added")
	}
}
