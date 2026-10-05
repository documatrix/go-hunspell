package core

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Source names a file of a dictionary: its contents when Data is not nil,
// otherwise the file at Path (or Path+".hz").
type Source struct {
	Path string
	Data []byte
}

func (s Source) name() string {
	if s.Path != "" || s.Data == nil {
		return s.Path
	}
	return "<data>"
}

func (s Source) open(key string, errs *[]string) lineReader {
	if s.Data != nil {
		if len(s.Data) >= 3 && (string(s.Data[:3]) == "hz0" || string(s.Data[:3]) == "hz1") {
			fm := &fileMgr{}
			fm.hin = newHunzip(s.name(), s.Data, key, errs)
			if !fm.hin.isOpen() {
				fm.failed = true
			}
			return fm
		}
		return &fileMgr{data: s.Data}
	}
	return openFile(s.Path, key, errs)
}

type flagMode int

const (
	flagChar flagMode = iota
	flagLong
	flagNum
	flagUni
)

// hentry options
const (
	hOpt        = 1 << 0 // is there optional morphological data?
	hOptAliasm  = 1 << 1 // using alias compression?
	hOptPhon    = 1 << 2 // is there ph: field in the morphological data?
	hOptInitcap = 1 << 3 // is dictionary word capitalized?
)

const (
	userWord       = 1000
	morphPhonRatio = 500
)

// hentry is one dictionary entry.
type hentry struct {
	hv          uint64 // the hash value of word, to reject other words cheaply
	word        string
	astr        []uint16
	next        *hentry
	nextHomonym *hentry
	clen        int
	opts        byte
	data        string
	hasData     bool
}

