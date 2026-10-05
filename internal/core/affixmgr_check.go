package core

import (
	"strings"
	"time"
)

// The time limits of Hunspell: max. ~1/4 sec wall time for a suggestion,
// including max. ~1/10 sec for a case sensitive plain or compound word
// suggestion, within ~1/20 sec long time consuming suggestion functions.
var (
	timelimitGlobal     = 250 * time.Millisecond
	timelimitSuggestion = 100 * time.Millisecond
	timelimit           = 50 * time.Millisecond
)

// timeLimits are the time limits of one dictionary; the package variables
// are the defaults.
type timeLimits struct {
	global, suggestion, compound time.Duration
}

func defaultTimeLimits() timeLimits {
	return timeLimits{timelimitGlobal, timelimitSuggestion, timelimit}
}

// noTimeLimits turns the time limits off when set to "1" at link time
// (-ldflags "-X github.com/documatrix/go-hunspell/internal/core.noTimeLimits=1"),
// for deterministic comparisons with a reference build.
var noTimeLimits string

func init() { applyNoTimeLimits(noTimeLimits) }

// applyNoTimeLimits raises the default time limits to an hour when flag is
// "1".
func applyNoTimeLimits(flag string) {
	if flag == "1" {
		timelimitGlobal = time.Hour
		timelimitSuggestion = time.Hour
		timelimit = time.Hour
	}
}

// clock and since read the time for the time limits. The tests replace them
// to run out of time at chosen points of a search.
var (
	clock = time.Now
	since = time.Since
)

const (
	minTimer     = 100
	maxPlusTimer = 100
)

// distinctRecords keeps the distinct records of one analysis, in the order
// they were first added.
type distinctRecords struct {
	records []string
	seen    map[string]bool
	sep     byte
}

func newDistinctRecords(sep byte) *distinctRecords {
	return &distinctRecords{seen: map[string]bool{}, sep: sep}
}

func (d *distinctRecords) append(add string) {
	pos := 0
	for pos < len(add) {
		end := strings.IndexByte(add[pos:], d.sep)
		var rec string
		if end < 0 {
			rec = add[pos:]
		} else {
			rec = add[pos : pos+end]
		}
		if rec != "" && !d.seen[rec] {
			d.seen[rec] = true
			d.records = append(d.records, rec)
		}
		if end < 0 {
			break
		}
		pos += end + 1
	}
}

func (d *distinctRecords) join() string {
	var b strings.Builder
	for _, r := range d.records {
		b.WriteString(r)
		b.WriteByte(d.sep)
	}
	return b.String()
}

func (a *AffixMgr) traceAvoidflag(t *traceCtx, avoidflag uint16, stem *hentry) {
	if t == nil {
		return
	}
	defer t.enter()()
	traceTest(t, "avoidflag", a, avoidflag, "dic", stem.astr, "fail, the caller is skipping stems with this flag")
}

// prefixCheck checks the word for prefixes.
func (a *AffixMgr) prefixCheck(word string, start, ln int, inCompound int, tr *traceCtx, needflag, avoidflag uint16) *hentry {
	a.pfx = nil
	a.sfxappnd = nil
	a.sfxextra = 0
	t := traceOn(tr)
	candidates := 0

	allowed := func(pe *PfxEntry) bool {
		// fogemorpheme
		return (inCompound != inCpdNot || !(pe.contclass != nil && testaff(pe.contclass, a.onlyincompound))) &&
			// permit prefixes in compounds
			(inCompound != inCpdEnd || (pe.contclass != nil && testaff(pe.contclass, a.compoundpermitflag)))
	}

	// first handle the special case of 0 length prefixes
	for pe := a.pStart[0]; pe != nil; pe = pe.next {
		if allowed(pe) {
			candidates++
			rv := pe.checkword(word, start, ln, inCompound, needflag, tr)
			// Skip a stem with the avoid flag and keep scanning the other prefixes.
			if rv != nil && avoidflag != 0 && testaff(rv.astr, avoidflag) {
				a.traceAvoidflag(t, avoidflag, rv)
				rv = nil
			}
			if rv != nil {
				a.pfx = pe
				return rv
			}
		}
	}

	// now handle the general case
	rest := substrN(word, start, -1)
	pptr := a.pStart[byteAt(word, start)]
	for pptr != nil {
		if isSubset(pptr.appnd, rest) {
			if allowed(pptr) {
				candidates++
				rv := pptr.checkword(word, start, ln, inCompound, needflag, tr)
				if rv != nil && avoidflag != 0 && testaff(rv.astr, avoidflag) {
					a.traceAvoidflag(t, avoidflag, rv)
					rv = nil
				}
				if rv != nil {
					a.pfx = pptr
					return rv
				}
			}
			pptr = pptr.nexteq
		} else {
			pptr = pptr.nextne
		}
	}
	if t != nil && candidates == 0 {
		trace(t, "pfx \"%s\" candidates=0", cstr(substrN(word, start, ln)))
	}
	return nil
}

// prefixCheckTwosfx checks the word for prefixes and two-level suffixes.
func (a *AffixMgr) prefixCheckTwosfx(word string, start, ln int, inCompound int, tr *traceCtx, needflag uint16) *hentry {
	a.pfx = nil
	a.sfxappnd = nil
	a.sfxextra = 0
	for pe := a.pStart[0]; pe != nil; pe = pe.next {
		if rv := pe.checkTwosfx(word, start, ln, inCompound, needflag, tr); rv != nil {
			return rv
		}
	}
	rest := substrN(word, start, -1)
	pptr := a.pStart[byteAt(word, start)]
	for pptr != nil {
		if isSubset(pptr.appnd, rest) {
			if rv := pptr.checkTwosfx(word, start, ln, inCompound, needflag, tr); rv != nil {
				a.pfx = pptr
				return rv
			}
			pptr = pptr.nexteq
		} else {
			pptr = pptr.nextne
		}
	}
	return nil
}

