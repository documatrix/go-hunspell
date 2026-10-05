package core

import (
	"strings"
	"time"
	"unsafe"
)

// info options
const (
	SpellCompound  = 1 << 0 // the result is a compound word
	SpellForbidden = 1 << 1
	SpellAllCap    = 1 << 2
	SpellNoCap     = 1 << 3
	SpellInitCap   = 1 << 4
	SpellOrigCap   = 1 << 5
	SpellWarn      = 1 << 6
	SpellCompound2 = 1 << 7 // permit only 2 dictionary words in the compound
	SpellBestSug   = 1 << 8 // limit suggestions for the best ones, i.e. ph:
)

const (
	actNext = iota // run the loop condition
	actBreak
	actReturn
)

func setByte(st []byte, k int, v byte) {
	if k >= 0 && k < len(st) {
		st[k] = v
	}
}

// compoundCheck checks whether a compound word is correctly spelled.
// huMovRule is the special Hungarian rule.
func (a *AffixMgr) compoundCheck(word string, wordnum, numsyllable, maxwordnum, wnum int, words, rwords []*hentry,
	huMovRule, isSug bool, info *int, tr *traceCtx) *hentry {
	var oldnumsyllable, oldnumsyllable2, oldwordnum, oldwordnum2 int
	var rv, rvFirst *hentry
	var ch byte
	striple, soldi, oldcmin, oldcmax, oldlen := 0, 0, 0, 0, 0
	checkedstriple := 0
	oldwords := words
	scpd := 0
	ln := len(word)
	t := traceOn(tr)

	// protect subsequent words[wnum + 1] reads and any recursion
	if wnum+1 >= maxwordnum {
		return nil
	}
	checkedPrefix := false

	// add a time limit to handle possible combinatorical explosion of the
	// overlapping words
	now := clock()
	if wnum == 0 {
		a.cpdStart = now
		a.cpdTimeExceeded = false
	} else if now.Sub(a.cpdStart) > a.limits.compound {
		a.cpdTimeExceeded = true
	}

	cmin, cmax := a.setcminmax(word, ln)
	// with SIMPLIFIEDTRIPLE a second part that starts at a doubled letter is
	// one letter longer than the surface shows, so the split can go one letter
	// past cmax
	cmaxtriple := cmax
	if a.simplifiedtriple {
		if a.utf8 {
			cmaxtriple = utf8Next(word, cmax)
		} else {
			cmaxtriple = cmax + 1
		}
	}
	st := []byte(word)
	cf, cb, cm, ce := a.compoundflag, a.compoundbegin, a.compoundmiddle, a.compoundend
	cff := a.compoundforbidflag
	inc := inCpdBegin
	if huMovRule {
		inc = inCpdOther
	}
	contHas := func(c []uint16, f uint16) bool { return c != nil && testaff(c, f) }
	pfxHas := func(f uint16) bool { return a.pfx != nil && contHas(a.pfx.contclass, f) }
	sfxHas := func(f uint16) bool { return a.sfx != nil && contHas(a.sfx.contclass, f) }
	badEntry := func(r *hentry) bool {
		return r.astr != nil && (testaff(r.astr, a.forbiddenword) || testaff(r.astr, onlyUpcaseFlag) ||
			(isSug && a.nosuggest != 0 && testaff(r.astr, a.nosuggest)))
	}
	origcap := func() bool { return info != nil && *info&SpellOrigCap != 0 }

	for i := cmin; i < cmaxtriple; i++ {
		// go to end of the UTF-8 character
		if a.utf8 {
			for isUTF8Cont(byteAt(string(st), i)) {
				i++
			}
			if i >= cmaxtriple {
				return nil
			}
		}
		if i >= cmax && !(i > 2 && word[i-1] == word[i-2]) {
			break
		}
		words = oldwords
		onlycpdrule := 0
		if words != nil {
			onlycpdrule = 1
		}

		for { // onlycpdrule loop
			oldnumsyllable = numsyllable
			oldwordnum = wordnum
			checkedPrefix = false

			simplifiedCond := func() bool {
				return onlycpdrule == 0 && a.simplifiedcpd && scpd <= len(a.checkcpdtable)
			}
			for { // simplified checkcompoundpattern loop
				act, ret := func() (int, *hentry) {
					if a.cpdTimeExceeded || a.cpdClockExceeded(a.cpdStart) {
						if t != nil && !a.cpdTimeExceeded {
							trace(t, "test timelimit -> fail, the compound search stops here and gives up")
						}
						a.cpdTimeExceeded = true
						return actReturn, nil
					}
					if scpd > 0 {
						for ; scpd <= len(a.checkcpdtable) &&
							(a.checkcpdtable[scpd-1].pattern3 == "" || i > len(word) ||
								substrN(word, i, len(a.checkcpdtable[scpd-1].pattern3)) != a.checkcpdtable[scpd-1].pattern3); scpd++ {
						}
						if scpd > len(a.checkcpdtable) {
							return actBreak, nil // break simplified checkcompoundpattern loop
						}
						p := &a.checkcpdtable[scpd-1]
						st = append(st[:i:i], p.pattern...)
						soldi = i
						i += len(p.pattern)
						st = append(st[:i:i], p.pattern2...)
						st = append(st[:i+len(p.pattern2):i+len(p.pattern2)], substrN(word, soldi+len(p.pattern3), -1)...)
						oldlen = ln
						ln += len(p.pattern) + len(p.pattern2) - len(p.pattern3)
						oldcmin = cmin
						oldcmax = cmax
						cmin, cmax = a.setcminmax(string(st), ln)
						cmax = ln - a.cpdmin + 1
					}
					if i >= len(st) {
						return actReturn, nil
					}
					ch = st[i]
					if t != nil {
						// a non-zero scpd means this split is the retry that one
						// CHECKCOMPOUNDPATTERN entry asks for
						if scpd != 0 {
							trace(t, "split at=%d left=\"%s\" right=\"%s\" cpdpattern=%d", i, cstr(string(st[:i])), cstr(string(st[i:])), scpd)
						} else {
							trace(t, "split at=%d left=\"%s\" right=\"%s\"", i, cstr(string(st[:i])), cstr(string(st[i:])))
						}
					}
					// everything this split point tries belongs to the split
					defer t.enter()()
					st[i] = 0
					a.sfx = nil
					a.pfx = nil

					// FIRST WORD
					affixed := true
					_ = affixed
					rv = a.lookup(string(st[:i])) // perhaps without prefix
					if t != nil {
						if rv != nil {
							trace(t, "first \"%s\" -> entry \"%s\" flags=%s", cstr(string(st)), rv.word, traceFlags(a, rv.astr))
						} else {
							trace(t, "first \"%s\" -> miss", cstr(string(st)))
						}
					}

					// forbid dictionary stems with COMPOUNDFORBIDFLAG in compound
					// words, overriding the effect of COMPOUNDPERMITFLAG
					if rv != nil && cff != 0 && testaff(rv.astr, cff) && !huMovRule {
						wouldContinue := onlycpdrule == 0 && a.simplifiedcpd
						if scpd == 0 && wouldContinue {
							a.warnf("break infinite loop\n")
							return actBreak, nil
						}
						if scpd > 0 && wouldContinue {
							cmin = oldcmin
							cmax = oldcmax
						}
						return actNext, nil
					}

					// search homonym with compound flag
					for rv != nil && !huMovRule &&
						((a.needaffix != 0 && testaff(rv.astr, a.needaffix)) ||
							!((cf != 0 && words == nil && onlycpdrule == 0 && testaff(rv.astr, cf)) ||
								(cb != 0 && wordnum == 0 && onlycpdrule == 0 && testaff(rv.astr, cb)) ||
								(cm != 0 && wordnum != 0 && words == nil && onlycpdrule == 0 && testaff(rv.astr, cm)) ||
								(len(a.defcpdtable) > 0 && onlycpdrule != 0 &&
									((words == nil && wordnum == 0 && a.defcpdCheck(&words, wnum, maxwordnum, rv, rwords, false)) ||
										(words != nil && a.defcpdCheck(&words, wnum, maxwordnum, rv, rwords, false))))) ||
							(scpd != 0 && a.checkcpdtable[scpd-1].cond != 0 && !testaff(rv.astr, a.checkcpdtable[scpd-1].cond))) {
						rv = rv.nextHomonym
					}

					if rv != nil {
						affixed = false
					}

					stS := bytesView(st)
					if rv == nil {
						if onlycpdrule != 0 {
							return actBreak, nil
						}
						if cf != 0 {
							rv = a.prefixCheck(stS, 0, i, inc, tr, cf, 0)
							if rv == nil {
								rv = a.suffixCheck(stS, 0, i, 0, nil, tr, 0, cf, inc, 0)
								if rv == nil && a.compoundmoresuffixes {
									rv = a.suffixCheckTwosfx(stS, 0, i, 0, nil, tr, cf)
								}
								if rv != nil && !huMovRule && a.sfx != nil && a.sfx.contclass != nil &&
									((cff != 0 && testaff(a.sfx.contclass, cff)) || (ce != 0 && testaff(a.sfx.contclass, ce))) {
									rv = nil
									// the suffix is dropped with the word it built
									a.sfx = nil
								}
							}
						}
						tryFlag := func(flag uint16) bool {
							if rv = a.suffixCheck(stS, 0, i, 0, nil, tr, 0, flag, inc, 0); rv != nil {
								return true
							}
							if a.compoundmoresuffixes {
								// twofold suffixes + compound
								if rv = a.suffixCheckTwosfx(stS, 0, i, 0, nil, tr, flag); rv != nil {
									return true
								}
							}
							rv = a.prefixCheck(stS, 0, i, inc, tr, flag, 0)
							return rv != nil
						}
						if rv != nil || (wordnum == 0 && cb != 0 && tryFlag(cb)) || (wordnum > 0 && cm != 0 && tryFlag(cm)) {
							checkedPrefix = true
						}
						// else check forbiddenwords and needaffix
					} else if rv.astr != nil && (testaff(rv.astr, a.forbiddenword) || testaff(rv.astr, a.needaffix) ||
						testaff(rv.astr, onlyUpcaseFlag) || (isSug && a.nosuggest != 0 && testaff(rv.astr, a.nosuggest))) {
						st[i] = ch
						return actBreak, nil
					}

					// check non_compound flag in suffix and prefix
					if rv != nil && !huMovRule && (pfxHas(cff) || sfxHas(cff)) {
						rv = nil
					}
					// C++ also drops rv here when !checked_prefix and the prefix or
					// suffix has the COMPOUNDEND or (for the first word)
					// COMPOUNDMIDDLE flag. Both are reset at each split point, and
					// an rv without checked_prefix comes from the lookup alone, so
					// neither is set then.
					// check forbiddenwords
					if rv != nil && badEntry(rv) {
						return actReturn, nil
					}
					// increment word number, if the second root has a compoundroot flag
					if rv != nil && a.compoundroot != 0 && testaff(rv.astr, a.compoundroot) {
						wordnum++
					}

					// first word is acceptable in compound words?
					firstOK := rv != nil &&
						(checkedPrefix || (words != nil && words[wnum] != nil) ||
							(cf != 0 && testaff(rv.astr, cf)) ||
							(oldwordnum == 0 && cb != 0 && testaff(rv.astr, cb)) ||
							(oldwordnum > 0 && cm != 0 && testaff(rv.astr, cm)) ||
							// LANG_hu section: spec. Hungarian rule
							(a.langnum == langHu && huMovRule &&
								(testaff(rv.astr, 'F') || testaff(rv.astr, 'G') || testaff(rv.astr, 'H')))) &&
						// test CHECKCOMPOUNDPATTERN conditions
						(scpd == 0 || a.checkcpdtable[scpd-1].cond == 0 ||
							joinSideHasFlag(rv, a.checkcpdtable[scpd-1].cond, a.pfx, a.sfx)) &&
						!((a.checkcompoundtriple && scpd == 0 && words == nil && i < len(word) && // test triple letters
							word[i-1] == word[i] &&
							((i > 1 && word[i-1] == word[i-2]) || word[i-1] == byteAt(word, i+1))) ||
							(a.checkcompoundcase && scpd == 0 && words == nil && i < len(word) && a.cpdcaseCheck(word, i)))
					if !firstOK && rv == nil && a.langnum == langHu && huMovRule {
						// LANG_hu section: spec. Hungarian rule
						rv = a.affixCheck(stS, 0, i, tr, 0, inCpdNot, 0, nil, nil)
						firstOK = rv != nil && a.sfx != nil && a.sfx.contclass != nil &&
							(testaff(a.sfx.contclass, 'x') || testaff(a.sfx.contclass, '%'))
					}
					if firstOK {
						// LANG_hu section: spec. Hungarian rule
						if a.langnum == langHu {
							// calculate syllable number of the word
							numsyllable += a.getSyllable(string(st[:i]))
							// + 1 word, if syllable number of the prefix > 1 (hungarian convention)
							if a.pfx != nil && a.getSyllable(a.pfx.appnd) > 1 {
								wordnum++
							}
						}

						// NEXT WORD(S)
						rvFirst = rv
						// the affixes that built the first word, so its
						// continuation classes stay reachable once the second
						// word has overwritten pfx and sfx
						rvFirstPfx := a.pfx
						rvFirstSfx := a.sfx
						st[i] = ch

						for { // striple loop
							act, ret := func() (int, *hentry) {
								// check simplifiedtriple
								if a.simplifiedtriple {
									if striple != 0 {
										checkedstriple = 1
										i-- // check "fahrt" instead of "ahrt" in "Schiffahrt"
									} else if i > 2 && i <= len(word) && word[i-1] == word[i-2] {
										striple = 1
										// past cmax the surface split is too short, so only the
										// form with the letter put back is tried
										if i >= cmax {
											checkedstriple = 1
											i--
										}
									}
								}

								rv = a.lookup(string(st[i:])) // perhaps without prefix
								if t != nil {
									if rv != nil {
										trace(t, "second \"%s\" -> entry \"%s\" flags=%s", cstr(string(st[i:])), rv.word, traceFlags(a, rv.astr))
									} else {
										trace(t, "second \"%s\" -> miss", cstr(string(st[i:])))
									}
								}

								// search homonym with compound flag
								for rv != nil &&
									((a.needaffix != 0 && testaff(rv.astr, a.needaffix)) ||
										!((cf != 0 && words == nil && testaff(rv.astr, cf)) ||
											(ce != 0 && words == nil && testaff(rv.astr, ce)) ||
											(len(a.defcpdtable) > 0 && words != nil && a.defcpdCheck(&words, wnum+1, maxwordnum, rv, nil, true))) ||
										(scpd != 0 && a.checkcpdtable[scpd-1].cond2 != 0 && !testaff(rv.astr, a.checkcpdtable[scpd-1].cond2))) {
									rv = rv.nextHomonym
								}

								// check FORCEUCASE
								if rv != nil && a.forceucase != 0 && testaff(rv.astr, a.forceucase) && !origcap() {
									rv = nil
								}
								if rv != nil && words != nil && words[wnum+1] != nil {
									return actReturn, rvFirst
								}
								oldnumsyllable2 = numsyllable
								oldwordnum2 = wordnum

								// LANG_hu section: spec. Hungarian rule, XXX hardwired dictionary code
								if rv != nil && a.langnum == langHu && testaff(rv.astr, 'I') && !testaff(rv.astr, 'J') {
									numsyllable--
								}
								// increment word number, if the second root has a compoundroot flag
								if rv != nil && a.compoundroot != 0 && testaff(rv.astr, a.compoundroot) {
									wordnum++
								}
								// check forbiddenwords
								if rv != nil && badEntry(rv) {
									return actReturn, nil
								}

								// second word is acceptable, as a root?
								// hungarian conventions: compounding is acceptable, when compound
								// forms consist of 2 words, or if more, then the syllable number
								// of root words must be 6, or lesser.
								if rv != nil &&
									((cf != 0 && testaff(rv.astr, cf)) || (ce != 0 && testaff(rv.astr, ce))) &&
									((a.cpdwordmax == -1 || wordnum+1 < a.cpdwordmax) ||
										(a.cpdmaxsyllable != 0 && numsyllable+a.getSyllable(rv.word) <= a.cpdmaxsyllable)) &&
									// test CHECKCOMPOUNDPATTERN
									(len(a.checkcpdtable) == 0 || scpd != 0 ||
										(i < len(word) && !a.cpdpatCheck(word, i, rvFirst, rv, t, rvFirstPfx, rvFirstSfx, nil, nil))) &&
									(!a.checkcompounddup || rv != rvFirst) &&
									// test CHECKCOMPOUNDPATTERN conditions
									(scpd == 0 || a.checkcpdtable[scpd-1].cond2 == 0 || testaff(rv.astr, a.checkcpdtable[scpd-1].cond2)) {
									// forbid compound word, if it is a non-compound word with typical fault
									if (a.checkcompoundrep && a.cpdrepCheck(word, ln, tr, &a.cpdTimeExceeded, a.cpdStart)) ||
										a.cpdwordpairCheck(word, ln, tr, &a.cpdTimeExceeded, a.cpdStart) {
										return actReturn, nil
									}
									return actReturn, rvFirst
								}

								numsyllable = oldnumsyllable2
								wordnum = oldwordnum2

								// perhaps second word has prefix or/and suffix
								a.sfx = nil
								a.sfxflag = 0
								// the affixes that built the second word, kept because
								// affix_check clears the members
								var rvSecondPfx *PfxEntry
								var rvSecondSfx *SfxEntry
								rv = nil
								if cf != 0 && onlycpdrule == 0 && i < len(word) {
									rv = a.affixCheck(word, i, len(word)-i, tr, cf, inCpdEnd, 0, &rvSecondPfx, &rvSecondSfx)
								}
								if rv == nil && ce != 0 && onlycpdrule == 0 {
									a.sfx = nil
									a.pfx = nil
									if i < len(word) {
										rv = a.affixCheck(word, i, len(word)-i, tr, ce, inCpdEnd, 0, &rvSecondPfx, &rvSecondSfx)
									}
								}
								if rv == nil && len(a.defcpdtable) > 0 && words != nil {
									if i < len(word) {
										rv = a.affixCheck(word, i, len(word)-i, tr, 0, inCpdEnd, 0, nil, nil)
									}
									if rv != nil && a.defcpdCheck(&words, wnum+1, maxwordnum, rv, nil, true) {
										return actReturn, rvFirst
									}
									rv = nil
								}
								// test CHECKCOMPOUNDPATTERN conditions (allowed forms)
								if rv != nil && !(scpd == 0 || a.checkcpdtable[scpd-1].cond2 == 0 ||
									joinSideHasFlag(rv, a.checkcpdtable[scpd-1].cond2, rvSecondPfx, rvSecondSfx)) {
									rv = nil
								}
								// test CHECKCOMPOUNDPATTERN conditions (forbidden compounds)
								if rv != nil && len(a.checkcpdtable) > 0 && scpd == 0 &&
									a.cpdpatCheck(word, i, rvFirst, rv, t, rvFirstPfx, rvFirstSfx, rvSecondPfx, rvSecondSfx) {
									rv = nil
								}
								// check non_compound flag in suffix and prefix
								if rv != nil && (pfxHas(cff) || sfxHas(cff)) {
									rv = nil
								}
								// check FORCEUCASE
								if rv != nil && a.forceucase != 0 && testaff(rv.astr, a.forceucase) && !origcap() {
									rv = nil
								}
								// check forbiddenwords
								if rv != nil && badEntry(rv) {
									return actReturn, nil
								}

								// pfxappnd = prefix of word+i, or NULL; calculate syllable
								// number of prefix. hungarian convention: when syllable number
								// of prefix is more, than 1, the prefix+word counts as two words.
								if a.langnum == langHu {
									if i < len(word) {
										// calculate syllable number of the word
										numsyllable += a.getSyllable(word[i:])
									}
									// - affix syllable num.
									// XXX only second suffix (inflections, not derivations)
									if a.sfxappnd != nil {
										numsyllable -= int(int16(a.getSyllable(reverseword(*a.sfxappnd)) + a.sfxextra))
									} else {
										numsyllable -= a.sfxextra
									}
									// + 1 word, if syllable number of the prefix > 1 (hungarian convention)
									if a.pfx != nil && a.getSyllable(a.pfx.appnd) > 1 {
										wordnum++
									}
									// increment syllable num, if last word has a SYLLABLENUM flag
									// and the suffix is beginning `s'
									if a.cpdsyllablenum != "" {
										switch a.sfxflag {
										case 'c':
											numsyllable += 2
										case 'J':
											numsyllable++
										case 'I':
											if rv != nil && testaff(rv.astr, 'J') {
												numsyllable++
											}
										}
									}
								}

								// increment word number, if the second word has a compoundroot flag
								if rv != nil && a.compoundroot != 0 && testaff(rv.astr, a.compoundroot) {
									wordnum++
								}
								// second word is acceptable, as a word with prefix or/and suffix?
								if rv != nil &&
									((a.cpdwordmax == -1 || wordnum+1 < a.cpdwordmax) ||
										(a.cpdmaxsyllable != 0 && numsyllable <= a.cpdmaxsyllable)) &&
									(!a.checkcompounddup || rv != rvFirst) {
									// forbid compound word, if it is a non-compound word with typical fault
									if (a.checkcompoundrep && a.cpdrepCheck(word, ln, tr, &a.cpdTimeExceeded, a.cpdStart)) ||
										a.cpdwordpairCheck(word, ln, tr, &a.cpdTimeExceeded, a.cpdStart) {
										return actReturn, nil
									}
									return actReturn, rvFirst
								}

								numsyllable = oldnumsyllable2
								wordnum = oldwordnum2

								// perhaps second word is a compound word (recursive call)
								// (only if SPELL_COMPOUND_2 is not set and maxwordnum is not exceeded)
								if (info == nil || *info&SpellCompound2 == 0) && wordnum+2 < maxwordnum && wnum+1 < maxwordnum {
									rv = a.compoundCheck(string(st[i:]), wordnum+1, numsyllable, maxwordnum, wnum+1, words, rwords, false, isSug, info, tr)
									if rv != nil && len(a.checkcpdtable) > 0 && i < len(word) &&
										((scpd == 0 && a.cpdpatCheck(word, i, rvFirst, rv, t, rvFirstPfx, rvFirstSfx, nil, nil)) ||
											(scpd != 0 && !a.cpdpatCheck(word, i, rvFirst, rv, t, rvFirstPfx, rvFirstSfx, nil, nil))) {
										rv = nil
									}
								} else {
									rv = nil
								}
								if rv != nil {
									// forbid compound word, if it is a non-compound word with
									// typical fault, or a dictionary word pair
									if a.cpdwordpairCheck(word, ln, tr, &a.cpdTimeExceeded, a.cpdStart) {
										return actReturn, nil
									}
									if a.checkcompoundrep || a.forbiddenword != 0 {
										if a.checkcompoundrep && a.cpdrepCheck(word, ln, tr, &a.cpdTimeExceeded, a.cpdStart) {
											return actReturn, nil
										}
										// check first part
										bl := len(rv.word)
										if i < len(word) && substrN(word, i, bl) == rv.word {
											r := byteAt(string(st), i+bl)
											setByte(st, i+bl, 0)
											stS := bytesView(st)
											if (a.checkcompoundrep && a.cpdrepCheck(stS, i+bl, tr, &a.cpdTimeExceeded, a.cpdStart)) ||
												a.cpdwordpairCheck(stS, i+bl, tr, &a.cpdTimeExceeded, a.cpdStart) {
												setByte(st, i+bl, r)
												return actNext, nil
											}
											if a.forbiddenword != 0 {
												rv2 := a.lookup(word)
												if rv2 == nil && ln <= len(word) {
													rv2 = a.affixCheck(word, 0, ln, tr, 0, inCpdNot, 0, nil, nil)
												}
												if rv2 != nil && rv2.astr != nil && testaff(rv2.astr, a.forbiddenword) &&
													strncmp(rv2.word, stS, i+bl) == 0 {
													return actReturn, nil
												}
											}
											setByte(st, i+bl, r)
										}
									}
									return actReturn, rvFirst
								}
								return actNext, nil
							}()
							if act == actReturn {
								return actReturn, ret
							}
							if !(striple != 0 && checkedstriple == 0) {
								break
							}
						} // end of striple loop

						if checkedstriple != 0 {
							i++
							checkedstriple = 0
							striple = 0
						}
					} // first word is ok condition

					if soldi != 0 {
						i = soldi
						ln = oldlen
						cmin = oldcmin
						cmax = oldcmax
					}
					scpd++
					return actNext, nil
				}()
				if act == actReturn {
					return ret
				}
				if act == actBreak || !simplifiedCond() {
					break
				}
			} // end of simplifiedcpd loop

			scpd = 0
			wordnum = oldwordnum
			numsyllable = oldnumsyllable

			if soldi != 0 {
				i = soldi
				st = []byte(word) // XXX add more optim.
				soldi = 0
				ln = oldlen
				cmin = oldcmin
				cmax = oldcmax
			} else {
				setByte(st, i, ch)
			}

			cont := len(a.defcpdtable) > 0 && oldwordnum == 0 && onlycpdrule < 1
			onlycpdrule++
			if !cont {
				break
			}
		} // end of onlycpd loop
	}
	return nil
}