// testaff reports whether the sorted flag vector a holds flag b.
func testaff(a []uint16, b uint16) bool {
	lo, hi := 0, len(a)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if a[m] < b {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo < len(a) && a[lo] == b
}

func sortFlags(f []uint16) {
	slices.Sort(f)
}

// entryData is HENTRY_DATA: the morphological data, ok false when none.
func (h *hentry) entryData() (string, bool) {
	if h.opts&hOpt == 0 || !h.hasData {
		return "", false
	}
	return h.data, true
}

// entryData2 is HENTRY_DATA2: the morphological data or "".
func (h *hentry) entryData2() string {
	d, _ := h.entryData()
	return d
}

// entryFind is HENTRY_FIND: whether the morphological data holds p.
func (h *hentry) entryFind(p string) bool {
	d, ok := h.entryData()
	return ok && strings.Contains(d, p)
}

type replentry struct {
	pattern    string
	outstrings [4]string // med, ini, fin, isol
}

// HashMgr holds the words of one .dic file.
type HashMgr struct {
	tableptr        []*hentry
	flagMode        flagMode
	complexprefixes bool
	utf8            bool
	forbiddenword   uint16
	langnum         int
	enc             string
	lang            string
	csconv          *[256]csInfo
	ignorechars     string
	ignorecharsU16  []uint16
	aliasf          [][]uint16
	aliasm          []string
	reptable        []replentry
	errs            *[]string
	slab            []hentry // see newEntry
	u16buf          []uint16
	warns           *[]string
}

func newHashMgr(dic, aff Source, key string, errs, warns *[]string) *HashMgr {
	h := &HashMgr{flagMode: flagChar, forbiddenword: forbiddenWord, errs: errs, warns: warns}
	h.loadConfig(aff, key)
	if h.csconv == nil {
		h.csconv = getCurrentCS(spellEncoding, h)
	}
	if ec := h.loadTables(dic, key); ec != 0 {
		*errs = append(*errs, fmt.Sprintf("Hash Manager Error : %d\n", ec))
		// keep table size to 1 to fix possible division with zero
		h.tableptr = make([]*hentry, 1)
	}
	return h
}

func (h *HashMgr) lookup(word string) *hentry {
	hv := hashValue(word)
	dp := h.tableptr[hv%uint64(len(h.tableptr))]
	if dp == nil {
		return nil
	}
	if strings.IndexByte(word, 0) < 0 {
		for ; dp != nil; dp = dp.next {
			if dp.hv == hv && dp.word == word {
				return dp
			}
		}
		return nil
	}
	// the C++ code hashes all len bytes, but compares the C strings
	w := cstr(word)
	for ; dp != nil; dp = dp.next {
		if w == dp.word {
			return dp
		}
	}
	return nil
}

// hash is a simple load and rotate algorithm. It works on the signed bytes
// and the 64-bit unsigned long of the C++ code so that words land in the same
// slots, which decides the order of the n-gram suggestions.
func (h *HashMgr) hash(word string) int {
	return int(hashValue(word) % uint64(len(h.tableptr)))
}

func hashValue(word string) uint64 {
	var hv uint64
	i := 0
	for i < 4 && i < len(word) {
		hv = (hv << 8) | uint64(int64(int8(word[i])))
		i++
	}
	for i < len(word) {
		hv = (hv << 5) | ((hv >> 27) & 31)
		hv ^= uint64(int64(int8(word[i])))
		i++
	}
	return hv
}

func (h *HashMgr) addWord(inWord string, wcl int, aff []uint16, desc *string, onlyupcase bool, captype int) int {
	if len(aff) > 32767 {
		h.warnf("error: affix len %d is over max limit\n", len(aff))
		return 1
	}
	word := inWord
	if (h.ignorechars != "" && !hasNoIgnoredChars(inWord, h.ignorechars)) || h.complexprefixes {
		if h.ignorechars != "" {
			if h.utf8 {
				word, wcl = removeIgnoredCharsUTF(word, h.ignorecharsU16)
			} else {
				word = removeIgnoredChars(word, h.ignorechars)
			}
		}
		if h.complexprefixes {
			if h.utf8 {
				word = warnReversewordUTF(h, word)
				wcl = len(word)
			} else {
				word = reverseword(word)
			}
			if desc != nil && len(h.aliasm) == 0 {
				var d string
				if h.utf8 {
					d = warnReversewordUTF(h, *desc)
				} else {
					d = reverseword(*desc)
				}
				desc = &d
			}
		}
	}
	if len(word) > 65535 {
		h.warnf("error: word len %d is over max limit\n", len(word))
		return 1
	}

	upcasehomonym := false
	hp := h.newEntry()
	// clen is an unsigned short in C++: the -1 length of a word with non-BMP
	// characters is stored as 65535
	*hp = hentry{hv: hashValue(word), word: word, clen: int(uint16(wcl)), astr: aff}
	if captype == initCap {
		hp.opts = hOptInitcap
	}
	i := h.hash(word)

	if desc != nil {
		hp.opts |= hOpt
		if len(h.aliasm) > 0 {
			hp.opts |= hOptAliasm
			if s, ok := h.getAliasm(atoi(*desc)); ok {
				hp.data, hp.hasData = s, true
			}
		} else {
			hp.data, hp.hasData = cstr(*desc), true
		}
		if hp.entryFind(morphPhon) {
			hp.opts |= hOptPhon
			h.addPhonReps(hp, inWord, captype)
		}
	}

	dp := h.tableptr[i]
	if dp == nil {
		h.tableptr[i] = hp
		return 0
	}
	for dp.next != nil {
		if dp.nextHomonym == nil && hp.word == dp.word {
			// remove hidden onlyupcase homonym
			if !onlyupcase {
				if dp.astr != nil && testaff(dp.astr, onlyUpcaseFlag) {
					dp.astr = hp.astr
					return 0
				} else if dp.astr == nil && hp.astr == nil {
					// word already exists with no flags, skip duplicate
					return 0
				} else {
					dp.nextHomonym = hp
				}
			} else {
				upcasehomonym = true
			}
		}
		dp = dp.next
	}
	if hp.word == dp.word {
		if !onlyupcase {
			if dp.astr != nil && testaff(dp.astr, onlyUpcaseFlag) {
				dp.astr = hp.astr
				return 0
			} else if dp.astr == nil && hp.astr == nil {
				return 0
			} else {
				dp.nextHomonym = hp
			}
		} else {
			upcasehomonym = true
		}
	}
	if !upcasehomonym {
		dp.next = hp
	}
	return 0
}

// addPhonReps stores the ph: fields (pronounciation, misspellings, old
// orthography etc.) of a morphological description in the REP table.
func (h *HashMgr) addPhonReps(hp *hentry, inWord string, captype int) {
	fields := hp.entryData2()
	pos := 0
	for {
		piece, ok := mystrsep(fields, &pos)
		if !ok {
			break
		}
		if !strings.HasPrefix(piece, morphPhon) {
			continue
		}
		ph := piece[len(morphPhon):]
		if ph == "" {
			continue
		}
		var wordpart string
		// dictionary based REP replacement, separated by "->"
		// for example "pretty ph:prity ph:priti->pretti" to handle
		// both prity -> pretty and pritier -> prettiest suggestions.
		if sp := strings.Index(ph, "->"); sp >= 0 && sp > 0 && sp < len(ph)-2 {
			wordpart = ph[sp+2:]
			ph = ph[:sp]
		} else {
			wordpart = inWord
		}
		// when the ph: field ends with the character *, strip last character
		// of the pattern and the replacement to match in REP suggestions also
		// at character changes
		if ph[len(ph)-1] == '*' {
			strippatt := 1
			stripword := 0
			if h.utf8 {
				for strippatt < len(ph) && isUTF8Cont(ph[len(ph)-strippatt-1]) {
					strippatt++
				}
				for stripword < len(wordpart) && isUTF8Cont(wordpart[len(wordpart)-stripword-1]) {
					stripword++
				}
			}
			strippatt++
			stripword++
			if len(ph) > strippatt && len(wordpart) > stripword {
				ph = ph[:len(ph)-strippatt]
				wordpart = wordpart[:len(wordpart)-stripword]
			}
		}
		// capitalize lowercase pattern for capitalized words to support good
		// suggestions also for capitalized misspellings
		if captype == initCap {
			var phCapitalized string
			if h.utf8 {
				w, _ := warnU8u16(h, ph)
				if getCaptypeUTF8(w, h.langnum) == noCap {
					mkinitcapUTF(w, h.langnum)
					phCapitalized = u16u8(w)
				}
			} else if getCaptype(ph, h.csconv) == noCap {
				// the C++ code capitalises the still empty result here, so no
				// capitalised pattern comes of an 8-bit dictionary
				phCapitalized = mkinitcap(phCapitalized, h.csconv)
			}
			if phCapitalized != "" {
				// add also lowercase word in the case of German or Hungarian
				// (only UTF-8 dictionaries get here, see above, so the 8-bit
				// lowercasing of the C++ code is left out)
				if h.langnum == langDe || h.langnum == langHu {
					w, _ := warnU8u16(h, wordpart)
					mkallsmallUTF(w, h.langnum)
					lower := u16u8(w)
					h.reptable = append(h.reptable, replentry{pattern: ph, outstrings: [4]string{lower}})
				}
				h.reptable = append(h.reptable, replentry{pattern: phCapitalized, outstrings: [4]string{wordpart}})
			}
		}
		h.reptable = append(h.reptable, replentry{pattern: ph, outstrings: [4]string{wordpart}})
	}
}

func (h *HashMgr) addHiddenCapitalizedWord(word string, wcl int, flags []uint16, dp *string, captype int) int {
	flagslen := len(flags)
	// add inner capitalized forms to handle the following allcap forms:
	// Mixed caps: OpenOffice.org -> OPENOFFICE.ORG
	// Allcaps with suffixes: CIA's -> CIA'S
	if (captype == huhCap || captype == huhInitCap || (captype == allCap && flagslen != 0)) &&
		!(flagslen != 0 && testaff(flags, h.forbiddenword)) {
		flags2 := make([]uint16, flagslen+1)
		copy(flags2, flags)
		flags2[flagslen] = onlyUpcaseFlag
		if flagslen > 0 {
			sortFlags(flags2)
		}
		if h.utf8 {
			w, _ := warnU8u16(h, word)
			mkallsmallUTF(w, h.langnum)
			mkinitcapUTF(w, h.langnum)
			return h.addWord(u16u8(w), wcl, flags2, dp, true, initCap)
		}
		nw := mkallsmall(word, h.csconv)
		nw = mkinitcap(nw, h.csconv)
		return h.addWord(nw, wcl, flags2, dp, true, initCap)
	}
	return 0
}

// newEntry allocates the hash entries in blocks: the entries live as long
// as the dictionary.
func (h *HashMgr) newEntry() *hentry {
	if len(h.slab) == 0 {
		h.slab = make([]hentry, 512)
	}
	hp := &h.slab[0]
	h.slab = h.slab[1:]
	return hp
}

func (h *HashMgr) getClenAndCaptype(word string) (int, int) {
	if h.utf8 {
		warnU8u16Check(h, word)
		var l int
		h.u16buf, l = u8u16Into(h.u16buf, word, false)
		return l, getCaptypeUTF8(h.u16buf, h.langnum)
	}
	return len(word), getCaptype(word, h.csconv)
}

// remove marks the word forbidden (personal dictionary function).
func (h *HashMgr) remove(word string) int {
	dp := h.lookup(word)
	for dp != nil {
		if (len(dp.astr) == 0 || !testaff(dp.astr, h.forbiddenword)) && len(dp.astr) < 32767 {
			flags := make([]uint16, len(dp.astr)+1)
			copy(flags, dp.astr)
			flags[len(dp.astr)] = h.forbiddenword
			sortFlags(flags)
			dp.astr = flags
		}
		dp = dp.nextHomonym
	}
	return 0
}

// removeForbiddenFlag removes the forbidden flag to add a personal word.
func (h *HashMgr) removeForbiddenFlag(word string) {
	dp := h.lookup(word)
	for dp != nil {
		if dp.astr != nil && testaff(dp.astr, h.forbiddenword) {
			if len(dp.astr) == 1 {
				dp.astr = nil
			} else {
				nf := make([]uint16, 0, len(dp.astr)-1)
				for _, f := range dp.astr {
					if f != h.forbiddenword {
						nf = append(nf, f)
					}
				}
				dp.astr = nf
			}
		}
		dp = dp.nextHomonym
	}
}

func (h *HashMgr) add(word string) int {
	h.removeForbiddenFlag(word)
	wcl, captype := h.getClenAndCaptype(word)
	if h.addWord(word, wcl, nil, nil, false, captype) != 0 {
		return 1
	}
	return h.addHiddenCapitalizedWord(word, wcl, nil, nil, captype)
}

func (h *HashMgr) addWithFlags(word, flags, desc string) int {
	h.removeForbiddenFlag(word)
	df := h.decodeFlags(flags, nil)
	wcl, captype := h.getClenAndCaptype(word)
	if h.addWord(word, wcl, df, &desc, false, captype) != 0 {
		return 1
	}
	return h.addHiddenCapitalizedWord(word, wcl, df, &desc, captype)
}

func (h *HashMgr) addWithAffix(word, example string) int {
	dp := h.lookup(example)
	h.removeForbiddenFlag(word)
	if dp != nil && dp.astr != nil {
		wcl, captype := h.getClenAndCaptype(word)
		flags := append([]uint16(nil), dp.astr...)
		if h.addWord(word, wcl, flags, nil, false, captype) != 0 {
			return 1
		}
		return h.addHiddenCapitalizedWord(word, wcl, flags, nil, captype)
	}
	return 1
}

// walkHashtable walks the hash table entry by entry; nil at the end.
// Start with col = -1 and hp = nil.
func (h *HashMgr) walkHashtable(col *int, hp *hentry) *hentry {
	if hp != nil && hp.next != nil {
		return hp.next
	}
	for *col++; *col < len(h.tableptr); *col++ {
		if h.tableptr[*col] != nil {
			return h.tableptr[*col]
		}
	}
	*col = -1
	return nil
}

func (h *HashMgr) loadTables(src Source, key string) int {
	dict := src.open(key, h.errs)
	ts, ok := dict.getline()
	if !ok {
		*h.errs = append(*h.errs, fmt.Sprintf("error: empty dic file %s\n", src.name()))
		return 2
	}
	ts = mychomp(ts)
	ts = strings.TrimPrefix(ts, "\xEF\xBB\xBF")
	tablesize := atoi(ts)
	const nExtra = 5 + userWord
	const maxAllowed = 10000000
	if tablesize <= 0 || tablesize >= maxAllowed {
		*h.errs = append(*h.errs, fmt.Sprintf("error: %s: line 1: missing or bad word count in the dic file\n", src.name()))
		return 4
	}
	tablesize += nExtra
	if tablesize&1 == 0 {
		tablesize++
	}
	h.tableptr = make([]*hentry, tablesize)

	nLineCount := 0
	for {
		ts, ok = dict.getline()
		if !ok {
			break
		}
		nLineCount++
		ts = mychomp(ts)
		// split each line into word and morphological description
		dpPos := 0
		for {
			k := strings.IndexByte(ts[dpPos:], ':')
			if k < 0 {
				dpPos = -1
				break
			}
			dpPos += k
			if dpPos > 3 && (ts[dpPos-3] == ' ' || ts[dpPos-3] == '\t') {
				for dpPos -= 3; dpPos > 0 && (ts[dpPos-1] == ' ' || ts[dpPos-1] == '\t'); dpPos-- {
				}
				if dpPos == 0 { // missing word
					dpPos = -1
				} else {
					dpPos++
				}
				break
			}
			dpPos++
		}
		// tabulator is the old morphological field separator
		if dp2 := strings.IndexByte(ts, '\t'); dp2 >= 0 && (dpPos < 0 || dp2 < dpPos) {
			dpPos = dp2 + 1
		}
		var dp string
		if dpPos >= 0 {
			dp = ts[dpPos:]
			ts = ts[:dpPos-1]
		}

		// split each line into word and affix char strings
		// "\/" signs slash in words (not affix separator)
		// "/" at beginning of the line is word character (not affix separator)
		apPos := strings.IndexByte(ts, '/')
		for apPos >= 0 {
			if apPos == 0 {
				apPos = indexFrom(ts, '/', 1)
				continue
			} else if ts[apPos-1] != '\\' {
				break
			}
			// replace "\/" with "/"
			ts = ts[:apPos-1] + ts[apPos:]
			apPos = indexFrom(ts, '/', apPos)
		}

		var flags []uint16
		if apPos >= 0 && apPos != len(ts) {
			ap := ts[apPos+1:]
			ts = ts[:apPos]
			if len(h.aliasf) > 0 {
				index := atoi(ap)
				flags = h.getAliasf(index, dict)
				if len(flags) == 0 {
					h.warnf("error: line %d: bad flag vector alias\n", dict.getlinenum())
				}
			} else {
				flags = h.decodeFlags(ap, dict)
				sortFlags(flags)
			}
		}

		wcl, captype := h.getClenAndCaptype(ts)
		var dpStr *string
		if dp != "" {
			dpStr = &dp
		}
		if h.addWord(ts, wcl, flags, dpStr, false, captype) != 0 ||
			h.addHiddenCapitalizedWord(ts, wcl, flags, dpStr, captype) != 0 {
			return 5
		}
	}
	// reject ludicrous tablesizes
	if tablesize > 8192+nExtra && tablesize > nLineCount*10+nExtra {
		h.warnf(".dic initial approximate word count line value of %d is too large for %d lines\n", tablesize, nLineCount)
		return 3
	}
	return 0
}

func indexFrom(s string, c byte, from int) int {
	if from > len(s) {
		return -1
	}
	k := strings.IndexByte(s[from:], c)
	if k < 0 {
		return -1
	}
	return from + k
}

// decodeFlags decodes a flag vector. Empty flags give nil.
func (h *HashMgr) decodeFlags(flags string, af lineReader) []uint16 {
	if flags == "" {
		return nil
	}
	switch h.flagMode {
	case flagLong: // two-character flags (1x2yZz -> 1x 2y Zz)
		l := len(flags)
		if l&1 == 1 && af != nil {
			h.warnf("error: line %d: bad flagvector\n", af.getlinenum())
		}
		l >>= 1
		res := make([]uint16, l)
		for i := 0; i < l; i++ {
			res[i] = uint16(flags[i<<1])<<8 | uint16(flags[i<<1|1])
		}
		return res
	case flagNum: // decimal numbers separated by comma (4521,23,233 -> 4521 23 233)
		parts := strings.Split(flags, ",")
		res := make([]uint16, len(parts))
		for k, p := range parts {
			i := atoi(p)
			if (i > 65535 || i < 0) && af != nil {
				h.warnf("error: line %d: flag id %d is out of range\n", af.getlinenum(), i)
				i = 0
			}
			res[k] = uint16(i)
			if res[k] == 0 && af != nil {
				h.warnf("error: line %d: 0 is wrong flag id\n", af.getlinenum())
			}
		}
		return res
	case flagUni: // UTF-8 characters
		w, _ := warnU8u16(h, flags)
		res := make([]uint16, len(w))
		copy(res, w)
		return res
	default: // Ispell's one-character flags (erfg -> e r f g)
		res := make([]uint16, len(flags))
		for i := 0; i < len(flags); i++ {
			res[i] = uint16(flags[i])
		}
		return res
	}
}

// decodeFlagsAppend decodes flags onto the end of result.
func (h *HashMgr) decodeFlagsAppend(result []uint16, flags string, af lineReader) []uint16 {
	if flags == "" {
		return result
	}
	if h.flagMode == flagNum {
		for _, p := range strings.Split(flags, ",") {
			i := atoi(p)
			if i > 65535 || i < 0 {
				h.warnf("error: line %d: flag id %d is out of range\n", af.getlinenum(), i)
				i = 0
			}
			result = append(result, uint16(i))
			if uint16(i) == 0 {
				h.warnf("error: line %d: 0 is wrong flag id\n", af.getlinenum())
			}
		}
		return result
	}
	return append(result, h.decodeFlags(flags, af)...)
}

func (h *HashMgr) decodeFlag(f string) uint16 {
	var s uint16
	switch h.flagMode {
	case flagLong:
		if len(f) >= 2 {
			s = uint16(f[0])<<8 | uint16(f[1])
		}
	case flagNum:
		i := atoi(f)
		if i > 65535 || i < 0 {
			h.warnf("error: flag id %d is out of range\n", i)
			i = 0
		}
		s = uint16(i)
	case flagUni:
		w, _ := warnU8u16(h, f)
		if len(w) > 0 {
			s = w[0]
		}
	default:
		if f != "" {
			s = uint16(f[0])
		}
	}
	if s == 0 {
		h.warnf("error: 0 is wrong flag id\n")
	}
	return s
}

func (h *HashMgr) encodeFlag(f uint16) string {
	if f == 0 {
		return "(NULL)"
	}
	switch h.flagMode {
	case flagLong:
		return string([]byte{byte(f >> 8), byte(f)})
	case flagNum:
		return strconv.Itoa(int(f))
	case flagUni:
		return u16u8([]uint16{f})
	}
	return string([]byte{byte(f)})
}

// loadConfig reads the .aff options the hash manager needs.
func (h *HashMgr) loadConfig(src Source, key string) int {
	afflst := src.open(key, h.errs)
	firstline := true
	for {
		line, ok := afflst.getline()
		if !ok {
			break
		}
		line = mychomp(line)
		if firstline {
			firstline = false
			line = strings.TrimPrefix(line, "\xEF\xBB\xBF")
		}
		if strings.HasPrefix(line, "FLAG") && len(line) > 4 && isSpace(line[4]) {
			if h.flagMode != flagChar {
				h.warnf("error: line %d: multiple definitions of the FLAG affix file parameter\n", afflst.getlinenum())
			}
			if strings.Contains(line, "long") {
				h.flagMode = flagLong
			}
			if strings.Contains(line, "num") {
				h.flagMode = flagNum
			}
			if strings.Contains(line, "UTF-8") {
				h.flagMode = flagUni
			}
			if h.flagMode == flagChar {
				h.warnf("error: line %d: FLAG needs `num', `long' or `UTF-8' parameter\n", afflst.getlinenum())
			}
		}
		if strings.HasPrefix(line, "FORBIDDENWORD") {
			var st string
			if !parseString(h, line, &st, afflst.getlinenum()) {
				return 1
			}
			h.forbiddenword = h.decodeFlag(st)
		}
		if strings.HasPrefix(line, "SET") {
			if !parseString(h, line, &h.enc, afflst.getlinenum()) {
				return 1
			}
			if h.enc == "UTF-8" {
				h.utf8 = true
			} else {
				h.csconv = getCurrentCS(h.enc, h)
			}
		}
		if strings.HasPrefix(line, "LANG") {
			if !parseString(h, line, &h.lang, afflst.getlinenum()) {
				return 1
			}
			h.langnum = getLangNum(h.lang)
		}
		if strings.HasPrefix(line, "IGNORE") {
			if !parseArray(h, line, &h.ignorechars, &h.ignorecharsU16, h.utf8, afflst.getlinenum()) {
				return 1
			}
		}
		if strings.HasPrefix(line, "AF") && len(line) > 2 && isSpace(line[2]) {
			if !h.parseAliasf(line, afflst) {
				return 1
			}
		}
		if strings.HasPrefix(line, "AM") && len(line) > 2 && isSpace(line[2]) {
			if !h.parseAliasm(line, afflst) {
				return 1
			}
		}
		if strings.HasPrefix(line, "COMPLEXPREFIXES") {
			h.complexprefixes = true
		}
		if strings.HasPrefix(line, "REP") {
			if !h.parseReptable(line, afflst) {
				return 1
			}
		}
		// don't check the full affix file, yet
		if (strings.HasPrefix(line, "SFX") || strings.HasPrefix(line, "PFX")) &&
			len(line) > 3 && isSpace(line[3]) && len(h.reptable) > 0 {
			// (REP table is in the end of Afrikaans aff file)
			break
		}
	}
	return 0
}

// tableHeader parses the "KEYWORD n" header line of a table.
func tableHeader(h warner, line string, af lineReader, minimum int) (int, bool) {
	return tableHeaderMsg(h, line, af, minimum, "bad entry number")
}

// tableHeaderMsg is tableHeader with the message of a bad entry number.
func tableHeaderMsg(h warner, line string, af lineReader, minimum int, bad string) (int, bool) {
	i, np, num := 0, 0, 0
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
			num = atoi(piece)
			if num < minimum {
				h.warnf("error: line %d: %s\n", af.getlinenum(), bad)
				return 0, false
			}
			np++
		}
		i++
	}
	if np != 2 {
		h.warnf("error: line %d: missing data\n", af.getlinenum())
		return 0, false
	}
	return num, true
}

