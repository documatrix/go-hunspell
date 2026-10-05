package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	spellXML               = "<?xml?>"
	maxSuggestion          = 15
	maxSharps              = 5
	maxBreakDepth          = 10
	maxSpellMLLen          = 8192
	maxCandidateStackDepth = 2048
)

// Hunspell is a spell checker over an .aff file and one or more .dic files.
// All words it takes and gives back are in the encoding of the dictionary.
// It is not safe for concurrent use.
type Hunspell struct {
	hmgrs           []*HashMgr
	trace           *traceCtx
	amgr            *AffixMgr
	smgr            *SuggestMgr
	affix           Source
	affixKey        string
	encoding        string
	csconv          *[256]csInfo
	langnum         int
	utf8            bool
	complexprefixes bool
	wordbreak       []string
	errs            []string
	spellStack      []string // the candidate stack of Spell, reused
	warns           []string
	rwords          [100]*hentry // buffer for COMPOUND pattern checking
}

// New loads a dictionary. Problems with the files do not stop the loading;
// they are reported by Errors, the way Hunspell prints them on stderr.
func New(aff, dic Source, key string) *Hunspell {
	h := &Hunspell{trace: &traceCtx{}, affix: aff, affixKey: key}
	// first set up the hash manager
	h.hmgrs = append(h.hmgrs, newHashMgr(dic, aff, key, &h.errs, &h.warns))
	// next set up the affix manager, it needs access to the hash manager
	// lookup methods
	h.amgr = newAffixMgr(aff, &h.hmgrs, key, &h.errs, &h.warns)
	// get the preferred try string and the dictionary encoding from the
	// affix manager for that dictionary
	tryString := h.amgr.trystring
	h.encoding = h.amgr.getEncoding()
	h.langnum = h.amgr.langnum
	h.utf8 = h.amgr.utf8
	if !h.utf8 {
		h.csconv = getCurrentCS(h.encoding, h)
	}
	h.complexprefixes = h.amgr.complexprefixes
	h.wordbreak = h.amgr.breaktable
	// and finally set up the suggestion manager
	h.smgr = newSuggestMgr(tryString, maxSuggestion, h.amgr)
	return h
}

// Errors returns the error messages of the loading, as the C++ library
// prints them on stderr.
func (h *Hunspell) Errors() []string { return h.errs }

// Warnings returns the diagnostics of the .aff and .dic parsers that only
// the debug builds of the C++ library print (HUNSPELL_WARNING).
func (h *Hunspell) Warnings() []string { return h.warns }

// AddDic loads an extra dictionary file with the affix rules of the first.
func (h *Hunspell) AddDic(dic Source, key string) int {
	// the affix file is read again for this dictionary, so it needs its key
	if key == "" {
		key = h.affixKey
	}
	h.hmgrs = append(h.hmgrs, newHashMgr(dic, h.affix, key, &h.errs, &h.warns))
	return 0
}

// SetTrace reports each decision Spell makes to fn, or stops with nil.
func (h *Hunspell) SetTrace(fn TraceFunc) {
	h.trace.callback = fn
}

func (h *Hunspell) activeTrace() *traceCtx {
	if h.trace.on() {
		return h.trace
	}
	return nil
}

func (h *Hunspell) expired(start time.Time) bool {
	return !start.IsZero() && h.amgr.limits.global != noLimit && since(start) > h.amgr.limits.global
}

// noLimit is the time limit that is not one.
const noLimit = time.Duration(1<<63 - 1)

// SetTimeLimits sets the time limits of Hunspell: the time of a whole
// suggestion search or generation (250 ms by default), of one group of
// suggestions (100 ms) and of the compound word checks within them (50 ms).
// A limit of 0 removes it.
func (h *Hunspell) SetTimeLimits(global, suggestion, compound time.Duration) {
	inf := func(d time.Duration) time.Duration {
		if d <= 0 {
			return noLimit
		}
		return d
	}
	h.amgr.limits = timeLimits{inf(global), inf(suggestion), inf(compound)}
}

// cleanIgnore removes the characters of the IGNORE rule.
func (h *Hunspell) cleanIgnore(src string) string {
	if h.amgr != nil && h.amgr.ignorechars != "" {
		if h.utf8 {
			d, _ := removeIgnoredCharsUTF(src, h.amgr.ignorecharsUTF16)
			return d
		}
		return removeIgnoredChars(src, h.amgr.ignorechars)
	}
	return src
}

// cleanword2 removes the ignored characters, the leading blanks and the
// trailing periods (counted in abbrev) of src, and sets its capitalization.
func (h *Hunspell) cleanword2(src string) (dest string, destUTF []uint16, captype int, abbrev int) {
	w2 := h.cleanIgnore(src)
	q := 0
	nl := len(w2)
	// first skip over any leading blanks
	for byteAt(w2, q) == ' ' {
		q++
		nl--
	}
	// now strip off any trailing periods (recording their presence)
	for nl > 0 && w2[q+nl-1] == '.' {
		nl--
		abbrev++
	}
	// if no characters are left it can't be capitalized
	if nl <= 0 {
		return "", nil, noCap, abbrev
	}
	dest = w2[q : q+nl]
	if h.utf8 {
		destUTF, _ = u8u16(dest)
		captype = getCaptypeUTF8(destUTF, h.langnum)
	} else {
		captype = getCaptype(dest, h.csconv)
	}
	return dest, destUTF, captype, abbrev
}

// cleanword is the old variant of cleanword2 used by generate.
func (h *Hunspell) cleanword(src string) (string, int) {
	q := 0
	nl := len(src)
	for byteAt(src, q) == ' ' {
		q++
		nl--
	}
	for nl > 0 && src[q+nl-1] == '.' {
		nl--
	}
	if nl <= 0 {
		return "", noCap
	}
	ncap, nneutral, nc := 0, 0, 0
	firstcap := false
	var dest string
	if !h.utf8 {
		d := src[q : q+nl]
		for i := 0; i < len(d); i++ {
			nc++
			if h.csconv[d[i]].ccase != 0 {
				ncap++
			}
			if h.csconv[d[i]].cupper == h.csconv[d[i]].clower {
				nneutral++
			}
		}
		dest = d
		firstcap = h.csconv[dest[0]].ccase != 0
	} else {
		t, _ := u8u16(src)
		for _, idx := range t {
			low := unicodetolower(idx, h.langnum)
			if idx != low {
				ncap++
			}
			if unicodetoupper(idx, h.langnum) == low {
				nneutral++
			}
		}
		dest = u16u8(t)
		if ncap > 0 {
			firstcap = t[0] != unicodetolower(t[0], h.langnum)
		}
	}
	switch {
	case ncap == 0:
		return dest, noCap
	case ncap == 1 && firstcap:
		return dest, initCap
	case ncap == nc || ncap+nneutral == nc:
		return dest, allCap
	case ncap > 1 && firstcap:
		return dest, huhInitCap
	}
	return dest, huhCap
}

func (h *Hunspell) mkallcap(u8 string) string {
	if h.utf8 {
		u, _ := u8u16(u8)
		mkallcapUTF(u, h.langnum)
		return u16u8(u)
	}
	return mkallcap(u8, h.csconv)
}

func (h *Hunspell) mkinitcap(u8 string) string {
	if h.utf8 {
		u, _ := u8u16(u8)
		mkinitcapUTF(u, h.langnum)
		return u16u8(u)
	}
	return mkinitcap(u8, h.csconv)
}

