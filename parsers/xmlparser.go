package parsers

import "strings"

const (
	stNonWord = iota
	stWord
	stTag
	stCharEntity
	stOtherTag
	stAttrib
)

type patternPair [2]string

var xmlPatterns = []patternPair{{"<!--", "-->"}, {"<[cdata[", "]]>"}, {"<", ">"}}

var htmlPatterns = []patternPair{
	{"<script", "</script>"},
	{"<style", "</style>"},
	{"<code", "</code>"},
	{"<samp", "</samp>"},
	{"<kbd", "</kbd>"},
	{"<var", "</var>"},
	{"<listing", "</listing>"},
	{"<address", "</address>"},
	{"<pre", "</pre>"},
	{"<!--", "-->"},
	{"<[cdata[", "]]>"},
	{"<", ">"},
}

// ALT and TITLE attributes are handled specially
var htmlPatterns2 = []patternPair{{"<img", "alt="}, {"<img", "title="}, {"<a ", "title="}}

var odfPatterns = []patternPair{
	{"<office:meta>", "</office:meta>"},
	{"<office:settings>", "</office:settings>"},
	{"<office:binary-data>", "</office:binary-data>"},
	{"<!--", "-->"},
	{"<[cdata[", "]]>"},
	{"<", ">"},
}

// in-word patterns: parts of re-edited words, for example an inserted letter
var odfPatterns3 = []patternPair{{"<text:span", ">"}, {"</text:span", ">"}}

// XMLParser tokenizes XML; HTML and ODF use it with their own patterns.
type XMLParser struct {
	TextParser
	patterns     []patternPair
	patterns2    []patternPair
	patterns3    []patternPair
	patternNum   int
	pattern2Num  int
	pattern3Num  int
	prevstate    int
	checkattr    int
	quotmark     byte
	stripInWords bool
}

func newXML(p1, p2, p3 []patternPair) *XMLParser {
	return &XMLParser{patterns: p1, patterns2: p2, patterns3: p3}
}

// NewXML returns an XML parser of 8-bit text.
func NewXML(wordchars string) *XMLParser {
	p := newXML(xmlPatterns, nil, nil)
	p.init(wordchars)
	return p
}

// NewXMLUTF8 returns an XML parser of UTF-8 text.
func NewXMLUTF8(wordchars []uint16) *XMLParser {
	p := newXML(xmlPatterns, nil, nil)
	p.initUTF8(wordchars)
	return p
}

// NewHTML returns an HTML parser of 8-bit text.
func NewHTML(wordchars string) *XMLParser {
	p := newXML(htmlPatterns, htmlPatterns2, nil)
	p.init(wordchars)
	return p
}

// NewHTMLUTF8 returns an HTML parser of UTF-8 text.
func NewHTMLUTF8(wordchars []uint16) *XMLParser {
	p := newXML(htmlPatterns, htmlPatterns2, nil)
	p.initUTF8(wordchars)
	return p
}

// NewODF returns a parser of the content.xml of ODF documents, 8-bit text.
func NewODF(wordchars string) *XMLParser {
	p := newXML(odfPatterns, nil, odfPatterns3)
	p.stripInWords = true
	p.init(wordchars)
	return p
}

// NewODFUTF8 returns a parser of the content.xml of ODF documents.
func NewODFUTF8(wordchars []uint16) *XMLParser {
	p := newXML(odfPatterns, nil, odfPatterns3)
	p.stripInWords = true
	p.initUTF8(wordchars)
	return p
}

func asciiLower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 0x20
	}
	return c
}

