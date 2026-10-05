package core

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoldenAPI compares the API results for the words of
// testdata/golden/api with the output of the Hunspell C++ library (see
// internal/gen/golden): spell with its info flags and root, suggest,
// analyze, stem, stem of the analyses, suffix_suggest and generate.
func TestGoldenAPI(t *testing.T) {
	files, err := filepath.Glob("../../testdata/golden/api/*.api")
	if err != nil || len(files) == 0 {
		t.Fatal("no golden files", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".api")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			header, body, _ := bytes.Cut(want, []byte("\n"))
			example := strings.TrimPrefix(string(header), "# example: ")
			var words []string
			for _, l := range strings.Split(string(body), "\n") {
				if w, ok := strings.CutPrefix(l, "> "); ok {
					words = append(words, w)
				}
			}
			base := filepath.Join("../../testdata/hunspell", name)
			h := New(Source{Path: base + ".aff"}, Source{Path: base + ".dic"}, "")
			h.SetTimeLimits(0, 0, 0)
			got := apiDump(h, words, example)
			if dir := os.Getenv("GOLDEN_DIFF_DIR"); dir != "" && got != string(body) {
				os.WriteFile(filepath.Join(dir, name+".got"), []byte(got), 0o644)
			}
			compareLines(t, string(body), got)
		})
	}
}

func apiDump(h *Hunspell, words []string, example string) string {
	var b strings.Builder
	exmorph := h.Analyze(example)
	for _, w := range words {
		dumpWord(&b, h, w, example, exmorph)
	}
	return b.String()
}

// dumpWord writes the API results for a word like apidump.cxx.
func dumpWord(b *strings.Builder, h *Hunspell, w, example string, exmorph []string) {
	fmt.Fprintf(b, "> %s\n", w)
	var info int
	var root string
	ok := h.Spell(w, &info, &root)
	fmt.Fprintf(b, "  spell %d %d %s\n", b2i(ok), info, root)
	for _, s := range h.Suggest(w) {
		fmt.Fprintf(b, "  sug %s\n", s)
	}
	an := h.Analyze(w)
	for _, s := range an {
		fmt.Fprintf(b, "  ana %s\n", s)
	}
	for _, s := range h.Stem(w) {
		fmt.Fprintf(b, "  stem %s\n", s)
	}
	for _, s := range h.StemMorph(an) {
		fmt.Fprintf(b, "  stemmorph %s\n", s)
	}
	for _, s := range h.SuffixSuggest(w) {
		fmt.Fprintf(b, "  sfx %s\n", s)
	}
	if example != "" {
		for _, s := range h.Generate(w, example) {
			fmt.Fprintf(b, "  gen %s\n", s)
		}
		for _, s := range h.GenerateMorph(w, exmorph) {
			fmt.Fprintf(b, "  genmorph %s\n", s)
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// compareLines reports the first lines where got differs from want.
func compareLines(t *testing.T, want, got string) {
	t.Helper()
	if want == got {
		return
	}
	ws := bufio.NewScanner(strings.NewReader(want))
	gs := bufio.NewScanner(strings.NewReader(got))
	ctx := ""
	for n, shown := 1, 0; shown < 5; n++ {
		wok, gok := ws.Scan(), gs.Scan()
		if !wok && !gok {
			break
		}
		if strings.HasPrefix(ws.Text(), "> ") {
			ctx = ws.Text()
		}
		if ws.Text() != gs.Text() || wok != gok {
			t.Errorf("line %d (%s): want %q, got %q", n, ctx, ws.Text(), gs.Text())
			shown++
		}
	}
}
