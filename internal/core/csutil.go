// Package core is a port of the Hunspell spell checking engine.
//
// The code follows the structure of the C++ sources (csutil, hashmgr,
// affentry, affixmgr, suggestmgr, hunspell) closely, so that its behaviour,
// down to the order of suggestions, matches the reference implementation.
// All strings handled here are in the encoding of the dictionary; conversion
// from and to UTF-8 happens in the public package.
package core

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// casing
const (
	noCap      = 0
	initCap    = 1
	allCap     = 2
	huhCap     = 3
	huhInitCap = 4
)

// default encoding and keystring
const (
	spellEncoding  = "ISO8859-1"
	spellKeystring = "qwertyuiop|asdfghjkl|zxcvbnm"
)

// default morphological fields
const (
	morphStem      = "st:"
	morphAllomorph = "al:"
	morphPos       = "po:"
	morphDeriPfx   = "dp:"
	morphInflPfx   = "ip:"
	morphTermPfx   = "tp:"
	morphDeriSfx   = "ds:"
	morphInflSfx   = "is:"
	morphTermSfx   = "ts:"
	morphSurfPfx   = "sp:"
	morphFreq      = "fr:"
	morphPhon      = "ph:"
	morphHyph      = "hy:"
	morphPart      = "pa:"
	morphFlag      = "fl:"
	morphHentry    = "_H:"
	morphTagLen    = 3
)

const (
	msepFld = ' '
	msepRec = '\n'
	msepAlt = '\v'
)

// default flags
const (
	defaultFlags   = 65510
	forbiddenWord  = 65510
	onlyUpcaseFlag = 65511
)

type csInfo struct {
	ccase  uint8
	clower uint8
	cupper uint8
}

type unicodeInfo struct {
	cletter bool
	cupper  uint16
	clower  uint16
}

