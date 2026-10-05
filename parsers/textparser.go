// Package parsers splits text into words the way the hunspell command line
// tool does: plain text, the first field of tab separated lines, LaTeX, HTML,
// XML, troff/man and ODF XML.
//
// A parser works on one line at a time. Feed a line with PutLine and take the
// words with NextToken until it reports false. Words are byte slices of the
// line in the line's own encoding.
package parsers

import (
	"sort"
	"strings"

	"github.com/documatrix/go-hunspell/internal/core"
)

const maxPrevLine = 4

const (
	entityApos = "&apos;"
	utf8Apos   = "\xe2\x80\x99"
	apostrophe = "'"
)

// ISO-8859-1 HTML character entities
var latin1 = []string{
	"&Agrave;", "&Atilde;", "&Aring;", "&AElig;", "&Egrave;", "&Ecirc;",
	"&Igrave;", "&Iuml;", "&ETH;", "&Ntilde;", "&Ograve;", "&Oslash;",
	"&Ugrave;", "&THORN;", "&agrave;", "&atilde;", "&aring;", "&aelig;",
	"&egrave;", "&ecirc;", "&igrave;", "&iuml;", "&eth;", "&ntilde;",
	"&ograve;", "&oslash;", "&ugrave;", "&thorn;", "&yuml;",
}

// Parser is a tokenizer of one text format.
type Parser interface {
	// PutLine sets the line to tokenize.
	PutLine(line string)
	// Line returns the current line, with the changes ChangeToken made.
	Line() string
	// PrevLine returns the n-th previous line (0 is the current one).
	PrevLine(n int) string
	// NextToken returns the next word of the line.
	NextToken() (string, bool)
	// Word strips format specific in-word markup from a token.
	Word(token string) string
	// ChangeToken replaces the last token in the line.
	ChangeToken(word string) bool
	// TokenPos returns the byte offset of the last token in the line.
	TokenPos() int
	// SetURLChecking turns on the checking of URLs, e-mail addresses and
	// paths, which are skipped by default.
	SetURLChecking(check bool)
	// IsUTF8 reports whether the parser reads UTF-8 text.
	IsUTF8() bool
}

// TextParser tokenizes plain text.
type TextParser struct {
	wordcharacters []bool
	lines          [maxPrevLine]string
	urlline        []bool
	checkurl       bool
	actual         int
	head           int
	token          int
	state          int
	utf8           bool
	wordcharsUTF16 []uint16
}

// ASCIILetters is a word character set of the ASCII letters for NewText.
const ASCIILetters = "qwertzuiopasdfghjklyxcvbnmQWERTZUIOPASDFGHJKLYXCVBNM"

// NewText returns a parser of 8-bit text. wordchars lists the bytes that
// belong to words (see ASCIILetters).
func NewText(wordchars string) *TextParser {
	p := &TextParser{}
	p.init(wordchars)
	return p
}

// NewTextUTF8 returns a parser of UTF-8 text. Letters belong to words, and
// so do the extra characters of wordchars (UTF-16 code units).
func NewTextUTF8(wordchars []uint16) *TextParser {
	p := &TextParser{}
	p.initUTF8(wordchars)
	return p
}

func (p *TextParser) init(wordchars string) {
	p.wordcharacters = make([]bool, 256)
	for i := 0; i < len(wordchars); i++ {
		p.wordcharacters[wordchars[i]] = true
	}
}

func (p *TextParser) initUTF8(wordchars []uint16) {
	p.utf8 = true
	wc := append([]uint16(nil), wordchars...)
	sort.Slice(wc, func(i, j int) bool { return wc[i] < wc[j] })
	p.wordcharsUTF16 = wc
	// build a cache for the simple cases
	p.wordcharacters = make([]bool, 0x80)
	for idx := 0; idx < 0x80; idx++ {
		p.wordcharacters[idx] = core.UnicodeIsAlpha(uint16(idx)) || p.inWordchars(uint16(idx))
	}
}

func (p *TextParser) inWordchars(c uint16) bool {
	i := sort.Search(len(p.wordcharsUTF16), func(i int) bool { return p.wordcharsUTF16[i] >= c })
	return i < len(p.wordcharsUTF16) && p.wordcharsUTF16[i] == c
}

func (p *TextParser) line() string { return p.lines[p.actual] }

