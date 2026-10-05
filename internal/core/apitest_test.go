package core

import (
	"bufio"
	"fmt"
	"os"
	"testing"
)

// TestAPIDump prints the API results for the lines of APITEST_WORDS, to
// compare with the C++ library.
func TestAPIDump(t *testing.T) {
	base := os.Getenv("APITEST_DICT")
	if base == "" {
		t.Skip()
	}
	h := New(Source{Path: base + ".aff"}, Source{Path: base + ".dic"}, "")
	f, _ := os.Open(os.Getenv("APITEST_WORDS"))
	out, _ := os.Create(os.Getenv("APITEST_OUT"))
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintf(out, "> %s\n", line)
		for _, s := range h.Suggest(line) {
			fmt.Fprintf(out, "  sug %s\n", s)
		}
		for _, s := range h.Analyze(line) {
			fmt.Fprintf(out, "  ana %s\n", s)
		}
		for _, s := range h.Stem(line) {
			fmt.Fprintf(out, "  stem %s\n", s)
		}
		for _, s := range h.SuffixSuggest(line) {
			fmt.Fprintf(out, "  sfx %s\n", s)
		}
		for _, s := range h.Generate(line, "drank") {
			fmt.Fprintf(out, "  gen %s\n", s)
		}
	}
	out.Close()
}