func (h *HashMgr) parseAliasf(line string, af lineReader) bool {
	if len(h.aliasf) > 0 {
		h.warnf("error: line %d: multiple table definitions\n", af.getlinenum())
		return false
	}
	num, ok := tableHeader(h, line, af, 1)
	if !ok {
		h.aliasf = nil
		return false
	}
	for j := 0; j < num; j++ {
		var alias []uint16
		if nl, ok := af.getline(); ok {
			nl = mychomp(nl)
			pos, i := 0, 0
			errored := false
			for !errored {
				piece, ok := mystrsep(nl, &pos)
				if !ok {
					break
				}
				switch i {
				case 0:
					if !strings.HasPrefix(piece, "AF") {
						errored = true
					}
				case 1:
					alias = h.decodeFlags(piece, af)
					sortFlags(alias)
				}
				i++
			}
		}
		if alias == nil {
			h.aliasf = nil
			h.warnf("error: line %d: table is corrupt\n", af.getlinenum())
			return false
		}
		h.aliasf = append(h.aliasf, alias)
	}
	return true
}

func (h *HashMgr) isAliasf() bool { return len(h.aliasf) > 0 }

func (h *HashMgr) getAliasf(index int, af lineReader) []uint16 {
	if index > 0 && index <= len(h.aliasf) {
		return h.aliasf[index-1]
	}
	ln := 0
	if af != nil {
		ln = af.getlinenum()
	}
	h.warnf("error: line %d: bad flag alias index: %d\n", ln, index)
	return nil
}

