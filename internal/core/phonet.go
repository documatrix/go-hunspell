package core

import "strings"

const (
	phonetHashSize   = 256
	maxPhonetLen     = 256
	maxPhonetUTF8Len = maxPhonetLen * 4
)

// phonetable holds the PHONE rules: pairs of search pattern and replacement,
// ending with a pair of empty strings.
type phonetable struct {
	utf8  bool
	rules []string
	hash  [phonetHashSize]int
}

func initPhonetHash(p *phonetable) {
	for i := range p.hash {
		p.hash[i] = -1
	}
	for i := 0; byteAt(p.rules[i], 0) != 0; i += 2 {
		k := p.rules[i][0]
		if p.hash[k] < 0 {
			p.hash[k] = i
		}
	}
}

// isAlpha is C's isalpha in the C locale, extended to every 8-bit byte.
func phonetIsAlpha(ch byte) bool {
	if ch < 128 {
		return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
	}
	return true
}

// cstrchr is strchr on the C string s. (strchr also finds the terminating
// NUL, but the callers only look for letters.)
func cstrchr(s string, c byte) bool {
	return strings.IndexByte(cstr(s), c) >= 0
}

// phonet does the phonetic transformation of an upper case word,
// see http://aspell.net/man-html/Phonetic-Code.html
func phonet(inword string, parms *phonetable) string {
	if len(inword) > maxPhonetUTF8Len {
		return ""
	}
	// the word is worked on in place as a NUL terminated buffer, with room
	// for the reads past its end that the algorithm does
	word := make([]byte, maxPhonetUTF8Len+16)
	copy(word, cstr(inword))
	ln := len(inword)
	// the reads stop at the terminating NUL of the word, and i - 1 is read
	// for i > 0 only
	at := func(i int) byte { return word[i] }
	strmove := func(dest, src int) {
		for word[src] != 0 {
			word[dest] = word[src]
			dest++
			src++
		}
		word[dest] = 0
	}
	rule := func(n int) string { return parms.rules[n] }
	rc := func(s string, i int) byte { return byteAt(s, i) }

	var target []byte
	i, k, z := 0, 0, 0
	p0 := -333
	var c byte
	for {
		c = at(i)
		if c == 0 {
			break
		}
		n := parms.hash[c]
		z0 := 0
		if n >= 0 && rule(n) != "" {
			// check all rules for the same letter
			for rc(rule(n), 0) == c {
				// check whole string
				k = 1  // number of found letters
				p := 5 // default priority
				s := rule(n)
				si := 1
				for rc(s, si) != 0 && at(i+k) == rc(s, si) && !isDigit(rc(s, si)) && strings.IndexByte("(-<^$", rc(s, si)) < 0 {
					k++
					si++
				}
				if rc(s, si) == '(' {
					// check letters in "(..)"
					if phonetIsAlpha(at(i+k)) && cstrchr(s[si+1:], at(i+k)) {
						k++
						for rc(s, si) != 0 && rc(s, si) != ')' {
							si++
						}
						if rc(s, si) == ')' {
							si++
						}
					}
				}
				p0 = int(rc(s, si))
				k0 := k
				for rc(s, si) == '-' && k > 1 {
					k--
					si++
				}
				if rc(s, si) == '<' {
					si++
				}
				if isDigit(rc(s, si)) {
					// determine priority
					p = int(rc(s, si) - '0')
					si++
				}
				if rc(s, si) == '^' && rc(s, si+1) == '^' {
					si++
				}

				if rc(s, si) == 0 ||
					(rc(s, si) == '^' && (i == 0 || !phonetIsAlpha(at(i-1))) &&
						(rc(s, si+1) != '$' || !phonetIsAlpha(at(i+k0)))) ||
					(rc(s, si) == '$' && i > 0 && phonetIsAlpha(at(i-1)) && !phonetIsAlpha(at(i+k0))) {
					// search for followup rules, if: k > 1 and NO '-' in searchstring
					c0 := at(i + k - 1)
					n0 := parms.hash[c0]

					if k > 1 && n0 >= 0 && p0 != int('-') && at(i+k) != 0 && rule(n0) != "" {
						// test follow-up rule for "word[i+k]"
						for rc(rule(n0), 0) == c0 {
							// check whole string
							k0 = k
							p0 = 5
							s = rule(n0)
							si = 1
							for rc(s, si) != 0 && at(i+k0) == rc(s, si) && !isDigit(rc(s, si)) && strings.IndexByte("(-<^$", rc(s, si)) < 0 {
								k0++
								si++
							}
							if rc(s, si) == '(' {
								// check letters
								if phonetIsAlpha(at(i+k0)) && cstrchr(s[si+1:], at(i+k0)) {
									k0++
									for rc(s, si) != ')' && rc(s, si) != 0 {
										si++
									}
									if rc(s, si) == ')' {
										si++
									}
								}
							}
							for rc(s, si) == '-' {
								// "k0" gets NOT reduced because "if (k0 == k)"
								si++
							}
							if rc(s, si) == '<' {
								si++
							}
							if isDigit(rc(s, si)) {
								p0 = int(rc(s, si) - '0')
								si++
							}

							if rc(s, si) == 0 ||
								// *s == '^' cuts
								(rc(s, si) == '$' && !phonetIsAlpha(at(i+k0))) {
								if k0 == k {
									// this is just a piece of the string
									n0 += 2
									continue
								}
								if p0 < p {
									// priority too low
									n0 += 2
									continue
								}
								// rule fits; stop search
								break
							}
							n0 += 2
						} // end of "while (parms.rules[n0][0] == c0)"

						if p0 >= p && rc(rule(n0), 0) == c0 {
							n += 2
							continue
						}
					} // end of follow-up stuff

					// replace string
					s = rule(n + 1)
					si = 0
					if rule(n) != "" && strings.IndexByte(cstr(rule(n))[1:], '<') >= 0 {
						p0 = 1
					} else {
						p0 = 0
					}
					if p0 == 1 && z == 0 {
						// rule with '<' is used
						if len(target) > 0 && rc(s, si) != 0 &&
							(target[len(target)-1] == c || target[len(target)-1] == rc(s, si)) {
							target = target[:len(target)-1]
						}
						z0 = 1
						z = 1
						k0 = 0
						for rc(s, si) != 0 && at(i+k0) != 0 {
							word[i+k0] = rc(s, si)
							k0++
							si++
						}
						if k > k0 {
							strmove(i+k0, i+k)
						}
						// new "actual letter"
						c = word[i]
					} else { // no '<' rule used
						i += k - 1
						z = 0
						for rc(s, si) != 0 && rc(s, si+1) != 0 && len(target) < ln {
							if len(target) == 0 || target[len(target)-1] != rc(s, si) {
								target = append(target, rc(s, si))
							}
							si++
						}
						// new "actual letter"
						c = rc(s, si)
						if rule(n) != "" && strings.Contains(cstr(rule(n))[1:], "^^") {
							if c != 0 {
								target = append(target, c)
							}
							strmove(0, i+1)
							i = 0
							z0 = 1
						}
					}
					break
				} // end of follow-up stuff
				n += 2
			} // end of while (parms.rules[n][0] == c)
		} // end of if (n >= 0)
		if z0 == 0 {
			if k != 0 && p0 == 0 && len(target) < ln && c != 0 {
				// condense only double letters
				target = append(target, c)
			}
			i++
			z = 0
			k = 0
		}
	}
	return string(target)
}
