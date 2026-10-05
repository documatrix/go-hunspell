package core

import (
	"strings"
	"unsafe"
)

// AffEntry options
const (
	aeXProduct      = 1 << 0
	aeUTF8          = 1 << 1
	aeAliasF        = 1 << 2
	aeAliasM        = 1 << 3
	aeLongCond      = 1 << 4
	aeRedundantCond = 1 << 5 // the condition was dropped as forced by the stripping
)

// compound options
const (
	inCpdNot   = 0
	inCpdBegin = 1
	inCpdEnd   = 2
	inCpdOther = 3
)

// affEntry is the part a prefix and a suffix entry share.
type affEntry struct {
	appnd      string
	strip      string
	conds      string
	numconds   int
	morphcode  string
	hasMorph   bool
	contclass  []uint16 // nil when the rule has no continuation classes
	line       int
	headerline int
	aflag      uint16
	opts       byte
	xprod      byte
}

// nextchar moves on to the next character of the condition, or gives -1 at
// its end. (The C++ function also passes a NULL position on; the callers here
// only call it with a valid one.)
func (e *affEntry) nextchar(p int) int {
	p++
	if p >= len(e.conds) || e.conds[p] == 0 {
		return -1
	}
	return p
}

// skipToGroupEnd moves p forward to the closing ] of a group.
func (e *affEntry) skipToGroupEnd(p int) int {
	for p >= 0 && e.conds[p] != ']' {
		p = e.nextchar(p)
	}
	return p
}

func (e *affEntry) allowCross() bool { return e.opts&aeXProduct != 0 }

// getCondition returns the condition as the .aff file writes it.
func (e *affEntry) getCondition(suffix bool) string {
	if e.numconds == 0 {
		return "."
	}
	if !suffix {
		return e.conds
	}
	// a suffix condition is held back to front, so turn it around again
	b := []byte(e.conds)
	reverseCondition(b)
	result := []byte(reverseword(string(b)))
	// that leaves the negation of a group at the end of the group, so move
	// it back in front of the characters it negates
	open := -1
	for k := 0; k < len(result); k++ {
		if result[k] == '[' {
			open = k
		} else if result[k] == ']' && open >= 0 {
			if k > open+1 && result[k-1] == '^' {
				copy(result[open+2:k], result[open+1:k-1])
				result[open+1] = '^'
			}
			open = -1
		}
	}
	return string(result)
}

// PfxEntry is a prefix rule.
type PfxEntry struct {
	affEntry
	mgr    *AffixMgr
	next   *PfxEntry
	nexteq *PfxEntry
	nextne *PfxEntry
	flgnxt *PfxEntry
}

// SfxEntry is a suffix rule.
type SfxEntry struct {
	affEntry
	mgr    *AffixMgr
	rappnd string
	next   *SfxEntry
	nexteq *SfxEntry
	nextne *SfxEntry
	flgnxt *SfxEntry
}

// substrN is std::string(s, pos, n): n bytes of s from pos, clamped to the end.
func substrN(s string, pos, n int) string {
	if pos >= len(s) {
		return ""
	}
	if n < 0 || pos+n > len(s) {
		return s[pos:]
	}
	return s[pos : pos+n]
}

// scratch builds a+b in *buf and returns a string that views it. The
// string stays valid until the next use of buf, which is how the C++ code
// reuses its scratch strings: each kind of affix check has a buffer of its
// own, and none of them nests in another of its kind.
func scratch(buf *[]byte, a, b string) string {
	*buf = append(append((*buf)[:0], a...), b...)
	return unsafe.String(unsafe.SliceData(*buf), len(*buf))
}

// scratchResized builds std::string(word, start) resized to n, followed by
// b, in *buf.
func scratchResized(buf *[]byte, word string, start, n int, b string) string {
	*buf = append((*buf)[:0], substrN(word, start, n)...)
	for len(*buf) < n {
		*buf = append(*buf, 0)
	}
	*buf = append(*buf, b...)
	return unsafe.String(unsafe.SliceData(*buf), len(*buf))
}

// add adds the prefix to word, assuming the conditions hold.
func (e *PfxEntry) add(word string) string {
	word = cstr(word)
	l := len(word)
	if (l > len(e.strip) || (l == 0 && e.mgr.fullstrip)) && l >= e.numconds && e.testCondition(word) &&
		(e.strip == "" || (l >= len(e.strip) && strings.HasPrefix(word, e.strip))) {
		return e.appnd + word[len(e.strip):]
	}
	return ""
}