// prefixCheckMorph checks the word for prefixes and gives its analyses.
func (a *AffixMgr) prefixCheckMorph(word string, start, ln int, inCompound int, tr *traceCtx, needflag uint16) string {
	result := newDistinctRecords(msepRec)
	a.pfx = nil
	a.sfxappnd = nil
	a.sfxextra = 0
	for pe := a.pStart[0]; pe != nil; pe = pe.next {
		if st := pe.checkMorph(word, start, ln, inCompound, needflag, tr); st != "" {
			result.append(st)
		}
	}
	rest := substrN(word, start, -1)
	pptr := a.pStart[byteAt(word, start)]
	for pptr != nil {
		if isSubset(pptr.appnd, rest) {
			st := pptr.checkMorph(word, start, ln, inCompound, needflag, tr)
			if st != "" {
				// fogemorpheme
				if inCompound != inCpdNot || !(pptr.contclass != nil && testaff(pptr.contclass, a.onlyincompound)) {
					result.append(st)
					a.pfx = pptr
				}
			}
			pptr = pptr.nexteq
		} else {
			pptr = pptr.nextne
		}
	}
	return result.join()
}

func (a *AffixMgr) prefixCheckTwosfxMorph(word string, start, ln int, inCompound int, tr *traceCtx, needflag uint16) string {
	result := newDistinctRecords(msepRec)
	a.pfx = nil
	a.sfxappnd = nil
	a.sfxextra = 0
	for pe := a.pStart[0]; pe != nil; pe = pe.next {
		if st := pe.checkTwosfxMorph(word, start, ln, inCompound, needflag, tr); st != "" {
			result.append(st)
		}
	}
	rest := substrN(word, start, -1)
	pptr := a.pStart[byteAt(word, start)]
	for pptr != nil {
		if isSubset(pptr.appnd, rest) {
			if st := pptr.checkTwosfxMorph(word, start, ln, inCompound, needflag, tr); st != "" {
				result.append(st)
				a.pfx = pptr
			}
			pptr = pptr.nexteq
		} else {
			pptr = pptr.nextne
		}
	}
	return result.join()
}

// isRevSubset reports whether s1 matches the text ending at word[end],
// read backwards, for at most ln bytes.
func isRevSubset(s1, word string, end, ln int) bool {
	i := 0
	for ln > 0 && i < len(s1) && (s1[i] == byteAt(word, end) || s1[i] == '.') {
		i++
		end--
		ln--
	}
	return i >= len(s1)
}

// circumfixOK: a circumfix is one affix split in two halves, so the flag has
// to be on both the prefix and the suffix, or on neither of them.
func (a *AffixMgr) circumfixOK(pfx *PfxEntry, sfx *SfxEntry, t *traceCtx) bool {
	if a.circumfix == 0 {
		return true
	}
	inPrefix := pfx != nil && pfx.contclass != nil && testaff(pfx.contclass, a.circumfix)
	inSuffix := sfx.contclass != nil && testaff(sfx.contclass, a.circumfix)
	if t != nil {
		traceCircumfix(t, a, a.circumfix, pfx, sfx, inPrefix, inSuffix)
	}
	return inPrefix == inSuffix
}

// suffixApplicable decides whether a suffix entry may be applied at all,
// before the suffix itself is matched against the word.
func (a *AffixMgr) suffixApplicable(pfx *PfxEntry, sfx *SfxEntry, cclass uint16, inCompound int, t *traceCtx) bool {
	// suffixes are only allowed at the beginning of a compound when they are
	// signed with the compoundpermitflag flag
	if inCompound == inCpdBegin && !(sfx.contclass != nil && a.compoundpermitflag != 0 && testaff(sfx.contclass, a.compoundpermitflag)) {
		if t != nil {
			traceTest(t, "compoundpermit", a, a.compoundpermitflag, "sfx-cont", sfx.contclass,
				"fail, a suffix at the start of a compound needs this flag")
		}
		return false
	}
	if !a.circumfixOK(pfx, sfx, t) {
		return false
	}
	// a fogemorpheme is only allowed inside a compound
	if inCompound == 0 && sfx.contclass != nil && testaff(sfx.contclass, a.onlyincompound) {
		if t != nil {
			traceTest(t, "onlyincompound", a, a.onlyincompound, "sfx-cont", sfx.contclass,
				"fail, this suffix is only allowed inside a compound")
		}
		return false
	}
	// a needaffix suffix needs a further affix, so it is allowed either as a
	// second suffix or when a prefix is present that does not itself need one
	if cclass == 0 && sfx.contclass != nil && testaff(sfx.contclass, a.needaffix) &&
		!(pfx != nil && !(pfx.contclass != nil && testaff(pfx.contclass, a.needaffix))) {
		if t != nil {
			traceTest(t, "needaffix", a, a.needaffix, "sfx-cont", sfx.contclass,
				"fail, this suffix needs a further affix and has none")
		}
		return false
	}
	return true
}