// mkallsmall2 lowers u16 in place (UTF-8) or u8 (8-bit) and returns the new u8.
func (h *Hunspell) mkallsmall2(u8 string, u16 []uint16) string {
	if h.utf8 {
		mkallsmallUTF(u16, h.langnum)
		return u16u8(u16)
	}
	return mkallsmall(u8, h.csconv)
}

func (h *Hunspell) mkinitcap2(u8 string, u16 []uint16) string {
	if h.utf8 {
		mkinitcapUTF(u16, h.langnum)
		return u16u8(u16)
	}
	return mkinitcap(u8, h.csconv)
}

func (h *Hunspell) mkinitsmall2(u8 string, u16 []uint16) string {
	if h.utf8 {
		mkinitsmallUTF(u16, h.langnum)
		return u16u8(u16)
	}
	return mkinitsmall(u8, h.csconv)
}

// spellsharps is the recursive search for the right ss - sharp s permutations.
func (h *Hunspell) spellsharps(base []byte, nPos, n, repnum int, info *int, root *string, start time.Time) *hentry {
	pos := strIndexFrom(string(base), "ss", nPos)
	if pos >= 0 && n < maxSharps {
		base[pos] = 0xC3
		base[pos+1] = 0x9F
		if he := h.spellsharps(base, pos+2, n+1, repnum+1, info, root, start); he != nil {
			return he
		}
		base[pos] = 's'
		base[pos+1] = 's'
		if he := h.spellsharps(base, pos+2, n+1, repnum, info, root, start); he != nil {
			return he
		}
	} else if repnum > 0 {
		if h.utf8 {
			return h.checkword(string(base), info, root, start)
		}
		// convert UTF-8 sharp S codes to latin 1
		return h.checkword(mystrrep(string(base), "\xC3\x9F", "\xDF"), info, root, start)
	}
	return nil
}

func (h *Hunspell) isKeepcase(rv *hentry) bool {
	return h.amgr != nil && rv.astr != nil && h.amgr.keepcase != 0 && testaff(rv.astr, h.amgr.keepcase)
}

// Spell checks a word. info receives the SPELL_* bits and root the stem of
// an affixed word. Like the C++ spell (whose start time defaults to
// time_point::max()), it has no global time limit; the zero start means
// none.
func (h *Hunspell) Spell(word string, info *int, root *string) bool {
	h.spellStack = h.spellStack[:0]
	return h.spell(word, &h.spellStack, info, root, time.Time{})
}

func (h *Hunspell) spell(word string, stack *[]string, info *int, root *string, start time.Time) bool {
	// something very broken if spell ends up calling itself with the same word
	if contains(*stack, word) {
		return false
	}
	if len(*stack) >= maxBreakDepth {
		return false
	}
	if h.expired(start) {
		return false
	}
	t := h.activeTrace()
	if t != nil {
		trace(t, "word \"%s\"", cstr(word))
	}
	// A word split on a BREAK pattern nests its parts a further level in.
	leave := t.enter()
	*stack = append(*stack, word)
	r := h.spellInternal(word, stack, info, root, start)
	*stack = (*stack)[:len(*stack)-1]
	if t != nil {
		res := "incorrect"
		if r {
			res = "correct"
		}
		trace(t, "result %s", res)
	}
	leave()
	if r && root != nil {
		// output conversion
		if rl := h.amgr.oconvtable; rl != nil {
			if ws, ok := rl.conv(*root, -1); ok {
				*root = ws
			}
		}
	}
	return r
}