func (e *PfxEntry) testCondition(s string) bool {
	st := 0
	pos := -1 // group with pos input position
	neg := false
	ingroup := false
	if e.numconds == 0 {
		return true
	}
	utf8 := e.opts&aeUTF8 != 0
	p := 0
	for {
		c := e.conds[p]
		// (C++ also returns 1 at the NUL that ends the condition; nextchar
		// stops before it, and the lines of the affix file end at a NUL)
		switch {
		case c == '[':
			neg = false
			ingroup = false
			p = e.nextchar(p)
			pos = st
		case c == '^':
			p = e.nextchar(p)
			neg = true
		case c == ']':
			if neg == ingroup {
				return false
			}
			pos = -1
			p = e.nextchar(p)
			// skip the next character
			if !ingroup && st < len(s) {
				if utf8 {
					st = utf8Next(s, st)
				} else {
					st++
				}
			}
			if st == len(s) && p >= 0 {
				return false // word <= condition
			}
		case c == '.' && pos == -1:
			// dots are not metacharacters in groups: [.]
			if st == len(s) {
				return false // dot has no character to match
			}
			p = e.nextchar(p)
			if utf8 {
				st = utf8Next(s, st)
			} else {
				st++
			}
		default:
			if st < len(s) && s[st] == c {
				st++
				p = e.nextchar(p)
				if utf8 && s[st-1]&0x80 != 0 { // multibyte character
					for p >= 0 && isUTF8Cont(e.conds[p]) {
						if st >= len(s) || e.conds[p] != s[st] {
							if pos == -1 {
								return false
							}
							st = pos
							break
						}
						p = e.nextchar(p)
						st++
					}
					if pos != -1 && st != pos {
						ingroup = true
						p = e.skipToGroupEnd(p)
					}
				} else if pos != -1 {
					ingroup = true
					p = e.skipToGroupEnd(p)
				}
			} else if pos != -1 { // group
				p = e.nextchar(p)
			} else {
				return false
			}
		}
		if p < 0 {
			return true
		}
	}
}

// appliesTo decides whether this prefix may be applied to a dictionary
// entry on its own.
func (e *PfxEntry) appliesTo(he *hentry, needflag uint16, t *traceCtx) bool {
	a := e.mgr
	ok := testaff(he.astr, e.aflag)
	if t != nil {
		traceTest(t, "pfx-aflag", a, e.aflag, "dic", he.astr, passFail(ok, ""))
	}
	if !ok {
		return false
	}
	// forbid single prefixes with needaffix flag
	needaffix := a.needaffix
	ok = !testaff(e.contclass, needaffix)
	if t != nil && needaffix != 0 {
		traceTest(t, "needaffix", a, needaffix, "pfx-cont", e.contclass, passFail(ok, "fail, this prefix needs a further affix"))
	}
	if !ok {
		return false
	}
	// a circumfix prefix needs a matching circumfix suffix, so it cannot
	// stand on its own
	circumfix := a.circumfix
	ok = !testaff(e.contclass, circumfix)
	if t != nil && circumfix != 0 {
		out := "pass, prefix may stand alone"
		if !ok {
			out = "fail, a circumfix prefix needs its suffix"
		}
		traceTest(t, "circumfix", a, circumfix, "pfx-cont", e.contclass, out)
	}
	if !ok {
		return false
	}
	if needflag != 0 {
		inDic := testaff(he.astr, needflag)
		inCont := e.contclass != nil && testaff(e.contclass, needflag)
		if t != nil {
			if inCont && !inDic {
				traceTest(t, "needflag", a, needflag, "pfx-cont", e.contclass, "pass")
			} else {
				traceTest(t, "needflag", a, needflag, "dic", he.astr, passFail(inDic, "fail, the stem lacks the flag the caller asked for"))
			}
		}
		if !inDic && !inCont {
			return false
		}
	}
	return true
}

func passFail(ok bool, fail string) string {
	if ok {
		return "pass"
	}
	if fail == "" {
		return "fail"
	}
	return fail
}

func traceLookup(t *traceCtx, a *AffixMgr, word string, he *hentry) {
	if he != nil {
		trace(t, "lookup \"%s\" -> entry \"%s\" flags=%s", cstr(word), he.word, traceFlags(a, he.astr))
	} else {
		trace(t, "lookup \"%s\" -> no more homonyms", cstr(word))
	}
}