// suffixCheck checks the word for suffixes.
func (a *AffixMgr) suffixCheck(word string, start, ln int, sfxopts int, ppfx *PfxEntry, tr *traceCtx,
	cclass, needflag uint16, inCompound int, avoidflag uint16) *hentry {
	t := traceOn(tr)
	candidates := 0
	reportEmptyPass := func() {
		if t != nil && candidates == 0 {
			x := ""
			if sfxopts&aeXProduct != 0 {
				x = " xprod=Y"
			}
			trace(t, "sfx \"%s\" candidates=0%s", cstr(substrN(word, start, ln)), x)
		}
	}
	reportRefused := func(se *SfxEntry) {
		if t == nil {
			return
		}
		candidates++
		traceAffix(t, "sfx", a, &se.affEntry)
		defer t.enter()()
		a.suffixApplicable(ppfx, se, cclass, inCompound, t)
	}
	var badflag uint16
	if inCompound == 0 {
		badflag = a.onlyincompound
	}

	// first handle the special case of 0 length suffixes
	for se := a.sStart[0]; se != nil; se = se.next {
		if cclass == 0 || se.contclass != nil {
			if a.suffixApplicable(ppfx, se, cclass, inCompound, nil) {
				candidates++
				rv := se.checkword(word, start, ln, sfxopts, ppfx, cclass, needflag, badflag, tr)
				// Skip a stem with the avoid flag and keep scanning the other suffixes.
				if rv != nil && avoidflag != 0 && testaff(rv.astr, avoidflag) {
					a.traceAvoidflag(t, avoidflag, rv)
					rv = nil
				}
				if rv != nil {
					a.sfx = se
					return rv
				}
			} else {
				reportRefused(se)
			}
		}
	}

	// now handle the general case
	if ln == 0 {
		reportEmptyPass()
		return nil // FULLSTRIP
	}
	end := start + ln - 1
	sptr := a.sStart[byteAt(word, end)]
	for sptr != nil {
		if isRevSubset(sptr.rappnd, word, end, ln) {
			if !a.suffixApplicable(ppfx, sptr, cclass, inCompound, nil) {
				reportRefused(sptr)
			} else if inCompound != inCpdEnd || ppfx != nil || !(sptr.contclass != nil && testaff(sptr.contclass, a.onlyincompound)) {
				candidates++
				rv := sptr.checkword(word, start, ln, sfxopts, ppfx, cclass, needflag, badflag, tr)
				if rv != nil && avoidflag != 0 && testaff(rv.astr, avoidflag) {
					a.traceAvoidflag(t, avoidflag, rv)
					rv = nil
				}
				if rv != nil {
					a.sfx = sptr
					a.sfxflag = sptr.aflag
					if sptr.contclass == nil {
						a.sfxappnd = &sptr.rappnd
					} else if a.langnum == langHu && len(sptr.appnd) > 0 && sptr.rappnd[0] == 'i' &&
						byteAt(sptr.rappnd, 1) != 'y' && byteAt(sptr.rappnd, 1) != 't' {
						// LANG_hu section: spec. Hungarian rule
						a.sfxextra = 1
					}
					return rv
				}
			}
			sptr = sptr.nexteq
		} else {
			sptr = sptr.nextne
		}
	}
	reportEmptyPass()
	return nil
}

// suffixCheckTwosfx checks the word for two-level suffixes.
func (a *AffixMgr) suffixCheckTwosfx(word string, start, ln int, sfxopts int, ppfx *PfxEntry, tr *traceCtx, needflag uint16) *hentry {
	for se := a.sStart[0]; se != nil; se = se.next {
		if a.contclasses[se.aflag] {
			if rv := se.checkTwosfx(word, start, ln, sfxopts, ppfx, needflag, tr); rv != nil {
				return rv
			}
		}
	}
	if ln == 0 {
		return nil // FULLSTRIP
	}
	end := start + ln - 1
	sptr := a.sStart[byteAt(word, end)]
	for sptr != nil {
		if isRevSubset(sptr.rappnd, word, end, ln) {
			if a.contclasses[sptr.aflag] {
				if rv := sptr.checkTwosfx(word, start, ln, sfxopts, ppfx, needflag, tr); rv != nil {
					a.sfxflag = sptr.aflag
					if sptr.contclass == nil {
						a.sfxappnd = &sptr.rappnd
					}
					return rv
				}
			}
			sptr = sptr.nexteq
		} else {
			sptr = sptr.nextne
		}
	}
	return nil
}

func (a *AffixMgr) suffixCheckTwosfxMorph(word string, start, ln int, sfxopts int, ppfx *PfxEntry, tr *traceCtx, needflag uint16) string {
	result := newDistinctRecords(msepRec)
	for se := a.sStart[0]; se != nil; se = se.next {
		if a.contclasses[se.aflag] {
			st := se.checkTwosfxMorph(word, start, ln, sfxopts, ppfx, needflag, tr)
			if st != "" {
				var analysis strings.Builder
				if ppfx != nil {
					if ppfx.hasMorph {
						analysis.WriteString(ppfx.morphcode)
						analysis.WriteByte(msepFld)
					} else {
						a.debugflag(&analysis, ppfx.aflag)
					}
				}
				analysis.WriteString(st)
				if se.hasMorph {
					analysis.WriteByte(msepFld)
					analysis.WriteString(se.morphcode)
				} else {
					a.debugflag(&analysis, se.aflag)
				}
				result.append(analysis.String())
			}
		}
	}
	if ln == 0 {
		return "" // FULLSTRIP
	}
	end := start + ln - 1
	sptr := a.sStart[byteAt(word, end)]
	for sptr != nil {
		if isRevSubset(sptr.rappnd, word, end, ln) {
			if a.contclasses[sptr.aflag] {
				st := sptr.checkTwosfxMorph(word, start, ln, sfxopts, ppfx, needflag, tr)
				if st != "" {
					a.sfxflag = sptr.aflag
					if sptr.contclass == nil {
						a.sfxappnd = &sptr.rappnd
					}
					var r3 strings.Builder
					if sptr.hasMorph {
						r3.WriteByte(msepFld)
						r3.WriteString(sptr.morphcode)
					} else {
						a.debugflag(&r3, sptr.aflag)
					}
					result.append(strlinecat(st, r3.String()))
				}
			}
			sptr = sptr.nexteq
		} else {
			sptr = sptr.nextne
		}
	}
	return result.join()
}