func (h *Hunspell) spellInternal(word string, stack *[]string, info *int, root *string, start time.Time) bool {
	var rv *hentry
	info2 := 0
	if info == nil {
		info = &info2
	} else {
		*info = 0
	}
	// Hunspell supports XML input of the simplified API (see manual)
	if word == spellXML {
		return true
	}
	if h.utf8 {
		if len(word) >= maxWordUTF8Len {
			return false
		}
	} else if len(word) >= maxWordLen {
		return false
	}
	var scw string
	var sunicw []uint16
	var captype, abbv int
	// input conversion
	if rl := h.amgr.iconvtable; rl != nil {
		if ws, ok := rl.conv(word, -1); ok {
			// the input conversion can grow the word without limit, so hold the
			// converted word to the same maximum length as the raw word
			if (h.utf8 && len(ws) >= maxWordUTF8Len) || (!h.utf8 && len(ws) >= maxWordLen) {
				return false
			}
			scw, sunicw, captype, abbv = h.cleanword2(ws)
		} else {
			scw, sunicw, captype, abbv = h.cleanword2(word)
		}
	} else {
		scw, sunicw, captype, abbv = h.cleanword2(word)
	}
	wl := len(scw)
	if wl == 0 || len(h.hmgrs) == 0 {
		return true
	}
	if root != nil {
		*root = ""
	}

	// allow numbers with dots, dashes and commas (but forbid double
	// separators: "..", "--" etc.), also in the Arabic-Indic and Extended
	// Arabic-Indic (Persian) digit ranges
	const (
		nBegin = iota
		nNum
		nSep
	)
	nstate := nBegin
	n := wl
	if h.utf8 {
		n = len(sunicw)
	}
	i := 0
	for ; i < n; i++ {
		var c uint16
		if h.utf8 {
			c = sunicw[i]
		} else {
			c = uint16(scw[i])
		}
		if (c >= '0' && c <= '9') || (c >= 0x0660 && c <= 0x0669) || (c >= 0x06F0 && c <= 0x06F9) {
			nstate = nNum
		} else if c == ',' || c == '.' || c == '-' {
			if nstate == nSep || i == 0 {
				break
			}
			nstate = nSep
		} else {
			break
		}
	}
	if i == n && nstate == nNum {
		return true
	}

	switch captype {
	case huhCap, huhInitCap, noCap:
		if captype != noCap {
			*info |= SpellOrigCap
		}
		rv = h.checkword(scw, info, root, start)
		if abbv != 0 && rv == nil {
			rv = h.checkword(scw+".", info, root, start)
		}
		// A stem with an inner capital, capitalised at the start of a sentence
		// (uLinda written ULinda), is found by lowering only the first letter.
		if rv == nil && captype == huhInitCap {
			u16 := append([]uint16(nil), sunicw...)
			u8 := h.mkinitsmall2(scw, u16)
			rv = h.checkword(u8, info, root, start)
			if rv != nil && h.isKeepcase(rv) {
				rv = nil
			}
		}
	case allCap, initCap:
		done := false
		if captype == allCap {
			*info |= SpellOrigCap
			rv = h.checkword(scw, info, root, start)
			if rv != nil {
				done = true
			}
			if !done && abbv != 0 {
				rv = h.checkword(scw+".", info, root, start)
				if rv != nil {
					done = true
				}
			}
			// Spec. prefix handling for Catalan, French, Italian: prefixes
			// separated by apostrophe (SANT'ELIA -> Sant'+Elia).
			if !done {
				if apos := strings.IndexByte(scw, '\''); apos >= 0 {
					scw = h.mkallsmall2(scw, sunicw)
					// conversion may result in string with different len to
					// pre-mkallsmall2 so re-scan
					if apos < len(scw)-1 {
						part1, part2 := scw[:apos+1], scw[apos+1:]
						if h.utf8 {
							part1u, _ := u8u16(part1)
							part2u, _ := u8u16(part2)
							part2 = h.mkinitcap2(part2, part2u)
							scw = part1 + part2
							sunicw = append(part1u, part2u...)
						} else {
							part2 = h.mkinitcap2(part2, sunicw)
							scw = part1 + part2
						}
						rv = h.checkword(scw, info, root, start)
						if rv != nil {
							done = true
						}
						if !done {
							scw = h.mkinitcap2(scw, sunicw)
							rv = h.checkword(scw, info, root, start)
							if rv != nil {
								done = true
							}
						}
					}
				}
			}
			if !done && h.amgr.checksharps && strings.Contains(scw, "SS") {
				scw = h.mkallsmall2(scw, sunicw)
				u8buffer := []byte(scw)
				rv = h.spellsharps(u8buffer, 0, 0, 0, info, root, start)
				if rv == nil {
					scw = h.mkinitcap2(scw, sunicw)
					rv = h.spellsharps([]byte(scw), 0, 0, 0, info, root, start)
				}
				if abbv != 0 && rv == nil {
					u8buffer = append(u8buffer, '.')
					rv = h.spellsharps(u8buffer, 0, 0, 0, info, root, start)
					if rv == nil {
						rv = h.spellsharps([]byte(scw+"."), 0, 0, 0, info, root, start)
					}
				}
				if rv != nil {
					done = true
				}
			}
		}
		if done {
			break
		}
		// handle special capitalization of dotted I
		idot := h.utf8 && byteAt(scw, 0) == 0xc4 && byteAt(scw, 1) == 0xb0
		*info |= SpellOrigCap
		if captype == allCap {
			scw = h.mkallsmall2(scw, sunicw)
			scw = h.mkinitcap2(scw, sunicw)
			if idot {
				scw = "\xc4\xb0" + scw[1:]
			}
		}
		if captype == initCap {
			*info |= SpellInitCap
		}
		rv = h.checkword(scw, info, root, start)
		if captype == initCap {
			*info &^= SpellInitCap
		}
		// forbid bad capitalization (for example, ijs -> Ijs instead of IJs in
		// Dutch); use explicit forms in dic: Ijs/F (F = FORBIDDENWORD flag)
		if *info&SpellForbidden != 0 {
			rv = nil
			break
		}
		if rv != nil && h.isKeepcase(rv) && captype == allCap {
			rv = nil
		}
		if rv != nil || (idot && h.langnum != langAz && h.langnum != langTr && h.langnum != langCrh) {
			break
		}
		scw = h.mkallsmall2(scw, sunicw)
		u8buffer := scw
		scw = h.mkinitcap2(scw, sunicw)
		rv = h.checkword(u8buffer, info, root, start)
		if abbv != 0 && rv == nil {
			u8buffer += "."
			rv = h.checkword(u8buffer, info, root, start)
			if rv == nil {
				u8buffer = scw + "."
				if captype == initCap {
					*info |= SpellInitCap
				}
				rv = h.checkword(u8buffer, info, root, start)
				if captype == initCap {
					*info &^= SpellInitCap
				}
				if rv != nil && h.isKeepcase(rv) && captype == allCap {
					rv = nil
				}
				break
			}
		}
		if rv != nil && h.isKeepcase(rv) &&
			(captype == allCap ||
				// if CHECKSHARPS: KEEPCASE words with \xDF are allowed in INITCAP form, too.
				!(h.amgr.checksharps &&
					((h.utf8 && strings.Contains(u8buffer, "\xC3\x9F")) || (!h.utf8 && strings.IndexByte(u8buffer, 0xDF) >= 0)))) {
			rv = nil
		}
	}

	if rv != nil {
		if h.amgr.warn != 0 && rv.astr != nil && testaff(rv.astr, h.amgr.warn) {
			*info |= SpellWarn
			if h.amgr.forbidwarn {
				return false
			}
			return true
		}
		return true
	}

	// recursive breaking at break points
	if len(h.wordbreak) > 0 && *info&SpellForbidden == 0 {
		nbr := 0
		wl = len(scw)
		// calculate break points for recursion limit
		for _, j := range h.wordbreak {
			pos := 0
			for {
				pos = strIndexFrom(scw, j, pos)
				if pos < 0 {
					break
				}
				nbr++
				pos += len(j)
			}
		}
		if nbr >= maxBreakDepth {
			return false
		}
		// check boundary patterns (^begin and end$)
		for _, j := range h.wordbreak {
			plen := len(j)
			if plen == 1 || plen > wl {
				continue
			}
			if j[0] == '^' && scw[:plen-1] == j[1:] && h.spell(scw[plen-1:], stack, nil, nil, start) {
				*info |= SpellCompound
				return true
			}
			if j[plen-1] == '$' && scw[wl-plen+1:] == j[:plen-1] {
				if h.spell(scw[:wl-plen+1], stack, nil, nil, start) {
					*info |= SpellCompound
					return true
				}
			}
		}
		// other patterns
		for _, j := range h.wordbreak {
			plen := len(j)
			found := strings.Index(scw, j)
			if found > 0 && found < wl-plen {
				found2 := strIndexFrom(scw, j, found+1)
				// try to break at the second occurance to recognize
				// dictionary words with wordbreak
				if found2 > 0 && found2 < wl-plen {
					found = found2
				}
				if !h.spell(scw[found+plen:], stack, nil, nil, start) {
					continue
				}
				// examine 2 sides of the break point
				if h.spell(scw[:found], stack, nil, nil, start) {
					*info |= SpellCompound
					return true
				}
				// LANG_hu: spec. dash rule
				if h.langnum == langHu && j == "-" {
					if h.spell(scw[:found+1], stack, nil, nil, start) {
						*info |= SpellCompound
						return true // check the first part with dash
					}
				}
			}
		}
		// other patterns (break at first break point)
		for _, j := range h.wordbreak {
			plen := len(j)
			found := strings.Index(scw, j)
			if found > 0 && found < wl-plen {
				if !h.spell(scw[found+plen:], stack, nil, nil, start) {
					continue
				}
				if h.spell(scw[:found], stack, nil, nil, start) {
					*info |= SpellCompound
					return true
				}
				if h.langnum == langHu && j == "-" {
					if h.spell(scw[:found+1], stack, nil, nil, start) {
						*info |= SpellCompound
						return true
					}
				}
			}
		}
	}
	return false
}