// byteAt returns s[i], or 0 past the end of s, the way a read of a NUL
// terminated C string behaves.
func byteAt(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

// cstr returns s up to its first NUL byte.
func cstr(s string) string {
	if i := strings.IndexByte(s, 0); i >= 0 {
		return s[:i]
	}
	return s
}

func isUTF8Cont(c byte) bool {
	return c&0xc0 == 0x80
}

// utf8Next steps over one UTF-8 character, without warning on malformed
// sequences.
func utf8Next(s string, pos int) int {
	if pos < len(s) {
		pos++
		for pos < len(s) && isUTF8Cont(s[pos]) {
			pos++
		}
	}
	return pos
}

// u16u8 encodes a BMP-only sequence of UTF-16 code units as UTF-8.
func u16u8(src []uint16) string {
	b := make([]byte, 0, len(src))
	for _, cp := range src {
		switch {
		case cp < 0x80:
			b = append(b, byte(cp))
		case cp < 0x800:
			b = append(b, byte(0xc0|(cp>>6)), byte(0x80|(cp&0x3f)))
		default:
			b = append(b, byte(0xe0|(cp>>12)), byte(0x80|((cp>>6)&0x3f)), byte(0x80|(cp&0x3f)))
		}
	}
	return string(b)
}

// u8u16 decodes UTF-8 into BMP code points. A 4-byte lead (a code point past
// the BMP) ends the conversion with U+FFFD and a result of -1. A malformed
// sequence decodes to U+FFFD.
func u8u16(src string) ([]uint16, int) {
	return u8u16Into(nil, src, false)
}

// u8u16Warnings returns the warnings the C++ u8_u16 prints for src in its
// debug builds (HUNSPELL_WARNING).
func u8u16Warnings(src string) []string {
	var res []string
	cs := cstr(src)
	missing := func(p int) {
		res = append(res, fmt.Sprintf("UTF-8 encoding error. Missing continuation byte in %d. character position:\n%s\n", p, cs))
	}
	p, end := 0, len(src)
	for p < end {
		b0 := src[p]
		switch {
		case b0 < 0x80:
		case b0 < 0xc0:
			res = append(res, fmt.Sprintf("UTF-8 encoding error. Unexpected continuation bytes in %d. character position\n%s\n", p, cs))
		case b0 < 0xe0:
			if p+1 < end && isUTF8Cont(src[p+1]) {
				p++
			} else {
				missing(p)
			}
		case b0 < 0xf0:
			if p+1 < end && isUTF8Cont(src[p+1]) {
				p++
				if p+1 < end && isUTF8Cont(src[p+1]) {
					p++
				} else {
					missing(p)
				}
			} else {
				missing(p)
			}
		default:
			return append(res, fmt.Sprintf("This UTF-8 encoding can't convert to UTF-16:\n%s\n", cs))
		}
		p++
	}
	return res
}

// warnU8u16 converts src like u8u16, reporting the warnings of the C++
// conversion to w.
func warnU8u16(w warner, src string) ([]uint16, int) {
	warnU8u16Check(w, src)
	return u8u16(src)
}

// warnU8u16Check reports the warnings of the C++ conversion of src to w.
// Valid UTF-8 without code points past the BMP has none.
func warnU8u16Check(w warner, src string) {
	if utf8.ValidString(src) && strings.IndexFunc(src, func(r rune) bool { return r > 0xFFFF }) < 0 {
		return
	}
	for _, m := range u8u16Warnings(src) {
		w.warnf("%s", m)
	}
}

func u8u16Into(dest []uint16, src string, onlyFirst bool) ([]uint16, int) {
	dest = dest[:0]
	p, end := 0, len(src)
	for p < end {
		b0 := src[p]
		var cp uint16
		switch {
		case b0 < 0x80:
			cp = uint16(b0)
		case b0 < 0xc0:
			cp = 0xfffd
		case b0 < 0xe0:
			if p+1 < end && isUTF8Cont(src[p+1]) {
				cp = uint16(b0&0x1f)<<6 | uint16(src[p+1]&0x3f)
				p++
			} else {
				cp = 0xfffd
			}
		case b0 < 0xf0:
			if p+1 < end && isUTF8Cont(src[p+1]) {
				b1 := src[p+1]
				p++
				if p+1 < end && isUTF8Cont(src[p+1]) {
					cp = uint16(b0&0x0f)<<12 | uint16(b1&0x3f)<<6 | uint16(src[p+1]&0x3f)
					p++
				} else {
					cp = 0xfffd
				}
			} else {
				cp = 0xfffd
			}
		default:
			dest = append(dest, 0xfffd)
			return dest, -1
		}
		dest = append(dest, cp)
		if onlyFirst {
			break
		}
		p++
	}
	return dest, len(dest)
}

// mystrsep returns the next field of str, separated by spaces and tabs,
// starting the search at *start, and advances *start past it. ok is false
// when no field is left.
func mystrsep(str string, start *int) (string, bool) {
	sp := *start
	for sp < len(str) && (str[sp] == ' ' || str[sp] == '\t') {
		sp++
	}
	dp := sp
	for dp < len(str) && str[dp] != ' ' && str[dp] != '\t' {
		dp++
	}
	*start = dp
	if sp == len(str) {
		return "", false
	}
	return str[sp:dp], true
}

// mychomp removes the line end characters.
func mychomp(s string) string {
	k := len(s)
	n := k
	if k > 0 && (s[k-1] == '\r' || s[k-1] == '\n') {
		n--
	}
	if k > 1 && s[k-2] == '\r' {
		n--
	}
	return s[:n]
}

// lineTok breaks text into its non-empty lines.
func lineTok(text string, breakchar byte) []string {
	var ret []string
	if text == "" {
		return ret
	}
	for _, tok := range strings.Split(text, string(breakchar)) {
		if tok != "" {
			ret = append(ret, tok)
		}
	}
	return ret
}

func dropRepeatedLines(lines []string) []string {
	seen := make(map[string]bool, len(lines))
	out := lines[:0]
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// lineUniq drops repeated lines of text, keeping the first of each.
func lineUniq(text string, breakchar byte) string {
	lines := lineTok(text, breakchar)
	if len(lines) == 0 {
		return ""
	}
	lines = dropRepeatedLines(lines)
	return strings.Join(lines, string(breakchar))
}

// lineUniqApp turns the alternatives of a compound analysis into the
// " ( a | b ) " form.
func lineUniqApp(text string, breakchar byte) string {
	// C++ returns at once a text without breakchar, and an empty one for a
	// text without lines: the case of at most one line gives the same
	lines := dropRepeatedLines(lineTok(text, breakchar))
	if len(lines) <= 1 {
		return strings.Join(lines, "")
	}
	var b strings.Builder
	b.WriteString(" ( ")
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString(" | ")
	}
	r := []byte(b.String())
	r[len(r)-2] = ')'
	return string(r)
}

// strlinecat appends apd to the end of every line of str.
func strlinecat(str, apd string) string {
	var b strings.Builder
	pos := 0
	for {
		end := strings.IndexByte(str[pos:], '\n')
		if end < 0 {
			break
		}
		b.WriteString(str[pos : pos+end])
		b.WriteString(apd)
		b.WriteByte('\n')
		pos += end + 1
	}
	b.WriteString(str[pos:])
	b.WriteString(apd)
	return b.String()
}

// fieldlen is the length of the field starting at r.
func fieldlen(r string) int {
	n := 0
	for n < len(r) && r[n] != ' ' && r[n] != '\t' && r[n] != 0 && r[n] != '\n' {
		n++
	}
	return n
}

// appendCompoundParts appends the field of each compound word part of desc
// except the last one to result, and returns the offset of that last part, or
// 0 when desc has no compound word part.
func appendCompoundParts(desc string, result *strings.Builder) int {
	part := strings.Index(desc, morphPart)
	if part < 0 {
		return 0
	}
	for {
		next := strings.Index(desc[part+1:], morphPart)
		if next < 0 {
			break
		}
		nextpart := part + 1 + next
		field := desc[part+morphTagLen:]
		l := fieldlen(field)
		if m := nextpart - part - morphTagLen; m < l {
			l = m
		}
		result.WriteString(field[:l])
		part = nextpart
	}
	return part
}

// copyField returns the value of the field var of morph.
func copyField(morph, v string) (string, bool) {
	// (C++ returns false for an empty morph first, which the search of the
	// non-empty field name does too)
	pos := strings.Index(morph, v)
	if pos < 0 {
		return "", false
	}
	pos += morphTagLen
	rest := morph[pos:]
	end := strings.IndexAny(rest, " \t\n")
	if end < 0 {
		return rest, true
	}
	return rest[:end], true
}

// mystrrep replaces every occurrence of search in str. The callers search
// for non-empty constants.
func mystrrep(str, search, replace string) string {
	return strings.ReplaceAll(str, search, replace)
}

func reverseword(word string) string {
	b := []byte(word)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

// reversewordUTF reverses the characters of an UTF-8 string.
func reversewordUTF(word string) string {
	return warnReversewordUTF(nil, word)
}

// warnReversewordUTF is reversewordUTF reporting the malformed UTF-8 to w,
// like the debug builds of the C++ code (HUNSPELL_WARNING).
func warnReversewordUTF(w warner, word string) string {
	b := []byte(word)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	missing := func() {
		if w != nil {
			w.warnf("UTF-8 encoding error. Missing character at the end\n%s\n", cstr(string(b)))
		}
	}
	// b is reversed byte by byte; walk it from its end (the start of the
	// original string) and restore the byte order inside each character
	it := len(b) - 1
	for it >= 0 {
		switch b[it] & 0xf0 {
		case 0x00, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70:
			it--
		case 0x80, 0x90, 0xa0, 0xb0:
			if w != nil {
				w.warnf("UTF-8 encoding error. Unexpected continuation bytes in %d. character position\n%s\n", it, cstr(string(b)))
			}
			it--
		case 0xc0, 0xd0:
			if it+1 >= 2 {
				b[it], b[it-1] = b[it-1], b[it]
				it -= 2
			} else {
				missing()
				it--
			}
		case 0xe0:
			if it+1 >= 3 {
				b[it], b[it-2] = b[it-2], b[it]
				it -= 3
			} else {
				missing()
				it--
			}
		default:
			if it+1 >= 4 {
				b[it], b[it-3] = b[it-3], b[it]
				b[it-1], b[it-2] = b[it-2], b[it-1]
				it -= 4
			} else {
				missing()
				it--
			}
		}
	}
	return string(b)
}

func uniqlist(list []string) []string {
	if len(list) < 2 {
		return list
	}
	ret := []string{list[0]}
	for _, s := range list[1:] {
		found := false
		for _, r := range ret {
			if r == s {
				found = true
				break
			}
		}
		if !found {
			ret = append(ret, s)
		}
	}
	return ret
}

func upperUTF(u uint16, langnum int) uint16 { return unicodetoupper(u, langnum) }
func lowerUTF(u uint16, langnum int) uint16 { return unicodetolower(u, langnum) }

func mkallcap(s string, cs *[256]csInfo) string {
	b := []byte(s)
	for i, c := range b {
		b[i] = cs[c].cupper
	}
	return string(b)
}

func mkallsmall(s string, cs *[256]csInfo) string {
	b := []byte(s)
	for i, c := range b {
		b[i] = cs[c].clower
	}
	return string(b)
}

func mkallsmallUTF(u []uint16, langnum int) []uint16 {
	for i := range u {
		u[i] = lowerUTF(u[i], langnum)
	}
	return u
}

func mkallcapUTF(u []uint16, langnum int) []uint16 {
	for i := range u {
		u[i] = upperUTF(u[i], langnum)
	}
	return u
}

func mkinitcap(s string, cs *[256]csInfo) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	b[0] = cs[b[0]].cupper
	return string(b)
}

func mkinitcapUTF(u []uint16, langnum int) []uint16 {
	if len(u) > 0 {
		u[0] = upperUTF(u[0], langnum)
	}
	return u
}

func mkinitsmall(s string, cs *[256]csInfo) string {
	b := []byte(s)
	if len(b) > 0 {
		b[0] = cs[b[0]].clower
	}
	return string(b)
}

func mkinitsmallUTF(u []uint16, langnum int) []uint16 {
	if len(u) > 0 {
		u[0] = lowerUTF(u[0], langnum)
	}
	return u
}

type encEntry struct {
	name  string
	table *[256]csInfo
}

var encds = []encEntry{
	{"iso88591", &csIso1},
	{"iso88592", &csIso2},
	{"iso88593", &csIso3},
	{"iso88594", &csIso4},
	{"iso88595", &csIso5},
	{"iso88596", &csIso6},
	{"iso88597", &csIso7},
	{"iso88598", &csIso8},
	{"iso88599", &csIso9},
	{"iso885910", &csIso10},
	{"tis620", &csTis620},
	{"tis6202533", &csTis620},
	{"iso885911", &csTis620},
	{"iso885913", &csIso13},
	{"iso885914", &csIso14},
	{"iso885915", &csIso15},
	{"koi8r", &csKoi8r},
	{"koi8u", &csKoi8u},
	{"cp1251", &csCp1251},
	{"microsoftcp1251", &csCp1251},
	{"xisciias", &csIsciiDevanagari},
	{"isciidevanagari", &csIsciiDevanagari},
}

func toASCIILowerAndRemoveNonAlphanumeric(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 0x41 && c <= 0x5a:
			b.WriteByte(c + 0x20)
		case (c >= 0x61 && c <= 0x7a) || (c >= 0x30 && c <= 0x39):
			b.WriteByte(c)
		}
	}
	return b.String()
}