func (a *AffixMgr) suffixMorphRecord(result *strings.Builder, ppfx *PfxEntry, rv *hentry, se *SfxEntry) {
	if ppfx != nil {
		if ppfx.hasMorph {
			result.WriteString(ppfx.morphcode)
			result.WriteByte(msepFld)
		} else {
			a.debugflag(result, ppfx.aflag)
		}
	}
	if d, ok := rv.entryData(); a.complexprefixes && ok {
		result.WriteString(d)
	}
	if !rv.entryFind(morphStem) {
		result.WriteByte(msepFld)
		result.WriteString(morphStem)
		result.WriteString(rv.word)
	}
	if d, ok := rv.entryData(); !a.complexprefixes && ok {
		result.WriteByte(msepFld)
		result.WriteString(d)
	}
	if se.hasMorph {
		result.WriteByte(msepFld)
		result.WriteString(se.morphcode)
	} else {
		a.debugflag(result, se.aflag)
	}
	result.WriteByte(msepRec)
}

func (a *AffixMgr) suffixCheckMorph(word string, start, ln int, sfxopts int, ppfx *PfxEntry, tr *traceCtx,
	cclass, needflag uint16, inCompound int) string {
	var result strings.Builder
	var rv *hentry
	for se := a.sStart[0]; se != nil; se = se.next {
		if cclass == 0 || se.contclass != nil {
			if a.suffixApplicable(ppfx, se, cclass, inCompound, nil) {
				rv = se.checkword(word, start, ln, sfxopts, ppfx, cclass, needflag, 0, tr)
			}
			for rv != nil {
				a.suffixMorphRecord(&result, ppfx, rv, se)
				rv = se.getNextHomonym(rv, sfxopts, ppfx, cclass, needflag)
			}
		}
	}
	if ln == 0 {
		return "" // FULLSTRIP
	}
	end := start + ln - 1
	sptr := a.sStart[byteAt(word, end)]
	for sptr != nil {
		if isRevSubset(sptr.rappnd, word, end, ln) {
			if a.suffixApplicable(ppfx, sptr, cclass, inCompound, nil) {
				rv = sptr.checkword(word, start, ln, sfxopts, ppfx, cclass, needflag, 0, tr)
			}
			for rv != nil {
				a.suffixMorphRecord(&result, ppfx, rv, sptr)
				rv = sptr.getNextHomonym(rv, sfxopts, ppfx, cclass, needflag)
			}
			sptr = sptr.nexteq
		} else {
			sptr = sptr.nextne
		}
	}
	return result.String()
}

// affixCheck checks whether a word with affixes is correctly spelled.
func (a *AffixMgr) affixCheck(word string, start, ln int, tr *traceCtx, needflag uint16, inCompound int,
	avoidflag uint16, foundPfx **PfxEntry, foundSfx **SfxEntry) *hentry {
	t := traceOn(tr)
	reportForm := func(rv *hentry) {
		if rv == nil {
			return
		}
		if t != nil {
			traceForm(t, a, substrN(word, start, ln), rv.word, a.pfx, a.sfx)
		}
		if foundPfx != nil {
			*foundPfx = a.pfx
		}
		if foundSfx != nil {
			*foundSfx = a.sfx
		}
	}
	// check all prefixes (also crossed with suffixes if allowed)
	rv := a.prefixCheck(word, start, ln, inCompound, tr, needflag, avoidflag)
	if rv != nil {
		reportForm(rv)
		return rv
	}
	// if still not found check all suffixes
	rv = a.suffixCheck(word, start, ln, 0, nil, tr, 0, needflag, inCompound, avoidflag)
	if a.havecontclass {
		if rv != nil {
			reportForm(rv)
		}
		a.sfx = nil
		a.pfx = nil
		if rv != nil {
			return rv
		}
		// if still not found check all two-level suffixes
		rv = a.suffixCheckTwosfx(word, start, ln, 0, nil, tr, needflag)
		if rv != nil {
			reportForm(rv)
			return rv
		}
		// if still not found check all two-level suffixes
		rv = a.prefixCheckTwosfx(word, start, ln, inCpdNot, tr, needflag)
	}
	reportForm(rv)
	return rv
}

// affixCheckMorph returns the analyses of a word with affixes.
func (a *AffixMgr) affixCheckMorph(word string, start, ln int, tr *traceCtx, needflag uint16, inCompound int) string {
	result := newDistinctRecords(msepRec)
	if st := a.prefixCheckMorph(word, start, ln, inCompound, tr, 0); st != "" {
		result.append(st)
	}
	if st := a.suffixCheckMorph(word, start, ln, 0, nil, tr, 0, needflag, inCompound); st != "" {
		result.append(st)
	}
	if a.havecontclass {
		a.sfx = nil
		a.pfx = nil
		if st := a.suffixCheckTwosfxMorph(word, start, ln, 0, nil, tr, needflag); st != "" {
			result.append(st)
		}
		if st := a.prefixCheckTwosfxMorph(word, start, ln, inCpdNot, tr, needflag); st != "" {
			result.append(st)
		}
	}
	return result.join()
}

func strIndexFrom(s, sub string, from int) int {
	if from < 0 || from > len(s) {
		return -1
	}
	k := strings.Index(s[from:], sub)
	if k < 0 {
		return -1
	}
	return from + k
}