func (h *Hunspell) checkword(w string, info *int, root *string, start time.Time) *hentry {
	// check overall suggest time limit
	if h.expired(start) {
		return nil
	}
	// remove IGNORE characters from the string
	word := h.cleanIgnore(w)
	if word == "" {
		return nil
	}
	// word reversing wrapper for complex prefixes
	if h.complexprefixes {
		if h.utf8 {
			word = reversewordUTF(word)
		} else {
			word = reverseword(word)
		}
	}
	ln := len(word)
	a := h.amgr
	var he *hentry
	t := h.activeTrace()
	for i := 0; i < len(h.hmgrs) && he == nil; i++ {
		he = h.hmgrs[i].lookup(word)
		if t != nil {
			dic := ""
			if len(h.hmgrs) > 1 {
				dic = " dic=" + strconv.Itoa(i)
			}
			if he != nil {
				trace(t, "lookup \"%s\"%s -> entry \"%s\" flags=%s", cstr(word), dic, he.word, traceFlags(a, he.astr))
			} else {
				trace(t, "lookup \"%s\"%s -> miss", cstr(word), dic)
			}
		}
		// check forbidden and onlyincompound words
		if he != nil && he.astr != nil && testaff(he.astr, a.forbiddenword) {
			if t != nil {
				trace(t, "test forbidden flag=%s in=dic have=%s -> fail, the word is forbidden",
					a.encodeFlag(a.forbiddenword), traceFlags(a, he.astr))
			}
			if info != nil {
				*info |= SpellForbidden
			}
			// LANG_hu section: set dash information for suggestions
			if h.langnum == langHu && a.compoundflag != 0 && testaff(he.astr, a.compoundflag) && info != nil {
				*info |= SpellCompound
			}
			return nil
		}
		// he = next not needaffix, onlyincompound homonym or onlyupcase word
		firstHe := he
		for he != nil && he.astr != nil &&
			((a.needaffix != 0 && testaff(he.astr, a.needaffix)) ||
				(a.onlyincompound != 0 && testaff(he.astr, a.onlyincompound)) ||
				(info != nil && *info&SpellInitCap != 0 && testaff(he.astr, onlyUpcaseFlag))) {
			if t != nil {
				// Name the branch that fired, in the order the condition tests them.
				reason := "onlyupcase"
				flag := uint16(onlyUpcaseFlag)
				if a.needaffix != 0 && testaff(he.astr, a.needaffix) {
					reason = "needaffix"
					flag = a.needaffix
				} else if a.onlyincompound != 0 && testaff(he.astr, a.onlyincompound) {
					reason = "onlyincompound"
					flag = a.onlyincompound
				}
				trace(t, "test %s flag=%s in=dic have=%s -> fail, on to the next homonym",
					reason, traceFlag(a, flag), traceFlags(a, he.astr))
			}
			he = he.nextHomonym
		}
		// Say which entry the walk left in hand.
		if t != nil && he != firstHe {
			if he != nil {
				trace(t, "lookup \"%s\" -> entry \"%s\" flags=%s", cstr(word), he.word, traceFlags(a, he.astr))
			} else {
				trace(t, "lookup \"%s\" -> no more homonyms", cstr(word))
			}
		}
	}

	// check with affixes
	if he == nil && a != nil {
		// try stripping off affixes
		tr := t
		// For an initial-capital query, make affix_check skip an
		// all-uppercase-only stem and keep looking, so a valid mixed-case
		// match behind it is found.
		var avoidflag uint16
		if info != nil && *info&SpellInitCap != 0 {
			avoidflag = onlyUpcaseFlag
		}
		he = a.affixCheck(word, 0, ln, tr, 0, inCpdNot, avoidflag, nil, nil)

		// check compound restriction and onlyupcase
		if he != nil && he.astr != nil &&
			((a.onlyincompound != 0 && testaff(he.astr, a.onlyincompound)) ||
				(info != nil && *info&SpellInitCap != 0 && testaff(he.astr, onlyUpcaseFlag))) {
			he = nil
		}
		if he != nil {
			if he.astr != nil && testaff(he.astr, a.forbiddenword) {
				if info != nil {
					*info |= SpellForbidden
				}
				return nil
			}
			if root != nil {
				*root = h.reverseIfComplex(he.word)
			}
			// try check compound word
		} else if a.getCompound() {
			h.rwords = [100]*hentry{}
			rwords := h.rwords[:]
			// first allow only 2 words in the compound
			setinfo := SpellCompound2
			if info != nil {
				setinfo |= *info
			}
			if tt := traceOn(tr); tt != nil {
				trace(tt, "compound words=2")
			}
			func() {
				defer traceOn(tr).enter()()
				he = a.compoundCheck(word, 0, 0, 100, 0, nil, rwords, false, false, &setinfo, tr)
			}()
			if info != nil {
				*info = setinfo &^ SpellCompound2
			}
			// if not 2-word compoud word, try with 3 or more words
			// (only if original info didn't forbid it)
			if he == nil && info != nil && *info&SpellCompound2 == 0 {
				*info &^= SpellCompound2
				if tt := traceOn(tr); tt != nil {
					trace(tt, "compound words=3+")
				}
				func() {
					defer traceOn(tr).enter()()
					he = a.compoundCheck(word, 0, 0, 100, 0, nil, rwords, false, false, info, tr)
				}()
				// accept the compound with 3 or more words only if it is
				// - not a dictionary word with a typo and
				// - not two words written separately,
				// - or if it's an arbitrary number accepted by compound rules (e.g. 999%)
				if he != nil && !isDigit(byteAt(word, 0)) {
					var slst []string
					var simple bool
					func() {
						// the suggester runs its whole search here, so report
						// what it decided rather than every candidate it tried
						defer suppress(tr)()
						simple = h.smgr.suggest(&slst, word, nil, true)
					}()
					if tt := traceOn(tr); tt != nil {
						if simple {
							trace(tt, "test simplesug -> fail, a simpler form of this word exists")
						} else {
							trace(tt, "test simplesug -> pass, no simpler form of this word exists")
						}
					}
					if simple {
						he = nil
					}
				}
			}
			// LANG_hu section: `moving rule' with last dash
			if he == nil && h.langnum == langHu && byteAt(word, ln-1) == '-' {
				dup := word[:ln-1]
				he = a.compoundCheck(dup, -5, 0, 100, 0, nil, rwords, true, false, info, tr)
			}
			// end of LANG specific region
			if he != nil {
				if root != nil {
					*root = h.reverseIfComplex(he.word)
				}
				if info != nil {
					*info |= SpellCompound
				}
			}
		}
	}
	return he
}

func (h *Hunspell) reverseIfComplex(s string) string {
	if h.complexprefixes {
		if h.utf8 {
			return reversewordUTF(s)
		}
		return reverseword(s)
	}
	return s
}

// Suggest returns the suggestions for a misspelled word.
func (h *Hunspell) Suggest(word string) []string {
	var stack []string
	return h.suggest(word, &stack, clock())
}

func (h *Hunspell) suggest(word string, stack *[]string, start time.Time) []string {
	// apply a fairly arbitrary depth limit; something very broken if suggest
	// ends up calling itself with the same word
	if len(*stack) > maxCandidateStackDepth || contains(*stack, word) {
		return nil
	}
	if h.expired(start) {
		return nil
	}
	var spellStack []string
	*stack = append(*stack, word)
	slst, capwords, abbv, captype := h.suggestInternal(word, &spellStack, stack, start)
	*stack = (*stack)[:len(*stack)-1]
	// word reversing wrapper for complex prefixes
	if h.complexprefixes {
		for j := range slst {
			slst[j] = h.reverseIfComplex(slst[j])
		}
	}
	// capitalize
	if capwords {
		for j := range slst {
			capitalized := h.mkinitcap(slst[j])
			if capitalized == word {
				continue // capitalizing would just reproduce the misspelled word
			}
			slst[j] = capitalized
		}
	}
	// expand suggestions with dot(s)
	if abbv != 0 && h.amgr.sugswithdots && len(word) >= abbv {
		for j := range slst {
			slst[j] += word[len(word)-abbv:]
		}
	}
	// remove bad capitalized and forbidden forms
	if h.amgr.keepcase != 0 || h.amgr.forbiddenword != 0 {
		switch captype {
		case initCap, allCap, huhCap, huhInitCap:
			l := 0
			for j := 0; j < len(slst); j++ {
				// A space-containing suggestion can be an "insert a space"
				// split whose parts each spell on their own.
				bad := !h.spell(slst[j], &spellStack, nil, nil, start)
				if bad && strings.IndexByte(slst[j], ' ') >= 0 {
					bad = false
					for _, part := range strings.Split(slst[j], " ") {
						if part != "" && !h.spell(part, &spellStack, nil, nil, start) {
							bad = true
							break
						}
					}
				}
				if bad {
					var s string
					var w []uint16
					if h.utf8 {
						w, _ = u8u16(slst[j])
					} else {
						s = slst[j]
					}
					s = h.mkallsmall2(s, w)
					if h.spell(s, &spellStack, nil, nil, start) {
						slst[l] = s
						l++
					} else {
						s = h.mkinitcap2(s, w)
						if h.spell(s, &spellStack, nil, nil, start) {
							slst[l] = s
							l++
						}
					}
				} else {
					slst[l] = slst[j]
					l++
				}
			}
			slst = slst[:l]
		}
	}
	// remove duplications
	l := 0
	for j := 0; j < len(slst); j++ {
		slst[l] = slst[j]
		for k := 0; k < l; k++ {
			if slst[k] == slst[j] {
				l--
				break
			}
		}
		l++
	}
	slst = slst[:l]
	// output conversion
	if rl := h.amgr.oconvtable; rl != nil {
		// hold the converted suggestions to the same ceiling as an analysis
		total := 0
		l := 0
		for i := 0; i < len(slst); i++ {
			if ws, ok := rl.conv(slst[i], maxMorphResult-total); ok {
				slst[i] = ws
			}
			if len(slst[i]) > maxMorphResult-total {
				break
			}
			total += len(slst[i])
			// OCONV can map a generated form back to the input word, leaving
			// the misspelled word as its own suggestion.
			if slst[i] == word {
				continue
			}
			slst[l] = slst[i]
			l++
		}
		slst = slst[:l]
	}
	return slst
}

