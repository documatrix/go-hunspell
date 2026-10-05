package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fuzz tests check that no affix file, dictionary file or word makes
// the engine panic. Their seeds are the upstream test dictionaries; run
// them longer with go test -fuzz FuzzDictionary ./internal/core.

func fuzzSeeds(f *testing.F, add func(aff, dic []byte, word string)) {
	affs, _ := filepath.Glob("../../testdata/hunspell/*.aff")
	for _, a := range affs {
		base := strings.TrimSuffix(a, ".aff")
		if filepath.Base(base) == "timelimit" {
			continue
		}
		aff, err1 := os.ReadFile(a)
		dic, err2 := os.ReadFile(base + ".dic")
		if err1 != nil || err2 != nil || len(aff)+len(dic) > 1<<16 {
			continue
		}
		word := "word"
		if good, err := os.ReadFile(base + ".good"); err == nil {
			if w := strings.Fields(string(good)); len(w) > 0 {
				word = w[0]
			}
		}
		add(aff, dic, word)
	}
}

func FuzzDictionary(f *testing.F) {
	fuzzSeeds(f, func(aff, dic []byte, word string) { f.Add(aff, dic, word) })
	f.Fuzz(func(t *testing.T, aff, dic []byte, word string) {
		h := New(Source{Data: append([]byte{}, aff...)}, Source{Data: append([]byte{}, dic...)}, "")
		exerciseWord(h, word)
	})
}

func FuzzWord(f *testing.F) {
	type dict struct {
		aff, dic []byte
	}
	var dicts []dict
	fuzzSeeds(f, func(aff, dic []byte, word string) {
		dicts = append(dicts, dict{aff, dic})
		f.Add(uint16(len(dicts)-1), word)
	})
	cache := map[uint16]*Hunspell{}
	f.Fuzz(func(t *testing.T, n uint16, word string) {
		i := n % uint16(len(dicts))
		h := cache[i]
		if h == nil {
			h = New(Source{Data: dicts[i].aff}, Source{Data: dicts[i].dic}, "")
			cache[i] = h
		}
		exerciseWord(h, word)
	})
}

func exerciseWord(h *Hunspell, word string) {
	if len(word) > 300 {
		word = word[:300]
	}
	var info int
	var root string
	h.Spell(word, &info, &root)
	h.Suggest(word)
	an := h.Analyze(word)
	h.Stem(word)
	h.StemMorph(an)
	h.SuffixSuggest(word)
	h.Generate(word, word)
	h.GenerateMorph(word, an)
	h.Expand(word)
	h.HasWord(word)
	h.Add(word)
	h.AddWithAffix(word, word)
	h.Remove(word)
}