// morphcmp compares the MORPH_DERI_SFX, MORPH_INFL_SFX and MORPH_TERM_SFX
// fields in the first line of the inputs: 0 if they are equal, 1 if they may
// be equal with a secondary suffix, otherwise -1.
func morphcmp(s, t string) int {
	s = cstr(s)
	t = cstr(t)
	se, te := false, false
	sl := strings.IndexByte(s, '\n')
	tl := strings.IndexByte(t, '\n')
	before := func(l, p int) bool { return l >= 0 && l < p }
	olds := 0
	oldsNull := false
	si := strIndexFrom(s, morphDeriSfx, 0)
	if si < 0 || before(sl, si) {
		si = strIndexFrom(s, morphInflSfx, olds)
	}
	if si < 0 || before(sl, si) {
		si = strIndexFrom(s, morphTermSfx, olds)
		oldsNull = true
	}
	oldt := 0
	ti := strIndexFrom(t, morphDeriSfx, 0)
	if ti < 0 || before(tl, ti) {
		ti = strIndexFrom(t, morphInflSfx, oldt)
	}
	if ti < 0 || before(tl, ti) {
		ti = strIndexFrom(t, morphTermSfx, oldt)
	}
	for si >= 0 && ti >= 0 && (sl < 0 || sl > si) && (tl < 0 || tl > ti) {
		si += morphTagLen
		ti += morphTagLen
		se, te = false, false
		if byteAt(s, si) == 0 && byteAt(t, ti) == 0 {
			se, te = true, true
		}
		for byteAt(s, si) == byteAt(t, ti) && !se && !te {
			si++
			ti++
			switch byteAt(s, si) {
			case ' ', '\n', '\t', 0:
				se = true
			}
			switch byteAt(t, ti) {
			case ' ', '\n', '\t', 0:
				te = true
			}
		}
		if !se || !te {
			// not terminal suffix difference
			if !oldsNull {
				return -1
			}
			return 1
		}
		olds = si
		oldsNull = false
		si = strIndexFrom(s, morphDeriSfx, si)
		if si < 0 || before(sl, si) {
			si = strIndexFrom(s, morphInflSfx, olds)
		}
		if si < 0 || before(sl, si) {
			si = strIndexFrom(s, morphTermSfx, olds)
			oldsNull = true
		}
		oldt = ti
		ti = strIndexFrom(t, morphDeriSfx, ti)
		if ti < 0 || before(tl, ti) {
			ti = strIndexFrom(t, morphInflSfx, oldt)
		}
		if ti < 0 || before(tl, ti) {
			ti = strIndexFrom(t, morphTermSfx, oldt)
		}
	}
	if si < 0 && ti < 0 && se && te {
		return 0
	}
	return 1
}

// morphgen generates the form of ts that has targetmorph.
func (a *AffixMgr) morphgen(ts string, ap []uint16, morph string, targetmorph string, level int, avoidflag uint16) string {
	// handle suffixes
	// (C++ returns for a NULL morph here: its callers pass a description)
	// check substandard flag
	if testaff(ap, a.substandard) {
		return ""
	}
	if morphcmp(morph, targetmorph) == 0 {
		return ts
	}
	var mymorph string
	stemmorphcatpos := -1
	// use input suffix fields, if exist
	if strings.Contains(cstr(morph), morphInflSfx) || strings.Contains(cstr(morph), morphDeriSfx) {
		mymorph = cstr(morph) + string(msepFld)
		stemmorphcatpos = len(mymorph)
	}
	for _, f := range ap {
		// A derivational suffix whose own continuation class re-enables the
		// same suffix would otherwise be applied twice here.
		if avoidflag != 0 && f == avoidflag {
			continue
		}
		for sptr := a.sFlag[byte(f&0xff)]; sptr != nil; sptr = sptr.flgnxt {
			if sptr.aflag == f && sptr.hasMorph &&
				(len(sptr.contclass) == 0 || !testaff(sptr.contclass, a.substandard)) {
				var stemmorph string
				if stemmorphcatpos >= 0 {
					mymorph = mymorph[:stemmorphcatpos] + sptr.morphcode
					stemmorph = mymorph
				} else {
					stemmorph = sptr.morphcode
				}
				cmp := morphcmp(stemmorph, targetmorph)
				if cmp == 0 {
					newword := sptr.add(ts, len(ts))
					if newword != "" {
						check := a.pHMgr.lookup(newword) // XXX extra dic
						if check == nil || check.astr == nil ||
							!(testaff(check.astr, a.forbiddenword) || testaff(check.astr, onlyUpcaseFlag)) {
							return newword
						}
					}
				}
				// recursive call for secondary suffixes
				if level == 0 && cmp == 1 && len(sptr.contclass) > 0 && !testaff(sptr.contclass, a.substandard) {
					newword := sptr.add(ts, len(ts))
					if newword != "" {
						newword2 := a.morphgen(newword, sptr.contclass, stemmorph, targetmorph, 1, sptr.aflag)
						if newword2 != "" {
							return newword2
						}
					}
				}
			}
		}
	}
	return ""
}

type guessword struct {
	word    string
	allow   bool
	orig    string
	hasOrig bool
}