// getCurrentCS returns the case table of an 8-bit encoding, falling back to
// ISO8859-1 for an unknown one.
func getCurrentCS(es string, w warner) *[256]csInfo {
	n := toASCIILowerAndRemoveNonAlphanumeric(es)
	for _, e := range encds {
		if n == e.name {
			return e.table
		}
	}
	if w != nil {
		w.warnf("error: unknown encoding %s: using %s as fallback\n", es, encds[0].name)
	}
	return encds[0].table
}

// language numbers for language specific codes
const (
	langAr  = 96
	langAz  = 100
	langBg  = 41
	langCa  = 37
	langCrh = 102
	langCs  = 42
	langDa  = 45
	langDe  = 49
	langEl  = 30
	langEn  = 1
	langEs  = 34
	langEu  = 10
	langFr  = 2
	langGl  = 38
	langHr  = 78
	langHu  = 36
	langIt  = 39
	langLa  = 99
	langLv  = 101
	langNl  = 31
	langPl  = 48
	langPt  = 3
	langRu  = 7
	langSv  = 50
	langTr  = 90
	langUk  = 80
	langXx  = 999
)

var lang2enc = []struct {
	lang string
	num  int
}{
	{"ar", langAr}, {"az", langAz}, {"az_AZ", langAz}, {"bg", langBg}, {"ca", langCa},
	{"crh", langCrh}, {"cs", langCs}, {"da", langDa}, {"de", langDe}, {"el", langEl},
	{"en", langEn}, {"es", langEs}, {"eu", langEu}, {"gl", langGl}, {"fr", langFr},
	{"hr", langHr}, {"hu", langHu}, {"hu_HU", langHu}, {"it", langIt}, {"la", langLa},
	{"lv", langLv}, {"nl", langNl}, {"pl", langPl}, {"pt", langPt}, {"sv", langSv},
	{"tr", langTr}, {"tr_TR", langTr}, {"ru", langRu}, {"uk", langUk},
}

