package gohunspell

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/documatrix/go-hunspell/internal/core"
	"github.com/documatrix/go-hunspell/parsers"
)

// Format is the markup of a text to check.
type Format int

// The text formats the tokenizer understands.
const (
	// Text is plain text.
	Text Format = iota
	// LaTeX skips TeX/LaTeX commands, math and verbatim blocks.
	LaTeX
	// HTML skips tags, scripts, styles and code but checks ALT and TITLE
	// attributes.
	HTML
	// XML skips tags and comments.
	XML
	// Man is troff/nroff (man page) source.
	Man
	// ODF is the content.xml of an OpenDocument file (also Flat ODF).
	ODF
	// FirstField takes the first tab separated field of each line as a word.
	FirstField
)

// Token is a word of a text.
type Token struct {
	// Word is the word as it stands in the text.
	Word string
	// Offset is the byte offset of the word in the text.
	Offset int
	// Line is the 1-based line number of the word.
	Line int
	// Column is the 0-based character (rune) offset of the word in its line.
	Column int
}

// Misspelling is a misspelled word of a text.
type Misspelling struct {
	Token
	// Suggestions holds the suggestions, if they were asked for.
	Suggestions []string
}

// TextOption configures text checking.
type TextOption func(*textOptions)

type textOptions struct {
	format   Format
	suggest  bool
	checkURL bool
}

// WithFormat sets the markup of the text.
func WithFormat(f Format) TextOption {
	return func(o *textOptions) { o.format = f }
}

// WithSuggestions makes CheckText fill in the suggestions of each
// misspelled word.
func WithSuggestions() TextOption {
	return func(o *textOptions) { o.suggest = true }
}

// WithURLChecking checks URLs, e-mail addresses and file paths, which are
// skipped by default.
func WithURLChecking() TextOption {
	return func(o *textOptions) { o.checkURL = true }
}

// wordcharsUTF16 returns the extra word characters as the hunspell tool
// hands them to its UTF-8 tokenizer.
func (d *Dictionary) wordcharsUTF16() []uint16 {
	if d.utf8 {
		return d.h.WordCharsUTF16()
	}
	w := core.UTF16(d.out(d.h.WordChars()))
	sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
	return w
}

func (d *Dictionary) parser(o textOptions) parsers.Parser {
	wc := d.wordcharsUTF16()
	var p parsers.Parser
	switch o.format {
	case LaTeX:
		p = parsers.NewLaTeXUTF8(wc)
	case HTML:
		p = parsers.NewHTMLUTF8(wc)
	case XML:
		p = parsers.NewXMLUTF8(wc)
	case Man:
		p = parsers.NewManUTF8(wc)
	case ODF:
		p = parsers.NewODFUTF8(wc)
	case FirstField:
		p = parsers.NewFirst("")
	default:
		p = parsers.NewTextUTF8(wc)
	}
	p.SetURLChecking(o.checkURL)
	return p
}

// Tokenize splits a UTF-8 text into words, using the word characters of the
// dictionary the way the hunspell tool does.
func (d *Dictionary) Tokenize(text string, opts ...TextOption) []Token {
	var o textOptions
	for _, opt := range opts {
		opt(&o)
	}
	var tokens []Token
	d.tokenize(text, o, func(t Token) { tokens = append(tokens, t) })
	return tokens
}

func (d *Dictionary) tokenize(text string, o textOptions, fn func(Token)) {
	p := d.parser(o)
	offset := 0
	for lineno, line := range strings.Split(text, "\n") {
		p.PutLine(line)
		colPos, col := 0, 0 // the rune column of byte colPos
		for {
			tok, ok := p.NextToken()
			if !ok {
				break
			}
			pos := p.TokenPos()
			if o.format == FirstField {
				// the position of the first field parser is the end of
				// the word, the tab
				pos = 0
			}
			// the parsers set the token position to their read position,
			// which only grows within a line, so col only grows too
			pos = min(pos, len(line))
			col += utf8.RuneCountInString(line[colPos:pos])
			colPos = pos
			fn(Token{
				Word:   p.Word(tok),
				Offset: offset + pos,
				Line:   lineno + 1,
				Column: col,
			})
		}
		offset += len(line) + 1
	}
}

const (
	entityApos = "&apos;"
	utf8Apos   = "’"
)

// checkToken checks a word of a text like the hunspell tool: an &apos;
// entity is an apostrophe, and a typographic apostrophe is tried as an
// ASCII one where the dictionary needs it.
func (d *Dictionary) checkToken(word string) bool {
	word = strings.ReplaceAll(word, entityApos, "'")
	if !d.utf8 {
		word = strings.ReplaceAll(word, utf8Apos, "'")
	}
	w, ok := d.in(word)
	if !ok {
		return false
	}
	if d.check(w).Correct {
		return true
	}
	if d.utf8 && strings.Contains(w, utf8Apos) {
		return d.check(strings.ReplaceAll(w, utf8Apos, "'")).Correct
	}
	return false
}

// CheckText spell checks a UTF-8 text and returns its misspelled words.
func (d *Dictionary) CheckText(text string, opts ...TextOption) []Misspelling {
	var o textOptions
	for _, opt := range opts {
		opt(&o)
	}
	var res []Misspelling
	d.tokenize(text, o, func(t Token) {
		d.mu.Lock()
		ok := d.checkToken(t.Word)
		var sugs []string
		if !ok && o.suggest {
			if w, conv := d.in(strings.ReplaceAll(t.Word, entityApos, "'")); conv {
				sugs = d.outAll(d.h.Suggest(w))
			}
		}
		d.mu.Unlock()
		if !ok {
			res = append(res, Misspelling{Token: t, Suggestions: sugs})
		}
	})
	return res
}