func (h *HashMgr) parseAliasm(line string, af lineReader) bool {
	if len(h.aliasm) > 0 {
		h.warnf("error: line %d: multiple table definitions\n", af.getlinenum())
		return false
	}
	num, ok := tableHeader(h, line, af, 1)
	if !ok {
		h.aliasm = nil
		return false
	}
	for j := 0; j < num; j++ {
		var alias string
		found := false
		if nl, ok := af.getline(); ok {
			nl = mychomp(nl)
			pos, i := 0, 0
			errored := false
			for !errored {
				start := pos
				piece, ok := mystrsep(nl, &pos)
				if !ok {
					break
				}
				switch i {
				case 0:
					if !strings.HasPrefix(piece, "AM") {
						errored = true
					}
				case 1:
					// add the remaining of the line
					for start < len(nl) && (nl[start] == ' ' || nl[start] == '\t') {
						start++
					}
					chunk := nl[start:]
					if h.complexprefixes {
						if h.utf8 {
							chunk = warnReversewordUTF(h, chunk)
						} else {
							chunk = reverseword(chunk)
						}
					}
					alias = chunk
					found = true
				}
				i++
			}
		}
		if !found {
			h.aliasm = nil
			h.warnf("error: line %d: table is corrupt\n", af.getlinenum())
			return false
		}
		h.aliasm = append(h.aliasm, alias)
	}
	return true
}