// checkword checks whether this prefix entry matches.
func (e *PfxEntry) checkword(word string, start, ln int, inCompound int, needflag uint16, tr *traceCtx) *hentry {
	a := e.mgr
	t := traceOn(tr)
	if t != nil {
		traceAffix(t, "pfx", a, &e.affEntry)
	}
	defer t.enter()()

	tmpl := ln - len(e.appnd)
	if tmpl > 0 || (tmpl == 0 && a.fullstrip) {
		// generate new root word by removing prefix and adding back any
		// characters that would have been stripped
		tmpword := scratch(&a.pfxWord, e.strip, substrN(word, start+len(e.appnd), tmpl))
		if t != nil {
			trace(t, "stem \"%s\"", cstr(tmpword))
		}
		passes := e.testCondition(tmpword)
		if t != nil {
			trace(t, "test condition cond=\"%s\" on \"%s\" -> %s", e.getCondition(false), cstr(tmpword), passFail(passes, ""))
		}
		if passes {
			tmpl += len(e.strip)
			if he := a.lookup(tmpword); he != nil {
				if t != nil {
					trace(t, "lookup \"%s\" -> entry \"%s\" flags=%s", cstr(tmpword), he.word, traceFlags(a, he.astr))
				}
				for he != nil {
					if e.appliesTo(he, needflag, t) {
						if t != nil {
							trace(t, "accept")
						}
						return he
					}
					he = he.nextHomonym
					if t != nil {
						traceLookup(t, a, tmpword, he)
					}
				}
			} else if t != nil {
				trace(t, "lookup \"%s\" -> miss", cstr(tmpword))
			}
			// prefix matched but no root word was found; if aeXPRODUCT is
			// allowed, try again but now cross checked combined with a suffix
			if e.opts&aeXProduct != 0 {
				if he := a.suffixCheck(tmpword, 0, tmpl, aeXProduct, e, tr, 0, needflag, inCompound, 0); he != nil {
					return he
				}
			}
		}
	} else if t != nil {
		trace(t, "test length have=%d -> fail, nothing would be left of the word", tmpl)
	}
	return nil
}

func (e *PfxEntry) checkTwosfx(word string, start, ln int, inCompound int, needflag uint16, tr *traceCtx) *hentry {
	a := e.mgr
	tmpl := ln - len(e.appnd)
	if (tmpl > 0 || (tmpl == 0 && a.fullstrip)) && tmpl+len(e.strip) >= e.numconds {
		tmpword := scratch(&a.pfxTwosfx, e.strip, substrN(word, start+len(e.appnd), tmpl))
		if e.testCondition(tmpword) {
			tmpl += len(e.strip)
			if e.opts&aeXProduct != 0 && inCompound != inCpdBegin {
				if he := a.suffixCheckTwosfx(tmpword, 0, tmpl, aeXProduct, e, tr, needflag); he != nil {
					return he
				}
			}
		}
	}
	return nil
}

func (e *PfxEntry) checkTwosfxMorph(word string, start, ln int, inCompound int, needflag uint16, tr *traceCtx) string {
	a := e.mgr
	tmpl := ln - len(e.appnd)
	if (tmpl > 0 || (tmpl == 0 && a.fullstrip)) && tmpl+len(e.strip) >= e.numconds {
		tmpword := scratch(&a.pfxTwosfx, e.strip, substrN(word, start+len(e.appnd), tmpl))
		if e.testCondition(tmpword) {
			tmpl += len(e.strip)
			if e.opts&aeXProduct != 0 && inCompound != inCpdBegin {
				return a.suffixCheckTwosfxMorph(tmpword, 0, tmpl, aeXProduct, e, tr, needflag)
			}
		}
	}
	return ""
}

