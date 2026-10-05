package gohunspell

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The benchmarks use en_US and the word lists of testdata/bench: 1000
// dictionary words and 1000 misspellings of dictionary words. Set
// GOHUNSPELL_BENCH_DICT (a path without extension, like
// /usr/share/hunspell/de_DE) and GOHUNSPELL_BENCH_WORDS (a word list) to
// benchmark another dictionary.

func benchBase() string {
	if base := os.Getenv("GOHUNSPELL_BENCH_DICT"); base != "" {
		return base
	}
	return filepath.Join("testdata", "en_US", "en_US")
}

func benchDict(b *testing.B, opts ...Option) *Dictionary {
	b.Helper()
	base := benchBase()
	d, err := Open(base+".aff", base+".dic", opts...)
	if err != nil {
		b.Fatal(err)
	}
	return d
}

func benchWords(b *testing.B, list string) []string {
	b.Helper()
	path := os.Getenv("GOHUNSPELL_BENCH_WORDS")
	if path == "" {
		path = filepath.Join("testdata", "bench", "en_US."+list)
	}
	f, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	var words []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			words = append(words, w)
		}
	}
	return words
}

func BenchmarkLoad(b *testing.B) {
	base := benchBase()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Open(base+".aff", base+".dic"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSpellCorrect(b *testing.B) {
	d, words := benchDict(b), benchWords(b, "good")
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		d.Spell(words[i%len(words)])
		i++
	}
}

func BenchmarkSpellMisspelled(b *testing.B) {
	d, words := benchDict(b), benchWords(b, "wrong")
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		d.Spell(words[i%len(words)])
		i++
	}
}

func BenchmarkSpellParallel(b *testing.B) {
	words := benchWords(b, "good")
	// one Dictionary per goroutine: a Dictionary serializes its calls
	pool := make(chan *Dictionary, runtime.GOMAXPROCS(0))
	for range cap(pool) {
		pool <- benchDict(b)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		d := <-pool
		defer func() { pool <- d }()
		i := 0
		for pb.Next() {
			d.Spell(words[i%len(words)])
			i++
		}
	})
}

func BenchmarkSuggest(b *testing.B) {
	d, words := benchDict(b, WithoutTimeLimits()), benchWords(b, "wrong")
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		d.Suggest(words[i%len(words)])
		i++
	}
}

func BenchmarkAnalyze(b *testing.B) {
	d, words := benchDict(b), benchWords(b, "good")
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		d.Analyze(words[i%len(words)])
		i++
	}
}

func BenchmarkStem(b *testing.B) {
	d, words := benchDict(b), benchWords(b, "good")
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		d.Stem(words[i%len(words)])
		i++
	}
}

func BenchmarkCheckText(b *testing.B) {
	d := benchDict(b)
	text := strings.Join(benchWords(b, "good")[:200], " ") + "\n" + strings.Join(benchWords(b, "wrong")[:50], " ")
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		d.CheckText(text)
	}
}
