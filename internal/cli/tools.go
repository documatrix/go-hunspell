package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/documatrix/go-hunspell/parsers"
)

// fgetsLines splits r into the pieces fgets returns with a buffer of size n,
// each without its newline.
func fgetsLines(r io.Reader, n int) []string {
	br := bufio.NewReader(r)
	var lines []string
	for {
		var b []byte
		for len(b) < n-1 {
			c, err := br.ReadByte()
			if err != nil {
				break
			}
			b = append(b, c)
			if c == '\n' {
				break
			}
		}
		if len(b) == 0 {
			return lines
		}
		lines = append(lines, string(b))
	}
}

// Analyze is the analyze tool: it spell checks, analyzes and stems each line
// of a word file, or generates from "word example" lines.
func Analyze(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, "correct syntax is:\nanalyze affix_file dictionary_file file_of_words_to_check\n"+
			"use two words per line for morphological generation\n")
		return 1
	}
	// only the dictionary arguments are checked: a missing word file is a
	// file that cannot be opened
	if len(args) < 3 {
		fmt.Fprint(stderr, "Error - could not open file to check\n")
		return 1
	}
	f, err := os.Open(args[2])
	if err != nil {
		fmt.Fprint(stderr, "Error - could not open file to check\n")
		return 1
	}
	defer f.Close()
	h := newHunspell(args[0], args[1], "")
	for _, e := range h.Errors() {
		fmt.Fprint(stderr, e)
	}
	out := bufio.NewWriter(stdout)
	defer out.Flush()
	for _, line := range fgetsLines(f, 100) {
		buf := cstr(line)
		if i := strings.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[:i]
		}
		if buf == "" {
			continue
		}
		// morphgen demo
		if s := strings.IndexByte(buf, ' '); s >= 0 {
			w, ex := buf[:s], buf[s+1:]
			result := h.Generate(w, ex)
			for _, r := range result {
				fmt.Fprintf(out, "generate(%s, %s) = %s\n", w, ex, cstr(r))
			}
			if len(result) == 0 {
				fmt.Fprintf(out, "generate(%s, %s) = NO DATA\n", w, ex)
			}
			continue
		}
		dp := h.Spell(buf, nil, nil)
		fmt.Fprintf(out, "> %s\n", buf)
		if dp {
			for _, r := range h.Analyze(buf) {
				fmt.Fprintf(out, "analyze(%s) = %s\n", buf, cstr(r))
			}
			for _, r := range h.Stem(buf) {
				fmt.Fprintf(out, "stem(%s) = %s\n", buf, cstr(r))
			}
		} else {
			fmt.Fprint(out, "Unknown word.\n")
		}
	}
	return 0
}

// Chmorph is the chmorph tool: it changes affixes by morphological analysis
// and generation.
func Chmorph(args []string, stdout, stderr io.Writer) int {
	if len(args) < 5 {
		fmt.Fprint(stderr, "chmorph - change affixes by morphological analysis and generation\n"+
			"correct syntax is:\nchmorph affix_file dictionary_file file_to_convert STRING1 STRING2\n"+
			"STRINGS may be arbitrary parts of the morphological descriptions\n"+
			"example: chmorph hu.aff hu.dic hu.txt SG_2 SG_3  (convert informal Hungarian second person texts to formal third person texts)\n")
		return 1
	}
	f, err := os.Open(args[2])
	if err != nil {
		fmt.Fprint(stderr, "Error - could not open file to check\n")
		return 1
	}
	defer f.Close()
	h := newHunspell(args[0], args[1], "")
	p := parsers.NewText(parsers.ASCIILetters)
	out := bufio.NewWriter(stdout)
	defer out.Flush()
	from, to := args[3], args[4]
	for _, line := range fgetsLines(f, maxLnLen) {
		p.PutLine(line)
		for {
			next, ok := p.NextToken()
			if !ok {
				break
			}
			pl := h.Analyze(next)
			if len(pl) == 0 {
				continue
			}
			gen := 0
			for _, a := range pl {
				if pos := strings.Index(cstr(a), from); pos >= 0 {
					pl[gen] = a[:pos] + to + a[pos+len(from):]
					gen++
				}
			}
			if gen > 0 {
				// generate only from the analyses that carried the source
				// description, so an unrelated homonym cannot win the result
				pl2 := h.GenerateMorph(next, pl[:gen])
				if len(pl2) > 0 {
					p.ChangeToken(pl2[0])
					// jump over the (possibly un)modified word
					p.NextToken()
				}
			}
		}
		fmt.Fprintf(out, "%s\n", cstr(p.Line()))
	}
	return 0
}