func insertSug(slst *[]string, word string) {
	*slst = append([]string{word}, *slst...)
}

func (h *Hunspell) suggestInternal(word string, spellStack, suggestStack *[]string, start time.Time) (slst []string, capwords bool, abbv int, captype int) {
	// The candidates tried here are the tool's own guesses, not the word the
	// user asked about.
	defer suppress(h.trace)()
	captype = noCap
	onlycmpdsug := 0
	// (C++ returns here without a suggestion manager or a dictionary, but New
	// always makes both)
	// process XML input of the simplified API (see manual)
	if strings.HasPrefix(word, spellXML[:len(spellXML)-2]) {
		if len(word) > maxSpellMLLen {
			return
		}
		slst = h.spellml(word)
		return
	}
	if (h.utf8 && len(word) >= maxWordUTF8Len) || (!h.utf8 && len(word) >= maxWordLen) {
		return
	}
	var scw string
	var sunicw []uint16
	if rl := h.amgr.iconvtable; rl != nil {
		if ws, ok := rl.conv(word, -1); ok {
			if (h.utf8 && len(ws) >= maxWordUTF8Len) || (!h.utf8 && len(ws) >= maxWordLen) {
				return
			}
			scw, sunicw, captype, abbv = h.cleanword2(ws)
		} else {
			scw, sunicw, captype, abbv = h.cleanword2(word)
		}
	} else {
		scw, sunicw, captype, abbv = h.cleanword2(word)
	}
	wl := len(scw)
	if wl == 0 {
		return
	}
	good := false
	sm := h.smgr

	// check capitalized form for FORCEUCASE
	if captype == noCap && h.amgr.forceucase != 0 {
		info := SpellOrigCap
		if h.checkword(scw, &info, nil, start) != nil {
			slst = append(slst, h.mkinitcap(scw))
			return
		}
	}

	switch captype {
	case noCap:
		good = sm.suggest(&slst, scw, &onlycmpdsug, false) || good
		if h.expired(start) {
			return
		}
		if abbv != 0 {
			good = sm.suggest(&slst, scw+".", &onlycmpdsug, false) || good
			if h.expired(start) {
				return
			}
		}
	case initCap:
		capwords = true
		good = sm.suggest(&slst, scw, &onlycmpdsug, false) || good
		if h.expired(start) {
			return
		}
		wspace := h.mkallsmall2(scw, sunicw)
		good = sm.suggest(&slst, wspace, &onlycmpdsug, false) || good
		if h.expired(start) {
			return
		}
	case huhInitCap, huhCap:
		if captype == huhInitCap {
			capwords = true
		}
		good = sm.suggest(&slst, scw, &onlycmpdsug, false) || good
		if h.expired(start) {
			return
		}
		// something.The -> something. The
		if dotPos := strings.IndexByte(scw, '.'); dotPos >= 0 {
			postdot := scw[dotPos+1:]
			var ct int
			if h.utf8 {
				pu, _ := u8u16(postdot)
				ct = getCaptypeUTF8(pu, h.langnum)
			} else {
				ct = getCaptype(postdot, h.csconv)
			}
			if ct == initCap {
				insertSug(&slst, scw[:dotPos+1]+" "+scw[dotPos+1:])
			}
		}
		var wspace string
		if captype == huhInitCap {
			// TheOpenOffice.org -> The OpenOffice.org
			wspace = h.mkinitsmall2(scw, sunicw)
			good = sm.suggest(&slst, wspace, &onlycmpdsug, false) || good
			if h.expired(start) {
				return
			}
		}
		wspace = h.mkallsmall2(scw, sunicw)
		if h.spell(wspace, spellStack, nil, nil, start) {
			insertSug(&slst, wspace)
		}
		prevns := len(slst)
		good = sm.suggest(&slst, wspace, &onlycmpdsug, false) || good
		if h.expired(start) {
			return
		}
		if captype == huhInitCap {
			wspace = h.mkinitcap2(wspace, sunicw)
			if h.spell(wspace, spellStack, nil, nil, start) {
				insertSug(&slst, wspace)
			}
			good = sm.suggest(&slst, wspace, &onlycmpdsug, false) || good
			if h.expired(start) {
				return
			}
		}
		// aNew -> "a New" (instead of "a new")
		for j := prevns; j < len(slst); j++ {
			sj := cstr(slst[j])
			if sp := strings.IndexByte(sj, ' '); sp >= 0 {
				slen := len(sj) - sp - 1
				// different case after space (need capitalisation)
				if slen < wl && cstr(scw[wl-slen:]) != sj[sp+1:] {
					first := sj[:sp+1]
					second := sj[sp+1:]
					var w []uint16
					if h.utf8 {
						w, _ = u8u16(second)
					}
					second = h.mkinitcap2(second, w)
					// set as first suggestion
					slst = append(slst[:j], slst[j+1:]...)
					insertSug(&slst, first+second)
				}
			}
		}
	case allCap:
		wspace := h.mkallsmall2(scw, sunicw)
		good = sm.suggest(&slst, wspace, &onlycmpdsug, false) || good
		if h.expired(start) {
			return
		}
		if h.amgr.keepcase != 0 && h.spell(wspace, spellStack, nil, nil, start) {
			insertSug(&slst, wspace)
		}
		wspace = h.mkinitcap2(wspace, sunicw)
		good = sm.suggest(&slst, wspace, &onlycmpdsug, false) || good
		if h.expired(start) {
			return
		}
		for j := range slst {
			slst[j] = h.mkallcap(slst[j])
			if h.amgr.checksharps {
				if h.utf8 {
					slst[j] = mystrrep(slst[j], "\xC3\x9F", "SS")
				} else {
					slst[j] = mystrrep(slst[j], "\xDF", "SS")
				}
			}
		}
	}

	// LANG_hu section: replace '-' with ' ' in Hungarian
	if h.langnum == langHu {
		for j := range slst {
			if pos := strings.IndexByte(slst[j], '-'); pos >= 0 {
				info := 0
				w := slst[j][:pos] + slst[j][pos+1:]
				h.spell(w, spellStack, &info, nil, start)
				b := []byte(slst[j])
				if info&SpellCompound != 0 && info&SpellForbidden != 0 {
					b[pos] = ' '
				} else {
					b[pos] = '-'
				}
				slst[j] = string(b)
			}
		}
	}

	// try ngram approach since found nothing good suggestion
	if !good && (len(slst) == 0 || onlycmpdsug != 0) && h.amgr.maxngramsugs != 0 {
		switch captype {
		case noCap:
			sm.ngsuggest(&slst, scw, h.hmgrs, noCap)
			if h.expired(start) {
				return
			}
		case huhInitCap, huhCap:
			if captype == huhInitCap {
				capwords = true
			}
			wspace := h.mkallsmall2(scw, sunicw)
			sm.ngsuggest(&slst, wspace, h.hmgrs, huhCap)
			if h.expired(start) {
				return
			}
		case initCap:
			capwords = true
			wspace := h.mkallsmall2(scw, sunicw)
			sm.ngsuggest(&slst, wspace, h.hmgrs, initCap)
			if h.expired(start) {
				return
			}
		case allCap:
			wspace := h.mkallsmall2(scw, sunicw)
			oldns := len(slst)
			sm.ngsuggest(&slst, wspace, h.hmgrs, allCap)
			if h.expired(start) {
				return
			}
			for j := oldns; j < len(slst); j++ {
				slst[j] = h.mkallcap(slst[j])
			}
		}
	}

	// try dash suggestion (Afo-American -> Afro-American)
	if dashPos := strings.IndexByte(scw, '-'); dashPos >= 0 {
		nodashsug := true
		for j := 0; j < len(slst) && nodashsug; j++ {
			if strings.IndexByte(slst[j], '-') >= 0 {
				nodashsug = false
			}
		}
		prevPos := 0
		last := false
		for !good && nodashsug && !last {
			if dashPos == len(scw) {
				last = true
			}
			chunk := scw[prevPos:dashPos]
			if chunk != word && !h.spell(chunk, spellStack, nil, nil, start) {
				nlst := h.suggest(chunk, suggestStack, start)
				if h.expired(start) {
					return
				}
				for j := len(nlst) - 1; j >= 0; j-- {
					wspace := scw[:prevPos] + nlst[j]
					if !last {
						wspace += "-" + scw[dashPos+1:]
					}
					info := 0
					if h.amgr.forbiddenword != 0 {
						h.checkword(wspace, &info, nil, start)
					}
					if info&SpellForbidden == 0 {
						insertSug(&slst, wspace)
					}
				}
				nodashsug = false
			}
			if !last {
				prevPos = dashPos + 1
				dashPos = indexFrom(scw, '-', prevPos)
			}
			if dashPos < 0 {
				dashPos = len(scw)
			}
		}
	}
	return
}