func (e *PfxEntry) checkMorph(word string, start, ln int, inCompound int, needflag uint16, tr *traceCtx) string {
	a := e.mgr
	var result strings.Builder
	tmpl := ln - len(e.appnd)
	if (tmpl > 0 || (tmpl == 0 && a.fullstrip)) && tmpl+len(e.strip) >= e.numconds {
		tmpword := scratch(&a.pfxWord, e.strip, substrN(word, start+len(e.appnd), tmpl))
		if e.testCondition(tmpword) {
			tmpl += len(e.strip)
			if he := a.lookup(tmpword); he != nil {
				for he != nil {
					if e.appliesTo(he, needflag, nil) {
						if e.hasMorph {
							result.WriteByte(msepFld)
							result.WriteString(e.morphcode)
						} else {
							result.WriteString(e.appnd)
						}
						if !he.entryFind(morphStem) {
							result.WriteByte(msepFld)
							result.WriteString(morphStem)
							result.WriteString(he.word)
						}
						if _, ok := he.entryData(); ok {
							result.WriteByte(msepFld)
							result.WriteString(he.entryData2())
						} else {
							// return with debug information
							result.WriteByte(msepFld)
							result.WriteString(morphFlag)
							result.WriteString(a.encodeFlag(e.aflag))
						}
						result.WriteByte(msepRec)
					}
					he = he.nextHomonym
				}
			}
			if e.opts&aeXProduct != 0 && inCompound != inCpdBegin {
				result.WriteString(a.suffixCheckMorph(tmpword, 0, tmpl, aeXProduct, e, tr, 0, needflag, inCpdNot))
			}
		}
	}
	return result.String()
}

// add adds the suffix to the first ln bytes of word, assuming the
// conditions hold.
func (e *SfxEntry) add(word string, ln int) string {
	// ln is the length of the C string word: the callers pass words that a
	// lookup found, which hold no NUL, or words cut at their NUL
	word = cstr(word)
	if (ln > len(e.strip) || (ln == 0 && e.mgr.fullstrip)) && ln >= e.numconds && e.testCondition(word, ln) &&
		(e.strip == "" || (ln >= len(e.strip) && word[ln-len(e.strip):] == e.strip)) {
		return word[:ln-len(e.strip)] + e.appnd
	}
	return ""
}

func (e *SfxEntry) initReverseWord() {
	e.rappnd = reverseword(e.appnd)
}

// testCondition checks the condition against the end of s[:end].
func (e *SfxEntry) testCondition(s string, end int) bool {
	posSet := false
	pos := 0
	neg := false
	ingroup := false
	if e.numconds == 0 {
		return true
	}
	utf8 := e.opts&aeUTF8 != 0
	// st stays below end, and the checks below stop before it is read at
	// a negative position
	at := func(i int) byte { return s[i] }
	p := 0
	st := end - 1
	i := 1
	for {
		c := e.conds[p]
		// (C++ also returns 1 at the NUL that ends the condition; nextchar
		// stops before it, and the lines of the affix file end at a NUL)
		switch {
		case c == '[':
			p = e.nextchar(p)
			pos = st
			posSet = true
		case c == '^':
			p = e.nextchar(p)
			neg = true
		case c == ']':
			if !neg && !ingroup {
				return false
			}
			i++
			// skip the next character
			if !ingroup {
				for utf8 && st >= 0 && isUTF8Cont(at(st)) {
					st--
				}
				st--
			}
			posSet = false
			neg = false
			ingroup = false
			p = e.nextchar(p)
			if st < 0 && p >= 0 {
				return false // word <= condition
			}
		case c == '.' && !posSet:
			// dots are not metacharacters in groups: [.]
			p = e.nextchar(p)
			// skip one character to the left
			for utf8 && st >= 0 && isUTF8Cont(at(st)) {
				st--
			}
			st--
			if st < 0 { // word <= condition
				return p < 0
			}
		default:
			if at(st) == c {
				p = e.nextchar(p)
				if utf8 && at(st)&0x80 != 0 {
					st--
					for p >= 0 && st >= 0 {
						if e.conds[p] != at(st) {
							if !posSet {
								return false
							}
							st = pos
							break
						}
						// first byte of the UTF-8 multibyte character
						if !isUTF8Cont(e.conds[p]) {
							break
						}
						p = e.nextchar(p)
						st--
					}
					if posSet && st != pos {
						if neg {
							return false
						} else if i == e.numconds {
							return true
						}
						ingroup = true
						p = e.skipToGroupEnd(p)
						st--
					}
					if p >= 0 && e.conds[p] != ']' {
						p = e.nextchar(p)
					}
				} else if posSet {
					if neg {
						return false
					} else if i == e.numconds {
						return true
					}
					ingroup = true
					p = e.skipToGroupEnd(p)
					st--
				}
				if !posSet {
					i++
					st--
				}
				if st < 0 && p >= 0 && e.conds[p] != ']' {
					return false // word <= condition
				}
			} else if posSet { // group
				p = e.nextchar(p)
			} else {
				return false
			}
		}
		if p < 0 {
			return true
		}
	}
}

