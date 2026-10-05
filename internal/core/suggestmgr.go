package core

import (
	"strings"
	"time"
)

const (
	maxRoots        = 100
	maxWords        = 100
	maxGuess        = 200
	maxNgramSugs    = 4
	maxPhonSugs     = 2
	maxCompoundSugs = 3
	maxCharDistance = 4

	ngramLongerWorse = 1 << 0
	ngramAnyMismatch = 1 << 1
	ngramLowering    = 1 << 2
	ngramWeighted    = 1 << 3

	maxWordLen     = 100
	maxWordUTF8Len = maxWordLen * 3
)

const (
	lcsUp = iota
	lcsLeft
	lcsUpLeft
)

const wVLine = uint16('|')

// SuggestMgr generates the suggestions.
type SuggestMgr struct {
	ckey              string
	ckeyl             int
	ckeyUTF           []uint16
	ctry              string
	ctryl             int
	ctryUTF           []uint16
	langWithDashUsage bool
	pAMgr             *AffixMgr
	maxSug            int
	csconv            *[256]csInfo
	utf8              bool
	langnum           int
	nosplitsugs       bool
	maxngramsugs      int
	maxcpdsugs        int
	complexprefixes   bool
	suggestStart      time.Time
	rwords            [100]*hentry // buffer for COMPOUND pattern checking
}