func getLangNum(lang string) int {
	for _, l := range lang2enc {
		if l.lang == lang {
			return l.num
		}
	}
	return langXx
}

func unicodetoupper(c uint16, langnum int) uint16 {
	// In Azeri and Turkish, I and i dictinct letters: there are a dotless lower
	// case i pair of upper `I', and an upper I with dot pair of lower `i'.
	if c == 0x0069 && (langnum == langAz || langnum == langTr || langnum == langCrh) {
		return 0x0130
	}
	up := utfPages[utfPageIndex[c>>8]][c&0xff].cupper
	if up != 0 {
		return up
	}
	return c
}

func unicodetolower(c uint16, langnum int) uint16 {
	if c == 0x0049 && (langnum == langAz || langnum == langTr || langnum == langCrh) {
		return 0x0131
	}
	lo := utfPages[utfPageIndex[c>>8]][c&0xff].clower
	if lo != 0 {
		return lo
	}
	return c
}

// UnicodeIsAlpha reports whether the BMP code point c is a letter in
// Hunspell's own Unicode table.
func UnicodeIsAlpha(c uint16) bool {
	return utfPages[utfPageIndex[c>>8]][c&0xff].cletter
}

// getCaptype returns the type of capitalization of an 8-bit word.
func getCaptype(word string, cs *[256]csInfo) int {
	ncap, nneutral := 0, 0
	firstcap := false
	// (C++ returns NOCAP for a NULL csconv; the 8-bit dictionaries always
	// have a character table, the default one at least)
	for i := 0; i < len(word); i++ {
		c := word[i]
		if cs[c].ccase != 0 {
			ncap++
		}
		if cs[c].cupper == cs[c].clower {
			nneutral++
		}
	}
	if ncap > 0 {
		firstcap = cs[word[0]].ccase != 0
	}
	switch {
	case ncap == 0:
		return noCap
	case ncap == 1 && firstcap:
		return initCap
	case ncap == len(word) || ncap+nneutral == len(word):
		return allCap
	case ncap > 1 && firstcap:
		return huhInitCap
	}
	return huhCap
}