// at returns the byte of the current line at i, 0 past its end.
func (p *TextParser) at(i int) byte {
	l := p.lines[p.actual]
	if i < 0 || i >= len(l) {
		return 0
	}
	return l[i]
}

// isWordcharAt reports whether the character at s[i:] belongs to a word.
func (p *TextParser) isWordcharStr(s string) bool {
	if s == "" || s[0] == 0 {
		return false
	}
	c := s[0]
	if p.utf8 {
		if c < 0x80 {
			return p.wordcharacters[c]
		}
		w, n := core.FirstUTF16(s)
		if n < 1 {
			return false
		}
		return core.UnicodeIsAlpha(w) || p.inWordchars(w)
	}
	return p.wordcharacters[c]
}

func (p *TextParser) isWordchar(i int) bool {
	return p.isWordcharStr(p.rest(i))
}

func (p *TextParser) rest(i int) string {
	l := p.lines[p.actual]
	if i >= len(l) {
		return ""
	}
	return l[i:]
}

func getLatin1(s string) string {
	if s != "" && s[0] == '&' {
		for _, e := range latin1 {
			if strings.HasPrefix(s, e) {
				return e
			}
		}
	}
	return ""
}

// nextChar steps over one character; it reports true at the end of the line.
func (p *TextParser) nextChar(pos *int) bool {
	if p.at(*pos) == 0 {
		return true
	}
	if p.utf8 {
		if p.at(*pos)&0x80 != 0 {
			// jump to next UTF-8 character
			for *pos++; p.at(*pos)&0xc0 == 0x80; *pos++ {
			}
		} else {
			*pos++
		}
	} else {
		*pos++
	}
	return false
}

// PutLine sets the line to tokenize.
func (p *TextParser) PutLine(line string) {
	if i := strings.IndexByte(line, 0); i >= 0 {
		line = line[:i]
	}
	p.actual = (p.actual + 1) % maxPrevLine
	p.lines[p.actual] = line
	p.token = 0
	p.head = 0
	p.checkURLs()
}

// PrevLine returns the n-th previous line.
func (p *TextParser) PrevLine(n int) string {
	return p.lines[(p.actual+maxPrevLine-n)%maxPrevLine]
}

// Line returns the current line.
func (p *TextParser) Line() string { return p.PrevLine(0) }

// IsUTF8 reports whether the parser reads UTF-8.
func (p *TextParser) IsUTF8() bool { return p.utf8 }

// NextToken returns the next word of the line.
func (p *TextParser) NextToken() (string, bool) {
	for {
		switch p.state {
		case 0: // non word chars
			if p.isWordchar(p.head) {
				p.state = 1
				p.token = p.head
			} else if l1 := getLatin1(p.rest(p.head)); l1 != "" {
				p.state = 1
				p.token = p.head
				p.head += len(l1)
			}
		case 1: // wordchar
			if l1 := getLatin1(p.rest(p.head)); l1 != "" {
				p.head += len(l1)
			} else if (p.isWordcharStr(apostrophe) || (p.utf8 && p.isWordcharStr(utf8Apos))) &&
				p.line() != "" && p.at(p.head) == '\'' && p.isWordchar(p.head+1) {
				p.head++
			} else if p.utf8 && p.isWordcharStr(apostrophe) && // add Unicode apostrophe to the WORDCHARS, if needed
				strings.HasPrefix(p.rest(p.head), utf8Apos) && p.isWordchar(p.head+len(utf8Apos)) {
				p.head += len(utf8Apos) - 1
			} else if !p.isWordchar(p.head) {
				p.state = 0
				if t, ok := p.allocToken(p.token, &p.head); ok {
					return t, true
				}
			}
		}
		if p.nextChar(&p.head) {
			return "", false
		}
	}
}

// TokenPos returns the byte offset of the last token.
func (p *TextParser) TokenPos() int { return p.token }

// ChangeToken replaces the last token in the line. The word ends at its
// first NUL byte, if any.
func (p *TextParser) ChangeToken(word string) bool {
	if i := strings.IndexByte(word, 0); i >= 0 {
		word = word[:i]
	}
	l := p.lines[p.actual]
	remainder := ""
	if p.head < len(l) {
		remainder = l[p.head:]
	}
	p.lines[p.actual] = l[:p.token] + word + remainder
	p.head = p.token
	p.checkURLs()
	return true
}

