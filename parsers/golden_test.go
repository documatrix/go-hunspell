package parsers

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/documatrix/go-hunspell/internal/core"
)

// The cases of testdata/golden/cli/parsers drive a parser line by line;
// NAME.out is what driver.cxx printed running the C++ parsers of Hunspell on
// NAME.in. This test runs the same steps on the Go parsers.
const parserGoldenDir = "../testdata/golden/cli/parsers"

// unescape decodes \\ and \xHH as driver.cxx does.
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '\\':
			b.WriteByte('\\')
			i++
		case s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x':
			v, _ := strconv.ParseUint(s[i+2:i+4], 16, 8)
			b.WriteByte(byte(v))
			i += 3
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// escape shows the bytes that are not printable ASCII as \xHH.
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c < 32 || c >= 127:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

type change struct{ from, to string }

// runParserCase runs a case file and returns the transcript.
func runParserCase(t *testing.T, name string) string {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var kind, wordchars string
	url := false
	var changes []change
	var lines []string
	body := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case body:
			lines = append(lines, unescape(l))
		case l == "---":
			body = true
		case strings.HasPrefix(l, "kind "):
			kind = l[5:]
		case strings.HasPrefix(l, "wordchars "):
			wordchars = unescape(l[10:])
		case l == "url":
			url = true
		case strings.HasPrefix(l, "change "):
			from, to, _ := strings.Cut(l[7:], " ")
			changes = append(changes, change{unescape(from), unescape(to)})
		}
	}
	w := core.UTF16(wordchars)
	sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
	var p Parser
	switch kind {
	case "text":
		p = NewText(wordchars)
	case "text-utf8":
		p = NewTextUTF8(w)
	case "first":
		p = NewFirst(wordchars)
	case "latex":
		p = NewLaTeX(wordchars)
	case "latex-utf8":
		p = NewLaTeXUTF8(w)
	case "html":
		p = NewHTML(wordchars)
	case "html-utf8":
		p = NewHTMLUTF8(w)
	case "xml":
		p = NewXML(wordchars)
	case "xml-utf8":
		p = NewXMLUTF8(w)
	case "odf":
		p = NewODF(wordchars)
	case "odf-utf8":
		p = NewODFUTF8(w)
	case "man":
		p = NewMan(wordchars)
	case "man-utf8":
		p = NewManUTF8(w)
	default:
		t.Fatalf("%s: unknown kind %q", name, kind)
	}
	p.SetURLChecking(url)
	var b strings.Builder
	utf8 := 0
	if p.IsUTF8() {
		utf8 = 1
	}
	fmt.Fprintf(&b, "utf8 %d\n", utf8)
	for _, line := range lines {
		p.PutLine(line)
		fmt.Fprintf(&b, "line [%s]\n", escape(p.Line()))
		for {
			tok, ok := p.NextToken()
			if !ok {
				break
			}
			fmt.Fprintf(&b, "token %d [%s] word [%s]\n", p.TokenPos(), escape(tok), escape(p.Word(tok)))
			for _, c := range changes {
				if c.from == tok {
					p.ChangeToken(c.to)
					fmt.Fprintf(&b, "change [%s]\n", escape(p.Line()))
					next, ok := p.NextToken()
					okn := 0
					if ok {
						okn = 1
					}
					fmt.Fprintf(&b, "skip %d [%s]\n", okn, escape(next))
					break
				}
			}
		}
	}
	for i := 0; i < maxPrevLine; i++ {
		fmt.Fprintf(&b, "prev %d [%s]\n", i, escape(p.PrevLine(i)))
	}
	return b.String()
}

// TestGoldenParsers compares the Go parsers with the C++ ones.
func TestGoldenParsers(t *testing.T) {
	cases, err := filepath.Glob(filepath.Join(parserGoldenDir, "*.in"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no parser cases")
	}
	for _, in := range cases {
		in := in
		name := strings.TrimSuffix(filepath.Base(in), ".in")
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(strings.TrimSuffix(in, ".in") + ".out")
			if err != nil {
				t.Fatal(err)
			}
			got := runParserCase(t, in)
			if got == string(want) {
				return
			}
			w := strings.Split(string(want), "\n")
			g := strings.Split(got, "\n")
			for i := 0; i < len(w) || i < len(g); i++ {
				var a, c string
				if i < len(w) {
					a = w[i]
				}
				if i < len(g) {
					c = g[i]
				}
				if a != c {
					t.Errorf("%s line %d:\n  want %s\n  got  %s", name, i+1, a, c)
				}
			}
		})
	}
}