// appliesTo decides whether this suffix may be applied to a dictionary entry.
func (e *SfxEntry) appliesTo(he *hentry, optflags int, ep *PfxEntry, cclass, needflag, badflag uint16, t *traceCtx) bool {
	a := e.mgr
	// the suffix flag is either on the entry itself, or on a prefix that
	// enables this suffix
	inDic := testaff(he.astr, e.aflag)
	inPrefix := ep != nil && ep.contclass != nil && testaff(ep.contclass, e.aflag)
	if t != nil {
		if inPrefix && !inDic {
			traceTest(t, "sfx-aflag", a, e.aflag, "pfx-cont", ep.contclass, "pass, the prefix enables this suffix")
		} else {
			traceTest(t, "sfx-aflag", a, e.aflag, "dic", he.astr, passFail(inDic, ""))
		}
	}
	if !inDic && !inPrefix {
		return false
	}
	// the prefix and the suffix have to be allowed to meet
	if optflags&aeXProduct != 0 {
		var pflag uint16
		if ep != nil {
			pflag = ep.aflag
		}
		inDic = ep != nil && testaff(he.astr, pflag)
		inCont := e.contclass != nil && ep != nil && testaff(e.contclass, pflag)
		if t != nil {
			if inCont && !inDic {
				traceTest(t, "xprod", a, pflag, "sfx-cont", e.contclass, "pass, this suffix enables the prefix")
			} else {
				traceTest(t, "xprod", a, pflag, "dic", he.astr, passFail(inDic, "fail, the stem does not take the prefix as well"))
			}
		}
		if !inDic && !inCont {
			return false
		}
	}
	// handle cont. class
	if cclass != 0 {
		ok := e.contclass != nil && testaff(e.contclass, cclass)
		if t != nil {
			traceTest(t, "cclass", a, cclass, "sfx-cont", e.contclass, passFail(ok, "fail, this suffix does not continue the last one"))
		}
		if !ok {
			return false
		}
	}
	// check only in compound homonyms (bad flags)
	if badflag != 0 {
		ok := !testaff(he.astr, badflag)
		if t != nil {
			traceTest(t, "badflag", a, badflag, "dic", he.astr, passFail(ok, "fail, the stem has a flag this context forbids"))
		}
		if !ok {
			return false
		}
	}
	// handle required flag
	if needflag != 0 {
		inDic = testaff(he.astr, needflag)
		inCont := e.contclass != nil && testaff(e.contclass, needflag)
		if t != nil {
			if inCont && !inDic {
				traceTest(t, "needflag", a, needflag, "sfx-cont", e.contclass, "pass")
			} else {
				traceTest(t, "needflag", a, needflag, "dic", he.astr, passFail(inDic, "fail, the stem lacks the flag the caller asked for"))
			}
		}
		if !inDic && !inCont {
			return false
		}
	}
	return true
}

// checkword checks whether this suffix is present in the word.
func (e *SfxEntry) checkword(word string, start, ln int, optflags int, ppfx *PfxEntry, cclass, needflag, badflag uint16, tr *traceCtx) *hentry {
	a := e.mgr
	t := traceOn(tr)
	if t != nil {
		traceAffix(t, "sfx", a, &e.affEntry)
	}
	defer t.enter()()

	// if this suffix is being cross checked with a prefix but it does not
	// support cross products skip it
	if optflags&aeXProduct != 0 && e.opts&aeXProduct == 0 {
		if t != nil {
			trace(t, "test xprod -> fail, this suffix class does not cross with a prefix")
		}
		return nil
	}
	tmpl := ln - len(e.appnd)
	if (tmpl > 0 || (tmpl == 0 && a.fullstrip)) && tmpl+len(e.strip) >= e.numconds {
		// generate new root word by removing suffix and adding back any
		// characters that would have been stripped
		tmpword := scratch(&a.sfxWord, substrN(word, start, tmpl), e.strip)
		if t != nil {
			trace(t, "stem \"%s\"", cstr(tmpword))
		}
		passes := e.testCondition(tmpword, len(tmpword))
		if t != nil {
			trace(t, "test condition cond=\"%s\" on \"%s\" -> %s", e.getCondition(true), cstr(tmpword), passFail(passes, ""))
		}
		if passes {
			if he := a.lookup(tmpword); he != nil {
				if t != nil {
					trace(t, "lookup \"%s\" -> entry \"%s\" flags=%s", cstr(tmpword), he.word, traceFlags(a, he.astr))
				}
				for he != nil {
					if e.appliesTo(he, optflags, ppfx, cclass, needflag, badflag, t) {
						if t != nil {
							trace(t, "accept")
						}
						return he
					}
					he = he.nextHomonym
					if t != nil {
						traceLookup(t, a, tmpword, he)
					}
				}
			} else if t != nil {
				trace(t, "lookup \"%s\" -> miss", cstr(tmpword))
			}
		}
	} else if t != nil {
		trace(t, "test length have=%d need=%d -> fail, too little is left of the word to test", tmpl, e.numconds)
	}
	return nil
}