func newSuggestMgr(tryme string, maxn int, a *AffixMgr) *SuggestMgr {
	s := &SuggestMgr{pAMgr: a, maxSug: maxn, maxngramsugs: maxNgramSugs, maxcpdsugs: maxCompoundSugs}
	if a != nil {
		s.langnum = a.langnum
		s.ckey = a.getKeyString()
		s.nosplitsugs = a.nosplitsugs
		if a.maxngramsugs >= 0 {
			s.maxngramsugs = a.maxngramsugs
		}
		s.utf8 = a.utf8
		if a.maxcpdsugs >= 0 {
			s.maxcpdsugs = a.maxcpdsugs
		}
		if !s.utf8 {
			s.csconv = getCurrentCS(a.getEncoding(), a)
		}
		s.complexprefixes = a.complexprefixes
	}
	if s.ckey != "" {
		if s.utf8 {
			w, l := u8u16(s.ckey)
			s.ckeyUTF = w
			if l != -1 {
				s.ckeyl = l
			}
		} else {
			s.ckeyl = len(s.ckey)
		}
	}
	s.ctry = tryme
	if s.ctry != "" {
		if s.utf8 {
			w, l := u8u16(s.ctry)
			s.ctryUTF = w
			if l != -1 {
				s.ctryl = l
			}
		} else {
			s.ctryl = len(s.ctry)
		}
	}
	// language with possible dash usage (latin letters or dash in TRY characters)
	s.langWithDashUsage = strings.IndexByte(s.ctry, '-') >= 0 || strings.IndexByte(s.ctry, 'a') >= 0
	return s
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// limits returns the time limits of the dictionary. (pAMgr is never nil:
// New always makes the affix manager, so the C++ checks for a NULL pAMgr
// that would be statements of their own are left out of this file.)
func (s *SuggestMgr) limits() timeLimits {
	return s.pAMgr.limits
}

func (s *SuggestMgr) suggestTimeout() bool {
	return since(s.suggestStart) > s.limits().suggestion
}

func (s *SuggestMgr) testsug(wlst *[]string, candidate string, cpdsuggest int, timer *int, tl *time.Time, info *int) {
	if len(*wlst) == s.maxSug {
		return
	}
	if !contains(*wlst, candidate) {
		if result := s.checkword(candidate, cpdsuggest, timer, tl); result != 0 {
			// compound word in the dictionary
			if cpdsuggest == 0 && result >= 2 {
				*info |= SpellCompound
			}
			*wlst = append(*wlst, candidate)
		}
	}
}

// suggest generates suggestions for a misspelled word. The return value is
// true if there is a good suggestion (REP, ph: or a dictionary word pair).
func (s *SuggestMgr) suggest(slst *[]string, w string, onlycompoundsug *int, testSimplesug bool) bool {
	nocompoundtwowords := false
	nsugorig := len(*slst)
	oldSug := 0
	goodSuggestion := false
	word := w
	// word reversing wrapper for complex prefixes
	if s.complexprefixes {
		if s.utf8 {
			word = reversewordUTF(w)
		} else {
			word = reverseword(w)
		}
	}
	s.suggestStart = clock()
	var wordUTF []uint16
	if s.utf8 {
		var wl int
		wordUTF, wl = u8u16(word)
		if wl == -1 {
			return false
		}
	}
	info := 0
	for cpdsuggest := 0; cpdsuggest < 3 && !nocompoundtwowords; cpdsuggest++ {
		// limit compound suggestion
		if cpdsuggest > 0 {
			oldSug = len(*slst)
		}
		canTry := func() bool {
			return len(*slst) < s.maxSug && (cpdsuggest == 0 || len(*slst) < oldSug+s.maxcpdsugs)
		}
		// suggestions for an uppercase word (html -> HTML)
		if len(*slst) < s.maxSug {
			i := len(*slst)
			if s.utf8 {
				s.capcharsUTF(slst, wordUTF, cpdsuggest, &info)
			} else {
				s.capchars(slst, word, cpdsuggest, &info)
			}
			if len(*slst) > i {
				goodSuggestion = true
			}
		}
		// perhaps we made a typical fault of spelling
		if canTry() {
			i := len(*slst)
			s.replchars(slst, word, cpdsuggest, &info)
			if len(*slst) > i {
				goodSuggestion = true
				if info&SpellBestSug != 0 {
					return true
				}
			}
		}
		if s.suggestTimeout() {
			return goodSuggestion
		}
		if testSimplesug && len(*slst) > 0 {
			return true
		}
		// perhaps we made chose the wrong char from a related set
		if canTry() {
			s.mapchars(slst, word, cpdsuggest, &info)
		}
		if s.suggestTimeout() {
			return goodSuggestion
		}
		if testSimplesug && len(*slst) > 0 {
			return true
		}
		// only suggest compound words when no other ~good suggestion
		if cpdsuggest == 0 && len(*slst) > nsugorig {
			nocompoundtwowords = true
		}
		steps := []func(){
			func() { // did we swap the order of chars by mistake
				if s.utf8 {
					s.swapcharUTF(slst, wordUTF, cpdsuggest, &info)
				} else {
					s.swapchar(slst, word, cpdsuggest, &info)
				}
			},
			func() { // did we swap the order of non adjacent chars by mistake
				if s.utf8 {
					s.longswapcharUTF(slst, wordUTF, cpdsuggest, &info)
				} else {
					s.longswapchar(slst, word, cpdsuggest, &info)
				}
			},
			func() { // did we just hit the wrong key in place of a good char (case and keyboard)
				if s.utf8 {
					s.badcharkeyUTF(slst, wordUTF, cpdsuggest, &info)
				} else {
					s.badcharkey(slst, word, cpdsuggest, &info)
				}
			},
			func() { // did we add a char that should not be there
				if s.utf8 {
					s.extracharUTF(slst, wordUTF, cpdsuggest, &info)
				} else {
					s.extrachar(slst, word, cpdsuggest, &info)
				}
			},
			func() { // did we forgot a char
				if s.utf8 {
					s.forgotcharUTF(slst, wordUTF, cpdsuggest, &info)
				} else {
					s.forgotchar(slst, word, cpdsuggest, &info)
				}
			},
			func() { // did we move a char
				if s.utf8 {
					s.movecharUTF(slst, wordUTF, cpdsuggest, &info)
				} else {
					s.movechar(slst, word, cpdsuggest, &info)
				}
			},
			func() { // did we just hit the wrong key in place of a good char
				if s.utf8 {
					s.badcharUTF(slst, wordUTF, cpdsuggest, &info)
				} else {
					s.badchar(slst, word, cpdsuggest, &info)
				}
			},
			func() { // did we double two characters
				if s.utf8 {
					s.doubletwocharsUTF(slst, wordUTF, cpdsuggest, &info)
				} else {
					s.doubletwochars(slst, word, cpdsuggest, &info)
				}
			},
		}
		for _, step := range steps {
			if canTry() {
				step()
			}
			if s.suggestTimeout() {
				return goodSuggestion
			}
			if testSimplesug && len(*slst) > 0 {
				return true
			}
		}
		// perhaps we forgot to hit space and two words ran together
		// (dictionary word pairs have top priority here, so we always suggest
		// them, in despite of nosplitsugs, and drop compound word and other
		// suggestions)
		if cpdsuggest == 0 || (!s.nosplitsugs && len(*slst) < oldSug+s.maxcpdsugs) {
			goodSuggestion = s.twowords(slst, word, cpdsuggest, goodSuggestion, &info)
			if info&SpellBestSug != 0 {
				return true
			}
		}
		if s.suggestTimeout() {
			return goodSuggestion
		}
		// testing returns after the first loop
		if testSimplesug {
			return len(*slst) > 0
		}
		// don't need third loop, if the second loop was successful or the
		// first loop found a dictionary-based compound word
		if cpdsuggest == 1 && (len(*slst) > oldSug || info&SpellCompound != 0) {
			nocompoundtwowords = true
		}
	}
	if !nocompoundtwowords && len(*slst) > 0 && onlycompoundsug != nil {
		*onlycompoundsug = 1
	}
	return goodSuggestion
}

// capcharsUTF: suggestions for an uppercase word (html -> HTML).
func (s *SuggestMgr) capcharsUTF(wlst *[]string, word []uint16, cpdsuggest int, info *int) {
	c := append([]uint16(nil), word...)
	mkallcapUTF(c, s.langnum)
	s.testsug(wlst, u16u8(c), cpdsuggest, nil, nil, info)
}

func (s *SuggestMgr) capchars(wlst *[]string, word string, cpdsuggest int, info *int) {
	s.testsug(wlst, mkallcap(word, s.csconv), cpdsuggest, nil, nil, info)
}

// mapchars: suggestions for when chose the wrong char out of a related set.
func (s *SuggestMgr) mapchars(wlst *[]string, word string, cpdsuggest int, info *int) {
	if len(word) < 2 || s.pAMgr == nil {
		return
	}
	maptable := s.pAMgr.maptable
	if len(maptable) == 0 {
		return
	}
	tl := clock()
	timer := minTimer
	var candidate []byte
	s.mapRelated(word, &candidate, 0, wlst, cpdsuggest, maptable, &timer, &tl, 0, info)
}

// mapRelated builds the candidates in *candidate, which the C++ code passes
// by reference and does not restore: a later MAP entry matching at the same
// position continues from what the recursion left there.
func (s *SuggestMgr) mapRelated(word string, candidate *[]byte, wn int, wlst *[]string, cpdsuggest int, maptable [][]string,
	timer *int, tl *time.Time, depth int, info *int) {
	if len(word) == wn {
		c := string(*candidate)
		if c == word {
			return
		}
		if !contains(*wlst, c) && s.checkword(c, cpdsuggest, timer, tl) != 0 {
			if len(*wlst) < s.maxSug {
				*wlst = append(*wlst, c)
			}
		}
		return
	}
	// C++ gives up past a recursion depth of 0x3F00 here; depth is at most
	// the length of the word, which suggest keeps under MAXWORDUTF8LEN bytes
	inMap := false
	for j := range maptable {
		for k := range maptable[j] {
			l := len(maptable[j][k])
			if l > 0 && substrN(word, wn, l) == maptable[j][k] {
				inMap = true
				cn := len(*candidate)
				for _, alt := range maptable[j] {
					*candidate = append((*candidate)[:cn], alt...)
					s.mapRelated(word, candidate, wn+l, wlst, cpdsuggest, maptable, timer, tl, depth+1, info)
					if *timer == 0 {
						return
					}
					if s.suggestTimeout() {
						*timer = 0
						return
					}
				}
			}
		}
	}
	if !inMap {
		*candidate = append(*candidate, word[wn])
		s.mapRelated(word, candidate, wn+1, wlst, cpdsuggest, maptable, timer, tl, depth+1, info)
	}
}

// replchars: suggestions for a typical fault of spelling, that differs with
// more, than 1 letter from the right form.
func (s *SuggestMgr) replchars(wlst *[]string, word string, cpdsuggest int, info *int) {
	if len(word) < 2 || s.pAMgr == nil {
		return
	}
	reptable := s.pAMgr.getReptable()
	start := clock()
	for ei := range reptable {
		entry := &reptable[ei]
		r := 0
		// search every occurence of the pattern in the word
		for {
			r = strIndexFrom(word, entry.pattern, r)
			if r < 0 {
				break
			}
			if since(start) > s.limits().suggestion {
				return
			}
			typ := 0
			if r == 0 {
				typ = 1
			}
			if r+len(entry.pattern) == len(word) {
				typ += 2
			}
			for typ != 0 && entry.outstrings[typ] == "" {
				if typ == 2 && r != 0 {
					typ = 0
				} else {
					typ--
				}
			}
			out := entry.outstrings[typ]
			if out == "" {
				r++
				continue
			}
			candidate := word[:r] + out + word[r+len(entry.pattern):]
			sp := strings.IndexByte(candidate, ' ')
			oldns := len(*wlst)
			s.testsug(wlst, candidate, cpdsuggest, nil, nil, info)
			if oldns < len(*wlst) {
				// REP suggestions are the best, don't search other type of suggestions
				*info |= SpellBestSug
			}
			// check REP suggestions with space
			if sp >= 0 {
				prev := 0
				for sp >= 0 {
					prevChunk := candidate[prev:sp]
					if s.checkword(prevChunk, 0, nil, nil) != 0 {
						oldns = len(*wlst)
						postChunk := candidate[sp+1:]
						s.testsug(wlst, postChunk, cpdsuggest, nil, nil, info)
						if oldns < len(*wlst) {
							(*wlst)[len(*wlst)-1] = candidate
						}
					}
					prev = sp + 1
					sp = indexFrom(candidate, ' ', prev)
				}
			}
			r++ // search for the next letter
		}
	}
}

// doubletwochars: perhaps we doubled two characters (vacation -> vacacation).
// The recognized pattern with regex back-references: "(.)(.)\1\2\1" or
// "..(.)(.)\1\2"
func (s *SuggestMgr) doubletwochars(wlst *[]string, word string, cpdsuggest int, info *int) {
	wl := len(word)
	if wl < 5 || s.pAMgr == nil {
		return
	}
	state := 0
	for i := 2; i < wl; i++ {
		if word[i] == word[i-2] {
			state++
			if state == 3 || (state == 2 && i >= 4) {
				s.testsug(wlst, word[:i-1]+word[i+1:], cpdsuggest, nil, nil, info)
				state = 0
			}
		} else {
			state = 0
		}
	}
}

func (s *SuggestMgr) doubletwocharsUTF(wlst *[]string, word []uint16, cpdsuggest int, info *int) {
	wl := len(word)
	if wl < 5 || s.pAMgr == nil {
		return
	}
	state := 0
	for i := 2; i < wl; i++ {
		if word[i] == word[i-2] {
			state++
			if state == 3 || (state == 2 && i >= 4) {
				c := append(append([]uint16(nil), word[:i-1]...), word[i+1:]...)
				s.testsug(wlst, u16u8(c), cpdsuggest, nil, nil, info)
				state = 0
			}
		} else {
			state = 0
		}
	}
}

// badcharkey: error is wrong char in place of correct one (case and keyboard
// related version).
func (s *SuggestMgr) badcharkey(wlst *[]string, word string, cpdsuggest int, info *int) {
	candidate := []byte(word)
	for i := 0; i < len(candidate); i++ {
		tmpc := candidate[i]
		// check with uppercase letters
		candidate[i] = s.csconv[tmpc].cupper
		if tmpc != candidate[i] {
			s.testsug(wlst, string(candidate), cpdsuggest, nil, nil, info)
			candidate[i] = tmpc
		}
		// check neighbor characters in keyboard string (C++ skips this for a
		// NULL ckey, but KEY has a default value)
		loc := 0
		for loc < s.ckeyl && s.ckey[loc] != tmpc {
			loc++
		}
		for loc < s.ckeyl {
			if s.suggestTimeout() {
				return
			}
			if loc > 0 && s.ckey[loc-1] != '|' {
				candidate[i] = s.ckey[loc-1]
				s.testsug(wlst, string(candidate), cpdsuggest, nil, nil, info)
			}
			if loc+1 < s.ckeyl && s.ckey[loc+1] != '|' {
				candidate[i] = s.ckey[loc+1]
				s.testsug(wlst, string(candidate), cpdsuggest, nil, nil, info)
			}
			for {
				loc++
				if !(loc < s.ckeyl && s.ckey[loc] != tmpc) {
					break
				}
			}
		}
		candidate[i] = tmpc
	}
}

func (s *SuggestMgr) badcharkeyUTF(wlst *[]string, word []uint16, cpdsuggest int, info *int) {
	candidate := append([]uint16(nil), word...)
	for i := 0; i < len(word); i++ {
		tmpc := candidate[i]
		// check with uppercase letters
		candidate[i] = upperUTF(candidate[i], 1)
		if tmpc != candidate[i] {
			s.testsug(wlst, u16u8(candidate), cpdsuggest, nil, nil, info)
			candidate[i] = tmpc
		}
		// check neighbor characters in keyboard string (KEY has a default
		// value, so ckey_utf is never empty)
		loc := 0
		for loc < s.ckeyl && s.ckeyUTF[loc] != tmpc {
			loc++
		}
		for loc < s.ckeyl {
			if s.suggestTimeout() {
				return
			}
			if loc > 0 && s.ckeyUTF[loc-1] != wVLine {
				candidate[i] = s.ckeyUTF[loc-1]
				s.testsug(wlst, u16u8(candidate), cpdsuggest, nil, nil, info)
			}
			if loc+1 < s.ckeyl && s.ckeyUTF[loc+1] != wVLine {
				candidate[i] = s.ckeyUTF[loc+1]
				s.testsug(wlst, u16u8(candidate), cpdsuggest, nil, nil, info)
			}
			for {
				loc++
				if !(loc < s.ckeyl && s.ckeyUTF[loc] != tmpc) {
					break
				}
			}
		}
		candidate[i] = tmpc
	}
}

// badchar: error is wrong char in place of correct one.
func (s *SuggestMgr) badchar(wlst *[]string, word string, cpdsuggest int, info *int) {
	candidate := []byte(word)
	tl := clock()
	timer := minTimer
	for j := 0; j < s.ctryl; j++ {
		for i := len(candidate) - 1; i >= 0; i-- {
			tmpc := candidate[i]
			if s.ctry[j] == tmpc {
				continue
			}
			candidate[i] = s.ctry[j]
			s.testsug(wlst, string(candidate), cpdsuggest, &timer, &tl, info)
			if timer == 0 {
				return
			}
			candidate[i] = tmpc
		}
	}
}

func (s *SuggestMgr) badcharUTF(wlst *[]string, word []uint16, cpdsuggest int, info *int) {
	candidate := append([]uint16(nil), word...)
	tl := clock()
	timer := minTimer
	for j := 0; j < s.ctryl; j++ {
		for i := len(candidate) - 1; i >= 0; i-- {
			tmpc := candidate[i]
			if tmpc == s.ctryUTF[j] {
				continue
			}
			candidate[i] = s.ctryUTF[j]
			s.testsug(wlst, u16u8(candidate), cpdsuggest, &timer, &tl, info)
			if timer == 0 {
				return
			}
			candidate[i] = tmpc
		}
	}
}

// extrachar: error is word has an extra letter it does not need.
func (s *SuggestMgr) extracharUTF(wlst *[]string, word []uint16, cpdsuggest int, info *int) {
	if len(word) < 2 {
		return
	}
	for i := 0; i < len(word); i++ {
		index := len(word) - 1 - i
		c := append(append([]uint16(nil), word[:index]...), word[index+1:]...)
		s.testsug(wlst, u16u8(c), cpdsuggest, nil, nil, info)
	}
}

func (s *SuggestMgr) extrachar(wlst *[]string, word string, cpdsuggest int, info *int) {
	if len(word) < 2 {
		return
	}
	for i := 0; i < len(word); i++ {
		index := len(word) - 1 - i
		s.testsug(wlst, word[:index]+word[index+1:], cpdsuggest, nil, nil, info)
	}
}

// forgotchar: error is missing a letter it needs.
func (s *SuggestMgr) forgotchar(wlst *[]string, word string, cpdsuggest int, info *int) {
	tl := clock()
	timer := minTimer
	// try inserting a tryme character before every letter (and the null terminator)
	for k := 0; k < s.ctryl; k++ {
		for i := 0; i <= len(word); i++ {
			index := len(word) - i
			s.testsug(wlst, word[:index]+s.ctry[k:k+1]+word[index:], cpdsuggest, &timer, &tl, info)
			if timer == 0 {
				return
			}
		}
	}
}

func (s *SuggestMgr) forgotcharUTF(wlst *[]string, word []uint16, cpdsuggest int, info *int) {
	tl := clock()
	timer := minTimer
	for k := 0; k < s.ctryl; k++ {
		for i := 0; i <= len(word); i++ {
			index := len(word) - i
			c := make([]uint16, 0, len(word)+1)
			c = append(c, word[:index]...)
			c = append(c, s.ctryUTF[k])
			c = append(c, word[index:]...)
			s.testsug(wlst, u16u8(c), cpdsuggest, &timer, &tl, info)
			if timer == 0 {
				return
			}
		}
	}
}

// twowords: error is should have been two words. The return value is true, if
// there is a dictionary word pair, or there was already a good suggestion
// before calling this function.
func (s *SuggestMgr) twowords(wlst *[]string, word string, cpdsuggest int, good bool, info *int) bool {
	wl := len(word)
	if wl < 3 {
		return false
	}
	forbidden := false
	if s.langnum == langHu {
		forbidden = s.checkForbidden(word)
	}
	cand := make([]byte, wl+2)
	copy(cand[1:], word)
	// the indexes stay within the NUL terminated copy of the word: the
	// loop below stops at its terminating NUL
	at := func(i int) byte { return cand[i] }
	cs := func(i int) string { // the C string at cand[i]
		e := i
		for e < len(cand) && cand[e] != 0 {
			e++
		}
		return string(cand[i:e])
	}
	mystrlen := func(t string) int {
		if s.utf8 {
			_, n := u8u16(t)
			return n
		}
		return len(t)
	}
	// split the string into two pieces after every char; if both pieces are
	// good words make them a suggestion
	for p := 1; at(p+1) != 0; p++ {
		cand[p-1] = cand[p]
		// go to end of the UTF-8 character
		for s.utf8 && isUTF8Cont(at(p+1)) {
			cand[p] = cand[p+1]
			p++
		}
		if s.utf8 && at(p+1) == 0 {
			break // last UTF-8 character
		}
		// Suggest only word pairs, if they are listed in the dictionary.
		cand[p] = ' '
		if cpdsuggest == 0 && s.checkword(cs(0), cpdsuggest, nil, nil) != 0 {
			// best solution
			*info |= SpellBestSug
			// remove not word pair suggestions
			if !good {
				good = true
				*wlst = (*wlst)[:0]
			}
			*wlst = append([]string{cs(0)}, *wlst...)
		}
		// word pairs with dash?
		if s.langWithDashUsage {
			cand[p] = '-'
			if cpdsuggest == 0 && s.checkword(cs(0), cpdsuggest, nil, nil) != 0 {
				// best solution
				*info |= SpellBestSug
				// remove not word pair suggestions
				if !good {
					good = true
					*wlst = (*wlst)[:0]
				}
				*wlst = append([]string{cs(0)}, *wlst...)
			}
		}
		if len(*wlst) < s.maxSug && !s.nosplitsugs && !good {
			cand[p] = 0
			c1 := s.checkword(cs(0), cpdsuggest, nil, nil)
			if c1 != 0 {
				c2 := s.checkword(cs(p+1), cpdsuggest, nil, nil)
				if c2 != 0 {
					// spec. Hungarian code (TODO need a better compound word support)
					if s.langnum == langHu && !forbidden &&
						// if 3 repeating letter, use - instead of space
						((at(p-1) == at(p+1) && ((p > 1 && at(p-1) == at(p-2)) || at(p-1) == at(p+2))) ||
							// or multiple compounding, with more, than 6 syllables
							(c1 == 3 && c2 >= 2)) {
						cand[p] = '-'
					} else {
						cand[p] = ' '
					}
					cwrd := !contains(*wlst, cs(0))
					if cwrd && len(*wlst) < s.maxSug {
						*wlst = append(*wlst, cs(0))
					}
					// add two word suggestion with dash, depending on the language
					// Note that cwrd doesn't modified for REP twoword sugg.
					if !s.nosplitsugs && s.langWithDashUsage && mystrlen(cs(p+1)) > 1 && mystrlen(cs(0))-mystrlen(cs(p)) > 1 {
						cand[p] = '-'
						if contains(*wlst, cs(0)) {
							cwrd = false
						}
						if len(*wlst) < s.maxSug && cwrd {
							*wlst = append(*wlst, cs(0))
						}
					}
				}
			}
		}
	}
	return good
}

// swapchar: error is adjacent letter were swapped.
func (s *SuggestMgr) swapchar(wlst *[]string, word string, cpdsuggest int, info *int) {
	if len(word) < 2 {
		return
	}
	candidate := []byte(word)
	// try swapping adjacent chars one by one
	for i := 0; i < len(candidate)-1; i++ {
		candidate[i], candidate[i+1] = candidate[i+1], candidate[i]
		s.testsug(wlst, string(candidate), cpdsuggest, nil, nil, info)
		candidate[i], candidate[i+1] = candidate[i+1], candidate[i]
	}
	// try double swaps for short words: ahev -> have, owudl -> would
	if n := len(candidate); n == 4 || n == 5 {
		candidate[0] = word[1]
		candidate[1] = word[0]
		candidate[2] = word[2]
		candidate[n-2] = word[n-1]
		candidate[n-1] = word[n-2]
		s.testsug(wlst, string(candidate), cpdsuggest, nil, nil, info)
		if n == 5 {
			candidate[0] = word[0]
			candidate[1] = word[2]
			candidate[2] = word[1]
			s.testsug(wlst, string(candidate), cpdsuggest, nil, nil, info)
		}
	}
}

func (s *SuggestMgr) swapcharUTF(wlst *[]string, word []uint16, cpdsuggest int, info *int) {
	if len(word) < 2 {
		return
	}
	candidate := append([]uint16(nil), word...)
	for i := 0; i < len(candidate)-1; i++ {
		if s.suggestTimeout() {
			return
		}
		candidate[i], candidate[i+1] = candidate[i+1], candidate[i]
		s.testsug(wlst, u16u8(candidate), cpdsuggest, nil, nil, info)
		candidate[i], candidate[i+1] = candidate[i+1], candidate[i]
	}
	// try double swaps for short words: ahev -> have, owudl -> would, suodn -> sound
	if n := len(candidate); n == 4 || n == 5 {
		candidate[0] = word[1]
		candidate[1] = word[0]
		candidate[2] = word[2]
		candidate[n-2] = word[n-1]
		candidate[n-1] = word[n-2]
		s.testsug(wlst, u16u8(candidate), cpdsuggest, nil, nil, info)
		if n == 5 {
			candidate[0] = word[0]
			candidate[1] = word[2]
			candidate[2] = word[1]
			s.testsug(wlst, u16u8(candidate), cpdsuggest, nil, nil, info)
		}
	}
}

// longswapchar: error is not adjacent letter were swapped.
func (s *SuggestMgr) longswapchar(wlst *[]string, word string, cpdsuggest int, info *int) {
	candidate := []byte(word)
	for p := 0; p < len(candidate); p++ {
		for q := 0; q < len(candidate); q++ {
			d := q - p
			if d < 0 {
				d = -d
			}
			if d > 1 && d <= maxCharDistance && candidate[p] != candidate[q] {
				candidate[p], candidate[q] = candidate[q], candidate[p]
				s.testsug(wlst, string(candidate), cpdsuggest, nil, nil, info)
				candidate[p], candidate[q] = candidate[q], candidate[p]
			}
		}
	}
}

func (s *SuggestMgr) longswapcharUTF(wlst *[]string, word []uint16, cpdsuggest int, info *int) {
	candidate := append([]uint16(nil), word...)
	for p := 0; p < len(candidate); p++ {
		for q := 0; q < len(candidate); q++ {
			d := q - p
			if d < 0 {
				d = -d
			}
			if d > 1 && d <= maxCharDistance && candidate[p] != candidate[q] {
				candidate[p], candidate[q] = candidate[q], candidate[p]
				s.testsug(wlst, u16u8(candidate), cpdsuggest, nil, nil, info)
				candidate[p], candidate[q] = candidate[q], candidate[p]
			}
		}
	}
}

// movechar: error is a letter was moved.
func (s *SuggestMgr) movechar(wlst *[]string, word string, cpdsuggest int, info *int) {
	if len(word) < 2 {
		return
	}
	candidate := []byte(word)
	// try moving a char
	for p := 0; p < len(candidate); p++ {
		for q := p + 1; q < len(candidate) && q-p <= maxCharDistance; q++ {
			candidate[q], candidate[q-1] = candidate[q-1], candidate[q]
			if q-p < 2 {
				continue // omit swap char
			}
			s.testsug(wlst, string(candidate), cpdsuggest, nil, nil, info)
		}
		copy(candidate, word)
	}
	// reverse direction: p runs from the end to the second character
	for p := len(candidate) - 1; p > 0; p-- {
		for q := p - 1; q >= 0 && p-q <= maxCharDistance; q-- {
			candidate[q], candidate[q+1] = candidate[q+1], candidate[q]
			if p-q < 2 {
				continue // omit swap char
			}
			s.testsug(wlst, string(candidate), cpdsuggest, nil, nil, info)
		}
		copy(candidate, word)
	}
}

func (s *SuggestMgr) movecharUTF(wlst *[]string, word []uint16, cpdsuggest int, info *int) {
	if len(word) < 2 {
		return
	}
	candidate := append([]uint16(nil), word...)
	for p := 0; p < len(candidate); p++ {
		for q := p + 1; q < len(candidate) && q-p <= maxCharDistance; q++ {
			candidate[q], candidate[q-1] = candidate[q-1], candidate[q]
			if q-p < 2 {
				continue // omit swap char
			}
			s.testsug(wlst, u16u8(candidate), cpdsuggest, nil, nil, info)
		}
		copy(candidate, word)
	}
	// the UTF-16 version walks back down to the first character
	for p := len(candidate) - 1; p >= 0; p-- {
		for q := p - 1; q >= 0 && p-q <= maxCharDistance; q-- {
			candidate[q], candidate[q+1] = candidate[q+1], candidate[q]
			if p-q < 2 {
				continue // omit swap char
			}
			s.testsug(wlst, u16u8(candidate), cpdsuggest, nil, nil, info)
		}
		copy(candidate, word)
	}
}

type guessEntry struct {
	word    string
	orig    string
	hasOrig bool
	set     bool
}

// ngsuggest generates a set of suggestions for very poorly spelled words.
func (s *SuggestMgr) ngsuggest(wlst *[]string, w string, rHMgr []*HashMgr, captype int) {
	var roots [maxRoots]*hentry
	var rootsphon [maxRoots]string
	var rootsphonSet [maxRoots]bool
	var scores, scoresphon [maxRoots]int
	for i := 0; i < maxRoots; i++ {
		scores[i] = -100 * i
		scoresphon[i] = -100 * i
	}
	hasRoots, hasRootsphon := false, false
	lp := maxRoots - 1
	lpphon := maxRoots - 1
	low := ngramLowering

	word := cstr(w)
	// word reversing wrapper for complex prefixes
	if s.complexprefixes {
		if s.utf8 {
			word = reversewordUTF(word)
		} else {
			word = reverseword(word)
		}
	}
	nc := len(word)
	n := nc
	nonbmp := false
	if s.utf8 {
		_, n = u8u16(word)
	}
	// set character based ngram suggestion for words with non-BMP Unicode
	// characters
	origconv := s.csconv
	if n == -1 {
		s.utf8 = false // XXX not state-free
		if s.csconv == nil {
			s.csconv = getCurrentCS("iso88591", nil)
		}
		n = nc
		nonbmp = true
		low = 0
		defer func() {
			s.csconv = origconv
			s.utf8 = true
		}()
	}
	// C++ abandons the n-gram search here for a word of more than 4 times
	// MAXWORDLEN (MAXWORDUTF8LEN) characters, which a replist entry could
	// generate. suggest rejects the words of MAXWORDLEN (MAXWORDUTF8LEN)
	// bytes or more before and after the input conversion, and the case
	// conversions keep the number of characters, so n stays below that.

	var ph *phonetable
	if s.pAMgr != nil {
		ph = s.pAMgr.phone
	}
	var target string
	if ph != nil {
		var candidate string
		if s.utf8 {
			wc, _ := u8u16(word)
			mkallcapUTF(wc, s.langnum)
			candidate = u16u8(wc)
		} else {
			candidate = word
			if !nonbmp {
				candidate = mkallcap(candidate, s.csconv)
			}
		}
		target = phonet(candidate, ph) // XXX phonet() is 8-bit (nc, not n)
	}

	var forbiddenword, nosuggest, nongramsuggest, onlyincompound uint16
	if s.pAMgr != nil {
		forbiddenword = s.pAMgr.forbiddenword
		nosuggest = s.pAMgr.nosuggest
		nongramsuggest = s.pAMgr.nongramsuggest
		onlyincompound = s.pAMgr.onlyincompound
	}

	var wWord, wTarget []uint16
	if s.utf8 {
		wWord, _ = u8u16(word)
		wTarget, _ = u8u16(target)
	}

	var wfBuf []uint16
	col := -1
	for _, hm := range rHMgr {
		var hp *hentry
		for {
			hp = hm.walkHashtable(&col, hp)
			if hp == nil {
				break
			}
			// skip exceptions
			if (abs(n-hp.clen) > 4 && !nonbmp) ||
				// don't suggest capitalized dictionary words for lower case
				// misspellings in ngram suggestions, except PHONE usage, German
				// or the capitalized word has special pronunciation
				(captype == noCap && hp.opts&hOptInitcap != 0 && ph == nil && s.langnum != langDe && hp.opts&hOptPhon == 0) ||
				// or it has one of the following special flags
				(hp.astr != nil && s.pAMgr != nil &&
					(testaff(hp.astr, forbiddenword) || testaff(hp.astr, onlyUpcaseFlag) ||
						testaff(hp.astr, nosuggest) || testaff(hp.astr, nongramsuggest) ||
						testaff(hp.astr, onlyincompound))) {
				continue
			}
			var sc int
			if s.utf8 {
				wfBuf, _ = u8u16Into(wfBuf, hp.word, false)
				wf := wfBuf
				leftcommon := s.leftcommonsubstringUTF(wWord, wf)
				if low != 0 {
					mkallsmallUTF(wf, s.langnum)
				}
				sc = s.ngramUTF(3, wWord, wf, ngramLongerWorse) + leftcommon
			} else {
				f := hp.word
				leftcommon := s.leftcommonsubstring(word, f)
				if low != 0 {
					f = mkallsmall(f, s.csconv)
				}
				sc = s.ngram(3, word, f, ngramLongerWorse) + leftcommon
			}
			// check special pronunciation
			if hp.opts&hOptPhon != 0 {
				if f, ok := copyField(hp.entryData2(), morphPhon); ok {
					var sc2 int
					if s.utf8 {
						wf, _ := u8u16(f)
						leftcommon := s.leftcommonsubstringUTF(wWord, wf)
						if low != 0 {
							mkallsmallUTF(wf, s.langnum)
						}
						sc2 = s.ngramUTF(3, wWord, wf, ngramLongerWorse) + leftcommon
					} else {
						leftcommon := s.leftcommonsubstring(word, f)
						if low != 0 {
							f = mkallsmall(f, s.csconv)
						}
						sc2 = s.ngram(3, word, f, ngramLongerWorse) + leftcommon
					}
					if sc2 > sc {
						sc = sc2
					}
				}
			}
			scphon := -20000
			if ph != nil && sc > 2 && abs(n-hp.clen) <= 3 {
				var candidate string
				if s.utf8 {
					wc, _ := u8u16(hp.word)
					mkallcapUTF(wc, s.langnum)
					candidate = u16u8(wc)
				} else {
					candidate = mkallcap(hp.word, s.csconv)
				}
				f := phonet(candidate, ph)
				if s.utf8 {
					wf, _ := u8u16(f)
					scphon = 2 * s.ngramUTF(3, wTarget, wf, ngramLongerWorse)
				} else {
					scphon = 2 * s.ngram(3, target, f, ngramLongerWorse)
				}
			}
			if sc > scores[lp] {
				scores[lp] = sc
				roots[lp] = hp
				hasRoots = true
				lval := sc
				for j := 0; j < maxRoots; j++ {
					if scores[j] < lval {
						lp = j
						lval = scores[j]
					}
				}
			}
			if scphon > scoresphon[lpphon] {
				scoresphon[lpphon] = scphon
				rootsphon[lpphon] = hp.word
				rootsphonSet[lpphon] = true
				hasRootsphon = true
				lval := scphon
				for j := 0; j < maxRoots; j++ {
					if scoresphon[j] < lval {
						lpphon = j
						lval = scoresphon[j]
					}
				}
			}
		}
	}
	if !hasRoots && !hasRootsphon {
		// with no roots there will be no guesses and no point running ngram
		return
	}

	// find minimum threshold for a passable suggestion; mangle original word
	// three differnt ways and score them to generate a minimum acceptable score
	thresh := 0
	for sp := 1; sp < 4; sp++ {
		if s.utf8 {
			wmw := append([]uint16(nil), wWord...)
			for k := sp; k < n; k += 4 {
				wmw[k] = '*'
			}
			if low != 0 {
				mkallsmallUTF(wmw, s.langnum)
			}
			thresh += s.ngramUTF(n, wWord, wmw, ngramAnyMismatch)
		} else {
			mw := []byte(word)
			for k := sp; k < n; k += 4 {
				mw[k] = '*'
			}
			m := string(mw)
			if low != 0 {
				m = mkallsmall(m, s.csconv)
			}
			thresh += s.ngram(n, word, m, ngramAnyMismatch)
		}
	}
	thresh = thresh / 3
	thresh--

	// now expand affixes on each of these root words and use length adjusted
	// ngram scores to select possible suggestions
	var guess [maxGuess]guessEntry
	var gscore [maxGuess]int
	for i := 0; i < maxGuess; i++ {
		gscore[i] = -100 * i
	}
	lp = maxGuess - 1

	for _, rp := range roots {
		if rp == nil {
			continue
		}
		var field string
		hasField := false
		if rp.opts&hOptPhon != 0 {
			if f, ok := copyField(rp.entryData2(), morphPhon); ok {
				field = f
				hasField = true
			}
		}
		glst := s.pAMgr.expandRootword(maxWords, rp.word, rp.astr, word, field, hasField)
		for _, g := range glst {
			var sc int
			if s.utf8 {
				wf, _ := u8u16(g.word)
				leftcommon := s.leftcommonsubstringUTF(wWord, wf)
				if low != 0 {
					mkallsmallUTF(wf, s.langnum)
				}
				sc = s.ngramUTF(n, wWord, wf, ngramAnyMismatch) + leftcommon
			} else {
				f := g.word
				leftcommon := s.leftcommonsubstring(word, f)
				if low != 0 {
					f = mkallsmall(f, s.csconv)
				}
				sc = s.ngram(n, word, f, ngramAnyMismatch) + leftcommon
			}
			if sc > thresh && sc > gscore[lp] {
				gscore[lp] = sc
				guess[lp] = guessEntry{word: g.word, orig: g.orig, hasOrig: g.hasOrig, set: true}
				lval := sc
				for j := 0; j < maxGuess; j++ {
					if gscore[j] < lval {
						lp = j
						lval = gscore[j]
					}
				}
			}
		}
	}

	// now we are done generating guesses; sort in order of decreasing score
	bubblesortGuess(guess[:], gscore[:])
	if ph != nil {
		bubblesortPhon(rootsphon[:], rootsphonSet[:], scoresphon[:])
	}

	// weight suggestions with a similarity index, based on the longest common
	// subsequent algorithm and resort
	isSwap := false
	re := 0
	fact := 1.0
	if s.pAMgr != nil {
		if maxd := s.pAMgr.maxdiff; maxd >= 0 {
			fact = (10.0 - float64(maxd)) / 5.0
		}
	}
	for i := 0; i < maxGuess; i++ {
		if !guess[i].set {
			continue
		}
		// lowering guess[i]
		var gl string
		var wgl []uint16
		var ln int
		if s.utf8 {
			wgl, ln = u8u16(guess[i].word)
			mkallsmallUTF(wgl, s.langnum)
			gl = u16u8(wgl)
		} else {
			gl = guess[i].word
			if !nonbmp {
				gl = mkallsmall(gl, s.csconv)
			}
			ln = len(guess[i].word)
		}
		lcs := s.lcslen(word, gl)
		// same characters with different casing
		if n == ln && n == lcs {
			gscore[i] += 2000
			break
		}
		// using 2-gram instead of 3, and other weightening
		if s.utf8 {
			wgl, _ = u8u16(gl)
			re = s.ngramUTF(2, wWord, wgl, ngramAnyMismatch|ngramWeighted)
			// low is set here: only the non-BMP mode clears it, and that mode
			// is not UTF-8 (C++ has an else branch with the word as it is)
			wf := append([]uint16(nil), wWord...)
			mkallsmallUTF(wf, s.langnum)
			re += s.ngramUTF(2, wgl, wf, ngramAnyMismatch|ngramWeighted)
		} else {
			re = s.ngram(2, word, gl, ngramAnyMismatch|ngramWeighted)
			if low != 0 {
				f := mkallsmall(word, s.csconv)
				re += s.ngram(2, gl, f, ngramAnyMismatch|ngramWeighted)
			} else {
				re += s.ngram(2, gl, word, ngramAnyMismatch|ngramWeighted)
			}
		}
		var ngramScore, leftcommonScore int
		if s.utf8 {
			ngramScore = s.ngramUTF(4, wWord, wgl, ngramAnyMismatch)
			leftcommonScore = s.leftcommonsubstringUTF(wWord, wgl)
		} else {
			ngramScore = s.ngram(4, word, gl, ngramAnyMismatch)
			leftcommonScore = s.leftcommonsubstring(word, gl)
		}
		ccp := 0
		if !nonbmp {
			var c int
			c, isSwap = s.commoncharacterpositions(word, gl)
			if c != 0 {
				ccp = 1
			}
		}
		swapBonus := 0
		if isSwap {
			swapBonus = 10
		}
		limit := 0
		if ph != nil {
			if float64(re) < float64(ln)*fact {
				limit = -1000
			}
		} else if float64(re) < float64(n+ln)*fact {
			limit = -1000
		}
		gscore[i] =
			// length of longest common subsequent minus length difference
			2*lcs - abs(n-ln) +
				// weight length of the left common substring
				leftcommonScore +
				// weight equal character positions
				ccp +
				// swap character (not neighboring)
				swapBonus +
				// ngram
				ngramScore +
				// weighted ngrams
				re +
				// different limit for dictionaries with PHONE rules
				limit
	}
	bubblesortGuess(guess[:], gscore[:])

	// phonetic version
	if ph != nil {
		for i := 0; i < maxRoots; i++ {
			if !rootsphonSet[i] {
				continue
			}
			// lowering rootphon[i]
			var gl string
			var wgl []uint16
			var ln int
			if s.utf8 {
				wgl, ln = u8u16(rootsphon[i])
				mkallsmallUTF(wgl, s.langnum)
				gl = u16u8(wgl)
			} else {
				gl = rootsphon[i]
				if !nonbmp {
					gl = mkallsmall(gl, s.csconv)
				}
				ln = len(rootsphon[i])
			}
			// weight length of the left common substring
			var leftcommonScore int
			if s.utf8 {
				leftcommonScore = s.leftcommonsubstringUTF(wWord, wgl)
			} else {
				leftcommonScore = s.leftcommonsubstring(word, gl)
			}
			// heuristic weigthing of ngram scores
			scoresphon[i] += 2*s.lcslen(word, gl) - abs(n-ln) + leftcommonScore
		}
		bubblesortPhon(rootsphon[:], rootsphonSet[:], scoresphon[:])
	}

	// copy over
	oldns := len(*wlst)
	same := false
	for i := 0; i < maxGuess; i++ {
		if !guess[i].set {
			continue
		}
		if len(*wlst) < oldns+s.maxngramsugs && len(*wlst) < s.maxSug && (!same || gscore[i] > 1000) {
			unique := true
			// leave only excellent suggestions, if exists
			if gscore[i] > 1000 {
				same = true
			} else if gscore[i] < -100 {
				same = true
				// keep the best ngram suggestions, unless in ONLYMAXDIFF mode
				if len(*wlst) > oldns || (s.pAMgr != nil && s.pAMgr.onlymaxdiff) {
					continue
				}
			}
			for _, j := range *wlst {
				// don't suggest previous suggestions or a previous suggestion
				// with prefixes or affixes
				if (!guess[i].hasOrig && strings.Contains(guess[i].word, j)) ||
					(guess[i].hasOrig && strings.Contains(guess[i].orig, j)) ||
					// check forbidden words
					s.checkword(guess[i].word, 0, nil, nil) == 0 {
					unique = false
					break
				}
			}
			if unique {
				if guess[i].hasOrig {
					*wlst = append(*wlst, guess[i].orig)
				} else {
					*wlst = append(*wlst, guess[i].word)
				}
			}
		}
	}

	oldns = len(*wlst)
	if ph != nil {
		for i := 0; i < maxRoots; i++ {
			if !rootsphonSet[i] {
				continue
			}
			if len(*wlst) < oldns+maxPhonSugs && len(*wlst) < s.maxSug {
				unique := true
				for _, j := range *wlst {
					// don't suggest previous suggestions or a previous
					// suggestion with prefixes or affixes
					if strings.Contains(rootsphon[i], j) ||
						// check forbidden words
						s.checkword(rootsphon[i], 0, nil, nil) == 0 {
						unique = false
						break
					}
				}
				if unique {
					*wlst = append(*wlst, rootsphon[i])
				}
			}
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// checkword sees if a candidate suggestion is spelled correctly. The return
// value 2 and 3 marks compounding (obsolote MySpell-HU modifications), 3 marks
// roots without suffix.
func (s *SuggestMgr) checkword(word string, cpdsuggest int, timer *int, tl *time.Time) int {
	// check overall suggestion time limit
	if s.suggestTimeout() {
		return 0
	}
	// check time limit
	if timer != nil {
		*timer--
		if *timer == 0 && tl != nil {
			if since(*tl) > s.limits().compound {
				return 0
			}
			*timer = maxPlusTimer
		}
	}
	a := s.pAMgr
	if cpdsuggest >= 1 {
		if a.getCompound() {
			s.rwords = [100]*hentry{}
			rwords := s.rwords[:]
			info := 0
			if cpdsuggest == 1 {
				info = SpellCompound2
			}
			rv := a.compoundCheck(word, 0, 0, 100, 0, nil, rwords, false, true, &info, nil) // EXT
			// TODO filter 3-word or more compound words, as in spell()
			if rv != nil {
				rv2 := a.lookup(word)
				if rv2 == nil || rv2.astr == nil ||
					!(testaff(rv2.astr, a.forbiddenword) || testaff(rv2.astr, a.nosuggest)) {
					return 3 // XXX obsolote categorisation + only ICONV needs affix flag check?
				}
			}
		}
		return 0
	}
	nosuffix := 0
	rv := a.lookup(word)
	if rv != nil {
		if rv.astr != nil && (testaff(rv.astr, a.forbiddenword) || testaff(rv.astr, a.nosuggest) ||
			testaff(rv.astr, a.substandard)) {
			return 0
		}
		for rv != nil {
			if rv.astr != nil && (testaff(rv.astr, a.needaffix) || testaff(rv.astr, onlyUpcaseFlag) ||
				testaff(rv.astr, a.onlyincompound)) {
				rv = rv.nextHomonym
			} else {
				break
			}
		}
	} else {
		rv = a.prefixCheck(word, 0, len(word), 0, nil, 0, 0) // only prefix, and prefix + suffix XXX
	}
	if rv != nil {
		nosuffix = 1
	} else {
		rv = a.suffixCheck(word, 0, len(word), 0, nil, nil, 0, 0, inCpdNot, 0) // only suffix
	}
	if rv == nil && a.havecontclass {
		rv = a.suffixCheckTwosfx(word, 0, len(word), 0, nil, nil, 0)
		if rv == nil {
			rv = a.prefixCheckTwosfx(word, 0, len(word), 0, nil, 0)
		}
	}
	// check forbidden words
	if rv != nil && rv.astr != nil && (testaff(rv.astr, a.forbiddenword) || testaff(rv.astr, onlyUpcaseFlag) ||
		testaff(rv.astr, a.nosuggest) || testaff(rv.astr, a.onlyincompound)) {
		return 0
	}
	if rv != nil { // XXX obsolote
		if a.compoundflag != 0 && testaff(rv.astr, a.compoundflag) {
			return 2 + nosuffix
		}
		return 1
	}
	return 0
}

func (s *SuggestMgr) checkForbidden(word string) bool {
	a := s.pAMgr
	rv := a.lookup(word)
	if rv != nil && rv.astr != nil && (testaff(rv.astr, a.needaffix) || testaff(rv.astr, a.onlyincompound)) {
		rv = nil
	}
	if a.prefixCheck(word, 0, len(word), 1, nil, 0, 0) == nil {
		rv = a.suffixCheck(word, 0, len(word), 0, nil, nil, 0, 0, inCpdNot, 0) // prefix+suffix, suffix
	}
	// check forbidden words
	return rv != nil && rv.astr != nil && testaff(rv.astr, a.forbiddenword)
}

func (s *SuggestMgr) suggestMorph(inW string) string {
	a := s.pAMgr
	w := inW
	// word reversing wrapper for complex prefixes
	if s.complexprefixes {
		if s.utf8 {
			w = reversewordUTF(w)
		} else {
			w = reverseword(w)
		}
	}
	var result strings.Builder
	for rv := a.lookup(w); rv != nil; rv = rv.nextHomonym {
		if rv.astr == nil || !(testaff(rv.astr, a.forbiddenword) || testaff(rv.astr, a.needaffix) ||
			testaff(rv.astr, a.onlyincompound)) {
			if !rv.entryFind(morphStem) {
				result.WriteByte(msepFld)
				result.WriteString(morphStem)
				result.WriteString(cstr(w))
			}
			if d, ok := rv.entryData(); ok {
				result.WriteByte(msepFld)
				result.WriteString(d)
			}
			result.WriteByte(msepRec)
		}
	}
	res := result.String()
	res += a.affixCheckMorph(w, 0, len(w), nil, 0, inCpdNot)
	if a.getCompound() && res == "" {
		rwords := make([]*hentry, 100) // buffer for COMPOUND pattern checking
		a.compoundCheckMorph(w, 0, 0, 100, 0, nil, rwords, &res, nil, nil)
	}
	return lineUniq(res, msepRec)
}

func getSfxcount(morph string, has bool) int {
	morph = cstr(morph)
	if !has || morph == "" {
		return 0
	}
	n := 0
	old := 0
	m := strIndexFrom(morph, morphDeriSfx, 0)
	if m < 0 {
		m = strIndexFrom(morph, morphInflSfx, old)
	}
	if m < 0 {
		m = strIndexFrom(morph, morphTermSfx, old)
	}
	for m >= 0 {
		n++
		old = m
		m = strIndexFrom(morph, morphDeriSfx, m+1)
		if m < 0 {
			m = strIndexFrom(morph, morphInflSfx, old+1)
		}
		if m < 0 {
			m = strIndexFrom(morph, morphTermSfx, old+1)
		}
	}
	return n
}

// suggestHentryGen does the affixation of one entry.
func (s *SuggestMgr) suggestHentryGen(rv *hentry, pattern string) string {
	a := s.pAMgr
	var result strings.Builder
	sfxcount := getSfxcount(pattern, true)
	data, hasData := rv.entryData()
	if getSfxcount(data, hasData) > sfxcount {
		return ""
	}
	if hasData {
		if aff := a.morphgen(rv.word, rv.astr, data, pattern, 0, 0); aff != "" {
			result.WriteString(aff)
			result.WriteByte(msepRec)
		}
	}
	// check all allomorphs
	if !hasData {
		return result.String()
	}
	d2 := cstr(rv.entryData2())
	p := strings.Index(d2, morphAllomorph)
	for p >= 0 {
		p += morphTagLen
		plen := fieldlen(d2[p:])
		allomorph := d2[p : p+plen]
		for rv2 := a.lookup(allomorph); rv2 != nil; rv2 = rv2.nextHomonym {
			if d, ok := rv2.entryData(); ok {
				d = cstr(d)
				if st := strings.Index(d, morphStem); st >= 0 {
					stv := d[st+morphTagLen:]
					fl := fieldlen(stv)
					if strncmp(stv, rv.word, fl) == 0 {
						if aff := a.morphgen(rv2.word, rv2.astr, d, pattern, 0, 0); aff != "" {
							result.WriteString(aff)
							result.WriteByte(msepRec)
						}
					}
				}
			}
		}
		p = strIndexFrom(d2, morphAllomorph, p+plen)
	}
	return result.String()
}

func (s *SuggestMgr) suggestGen(desc []string, inPattern string, startTime time.Time, hasStart bool) string {
	if len(desc) == 0 || s.pAMgr == nil {
		return ""
	}
	expired := func() bool { return hasStart && since(startTime) > s.limits().global }
	pattern := cstr(inPattern)
	var result2 strings.Builder
	// search affixed forms with and without derivational suffixes
	for {
		for _, k := range desc {
			if expired() {
				return result2.String()
			}
			var result strings.Builder
			// add compound word parts (except the last one)
			lastpart := appendCompoundParts(k, &result)
			sp := k[lastpart:]
			tok := []byte(k[lastpart:])
			for pos := strIndexFrom(string(tok), " | ", 0); pos >= 0; pos = strIndexFrom(string(tok), " | ", pos) {
				tok[pos+1] = msepAlt
			}
			pl := lineTok(string(tok), msepAlt)
			for _, i := range pl {
				// remove inflectional and terminal suffixes
				if is := strings.Index(i, morphInflSfx); is >= 0 {
					i = i[:is]
				}
				ib := []byte(i)
				for ts := strings.Index(string(ib), morphTermSfx); ts >= 0; ts = strings.Index(string(ib), morphTermSfx) {
					ib[ts] = '_'
				}
				i = string(ib)
				if st := strings.Index(cstr(sp), morphStem); st >= 0 {
					stem, _ := copyField(cstr(sp)[st:], morphStem)
					for rv := s.pAMgr.lookup(stem); rv != nil; rv = rv.nextHomonym {
						if expired() {
							return result2.String()
						}
						newpat := i + pattern
						sg := s.suggestHentryGen(rv, newpat)
						if sg == "" {
							sg = s.suggestHentryGen(rv, pattern)
						}
						if sg != "" {
							for _, j := range lineTok(sg, msepRec) {
								result2.WriteByte(msepRec)
								result2.WriteString(result.String())
								if strings.Contains(i, morphSurfPfx) {
									field, _ := copyField(i, morphSurfPfx)
									result2.WriteString(field)
								}
								result2.WriteString(j)
							}
						}
					}
				}
			}
		}
		if result2.Len() > 0 || !strings.Contains(pattern, morphDeriSfx) {
			break
		}
		pattern = mystrrep(pattern, morphDeriSfx, morphTermSfx)
	}
	return result2.String()
}

// ngramUTF generates an n-gram score comparing su1 and su2.
func (s *SuggestMgr) ngramUTF(n int, su1, su2 []uint16, opt int) int {
	l1, l2 := len(su1), len(su2)
	if l2 == 0 {
		return 0
	}
	nscore := 0
	for j := 1; j <= n; j++ {
		ns := 0
		for i := 0; i <= l1-j; i++ {
			needle := su1[i : i+j]
			first := needle[0]
			found := false
			if l2-j < 0 {
				goto score
			}
			for l, c := range su2[:l2-j+1] {
				if c != first {
					continue
				}
				hay := su2[l : l+j]
				k := 1
				for k < j && hay[k] == needle[k] {
					k++
				}
				if k == j {
					found = true
					break
				}
			}
		score:
			if found {
				ns++
			} else if opt&ngramWeighted != 0 {
				ns--
				if i == 0 || i == l1-j {
					ns-- // side weight
				}
			}
		}
		nscore += ns
		if ns < 2 && opt&ngramWeighted == 0 {
			break
		}
	}
	ns := 0
	if opt&ngramLongerWorse != 0 {
		ns = (l2 - l1) - 2
	}
	if opt&ngramAnyMismatch != 0 {
		ns = abs(l2-l1) - 2
	}
	if ns > 0 {
		return nscore - ns
	}
	return nscore
}

// ngram generates an n-gram score comparing s1 and s2, 8-bit version.
func (s *SuggestMgr) ngram(n int, s1, s2 string, opt int) int {
	l2 := len(s2)
	if l2 == 0 {
		return 0
	}
	l1 := len(s1)
	nscore := 0
	for j := 1; j <= n; j++ {
		ns := 0
		for i := 0; i <= l1-j; i++ {
			// s2 is haystack, s1[i..i+j) is needle
			if strings.Contains(s2, s1[i:i+j]) {
				ns++
			} else if opt&ngramWeighted != 0 {
				ns--
				if i == 0 || i == l1-j {
					ns-- // side weight
				}
			}
		}
		nscore += ns
		if ns < 2 && opt&ngramWeighted == 0 {
			break
		}
	}
	ns := 0
	if opt&ngramLongerWorse != 0 {
		ns = (l2 - l1) - 2
	}
	if opt&ngramAnyMismatch != 0 {
		ns = abs(l2-l1) - 2
	}
	if ns > 0 {
		return nscore - ns
	}
	return nscore
}

// leftcommonsubstringUTF is the length of the left common substring of su1
// and the decapitalised su2.
func (s *SuggestMgr) leftcommonsubstringUTF(su1, su2 []uint16) int {
	l1, l2 := len(su1), len(su2)
	if s.complexprefixes {
		if l1 > 0 && l2 > 0 && su1[l1-1] == su2[l2-1] {
			return 1
		}
		return 0
	}
	var idx, otheridx uint16
	if l2 > 0 {
		idx = su2[0]
	}
	if l1 > 0 {
		otheridx = su1[0]
	}
	if otheridx != idx && otheridx != unicodetolower(idx, s.langnum) {
		return 0
	}
	i := 1
	for i < l1 && i < l2 && su1[i] == su2[i] {
		i++
	}
	return i
}

func (s *SuggestMgr) leftcommonsubstring(s1, s2 string) int {
	if s.complexprefixes {
		l1, l2 := len(cstr(s1)), len(cstr(s2))
		if l1 > 0 && l1 <= l2 && s1[l1-1] == s2[l2-1] {
			return 1
		}
	} else if s.csconv != nil {
		c1, c2 := byteAt(s1, 0), byteAt(s2, 0)
		// decapitalise dictionary word
		if c1 != c2 && c1 != s.csconv[c2].clower {
			return 0
		}
		i := 0
		for {
			i++
			if !(byteAt(s1, i) == byteAt(s2, i) && byteAt(s1, i) != 0) {
				break
			}
		}
		return i
	}
	return 0
}

func (s *SuggestMgr) commoncharacterpositions(s1, s2 string) (int, bool) {
	num, diff := 0, 0
	var diffpos [2]int
	isSwap := false
	s1, s2 = cstr(s1), cstr(s2)
	if s.utf8 {
		// C++ returns 0 here for an empty string or one past the BMP, which
		// ngsuggest never passes: it only scores BMP words this way, and an
		// empty word (a C string cut by a NUL) selects only empty guesses,
		// which end the scoring before this call
		su1, l1 := u8u16(s1)
		su2, l2 := u8u16(s2)
		// decapitalize dictionary word
		if s.complexprefixes {
			su2[l2-1] = lowerUTF(su2[l2-1], s.langnum)
		} else {
			su2[0] = lowerUTF(su2[0], s.langnum)
		}
		for i := 0; i < l1 && i < l2; i++ {
			if su1[i] == su2[i] {
				num++
			} else {
				if diff < 2 {
					diffpos[diff] = i
				}
				diff++
			}
		}
		if diff == 2 && l1 == l2 && su1[diffpos[0]] == su2[diffpos[1]] && su1[diffpos[1]] == su2[diffpos[0]] {
			isSwap = true
		}
	} else {
		// (C++ returns 0 here for an empty string, see above)
		t := []byte(s2)
		// decapitalize dictionary word
		if s.complexprefixes {
			t[len(t)-1] = s.csconv[t[len(t)-1]].clower
		} else {
			t = []byte(mkallsmall(string(t), s.csconv))
		}
		i := 0
		for ; i < len(t) && i < len(s1); i++ {
			if s1[i] == t[i] {
				num++
			} else {
				if diff < 2 {
					diffpos[diff] = i
				}
				diff++
			}
		}
		if diff == 2 && i == len(s1) && i == len(t) && s1[diffpos[0]] == t[diffpos[1]] && s1[diffpos[1]] == t[diffpos[0]] {
			isSwap = true
		}
	}
	return num, isSwap
}

// bubblesortGuess sorts in decreasing order of score.
func bubblesortGuess(rword []guessEntry, rsc []int) {
	n := len(rsc)
	for m := 1; m < n; m++ {
		for j := m; j > 0; j-- {
			if rsc[j-1] < rsc[j] {
				rsc[j-1], rsc[j] = rsc[j], rsc[j-1]
				rword[j-1], rword[j] = rword[j], rword[j-1]
			} else {
				break
			}
		}
	}
}

func bubblesortPhon(rword []string, set []bool, rsc []int) {
	n := len(rsc)
	for m := 1; m < n; m++ {
		for j := m; j > 0; j-- {
			if rsc[j-1] < rsc[j] {
				rsc[j-1], rsc[j] = rsc[j], rsc[j-1]
				rword[j-1], rword[j] = rword[j], rword[j-1]
				set[j-1], set[j] = set[j], set[j-1]
			} else {
				break
			}
		}
	}
}

// lcslen is the length of the longest common subsequence of s and s2.
func (s *SuggestMgr) lcslen(s1, s2 string) int {
	s1, s2 = cstr(s1), cstr(s2)
	var su, su2 []uint16
	var m, n int
	if s.utf8 {
		su, m = u8u16(s1)
		su2, n = u8u16(s2)
	} else {
		m, n = len(s1), len(s2)
	}
	if m <= 0 || n <= 0 {
		return 0
	}
	// the C++ code counts in a char array
	c := make([]int8, (m+1)*(n+1))
	b := make([]int8, (m+1)*(n+1))
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			eq := false
			if s.utf8 {
				eq = su[i-1] == su2[j-1]
			} else {
				eq = s1[i-1] == s2[j-1]
			}
			if eq {
				c[i*(n+1)+j] = c[(i-1)*(n+1)+j-1] + 1
				b[i*(n+1)+j] = lcsUpLeft
			} else if c[(i-1)*(n+1)+j] >= c[i*(n+1)+j-1] {
				c[i*(n+1)+j] = c[(i-1)*(n+1)+j]
				b[i*(n+1)+j] = lcsUp
			} else {
				c[i*(n+1)+j] = c[i*(n+1)+j-1]
				b[i*(n+1)+j] = lcsLeft
			}
		}
	}
	l := 0
	i, j := m, n
	for i != 0 && j != 0 {
		switch b[i*(n+1)+j] {
		case lcsUpLeft:
			l++
			i--
			j--
		case lcsUp:
			i--
		default:
			j--
		}
	}
	return l
}