func (h *HashMgr) isAliasm() bool { return len(h.aliasm) > 0 }

func (h *HashMgr) getAliasm(index int) (string, bool) {
	if index > 0 && index <= len(h.aliasm) {
		return h.aliasm[index-1], true
	}
	h.warnf("error: bad morph. alias index: %d\n", index)
	return "", false
}

func (h *HashMgr) parseReptable(line string, af lineReader) bool {
	if len(h.reptable) > 0 {
		h.warnf("error: line %d: multiple table definitions\n", af.getlinenum())
		return false
	}
	num, ok := tableHeaderMsg(h, line, af, 1, "incorrect entry number")
	if !ok {
		return false
	}
	for j := 0; j < num; j++ {
		h.reptable = append(h.reptable, replentry{})
		r := &h.reptable[len(h.reptable)-1]
		typ := 0
		if nl, ok := af.getline(); ok {
			nl = mychomp(nl)
			pos, i := 0, 0
			errored := false
			for !errored {
				piece, ok := mystrsep(nl, &pos)
				if !ok {
					break
				}
				switch i {
				case 0:
					if !strings.HasPrefix(piece, "REP") {
						errored = true
					}
				case 1:
					if piece[0] == '^' {
						typ = 1
					}
					r.pattern = mystrrep(piece[typ:], "_", " ")
					if r.pattern != "" && r.pattern[len(r.pattern)-1] == '$' {
						typ += 2
						r.pattern = r.pattern[:len(r.pattern)-1]
					}
				case 2:
					r.outstrings[typ] = mystrrep(piece, "_", " ")
				}
				i++
			}
		}
		if r.pattern == "" || r.outstrings[typ] == "" {
			h.warnf("error: line %d: table is corrupt\n", af.getlinenum())
			h.reptable = nil
			return false
		}
	}
	return true
}

// warnf records a diagnostic of the debug builds of Hunspell
// (HUNSPELL_WARNING).
func (h *HashMgr) warnf(format string, args ...any) {
	*h.warns = append(*h.warns, fmt.Sprintf(format, args...))
}