// getCaptypeUTF8 returns the type of capitalization of a UTF-16 word.
func getCaptypeUTF8(word []uint16, langnum int) int {
	ncap, nneutral := 0, 0
	firstcap := false
	for _, idx := range word {
		lwr := unicodetolower(idx, langnum)
		if idx != lwr {
			ncap++
		}
		if unicodetoupper(idx, langnum) == lwr {
			nneutral++
		}
	}
	if ncap > 0 {
		idx := word[0]
		firstcap = idx != unicodetolower(idx, langnum)
	}
	switch {
	case ncap == 0:
		return noCap
	case ncap == 1 && firstcap:
		return initCap
	case ncap == len(word) || ncap+nneutral == len(word):
		return allCap
	case ncap > 1 && firstcap:
		return huhInitCap
	}
	return huhCap
}

func binarySearchU16(list []uint16, c uint16) bool {
	i := sort.Search(len(list), func(i int) bool { return list[i] >= c })
	return i < len(list) && list[i] == c
}

// removeIgnoredCharsUTF strips the ignored characters of an UTF-8 word.
func removeIgnoredCharsUTF(word string, ignored []uint16) (string, int) {
	w, _ := u8u16(word)
	w2 := make([]uint16, 0, len(w))
	for _, c := range w {
		if !binarySearchU16(ignored, c) {
			w2 = append(w2, c)
		}
	}
	return u16u8(w2), len(w2)
}