// checkTwosfx checks whether a two-level suffix is present in the word.
func (e *SfxEntry) checkTwosfx(word string, start, ln int, optflags int, ppfx *PfxEntry, needflag uint16, tr *traceCtx) *hentry {
	a := e.mgr
	if optflags&aeXProduct != 0 && e.opts&aeXProduct == 0 {
		return nil
	}
	tmpl := ln - len(e.appnd)
	if (tmpl > 0 || (tmpl == 0 && a.fullstrip)) && tmpl+len(e.strip) >= e.numconds {
		tmpword := scratchResized(&a.sfxTwosfx, word, start, tmpl, e.strip)
		tmpl += len(e.strip)
		if e.testCondition(tmpword, tmpl) {
			var he *hentry
			if ppfx != nil {
				// handle conditional suffix
				if e.contclass != nil && testaff(e.contclass, ppfx.aflag) {
					he = a.suffixCheck(tmpword, 0, tmpl, 0, nil, tr, e.aflag, needflag, inCpdNot, 0)
				} else {
					he = a.suffixCheck(tmpword, 0, tmpl, optflags, ppfx, tr, e.aflag, needflag, inCpdNot, 0)
				}
			} else {
				he = a.suffixCheck(tmpword, 0, tmpl, 0, nil, tr, e.aflag, needflag, inCpdNot, 0)
			}
			if he != nil {
				return he
			}
		}
	}
	return nil
}

func (e *SfxEntry) checkTwosfxMorph(word string, start, ln int, optflags int, ppfx *PfxEntry, needflag uint16, tr *traceCtx) string {
	a := e.mgr
	result := ""
	if optflags&aeXProduct != 0 && e.opts&aeXProduct == 0 {
		return result
	}
	tmpl := ln - len(e.appnd)
	if (tmpl > 0 || (tmpl == 0 && a.fullstrip)) && tmpl+len(e.strip) >= e.numconds {
		tmpword := scratchResized(&a.sfxTwosfx, word, start, tmpl, e.strip)
		tmpl += len(e.strip)
		if e.testCondition(tmpword, tmpl) {
			if ppfx != nil {
				// handle conditional suffix
				if e.contclass != nil && testaff(e.contclass, ppfx.aflag) {
					st := a.suffixCheckMorph(tmpword, 0, tmpl, 0, nil, tr, e.aflag, needflag, inCpdNot)
					if st != "" {
						if ppfx.hasMorph {
							result += ppfx.morphcode
							result += string(msepFld)
						}
						result += st
						result = mychomp(result)
					}
				} else {
					st := a.suffixCheckMorph(tmpword, 0, tmpl, optflags, ppfx, tr, e.aflag, needflag, inCpdNot)
					if st != "" {
						result += st
						result = mychomp(result)
					}
				}
			} else {
				st := a.suffixCheckMorph(tmpword, 0, tmpl, 0, nil, tr, e.aflag, needflag, inCpdNot)
				if st != "" {
					result += st
					result = mychomp(result)
				}
			}
		}
	}
	return result
}

// getNextHomonym returns the next homonym of he with the same affix.
func (e *SfxEntry) getNextHomonym(he *hentry, optflags int, ppfx *PfxEntry, cclass, needflag uint16) *hentry {
	var eFlag uint16
	if ppfx != nil {
		eFlag = ppfx.aflag
	}
	for he.nextHomonym != nil {
		he = he.nextHomonym
		if (testaff(he.astr, e.aflag) || (ppfx != nil && ppfx.contclass != nil && testaff(ppfx.contclass, e.aflag))) &&
			(optflags&aeXProduct == 0 || testaff(he.astr, eFlag) ||
				// handle conditional suffix
				(e.contclass != nil && testaff(e.contclass, eFlag))) &&
			// handle cont. class
			(cclass == 0 || (e.contclass != nil && testaff(e.contclass, cclass))) &&
			// handle required flag
			(needflag == 0 || (testaff(he.astr, needflag) || (e.contclass != nil && testaff(e.contclass, needflag)))) {
			return he
		}
	}
	return nil
}
