package parsers

import "strings"

type latexPattern struct {
	pat    [2]string
	hasEnd bool
	arg    int
}

func lp(open, close string, arg int) latexPattern {
	return latexPattern{pat: [2]string{open, close}, hasEnd: close != "", arg: arg}
}

var latexPatterns = []latexPattern{
	lp("\\(", "\\)", 0),
	lp("$$", "$$", 0),
	lp("$", "$", 0),
	lp("\\begin{math}", "\\end{math}", 0),
	lp("\\[", "\\]", 0),
	lp("\\begin{displaymath}", "\\end{displaymath}", 0),
	lp("\\begin{equation}", "\\end{equation}", 0),
	lp("\\begin{equation*}", "\\end{equation*}", 0),
	lp("\\begin{align}", "\\end{align}", 0),
	lp("\\begin{align*}", "\\end{align*}", 0),
	lp("\\begin{lstlisting}", "\\end{lstlisting}", 0),
	lp("\\cite", "", 1),
	lp("\\textcite", "", 1),
	lp("\\autocite", "", 1),
	lp("\\nocite", "", 1),
	lp("\\index", "", 1),
	lp("\\label", "", 1),
	lp("\\ref", "", 1),
	lp("\\pageref", "", 1),
	lp("\\autoref", "", 1),
	lp("\\parbox", "", 1),
	lp("\\begin{verbatim}", "\\end{verbatim}", 0),
	lp("\\verb+", "+", 0),
	lp("\\verb|", "|", 0),
	lp("\\verb#", "#", 0),
	lp("\\verb*", "*", 0),
	lp("\\documentstyle", "\\begin{document}", 0),
	lp("\\documentclass", "\\begin{document}", 0),
	lp("\\usepackage", "", 1),
	lp("\\includeonly", "", 1),
	lp("\\include", "", 1),
	lp("\\input", "", 1),
	lp("\\vspace", "", 1),
	lp("\\setlength", "", 2),
	lp("\\addtolength", "", 2),
	lp("\\settowidth", "", 2),
	lp("\\rule", "", 2),
	lp("\\hspace", "", 1),
	lp("\\vspace", "", 1),
	lp("\\\\[", "]", 0),
	lp("\\pagebreak[", "]", 0),
	lp("\\nopagebreak[", "]", 0),
	lp("\\enlargethispage", "", 1),
	lp("\\begin{tabular}", "", 1),
	lp("\\addcontentsline", "", 2),
	lp("\\gls", "", 1),
	lp("\\glspl", "", 1),
	lp("\\Gls", "", 1),
	lp("\\Glspl", "", 1),
	lp("\\begin{thebibliography}", "", 1),
	lp("\\bibliography", "", 1),
	lp("\\bibliographystyle", "", 1),
	lp("\\bibitem", "", 1),
	lp("\\begin", "", 1),
	lp("\\end", "", 1),
	lp("\\pagestyle", "", 1),
	lp("\\pagenumbering", "", 1),
	lp("\\thispagestyle", "", 1),
	lp("\\newtheorem", "", 2),
	lp("\\newcommand", "", 2),
	lp("\\renewcommand", "", 2),
	lp("\\setcounter", "", 2),
	lp("\\addtocounter", "", 1),
	lp("\\stepcounter", "", 1),
	lp("\\selectlanguage", "", 1),
	lp("\\inputencoding", "", 1),
	lp("\\hyphenation", "", 1),
	lp("\\definecolor", "", 3),
	lp("\\color", "", 1),
	lp("\\textcolor", "", 1),
	lp("\\pagecolor", "", 1),
	lp("\\colorbox", "", 2),
	lp("\\fcolorbox", "", 2),
	lp("\\declaregraphicsextensions", "", 1),
	lp("\\psfig", "", 1),
	lp("\\url", "", 1),
	lp("\\eqref", "", 1),
	lp("\\cref", "", 1),
	lp("\\Cref", "", 1),
	lp("\\vskip", "", 1),
	lp("\\vglue", "", 1),
	lp("''", "", 1),
}