// Encoding returns the character encoding of the dictionary.
func (h *Hunspell) Encoding() string { return h.encoding }

// WordChars returns the extra word characters of the dictionary.
func (h *Hunspell) WordChars() string { return h.amgr.wordchars }

// WordCharsUTF16 returns the extra word characters of an UTF-8 dictionary.
func (h *Hunspell) WordCharsUTF16() []uint16 { return h.amgr.wordcharsUTF16 }

// Version returns the VERSION line of the affix file.
func (h *Hunspell) Version() string { return h.amgr.version }

// LangNum returns the language number of the LANG option.
func (h *Hunspell) LangNum() int { return h.langnum }

// Lang returns the LANG option.
func (h *Hunspell) Lang() string { return h.amgr.lang }

// Stem returns the stems of a word.
func (h *Hunspell) Stem(word string) []string {
	return h.StemMorph(h.Analyze(word))
}

// StemMorph returns the stems of the given analyses.
func (h *Hunspell) StemMorph(desc []string) []string {
	if len(desc) == 0 {
		return nil
	}
	var result2 strings.Builder
	for _, i := range desc {
		if result2.Len() > maxMorphResult {
			break
		}
		var result strings.Builder
		// add compound word parts (except the last one)
		tok := []byte(i[appendCompoundParts(i, &result):])
		for alt := strIndexFrom(string(tok), " | ", 0); alt >= 0; alt = strIndexFrom(string(tok), " | ", alt) {
			tok[alt+1] = msepAlt
		}
		for _, k := range lineTok(string(tok), msepAlt) {
			if result2.Len() > maxMorphResult {
				break
			}
			// add derivational suffixes
			if strings.Contains(k, morphDeriSfx) {
				// remove inflectional suffixes
				if is := strings.Index(k, morphInflSfx); is >= 0 {
					k = k[:is]
				}
				sg := h.smgr.suggestGen([]string{k}, k, time.Time{}, false)
				if sg != "" {
					for _, j := range lineTok(sg, msepRec) {
						result2.WriteByte(msepRec)
						result2.WriteString(result.String())
						result2.WriteString(j)
					}
				}
			} else {
				result2.WriteByte(msepRec)
				result2.WriteString(result.String())
				if strings.Contains(k, morphSurfPfx) {
					field, _ := copyField(k, morphSurfPfx)
					result2.WriteString(field)
				}
				field, _ := copyField(k, morphStem)
				result2.WriteString(field)
			}
		}
	}
	return uniqlist(lineTok(result2.String(), msepRec))
}

func catResult(result *string, st string) {
	if len(*result) > maxMorphResult {
		return
	}
	if st != "" {
		if *result != "" {
			*result += "\n"
		}
		*result += st
	}
}

// Analyze returns the morphological analyses of a word.
func (h *Hunspell) Analyze(word string) []string {
	slst := h.analyzeInternal(word)
	// output conversion
	if rl := h.amgr.oconvtable; rl != nil {
		total := 0
		i := 0
		for ; i < len(slst); i++ {
			if ws, ok := rl.conv(slst[i], maxMorphResult-total); ok {
				slst[i] = ws
			}
			if len(slst[i]) > maxMorphResult-total {
				break
			}
			total += len(slst[i])
		}
		slst = slst[:i]
	}
	return slst
}