// expandRootword lists the forms of a root word for the n-gram suggestions.
func (a *AffixMgr) expandRootword(maxn int, ts string, ap []uint16, bad string, phon string, hasPhon bool) []guessword {
	ts = cstr(ts)
	bad = cstr(bad)
	badl := len(bad)
	al := len(ap)
	var wlst []guessword
	// first add root word to list
	if len(wlst) < maxn && !(al > 0 && ((a.needaffix != 0 && testaff(ap, a.needaffix)) ||
		(a.onlyincompound != 0 && testaff(ap, a.onlyincompound)))) {
		wlst = append(wlst, guessword{word: ts})
		// add special phonetic version
		if hasPhon && len(wlst) < maxn {
			wlst = append(wlst, guessword{word: phon, orig: ts, hasOrig: true})
		}
	}
	// handle suffixes
	for _, f := range ap {
		for sptr := a.sFlag[byte(f&0xff)]; sptr != nil; sptr = sptr.flgnxt {
			if sptr.aflag == f &&
				(len(sptr.appnd) == 0 || (badl > len(sptr.appnd) && bad[badl-len(sptr.appnd):] == sptr.appnd)) &&
				// check needaffix flag
				!(sptr.contclass != nil && ((a.needaffix != 0 && testaff(sptr.contclass, a.needaffix)) ||
					(a.circumfix != 0 && testaff(sptr.contclass, a.circumfix)) ||
					(a.onlyincompound != 0 && testaff(sptr.contclass, a.onlyincompound)))) {
				newword := sptr.add(ts, len(ts))
				if newword != "" && len(wlst) < maxn {
					wlst = append(wlst, guessword{word: newword, allow: sptr.allowCross()})
					// add special phonetic version
					if hasPhon && len(wlst) < maxn {
						wlst = append(wlst, guessword{word: phon + reverseword(sptr.rappnd), orig: newword, hasOrig: true})
					}
				}
			}
		}
	}
	n := len(wlst)
	// handle cross products of prefixes and suffixes
	for j := 1; j < n; j++ {
		if !wlst[j].allow {
			continue
		}
		for _, f := range ap {
			for cptr := a.pFlag[byte(f&0xff)]; cptr != nil; cptr = cptr.flgnxt {
				if cptr.aflag == f && cptr.allowCross() &&
					(len(cptr.appnd) == 0 || (badl > len(cptr.appnd) && strings.HasPrefix(bad, cptr.appnd))) {
					newword := cptr.add(wlst[j].word)
					if newword != "" && len(wlst) < maxn {
						wlst = append(wlst, guessword{word: newword, allow: cptr.allowCross()})
					}
				}
			}
		}
	}
	// now handle pure prefixes
	for _, f := range ap {
		for ptr := a.pFlag[byte(f&0xff)]; ptr != nil; ptr = ptr.flgnxt {
			if ptr.aflag == f &&
				(len(ptr.appnd) == 0 || (badl > len(ptr.appnd) && strings.HasPrefix(bad, ptr.appnd))) &&
				// check needaffix flag
				!(ptr.contclass != nil && ((a.needaffix != 0 && testaff(ptr.contclass, a.needaffix)) ||
					(a.circumfix != 0 && testaff(ptr.contclass, a.circumfix)) ||
					(a.onlyincompound != 0 && testaff(ptr.contclass, a.onlyincompound)))) {
				newword := ptr.add(ts)
				if newword != "" && len(wlst) < maxn {
					wlst = append(wlst, guessword{word: newword, allow: ptr.allowCross()})
				}
			}
		}
	}
	return wlst
}

// expandAll lists the forms of a root word with one suffix, one prefix or
// both (the way of the unmunch tool), without the forms that need further
// affixes.
func (a *AffixMgr) expandAll(ts string, ap []uint16) []string {
	var wlst []guessword
	needed := func(cont []uint16) bool {
		return cont != nil && ((a.needaffix != 0 && testaff(cont, a.needaffix)) ||
			(a.circumfix != 0 && testaff(cont, a.circumfix)) ||
			(a.onlyincompound != 0 && testaff(cont, a.onlyincompound)))
	}
	if !(len(ap) > 0 && ((a.needaffix != 0 && testaff(ap, a.needaffix)) ||
		(a.onlyincompound != 0 && testaff(ap, a.onlyincompound)))) {
		wlst = append(wlst, guessword{word: ts})
	}
	var sfx []guessword
	for _, f := range ap {
		for sptr := a.sFlag[byte(f&0xff)]; sptr != nil; sptr = sptr.flgnxt {
			if sptr.aflag == f && !needed(sptr.contclass) {
				if nw := sptr.add(ts, len(ts)); nw != "" {
					sfx = append(sfx, guessword{word: nw, allow: sptr.allowCross()})
				}
			}
		}
	}
	wlst = append(wlst, sfx...)
	for _, s := range sfx {
		if !s.allow {
			continue
		}
		for _, f := range ap {
			for cptr := a.pFlag[byte(f&0xff)]; cptr != nil; cptr = cptr.flgnxt {
				if cptr.aflag == f && cptr.allowCross() && !needed(cptr.contclass) {
					if nw := cptr.add(s.word); nw != "" {
						wlst = append(wlst, guessword{word: nw})
					}
				}
			}
		}
	}
	for _, f := range ap {
		for ptr := a.pFlag[byte(f&0xff)]; ptr != nil; ptr = ptr.flgnxt {
			if ptr.aflag == f && !needed(ptr.contclass) {
				if nw := ptr.add(ts); nw != "" {
					wlst = append(wlst, guessword{word: nw})
				}
			}
		}
	}
	res := make([]string, len(wlst))
	for i, g := range wlst {
		res[i] = g.word
	}
	return res
}

func timeExceeded(start time.Time, limit time.Duration) bool {
	return since(start) > limit
}

// cpdrepCheck reports whether the word is a non-compound with a REP
// substitution (see checkcompoundrep).
func (a *AffixMgr) cpdrepCheck(inWord string, wl int, tr *traceCtx, exceeded *bool, start time.Time) bool {
	reptable := a.getReptable()
	if wl < 2 || len(reptable) == 0 {
		return false
	}
	word := substrN(inWord, 0, wl)
	for i := range reptable {
		if *exceeded || timeExceeded(start, a.limits.compound) {
			*exceeded = true
			return false
		}
		e := &reptable[i]
		// use only available mid patterns
		if e.outstrings[0] != "" {
			r := 0
			// search every occurence of the pattern in the word
			for {
				r = strIndexFrom(word, e.pattern, r)
				if r < 0 {
					break
				}
				candidate := word[:r] + e.outstrings[0] + word[r+len(e.pattern):]
				if a.candidateCheck(candidate, tr) {
					return true
				}
				r++ // search for the next letter
			}
		}
	}
	return false
}