// lookPattern returns the index of the pattern (column 0 for the opening and
// 1 for the closing string) starting at the head of the line, or -1.
func (p *XMLParser) lookPattern(pats []patternPair, column int) int {
	for i, pat := range pats {
		k := pat[column]
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
func (p *XMLParser) NextToken() (string, bool) {
	for {
		switch p.state {
		case stNonWord: // non word chars
			p.prevstate = stNonWord
			if p.patternNum = p.lookPattern(p.patterns, 0); p.patternNum != -1 {
				p.checkattr = 0
				if p.pattern2Num = p.lookPattern(p.patterns2, 0); p.pattern2Num != -1 {
					p.checkattr = 1
				}
				p.state = stTag
			} else if p.isWordchar(p.head) {
				p.state = stWord
				p.token = p.head
			} else if l1 := getLatin1(p.rest(p.head)); l1 != "" {
				p.state = stWord
				p.token = p.head
				p.head += len(l1)
			} else if p.at(p.head) == '&' {
				p.state = stCharEntity
			}
		case stWord: // wordchar
			if l1 := getLatin1(p.rest(p.head)); l1 != "" {
				p.head += len(l1)
			} else if (p.isWordcharStr(apostrophe) || (p.utf8 && p.isWordcharStr(utf8Apos))) &&
				strings.HasPrefix(p.rest(p.head), entityApos) && p.isWordchar(p.head+len(entityApos)) {
				p.head += len(entityApos) - 1
			} else if p.utf8 && p.isWordcharStr(apostrophe) &&
				strings.HasPrefix(p.rest(p.head), utf8Apos) && p.isWordchar(p.head+len(utf8Apos)) {
				p.head += len(utf8Apos) - 1
			} else if !p.isWordchar(p.head) {
				// in-word patterns
				inWord := false
				if p.pattern3Num = p.lookPattern(p.patterns3, 0); p.pattern3Num != -1 {
					closing := p.patterns3[p.pattern3Num][1]
					if pos := strings.Index(p.rest(p.head), closing); pos >= 0 {
						endpos := p.head + pos + len(closing) - 1
						if p.isWordchar(endpos + 1) {
							p.head = endpos
							inWord = true
						}
					}
				}
				if !inWord {
					p.state = p.prevstate
					// return with the token, except in the case of in-word patterns
					if t, ok := p.allocToken(p.token, &p.head); ok {
						return t, true
					}
				}
			}
		case stTag: // comment, labels, etc
			if i := p.lookPattern(p.patterns2, 1); p.checkattr == 1 && i != -1 &&
				p.patterns2[i][0] == p.patterns2[p.pattern2Num][0] {
				p.checkattr = 2
			} else if p.checkattr > 0 && p.at(p.head) == '>' {
				p.state = stNonWord
			} else if i := p.lookPattern(p.patterns, 1); i != -1 && p.patterns[i][1] == p.patterns[p.patternNum][1] {
				p.state = stNonWord
				p.head += len(p.patterns[p.patternNum][1]) - 1
			} else if p.patterns[p.patternNum][0] == "<" && (p.at(p.head) == '"' || p.at(p.head) == '\'') {
				p.quotmark = p.at(p.head)
				p.state = stAttrib
			}
		case stAttrib: // non word chars
			p.prevstate = stAttrib
			if p.at(p.head) == p.quotmark {
				p.state = stTag
				if p.checkattr == 2 {
					p.checkattr = 1
				}
			} else if p.isWordchar(p.head) && p.checkattr == 2 { // for IMG ALT
				p.state = stWord
				p.token = p.head
			} else if p.at(p.head) == '&' {
				p.state = stCharEntity
			}
		case stCharEntity: // SGML element
			if p.at(p.head) == ';' {
				p.state = p.prevstate
				if p.head > 0 {
					p.head--
				}
			}
		}
		if p.nextChar(&p.head) {
			return "", false
		}
	}
}

// Word removes the in-word patterns of ODF from a token.
func (p *XMLParser) Word(tok string) string {
	if !p.stripInWords {
		return tok
	}
	word := tok
	for _, pat := range p.patterns3 {
		for {
			pos := strings.Index(word, pat[0])
			if pos < 0 {
				break
			}
			end := strings.Index(word[pos:], pat[1])
			if end < 0 {
				return word
			}
			word = word[:pos] + word[pos+end+len(pat[1]):]
		}
	}
	return word
}

// ChangeToken replaces the last token, escaping XML special characters.
func (p *XMLParser) ChangeToken(word string) bool {
	if i := strings.IndexByte(word, 0); i >= 0 {
		word = word[:i]
	}
	if strings.ContainsAny(word, "'\"&<>") {
		// in two steps, as the C++ parser does, so a literal "__namp;__"
		// becomes "&amp;" too
		r := strings.ReplaceAll(word, "&", "__namp;__")
		r = strings.ReplaceAll(r, "__namp;__", "&amp;")
		r = strings.ReplaceAll(r, apostrophe, entityApos)
		r = strings.ReplaceAll(r, "\"", "&quot;")
		r = strings.ReplaceAll(r, ">", "&gt;")
		r = strings.ReplaceAll(r, "<", "&lt;")
		return p.TextParser.ChangeToken(r)
	}
	return p.TextParser.ChangeToken(word)
}

// IsODF reports whether p parses the content.xml of ODF documents.
func (p *XMLParser) IsODF() bool { return p.stripInWords }