// removeIgnoredChars strips the ignored characters of an 8-bit word.
func removeIgnoredChars(word, ignored string) string {
	b := make([]byte, 0, len(word))
	for i := 0; i < len(word); i++ {
		if strings.IndexByte(ignored, word[i]) < 0 {
			b = append(b, word[i])
		}
	}
	return string(b)
}

func hasNoIgnoredChars(word, ignored string) bool {
	for i := 0; i < len(ignored); i++ {
		if strings.IndexByte(word, ignored[i]) >= 0 {
			return false
		}
	}
	return true
}

// warner receives the diagnostics of the parsers.
type warner interface {
	warnf(format string, args ...any)
}

// parseString reads the second field of an .aff line into *out.
func parseString(a warner, line string, out *string, ln int) bool {
	if *out != "" {
		a.warnf("error: line %d: multiple definitions\n", ln)
		return false
	}
	i, np := 0, 0
	pos := 0
	for {
		piece, ok := mystrsep(line, &pos)
		if !ok {
			break
		}
		switch i {
		case 0:
			np++
		case 1:
			*out = piece
			np++
		}
		i++
	}
	if np != 2 {
		a.warnf("error: line %d: missing data\n", ln)
		return false
	}
	return true
}

func parseArray(a warner, line string, out *string, outUTF16 *[]uint16, utf8 bool, ln int) bool {
	if !parseString(a, line, out, ln) {
		return false
	}
	if utf8 {
		w, _ := warnU8u16(a, *out)
		sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
		*outUTF16 = w
	}
	return true
}

// atoi converts the leading decimal number of s, like C's atoi in glibc:
// strtol, which saturates at the range of the 64-bit long, cast to int.
func atoi(s string) int {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\v' || s[i] == '\f') {
		i++
	}
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	limit := uint64(1<<63 - 1) // LONG_MAX
	if neg {
		limit++ // -LONG_MIN
	}
	n := uint64(0)
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		d := uint64(s[i] - '0')
		if n > (limit-d)/10 {
			n = limit
		} else {
			n = n*10 + d
		}
	}
	if neg {
		n = -n
	}
	return int(int32(n))
}

// isSpace is C's isspace in the C locale.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// FirstUTF16 decodes the first character of the UTF-8 string s the way
// Hunspell's u8_u16 does: n is 1 for a BMP character, -1 for one past the
// BMP and 0 for an empty string.
func FirstUTF16(s string) (c uint16, n int) {
	if s == "" {
		return 0, 0
	}
	if s[0] < 0x80 {
		return uint16(s[0]), 1
	}
	var buf [1]uint16
	w, n := u8u16Into(buf[:0], s, true)
	return w[0], n
}

// UTF16 decodes the UTF-8 string s into BMP code points the way Hunspell's
// u8_u16 does.
func UTF16(s string) []uint16 {
	w, _ := u8u16(s)
	return w
}