// cpdwordpairCheck forbids compound words that are in the dictionary as a
// word pair separated by space.
func (a *AffixMgr) cpdwordpairCheck(word string, wl int, tr *traceCtx, exceeded *bool, start time.Time) bool {
	t := traceOn(tr)
	pairFound := false
	var pair string
	func() {
		// this check puts a space at every position in turn, so the trace
		// reports the verdict alone
		defer suppress(tr)()
		if wl > 2 {
			candidate := substrN(word, 0, wl)
			for i := 1; i < len(candidate); i++ {
				if *exceeded || timeExceeded(start, a.limits.compound) {
					*exceeded = true
					break
				}
				// go to end of the UTF-8 character
				if a.utf8 && isUTF8Cont(candidate[i]) {
					continue
				}
				c := candidate[:i] + " " + candidate[i:]
				if a.candidateCheck(c, tr) {
					pairFound = true
					pair = c
					break
				}
			}
		}
	}()
	if t != nil {
		if pairFound {
			trace(t, "test wordpair pair=\"%s\" -> fail, the parts are a known word pair", cstr(pair))
		} else if !*exceeded {
			trace(t, "test wordpair -> pass, no space splits this into a known word pair")
		}
	}
	return pairFound
}

func traceCondFlag(a *AffixMgr, cond uint16) string {
	if cond == 0 {
		return "(any)"
	}
	return traceFlag(a, cond)
}

// traceJoinWord describes one side of a compound join. (The C++ function
// gives "(none)" for a NULL entry, but the join is only traced when both
// sides are known.)
func traceJoinWord(a *AffixMgr, e *hentry) string {
	return "\"" + e.word + "\"/" + traceFlags(a, e.astr)
}

// joinSideHasFlag: one side of a compound join carries a flag when the
// dictionary entry has it, or when a prefix or suffix that built that side
// passes it on through its continuation classes.
func joinSideHasFlag(e *hentry, cond uint16, p *PfxEntry, s *SfxEntry) bool {
	return (e.astr != nil && testaff(e.astr, cond)) ||
		(p != nil && p.contclass != nil && testaff(p.contclass, cond)) ||
		(s != nil && s.contclass != nil && testaff(s.contclass, cond))
}

// cpdpatCheck forbids compoundings when there are special patterns at the
// word bound.
func (a *AffixMgr) cpdpatCheck(word string, pos int, r1, r2 *hentry, t *traceCtx, p1 *PfxEntry, s1 *SfxEntry, p2 *PfxEntry, s2 *SfxEntry) bool {
	for k := range a.checkcpdtable {
		i := &a.checkcpdtable[k]
		rightTextOK := isSubset(i.pattern2, substrN(word, pos, -1))
		leftFlagOK := rightTextOK && (r1 == nil || i.cond == 0 || joinSideHasFlag(r1, i.cond, p1, s1))
		rightFlagOK := leftFlagOK && (r2 == nil || i.cond2 == 0 || joinSideHasFlag(r2, i.cond2, p2, s2))
		// zero length pattern => only TESTAFF
		// zero pattern (0/flag) => unmodified stem (zero affixes allowed)
		leftTextOK := rightFlagOK &&
			(i.pattern == "" ||
				(i.pattern[0] == '0' && len(r1.word) <= pos && word[pos-len(r1.word):pos] == r1.word) ||
				(i.pattern[0] != '0' && len(i.pattern) <= pos && word[pos-len(i.pattern):pos] == i.pattern))
		if t != nil {
			fields := "left=\"" + i.pattern + "\"/" + traceCondFlag(a, i.cond) +
				" right=\"" + i.pattern2 + "\"/" + traceCondFlag(a, i.cond2) +
				" first=" + traceJoinWord(a, r1) + " second=" + traceJoinWord(a, r2)
			switch {
			case leftTextOK:
				trace(t, "test cpdpattern %s -> fail, this pair is forbidden at the join", fields)
			case !rightTextOK:
				trace(t, "test cpdpattern %s -> pass, the text after the join does not start with \"%s\"", fields, i.pattern2)
			case !leftFlagOK:
				trace(t, "test cpdpattern %s -> pass, \"%s\" has no %s", fields, r1.word, traceFlag(a, i.cond))
			case !rightFlagOK:
				trace(t, "test cpdpattern %s -> pass, \"%s\" has no %s", fields, r2.word, traceFlag(a, i.cond2))
			default:
				trace(t, "test cpdpattern %s -> pass, the text before the join does not end with \"%s\"", fields, i.pattern)
			}
		}
		if leftTextOK {
			return true
		}
	}
	return false
}

// cpdcaseCheck forbids compounding with neighbouring upper and lower case
// characters at word bounds.
func (a *AffixMgr) cpdcaseCheck(word string, pos int) bool {
	if a.utf8 {
		p := pos - 1
		for p > 0 && isUTF8Cont(byteAt(word, p)) {
			p--
		}
		pair := cstr(substrN(word, p, -1))
		pu, _ := u8u16(pair)
		var aa, b uint16
		if len(pu) > 1 {
			aa = pu[1]
		}
		if len(pu) > 0 {
			b = pu[0]
		}
		ln := a.langnum
		if ((unicodetoupper(aa, ln) == aa && unicodetolower(aa, ln) != aa) ||
			(unicodetoupper(b, ln) == b && unicodetolower(b, ln) != b)) && aa != '-' && b != '-' {
			return true
		}
	} else {
		aa, b := byteAt(word, pos-1), byteAt(word, pos)
		if (a.csconv[aa].ccase != 0 || a.csconv[b].ccase != 0) && aa != '-' && b != '-' {
			return true
		}
	}
	return false
}