// compoundCheckMorph appends the analyses of a compound word to *result.
// The C++ function also takes hu_mov_rule, but every call passes 0, so the
// code it enables (the Hungarian rule of compound_check) is left out here.
func (a *AffixMgr) compoundCheckMorph(word string, wordnum, numsyllable, maxwordnum, wnum int, words, rwords []*hentry,
	result *string, partresult *string, tr *traceCtx) {
	var oldnumsyllable, oldnumsyllable2, oldwordnum, oldwordnum2 int
	var rv, rvFirst *hentry
	var ch byte
	ok := false
	oldwords := words
	ln := len(word)

	// C++ returns here when wnum + 1 >= maxwordnum, to protect the reads of
	// words[wnum + 1]. That never happens here: the recursive call below needs
	// wordnum + 2 < maxwordnum, and wordnum, which starts at 0 like wnum,
	// grows at least as fast.
	now := clock()
	if wnum == 0 {
		a.cpdmStart = now
		a.cpdmTimeExceeded = false
	} else if now.Sub(a.cpdmStart) > a.limits.compound {
		a.cpdmTimeExceeded = true
	}

	cmin, cmax := a.setcminmax(word, ln)
	st := []byte(word)
	cf, cb, cm, ce := a.compoundflag, a.compoundbegin, a.compoundmiddle, a.compoundend
	cff := a.compoundforbidflag
	inc := inCpdBegin
	contHas := func(c []uint16, f uint16) bool { return c != nil && testaff(c, f) }
	pfxHas := func(f uint16) bool { return a.pfx != nil && contHas(a.pfx.contclass, f) }
	sfxHas := func(f uint16) bool { return a.sfx != nil && contHas(a.sfx.contclass, f) }

	for i := cmin; i < cmax; i++ {
		// go to end of the UTF-8 character
		if a.utf8 {
			for isUTF8Cont(byteAt(string(st), i)) {
				i++
			}
			if i >= cmax {
				return
			}
		}
		words = oldwords
		onlycpdrule := 0
		if words != nil {
			onlycpdrule = 1
		}
		for { // onlycpdrule loop
			act := func() int {
				if a.cpdmTimeExceeded || a.cpdClockExceeded(a.cpdmStart) {
					a.cpdmTimeExceeded = true
					return actReturn
				}
				if len(*result) > maxMorphResult {
					return actReturn
				}
				oldnumsyllable = numsyllable
				oldwordnum = wordnum
				checkedPrefix := false
				// (C++ returns here for i >= st.size(), but i < cmax <= len(st))
				ch = st[i]
				st[i] = 0
				a.sfx = nil

				// FIRST WORD
				var presult strings.Builder
				if partresult != nil {
					presult.WriteString(*partresult)
				}
				rv = a.lookup(string(st[:i])) // perhaps without prefix

				// forbid dictionary stems with COMPOUNDFORBIDFLAG in compound
				// words, overriding the effect of COMPOUNDPERMITFLAG
				if rv != nil && cff != 0 && testaff(rv.astr, cff) {
					return actNext
				}

				// search homonym with compound flag
				for rv != nil &&
					((a.needaffix != 0 && testaff(rv.astr, a.needaffix)) ||
						!((cf != 0 && words == nil && onlycpdrule == 0 && testaff(rv.astr, cf)) ||
							(cb != 0 && wordnum == 0 && onlycpdrule == 0 && testaff(rv.astr, cb)) ||
							(cm != 0 && wordnum != 0 && words == nil && onlycpdrule == 0 && testaff(rv.astr, cm)) ||
							(len(a.defcpdtable) > 0 && onlycpdrule != 0 &&
								((words == nil && wordnum == 0 && a.defcpdCheck(&words, wnum, maxwordnum, rv, rwords, false)) ||
									(words != nil && a.defcpdCheck(&words, wnum, maxwordnum, rv, rwords, false)))))) {
					rv = rv.nextHomonym
				}

				if rv != nil {
					presult.WriteByte(msepFld)
					presult.WriteString(morphPart)
					presult.Write(st[:i])
					if !rv.entryFind(morphStem) {
						presult.WriteByte(msepFld)
						presult.WriteString(morphStem)
						presult.Write(st[:i])
					}
					if d, ok := rv.entryData(); ok {
						presult.WriteByte(msepFld)
						presult.WriteString(d)
					}
				}

				stS := bytesView(st)
				if rv == nil {
					if cf != 0 {
						rv = a.prefixCheck(stS, 0, i, inc, tr, cf, 0)
						if rv == nil {
							rv = a.suffixCheck(stS, 0, i, 0, nil, tr, 0, cf, inc, 0)
							if rv == nil && a.compoundmoresuffixes {
								rv = a.suffixCheckTwosfx(stS, 0, i, 0, nil, tr, cf)
							}
							if rv != nil && a.sfx != nil && a.sfx.contclass != nil &&
								((cff != 0 && testaff(a.sfx.contclass, cff)) || (ce != 0 && testaff(a.sfx.contclass, ce))) {
								rv = nil
							}
						}
					}
					tryFlag := func(flag uint16) bool {
						if rv = a.suffixCheck(stS, 0, i, 0, nil, tr, 0, flag, inc, 0); rv != nil {
							return true
						}
						if a.compoundmoresuffixes {
							if rv = a.suffixCheckTwosfx(stS, 0, i, 0, nil, tr, flag); rv != nil {
								return true
							}
						}
						rv = a.prefixCheck(stS, 0, i, inc, tr, flag, 0)
						return rv != nil
					}
					if rv != nil || (wordnum == 0 && cb != 0 && tryFlag(cb)) || (wordnum > 0 && cm != 0 && tryFlag(cm)) {
						var p string
						if cf != 0 {
							p = a.affixCheckMorph(stS, 0, i, tr, cf, inCpdNot)
						}
						if p == "" {
							if wordnum == 0 && cb != 0 {
								p = a.affixCheckMorph(stS, 0, i, tr, cb, inCpdNot)
							} else if wordnum > 0 && cm != 0 {
								p = a.affixCheckMorph(stS, 0, i, tr, cm, inCpdNot)
							}
						}
						presult.WriteByte(msepFld)
						presult.WriteString(morphPart)
						presult.Write(st[:i])
						if p != "" {
							p = lineUniqApp(p, msepRec)
							if p != "" && p[0] != msepFld {
								presult.WriteByte(msepFld)
							}
							presult.WriteString(p)
						}
						checkedPrefix = true
					}
					// else check forbiddenwords
				} else if rv.astr != nil && (testaff(rv.astr, a.forbiddenword) || testaff(rv.astr, onlyUpcaseFlag) ||
					testaff(rv.astr, a.needaffix)) {
					st[i] = ch
					return actNext
				}

				// check non_compound flag in suffix and prefix
				if rv != nil && (pfxHas(cff) || sfxHas(cff)) {
					return actNext
				}
				// check compoundend flag in suffix and prefix
				if rv != nil && !checkedPrefix && ce != 0 && (pfxHas(ce) || sfxHas(ce)) {
					return actNext
				}
				// check compoundmiddle flag in suffix and prefix
				if rv != nil && !checkedPrefix && wordnum == 0 && cm != 0 && (pfxHas(cm) || sfxHas(cm)) {
					rv = nil
				}
				// check forbiddenwords
				if rv != nil && rv.astr != nil && (testaff(rv.astr, a.forbiddenword) || testaff(rv.astr, onlyUpcaseFlag)) {
					return actNext
				}
				// increment word number, if the second root has a compoundroot flag
				if rv != nil && a.compoundroot != 0 && testaff(rv.astr, a.compoundroot) {
					wordnum++
				}

				// first word is acceptable in compound words?
				firstOK := rv != nil &&
					(checkedPrefix || (words != nil && words[wnum] != nil) || (cf != 0 && testaff(rv.astr, cf)) ||
						(oldwordnum == 0 && cb != 0 && testaff(rv.astr, cb)) ||
						(oldwordnum > 0 && cm != 0 && testaff(rv.astr, cm))) &&
					!((a.checkcompoundtriple && words == nil && // test triple letters
						byteAt(word, i-1) == byteAt(word, i) &&
						((i > 1 && byteAt(word, i-1) == byteAt(word, i-2)) || byteAt(word, i-1) == byteAt(word, i+1))) ||
						// test CHECKCOMPOUNDPATTERN
						(len(a.checkcpdtable) > 0 && words == nil && a.cpdpatCheck(word, i, rv, nil, nil, nil, nil, nil, nil)) ||
						(a.checkcompoundcase && words == nil && a.cpdcaseCheck(word, i)))
				if firstOK {
					// LANG_hu section: spec. Hungarian rule
					if a.langnum == langHu {
						numsyllable += a.getSyllable(string(st[:i]))
						if a.pfx != nil && a.getSyllable(a.pfx.appnd) > 1 {
							wordnum++
						}
					}

					// NEXT WORD(S)
					rvFirst = rv
					rest := substrN(word, i, -1)
					rv = a.lookup(rest) // perhaps without prefix

					// search homonym with compound flag
					for rv != nil && ((a.needaffix != 0 && testaff(rv.astr, a.needaffix)) ||
						!((cf != 0 && words == nil && testaff(rv.astr, cf)) ||
							(ce != 0 && words == nil && testaff(rv.astr, ce)) ||
							(len(a.defcpdtable) > 0 && words != nil && a.defcpdCheck(&words, wnum+1, maxwordnum, rv, nil, true)))) {
						rv = rv.nextHomonym
					}

					if rv != nil && words != nil && words[wnum+1] != nil {
						var r strings.Builder
						r.WriteString(presult.String())
						r.WriteByte(msepFld)
						r.WriteString(morphPart)
						r.WriteString(rest)
						if d, ok := rv.entryData(); a.complexprefixes && ok {
							r.WriteString(d)
						}
						if !rv.entryFind(morphStem) {
							r.WriteByte(msepFld)
							r.WriteString(morphStem)
							r.WriteString(rv.word)
						}
						// store the pointer of the hash entry
						if d, ok := rv.entryData(); !a.complexprefixes && ok {
							r.WriteByte(msepFld)
							r.WriteString(d)
						}
						r.WriteByte(msepRec)
						*result += r.String()
						return actReturn
					}

					oldnumsyllable2 = numsyllable
					oldwordnum2 = wordnum

					// LANG_hu section: spec. Hungarian rule
					if rv != nil && a.langnum == langHu && testaff(rv.astr, 'I') && !testaff(rv.astr, 'J') {
						numsyllable--
					}
					// increment word number, if the second root has a compoundroot flag
					if rv != nil && a.compoundroot != 0 && testaff(rv.astr, a.compoundroot) {
						wordnum++
					}
					// check forbiddenwords
					if rv != nil && rv.astr != nil && (testaff(rv.astr, a.forbiddenword) || testaff(rv.astr, onlyUpcaseFlag)) {
						st[i] = ch
						return actNext
					}

					// second word is acceptable, as a root?
					if rv != nil &&
						((cf != 0 && testaff(rv.astr, cf)) || (ce != 0 && testaff(rv.astr, ce))) &&
						((a.cpdwordmax == -1 || wordnum+1 < a.cpdwordmax) ||
							(a.cpdmaxsyllable != 0 && numsyllable+a.getSyllable(rv.word) <= a.cpdmaxsyllable)) &&
						(!a.checkcompounddup || rv != rvFirst) {
						// bad compound word
						var r strings.Builder
						r.WriteString(presult.String())
						r.WriteByte(msepFld)
						r.WriteString(morphPart)
						r.WriteString(rest)
						if d, ok := rv.entryData(); ok {
							if a.complexprefixes {
								r.WriteString(d)
							}
							if !rv.entryFind(morphStem) {
								r.WriteByte(msepFld)
								r.WriteString(morphStem)
								r.WriteString(rv.word)
							}
							// store the pointer of the hash entry
							if !a.complexprefixes {
								r.WriteByte(msepFld)
								r.WriteString(d)
							}
						}
						r.WriteByte(msepRec)
						*result += r.String()
						ok = true
					}

					numsyllable = oldnumsyllable2
					wordnum = oldwordnum2

					// perhaps second word has prefix or/and suffix
					a.sfx = nil
					a.sfxflag = 0
					if cf != 0 && onlycpdrule == 0 {
						rv = a.affixCheck(word, i, len(word)-i, tr, cf, inCpdNot, 0, nil, nil)
					} else {
						rv = nil
					}
					if rv == nil && ce != 0 && onlycpdrule == 0 {
						a.sfx = nil
						a.pfx = nil
						rv = a.affixCheck(word, i, len(word)-i, tr, ce, inCpdEnd, 0, nil, nil)
					}
					if rv == nil && len(a.defcpdtable) > 0 && words != nil {
						rv = a.affixCheck(word, i, len(word)-i, tr, 0, inCpdEnd, 0, nil, nil)
						if rv != nil && words != nil && a.defcpdCheck(&words, wnum+1, maxwordnum, rv, nil, true) {
							var m string
							if cf != 0 {
								m = a.affixCheckMorph(word, i, len(word)-i, tr, cf, inCpdNot)
							}
							if m == "" && ce != 0 {
								m = a.affixCheckMorph(word, i, len(word)-i, tr, ce, inCpdNot)
							}
							var r strings.Builder
							r.WriteString(presult.String())
							if m != "" {
								r.WriteByte(msepFld)
								r.WriteString(morphPart)
								r.WriteString(rest)
								r.WriteString(lineUniqApp(m, msepRec))
							}
							r.WriteByte(msepRec)
							*result += r.String()
							ok = true
						}
					}

					// check non_compound flag in suffix and prefix
					if rv != nil && (pfxHas(cff) || sfxHas(cff)) {
						rv = nil
					}
					// check forbiddenwords
					if rv != nil && rv.astr != nil && (testaff(rv.astr, a.forbiddenword) || testaff(rv.astr, onlyUpcaseFlag)) &&
						!testaff(rv.astr, a.needaffix) {
						st[i] = ch
						return actNext
					}

					if a.langnum == langHu {
						// calculate syllable number of the word
						numsyllable += a.getSyllable(cstr(rest))
						// - affix syllable num.
						// XXX only second suffix (inflections, not derivations)
						if a.sfxappnd != nil {
							numsyllable -= int(int16(a.getSyllable(reverseword(*a.sfxappnd)) + a.sfxextra))
						} else {
							numsyllable -= a.sfxextra
						}
						// + 1 word, if syllable number of the prefix > 1 (hungarian convention)
						if a.pfx != nil && a.getSyllable(a.pfx.appnd) > 1 {
							wordnum++
						}
						// increment syllable num, if last word has a SYLLABLENUM flag
						// and the suffix is beginning `s'
						if a.cpdsyllablenum != "" {
							switch a.sfxflag {
							case 'c':
								numsyllable += 2
							case 'J':
								numsyllable++
							case 'I':
								if rv != nil && testaff(rv.astr, 'J') {
									numsyllable++
								}
							}
						}
					}

					// increment word number, if the second word has a compoundroot flag
					if rv != nil && a.compoundroot != 0 && testaff(rv.astr, a.compoundroot) {
						wordnum++
					}
					// second word is acceptable, as a word with prefix or/and suffix?
					if rv != nil &&
						((a.cpdwordmax == -1 || wordnum+1 < a.cpdwordmax) ||
							(a.cpdmaxsyllable != 0 && numsyllable <= a.cpdmaxsyllable)) &&
						(!a.checkcompounddup || rv != rvFirst) {
						var m string
						if cf != 0 {
							m = a.affixCheckMorph(word, i, len(word)-i, tr, cf, inCpdEnd)
						}
						if m == "" && ce != 0 {
							m = a.affixCheckMorph(word, i, len(word)-i, tr, ce, inCpdEnd)
						}
						var r strings.Builder
						r.WriteString(presult.String())
						if m != "" {
							r.WriteByte(msepFld)
							r.WriteString(morphPart)
							r.WriteString(rest)
							m = lineUniqApp(m, msepRec)
							r.WriteByte(msepFld)
							r.WriteString(m)
						}
						r.WriteByte(msepRec)
						*result += r.String()
						ok = true
					}

					numsyllable = oldnumsyllable2
					wordnum = oldwordnum2

					// perhaps second word is a compound word (recursive call)
					if wordnum+2 < maxwordnum && wnum+1 < maxwordnum && !ok {
						pr := presult.String()
						a.compoundCheckMorph(rest, wordnum+1, numsyllable, maxwordnum, wnum+1, words, rwords, result, &pr, tr)
					} else {
						rv = nil
					}
				}
				st[i] = ch
				wordnum = oldwordnum
				numsyllable = oldnumsyllable
				return actNext
			}()
			if act == actReturn {
				return
			}
			cont := len(a.defcpdtable) > 0 && oldwordnum == 0 && onlycpdrule < 1
			onlycpdrule++
			if !cont {
				break
			}
		} // end of onlycpd loop
	}
}

// bytesView returns a string viewing b. The compound checks hand their
// working copy of the word to the affix checks this way, without copying it,
// as long as they do not change it.
func bytesView(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// cpdClockExceeded reports whether the compound time limit is exceeded. The
// inner loop of the compound check asks for every word part; reading the
// clock at every 4th call makes the limit fire at most a few iterations
// later, and without a limit the clock is not read at all.
func (a *AffixMgr) cpdClockExceeded(start time.Time) bool {
	if a.limits.compound == noLimit {
		return false
	}
	a.clockTick++
	if a.clockTick&3 != 0 {
		return false
	}
	return since(start) > a.limits.compound
}