// LaTeXParser tokenizes TeX/LaTeX text.
type LaTeXParser struct {
	TextParser
	patternNum int
	depth      int
	arg        int
	opt        bool
}

// NewLaTeX returns a LaTeX parser of 8-bit text.
func NewLaTeX(wordchars string) *LaTeXParser {
	p := &LaTeXParser{}
	p.init(wordchars)
	return p
}

// NewLaTeXUTF8 returns a LaTeX parser of UTF-8 text.
func NewLaTeXUTF8(wordchars []uint16) *LaTeXParser {
	p := &LaTeXParser{}
	p.initUTF8(wordchars)
	return p
}

func (p *LaTeXParser) lookPattern(col int) int {
	for i, pat := range latexPatterns {
		k := pat.pat[col]
		if col == 1 && !pat.hasEnd {
			continue
		}
		j := 0
		for j < len(k) && asciiLower(p.at(p.head+j)) == k[j] {
			j++
		}
		if j == len(k) {
			return i
		}
	}
	return -1
}

// NextToken returns the next word of the line.
func (p *LaTeXParser) NextToken() (string, bool) {
	slash := false
	for {
		switch p.state {
		case 0: // non word chars
			if p.patternNum = p.lookPattern(0); p.patternNum != -1 {
				if latexPatterns[p.patternNum].hasEnd {
					p.state = 2
				} else {
					p.state = 4
					p.depth = 0
					p.arg = 0
					p.opt = true
				}
				p.head += len(latexPatterns[p.patternNum].pat[0]) - 1
			} else if p.at(p.head) == '%' {
				p.state = 5
			} else if p.isWordchar(p.head) {
				p.state = 1
				p.token = p.head
			} else if p.at(p.head) == '\\' {
				if c := p.at(p.head + 1); c == '\\' || c == '$' || c == '%' {
					// \\ (linebreak), \$ (dollar sign), \% (percent)
					p.head++
					break
				}
				p.state = 3
			}
		case 1: // wordchar
			if (p.isWordcharStr(apostrophe) || (p.utf8 && p.isWordcharStr(utf8Apos))) &&
				p.line() != "" && p.at(p.head) == '\'' && p.isWordchar(p.head+1) {
				p.head++
			} else if p.utf8 && p.isWordcharStr(apostrophe) &&
				strings.HasPrefix(p.rest(p.head), utf8Apos) && p.isWordchar(p.head+len(utf8Apos)) {
				p.head += len(utf8Apos) - 1
			} else if !p.isWordchar(p.head) {
				// C++ also ends the word before two apostrophes here, and
				// skips them. They never get here: an apostrophe that is a
				// word character, followed by another one, takes the first
				// branch above.
				p.state = 0
				t, ok := p.allocToken(p.token, &p.head)
				if ok {
					return t, true
				}
			}
		case 2: // comment, labels, etc
			if i := p.lookPattern(1); i != -1 && latexPatterns[i].pat[1] == latexPatterns[p.patternNum].pat[1] {
				p.state = 0
				p.head += len(latexPatterns[p.patternNum].pat[1]) - 1
			}
		case 3: // command
			if c := asciiLower(p.at(p.head)); c < 'a' || c > 'z' {
				p.state = 0
				p.head--
			}
		case 4: // command with arguments
			c := p.at(p.head)
			if slash && c != 0 {
				slash = false
				p.head++
				break
			} else if c == '\\' {
				slash = true
			} else if c == '{' || (p.opt && c == '[') {
				p.depth++
				p.opt = false
			} else if c == '}' {
				p.depth--
				if p.depth == 0 {
					p.opt = true
					p.arg++
				}
				if (p.depth == 0 && p.arg == latexPatterns[p.patternNum].arg) || p.depth < 0 {
					p.state = 0 // XXX not handles the last optional arg.
				}
			} else if c == ']' {
				p.depth--
			}
		}
		if p.nextChar(&p.head) {
			if p.state == 5 {
				p.state = 0
			}
			return "", false
		}
	}
}