type metacharData struct {
	btpp  int // metacharacter (*, ?) position for backtracking
	btwp  int // word position for metacharacters
	btnum int // number of matched characters in metacharacter
}

// defcpdCheck checks the compound patterns (COMPOUNDRULE).
func (a *AffixMgr) defcpdCheck(words *[]*hentry, wnum, maxwordnum int, rv *hentry, def []*hentry, all bool) bool {
	w := false
	if *words == nil {
		w = true
		*words = def
	}
	// C++ returns here when *words is still NULL or wnum >= maxwordnum. Every
	// caller passes either the words of a COMPOUNDRULE match or the rwords
	// buffer of maxwordnum entries, and a wnum below maxwordnum: the compound
	// checks stop before wnum + 1 reaches maxwordnum.
	reset := func() bool {
		(*words)[wnum] = nil
		if w {
			*words = nil
		}
		return false
	}
	btinfo := make([]metacharData, 1)
	bt := 0
	(*words)[wnum] = rv

	// has the last word COMPOUNDRULE flag?
	if len(rv.astr) == 0 {
		return reset()
	}
	ok := false
	for _, rule := range a.defcpdtable {
		for _, j := range rule {
			if j != '*' && j != '?' && testaff(rv.astr, j) {
				ok = true
				break
			}
		}
	}
	if !ok {
		return reset()
	}
	wds := *words
	hasFlag := func(wp int, f uint16) bool {
		return wds[wp] != nil && len(wds[wp].astr) > 0 && testaff(wds[wp].astr, f)
	}
	for _, rule := range a.defcpdtable {
		pp := 0 // pattern position
		wp := 0 // "words" position
		ok2 := true
		ok = true
		for {
			for pp < len(rule) && wp <= wnum {
				if pp+1 < len(rule) && (rule[pp+1] == '*' || rule[pp+1] == '?') {
					wend := wnum
					if rule[pp+1] == '?' {
						wend = wp
					}
					ok2 = true
					pp += 2
					btinfo[bt].btpp = pp
					btinfo[bt].btwp = wp
					for wp <= wend {
						if !hasFlag(wp, rule[pp-2]) {
							ok2 = false
							break
						}
						wp++
					}
					if wp <= wnum {
						ok2 = false
					}
					btinfo[bt].btnum = wp - btinfo[bt].btwp
					if btinfo[bt].btnum > 0 {
						bt++
						btinfo = append(btinfo, metacharData{})
					}
					if ok2 {
						break
					}
				} else {
					ok2 = true
					if !hasFlag(wp, rule[pp]) {
						ok = false
						break
					}
					pp++
					wp++
					if len(rule) == pp && !(wp > wnum) {
						ok = false
					}
				}
			}
			if ok && ok2 {
				r := pp
				for len(rule) > r && r+1 < len(rule) && (rule[r+1] == '*' || rule[r+1] == '?') {
					r += 2
				}
				if len(rule) <= r {
					return true
				}
			}
			// backtrack
			if bt != 0 {
				for {
					ok = true
					btinfo[bt-1].btnum--
					pp = btinfo[bt-1].btpp
					wp = btinfo[bt-1].btwp + btinfo[bt-1].btnum
					if !(btinfo[bt-1].btnum < 0) {
						break
					}
					bt--
					if bt == 0 {
						break
					}
				}
			}
			if bt == 0 {
				break
			}
		}
		if ok && ok2 && (!all || len(rule) <= pp) {
			return true
		}
		// C++ then skips the optional (* and ?) parts after pp, and accepts
		// the word if that reaches the end of the rule. It never does: ok and
		// ok2 are both set here only after a run of the loop above that ended
		// with ok2 set. If that run ended with ok set, it tried the same zero
		// ending already, from a pp in the same tail of optional parts. If it
		// ended with ok cleared, a part that is not optional failed after the
		// first backtracking position, where the backtracking set pp and ok
		// again, so that part is in the tail after pp.
	}
	return reset()
}

func (a *AffixMgr) candidateCheck(word string, tr *traceCtx) bool {
	if a.lookup(word) != nil {
		return true
	}
	return a.affixCheck(word, 0, len(word), tr, 0, inCpdNot, 0, nil, nil) != nil
}

// getSyllable counts the syllables of a word for compound checking.
func (a *AffixMgr) getSyllable(word string) int {
	if a.cpdmaxsyllable == 0 {
		return 0
	}
	num := 0
	if !a.utf8 {
		for i := 0; i < len(word); i++ {
			if binarySearchSigned(a.cpdvowels, word[i]) {
				num++
			}
		}
	} else if len(a.cpdvowelsUTF16) > 0 {
		w, _ := u8u16(word)
		for _, c := range w {
			if binarySearchU16(a.cpdvowelsUTF16, c) {
				num++
			}
		}
	}
	return int(int16(num))
}

// binarySearchSigned searches a string sorted in signed char order.
func binarySearchSigned(s string, c byte) bool {
	lo, hi := 0, len(s)
	for lo < hi {
		m := (lo + hi) / 2
		if int8(s[m]) < int8(c) {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo < len(s) && s[lo] == c
}

func (a *AffixMgr) setcminmax(word string, ln int) (int, int) {
	if a.utf8 {
		cmin := 0
		for i := 0; i < a.cpdmin && cmin < ln; i++ {
			for cmin++; cmin < ln && isUTF8Cont(byteAt(word, cmin)); cmin++ {
			}
		}
		cmax := ln
		for i := 0; i < a.cpdmin-1 && cmax > 0; i++ {
			for cmax--; cmax > 0 && isUTF8Cont(byteAt(word, cmax)); cmax-- {
			}
		}
		return cmin, cmax
	}
	return a.cpdmin, ln - a.cpdmin + 1
}