func (h *Hunspell) analyzeInternal(word string) []string {
	var stack []string
	// (C++ returns here without a suggestion manager or a dictionary, but New
	// always makes both)
	if (h.utf8 && len(word) >= maxWordUTF8Len) || (!h.utf8 && len(word) >= maxWordLen) {
		return nil
	}
	var scw string
	var sunicw []uint16
	var captype, abbv int
	if rl := h.amgr.iconvtable; rl != nil {
		if ws, ok := rl.conv(word, -1); ok {
			if (h.utf8 && len(ws) >= maxWordUTF8Len) || (!h.utf8 && len(ws) >= maxWordLen) {
				return nil
			}
			scw, sunicw, captype, abbv = h.cleanword2(ws)
		} else {
			scw, sunicw, captype, abbv = h.cleanword2(word)
		}
	} else {
		scw, sunicw, captype, abbv = h.cleanword2(word)
	}
	wl := len(scw)
	if wl == 0 {
		if abbv == 0 {
			return nil
		}
		scw = strings.Repeat(".", abbv)
		wl = abbv
		abbv = 0
	}
	result := ""
	start := clock()
	sm := h.smgr

	n := 0
	// test numbers; LANG_hu section: set dash information for suggestions
	if h.langnum == langHu {
		n2, n3 := 0, 0
		for n < wl && ((byteAt(scw, n) <= '9' && byteAt(scw, n) >= '0') || ((byteAt(scw, n) == '.' || byteAt(scw, n) == ',') && n > 0)) {
			n++
			if byteAt(scw, n) == '.' || byteAt(scw, n) == ',' {
				if (n2 == 0 && n > 3) || (n2 > 0 && (byteAt(scw, n-1) == '.' || byteAt(scw, n-1) == ',')) {
					break
				}
				n2++
				n3 = n
			}
		}
		if n == wl && n3 > 0 && n-n3 > 3 {
			return nil
		}
		if n == wl || (n > 0 && (byteAt(scw, n) == '%' || byteAt(scw, n) == 0xB0) && h.checkword(scw[n:], nil, nil, start) != nil) {
			result = scw[:n-1]
			if n == wl {
				catResult(&result, sm.suggestMorph(scw[n-1:]))
			} else {
				catResult(&result, sm.suggestMorph(scw[n-1:n]))
				result += "+" // XXX SPEC. MORPHCODE
				catResult(&result, sm.suggestMorph(scw[n:]))
			}
			return lineTok(result, msepRec)
		}
	}
	// END OF LANG_hu section

	switch captype {
	case huhCap, huhInitCap, noCap:
		catResult(&result, sm.suggestMorph(scw))
		if abbv != 0 {
			catResult(&result, sm.suggestMorph(scw+"."))
		}
	case initCap:
		scw = h.mkallsmall2(scw, sunicw)
		u8buffer := scw
		scw = h.mkinitcap2(scw, sunicw)
		catResult(&result, sm.suggestMorph(u8buffer))
		catResult(&result, sm.suggestMorph(scw))
		if abbv != 0 {
			catResult(&result, sm.suggestMorph(u8buffer+"."))
			catResult(&result, sm.suggestMorph(scw+"."))
		}
	case allCap:
		catResult(&result, sm.suggestMorph(scw))
		if abbv != 0 {
			catResult(&result, sm.suggestMorph(scw+"."))
		}
		scw = h.mkallsmall2(scw, sunicw)
		u8buffer := scw
		scw = h.mkinitcap2(scw, sunicw)
		catResult(&result, sm.suggestMorph(u8buffer))
		catResult(&result, sm.suggestMorph(scw))
		if abbv != 0 {
			catResult(&result, sm.suggestMorph(u8buffer+"."))
			catResult(&result, sm.suggestMorph(scw+"."))
		}
	}

	if result != "" {
		// word reversing wrapper for complex prefixes
		result = h.reverseIfComplex(result)
		return lineTok(result, msepRec)
	}

	// compound word with dash (HU) I18n
	dashPos := -1
	if h.langnum == langHu {
		dashPos = strings.IndexByte(scw, '-')
	}
	if dashPos >= 0 {
		nresult := false
		part1, part2 := scw[:dashPos], scw[dashPos+1:]
		// examine 2 sides of the dash
		if part2 == "" { // base word ending with dash
			if h.spell(part1, &stack, nil, nil, start) {
				if p := sm.suggestMorph(part1); p != "" {
					return lineTok(p, msepRec)
				}
			}
		} else if part2 == "e" { // XXX (HU) -e hat.
			if h.spell(part1, &stack, nil, nil, start) && h.spell("-e", &stack, nil, nil, start) {
				result += sm.suggestMorph(part1)
				result += "+" // XXX spec. separator in MORPHCODE
				result += sm.suggestMorph("-e")
				return lineTok(result, msepRec)
			}
		} else {
			// first word ending with dash: word- XXX ???
			nresult = h.spell(part1+" ", &stack, nil, nil, start)
			if nresult && h.spell(part2, &stack, nil, nil, start) &&
				(len(part2) > 1 || (part2[0] > '0' && part2[0] < '9')) {
				if st := sm.suggestMorph(part1); st != "" {
					result += st
					result += "+" // XXX spec. separator in MORPHCODE
				}
				result += sm.suggestMorph(part2)
				return lineTok(result, msepRec)
			}
		}
		// affixed number in correct word
		if nresult && dashPos > 0 &&
			((scw[dashPos-1] <= '9' && scw[dashPos-1] >= '0') || scw[dashPos-1] == '.') {
			n = 1
			if scw[dashPos-n] == '.' {
				n++
			}
			// search first not a number character to left from dash
			for dashPos >= n && (byteAt(scw, dashPos-n) == '0' || n < 3) && n < 6 {
				n++
			}
			if dashPos < n {
				n--
			}
			// numbers: valami1000000-hoz
			for ; n >= 1; n-- {
				if scw[dashPos-n] < '0' || scw[dashPos-n] > '9' {
					continue
				}
				chunk := scw[dashPos-n:]
				if h.checkword(chunk, nil, nil, start) != nil {
					result += chunk
					result += sm.suggestMorph(chunk)
					return lineTok(result, msepRec)
				}
			}
		}
	}
	return nil
}

// GenerateMorph generates the forms of word with the given morphological
// descriptions.
func (h *Hunspell) GenerateMorph(word string, pl []string) []string {
	if h.smgr == nil || len(pl) == 0 {
		return nil
	}
	pl2 := h.Analyze(word)
	_, captype := h.cleanword(word)
	result := ""
	start := clock()
	for _, i := range pl {
		catResult(&result, h.smgr.suggestGen(pl2, i, start, true))
	}
	if result == "" {
		return nil
	}
	// allcap
	if captype == allCap {
		result = h.mkallcap(result)
	}
	// line split
	slst := lineTok(result, msepRec)
	// capitalize
	if captype == initCap || captype == huhInitCap {
		for i := range slst {
			slst[i] = h.mkinitcap(slst[i])
		}
	}
	// temporary filtering of prefix related errors (eg. generate("undrinkable",
	// "eats") --> "undrinkables" and "*undrinks")
	out := slst[:0]
	for i, s := range slst {
		if h.expired(start) {
			out = append(out, slst[i:]...)
			break
		}
		var stack []string
		if h.spell(s, &stack, nil, nil, start) {
			out = append(out, s)
		}
	}
	return out
}

// Generate generates the forms of word that are like the example.
func (h *Hunspell) Generate(word, pattern string) []string {
	return uniqlist(h.GenerateMorph(word, h.Analyze(pattern)))
}

// SuffixSuggest returns the forms the suffixes of a root word make.
func (h *Hunspell) SuffixSuggest(rootWord string) []string {
	word := rootWord
	if h.amgr.ignorechars != "" {
		word = h.cleanIgnore(rootWord)
	}
	if word == "" {
		return nil
	}
	var he *hentry
	for i := 0; i < len(h.hmgrs) && he == nil; i++ {
		he = h.hmgrs[i].lookup(word)
	}
	if he != nil {
		return h.amgr.getSuffixWords(he.astr, rootWord)
	}
	return nil
}

// Add adds a word to the run-time dictionary.
func (h *Hunspell) Add(word string) int { return h.hmgrs[0].add(word) }

// AddWithFlags adds a word with affix flags and a morphological description.
func (h *Hunspell) AddWithFlags(word, flags, desc string) int {
	return h.hmgrs[0].addWithFlags(word, flags, desc)
}