// Word returns the token itself.
func (p *TextParser) Word(token string) string { return token }

func (p *TextParser) checkURLs() {
	l := p.lines[p.actual]
	p.urlline = make([]bool, len(l)+1)
	urlState := 0
	urlHead := 0
	urlToken := 0
	url := false
	for {
		switch urlState {
		case 0: // non word chars
			if p.isWordchar(urlHead) {
				urlState = 1
				urlToken = urlHead
			} else if p.at(urlHead) == '/' { // Unix path
				urlState = 1
				urlToken = urlHead
				url = true
			}
		case 1: // wordchar
			ch := p.at(urlHead)
			r := p.rest(urlHead)
			// e-mail address, MS-DOS/Windows path or URL
			if ch == '@' || strings.HasPrefix(r, ":\\") || strings.HasPrefix(r, "://") {
				url = true
			} else if !(p.isWordchar(urlHead) || ch == '-' || ch == '_' || ch == '\\' || ch == '.' ||
				ch == ':' || ch == '/' || ch == '~' || ch == '%' || ch == '*' || ch == '$' ||
				ch == '[' || ch == ']' || ch == '?' || ch == '!' || (ch >= '0' && ch <= '9')) {
				urlState = 0
				if url {
					for i := urlToken; i < urlHead; i++ {
						p.urlline[i] = true
					}
				}
				url = false
			}
		}
		p.urlline[urlHead] = false
		if p.nextChar(&urlHead) {
			return
		}
	}
}

func (p *TextParser) getURL(tokenPos int, hd *int) bool {
	l := p.lines[p.actual]
	for i := *hd; i < len(l) && p.urlline[i]; i++ {
		*hd++
	}
	if p.checkurl {
		return false
	}
	return p.urlline[tokenPos]
}

// SetURLChecking turns on the checking of URLs.
func (p *TextParser) SetURLChecking(check bool) { p.checkurl = check }

func (p *TextParser) allocToken(tokn int, hd *int) (string, bool) {
	if p.getURL(tokn, hd) {
		return "", false
	}
	t := p.lines[p.actual][tokn:*hd]
	// remove colon for Finnish and Swedish language
	if t != "" && t[len(t)-1] == ':' {
		t = t[:len(t)-1]
		if t == "" {
			return "", false
		}
	}
	return t, true
}

// FirstParser returns the first field of tab separated lines.
type FirstParser struct {
	TextParser
}

// NewFirst returns a parser of the first, tab separated field of lines.
func NewFirst(wordchars string) *FirstParser {
	p := &FirstParser{}
	p.init(wordchars)
	return p
}

// NextToken returns the first field of the line.
func (p *FirstParser) NextToken() (string, bool) {
	l := p.line()
	tabpos := strings.IndexByte(l, '\t')
	if tabpos >= 0 && tabpos > p.token {
		p.token = tabpos
		return l[:tabpos], true
	}
	return "", false
}

// ManParser tokenizes troff/man text.
type ManParser struct {
	TextParser
}

// NewMan returns a man page parser of 8-bit text.
func NewMan(wordchars string) *ManParser {
	p := &ManParser{}
	p.init(wordchars)
	return p
}

// NewManUTF8 returns a man page parser of UTF-8 text.
func NewManUTF8(wordchars []uint16) *ManParser {
	p := &ManParser{}
	p.initUTF8(wordchars)
	return p
}

// NextToken returns the next word of the line.
func (p *ManParser) NextToken() (string, bool) {
	for {
		switch p.state {
		case 1: // command arguments
			if p.at(p.head) == ' ' {
				p.state = 2
			}
		case 0, 2:
			if p.state == 0 {
				// dot in begin of line
				if p.at(0) == '.' {
					p.state = 1
					break
				}
				p.state = 2
			}
			// non word chars
			if p.isWordchar(p.head) {
				p.state = 3
				p.token = p.head
			} else if p.at(p.head) == '\\' && p.at(p.head+1) == 'f' && p.at(p.head+2) != 0 {
				p.head += 2
			}
		case 3: // wordchar
			if !p.isWordchar(p.head) {
				p.state = 2
				if t, ok := p.allocToken(p.token, &p.head); ok {
					return t, true
				}
			}
		}
		if p.nextChar(&p.head) {
			p.state = 0
			return "", false
		}
	}
}