// AddWithAffix adds a word with the affix flags of the example (a dictionary
// word): the affixed forms of the new word are recognized, too.
func (h *Hunspell) AddWithAffix(word, example string) int {
	return h.hmgrs[0].addWithAffix(word, example)
}

// Remove removes a word from the run-time dictionary.
func (h *Hunspell) Remove(word string) int { return h.hmgrs[0].remove(word) }

// InputConv applies the ICONV table to word.
func (h *Hunspell) InputConv(word string) (string, bool) {
	if rl := h.amgr.iconvtable; rl != nil {
		return rl.conv(word, -1)
	}
	return word, false
}

// minimal XML parser functions

func getXMLPar(par string, pos int) string {
	if pos < 0 {
		return ""
	}
	end := byteAt(par, pos)
	if end == '>' {
		end = '<'
	} else if end != '\'' && end != '"' {
		return "" // bad XML
	}
	var b strings.Builder
	for i := pos + 1; byteAt(par, i) != 0 && par[i] != end; i++ {
		b.WriteByte(par[i])
	}
	d := mystrrep(b.String(), "&lt;", "<")
	return mystrrep(d, "&amp;", "&")
}

// getXMLPos returns the position of the value of the attribute attr in the
// element at pos. (The C++ function returns the end of the element for a
// NULL attr; the only caller, checkXMLPar, passes the position of an element
// and an attribute name.)
func getXMLPos(s string, pos int, attr string) int {
	endpos := indexFrom(s, '>', pos)
	for {
		pos = strIndexFrom(s, attr, pos)
		if pos < 0 || (endpos >= 0 && pos >= endpos) {
			return -1
		}
		if pos == 0 || s[pos-1] == ' ' || s[pos-1] == '\n' {
			break
		}
		pos += len(attr)
	}
	return pos + len(attr)
}

func checkXMLPar(q string, pos int, attr, value string) bool {
	return getXMLPar(q, getXMLPos(q, pos, attr)) == value
}

func getXMLList(list string, pos int, tag string) []string {
	var slst []string
	if pos < 0 {
		return slst
	}
	for {
		pos = strIndexFrom(list, tag, pos)
		if pos < 0 {
			break
		}
		cw := getXMLPar(list, pos+len(tag)-1)
		if cw == "" {
			break
		}
		slst = append(slst, cw)
		pos++
	}
	return slst
}

func (h *Hunspell) spellml(inWord string) []string {
	// The XML interface spell checks words of its own to answer the query.
	defer suppress(h.trace)()
	qpos := strings.Index(inWord, "<query")
	if qpos < 0 {
		return nil // bad XML input
	}
	q2pos := indexFrom(inWord, '>', qpos)
	if q2pos < 0 {
		return nil
	}
	q2pos = strIndexFrom(inWord, "<word", q2pos)
	if q2pos < 0 {
		return nil
	}
	switch {
	case checkXMLPar(inWord, qpos, "type=", "analyze"):
		cw := getXMLPar(inWord, indexFrom(inWord, '>', q2pos))
		var slst []string
		if cw != "" {
			slst = h.Analyze(cw)
		}
		if len(slst) == 0 {
			return slst
		}
		// convert the result to <code><a>ana1</a><a>ana2</a></code> format
		var r strings.Builder
		r.WriteString("<code>")
		for _, entry := range slst {
			r.WriteString("<a>")
			entry = mystrrep(entry, "\t", " ")
			entry = mystrrep(entry, "&", "&amp;")
			entry = mystrrep(entry, "<", "&lt;")
			r.WriteString(entry)
			r.WriteString("</a>")
		}
		r.WriteString("</code>")
		return []string{r.String()}
	case checkXMLPar(inWord, qpos, "type=", "stem"):
		if cw := getXMLPar(inWord, indexFrom(inWord, '>', q2pos)); cw != "" {
			return h.Stem(cw)
		}
	case checkXMLPar(inWord, qpos, "type=", "generate"):
		cw := getXMLPar(inWord, indexFrom(inWord, '>', q2pos))
		if cw == "" {
			return nil
		}
		if q3pos := strIndexFrom(inWord, "<word", q2pos+1); q3pos >= 0 {
			if cw2 := getXMLPar(inWord, indexFrom(inWord, '>', q3pos)); cw2 != "" {
				return h.Generate(cw, cw2)
			}
		} else if q2pos = strIndexFrom(inWord, "<code", q2pos+1); q2pos >= 0 {
			if slst2 := getXMLList(inWord, indexFrom(inWord, '>', q2pos), "<a>"); len(slst2) > 0 {
				return uniqlist(h.GenerateMorph(cw, slst2))
			}
		}
	case checkXMLPar(inWord, qpos, "type=", "add"):
		cw := getXMLPar(inWord, indexFrom(inWord, '>', q2pos))
		if cw == "" {
			return nil
		}
		if q3pos := strIndexFrom(inWord, "<word", q2pos+1); q3pos >= 0 {
			if cw2 := getXMLPar(inWord, indexFrom(inWord, '>', q3pos)); cw2 != "" {
				h.AddWithAffix(cw, cw2)
			} else {
				h.Add(cw)
			}
		} else {
			h.Add(cw)
		}
	}
	return nil
}

// visible reports whether a homonym of the entry is a plain dictionary word:
// not a hidden capitalized homonym and not forbidden.
func (h *Hunspell) visible(he *hentry) bool {
	forbidden := h.amgr.forbiddenword
	for ; he != nil; he = he.nextHomonym {
		if !testaff(he.astr, onlyUpcaseFlag) && !testaff(he.astr, forbidden) {
			return true
		}
	}
	return false
}

// HasWord reports whether word is an entry of the dictionaries, as written,
// without affixes, compounding or case conversion.
func (h *Hunspell) HasWord(word string) bool {
	for _, m := range h.hmgrs {
		if h.visible(m.lookup(word)) {
			return true
		}
	}
	return false
}

// ForEachWord calls fn for each entry of the dictionaries, once per word, in
// hash table order. It stops when fn returns false.
func (h *Hunspell) ForEachWord(fn func(word string) bool) {
	seen := map[string]bool{}
	for i, m := range h.hmgrs {
		for _, head := range m.tableptr {
			for he := head; he != nil; he = he.next {
				if m.lookup(he.word) != he || !h.visible(he) {
					continue // a homonym or a hidden word
				}
				if i > 0 {
					if seen[he.word] {
						continue
					}
					seen[he.word] = true
				} else if len(h.hmgrs) > 1 {
					seen[he.word] = true
				}
				if !fn(he.word) {
					return
				}
			}
		}
	}
}

// Expand returns the dictionary word and its forms with one prefix, one
// suffix or both, for each homonym of the word in the dictionaries. Forms
// that need further affixes are left out.
func (h *Hunspell) Expand(word string) []string {
	var res []string
	seen := map[string]bool{}
	forbidden := h.amgr.forbiddenword
	for _, m := range h.hmgrs {
		for he := m.lookup(word); he != nil; he = he.nextHomonym {
			if testaff(he.astr, onlyUpcaseFlag) || testaff(he.astr, forbidden) {
				continue
			}
			for _, w := range h.amgr.expandAll(he.word, he.astr) {
				if !seen[w] {
					seen[w] = true
					res = append(res, w)
				}
			}
		}
	}
	return res
}

// warnf records a diagnostic of the debug builds of Hunspell
// (HUNSPELL_WARNING).
func (h *Hunspell) warnf(format string, args ...any) {
	h.warns = append(h.warns, fmt.Sprintf(format, args...))
}
