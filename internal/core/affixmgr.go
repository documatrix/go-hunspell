package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	setSize        = 256
	contSize       = 65536
	minCpdLen      = 3
	maxCondLen     = 20
	dupSFX         = 1 << 0
	dupPFX         = 1 << 1
	maxMorphResult = 4 * 1024 * 1024
)

type patentry struct {
	pattern  string
	pattern2 string
	pattern3 string
	cond     uint16
	cond2    uint16
}

// AffixMgr holds the rules of an .aff file.
type AffixMgr struct {
	limits    timeLimits
	clockTick uint // see cpdClockExceeded
	pStart    [setSize]*PfxEntry
	sStart    [setSize]*SfxEntry
	pFlag     [setSize]*PfxEntry
	sFlag     [setSize]*SfxEntry
	alldic    *[]*HashMgr
	pHMgr     *HashMgr

	keystring            string
	trystring            string
	encoding             string
	csconv               *[256]csInfo
	utf8                 bool
	complexprefixes      bool
	compoundflag         uint16
	compoundbegin        uint16
	compoundmiddle       uint16
	compoundend          uint16
	compoundroot         uint16
	compoundforbidflag   uint16
	compoundpermitflag   uint16
	compoundmoresuffixes bool
	checkcompounddup     bool
	checkcompoundrep     bool
	checkcompoundcase    bool
	checkcompoundtriple  bool
	simplifiedtriple     bool
	forbiddenword        uint16
	nosuggest            uint16
	nongramsuggest       uint16
	needaffix            uint16
	cpdmin               int
	iconvtable           *repList
	oconvtable           *repList
	parsedmaptable       bool
	maptable             [][]string
	parsedbreaktable     bool
	breaktable           []string
	parsedcheckcpd       bool
	checkcpdtable        []patentry
	simplifiedcpd        bool
	parseddefcpd         bool
	defcpdtable          [][]uint16
	phone                *phonetable
	maxngramsugs         int
	maxcpdsugs           int
	maxdiff              int
	onlymaxdiff          bool
	nosplitsugs          bool
	sugswithdots         bool
	cpdwordmax           int
	cpdmaxsyllable       int
	cpdvowels            string
	cpdvowelsUTF16       []uint16
	cpdsyllablenum       string
	sfxappnd             *string   // BUG: not stateless
	sfxextra             int       // BUG: not stateless
	sfxflag              uint16    // BUG: not stateless
	sfx                  *SfxEntry // BUG: not stateless
	pfx                  *PfxEntry // BUG: not stateless
	checknum             bool
	wordchars            string
	wordcharsUTF16       []uint16
	ignorechars          string
	ignorecharsUTF16     []uint16
	version              string
	lang                 string
	langnum              int
	lemmaPresent         uint16
	circumfix            uint16
	onlyincompound       uint16
	keepcase             uint16
	forceucase           uint16
	warn                 uint16
	forbidwarn           bool
	substandard          uint16
	checksharps          bool
	fullstrip            bool
	havecontclass        bool
	contclasses          [contSize]bool

	// the start time and time limit state of compound_check and
	// compound_check_morph (thread locals in the C++ code)
	cpdStart         time.Time
	cpdTimeExceeded  bool
	cpdmStart        time.Time
	cpdmTimeExceeded bool

	errs  *[]string
	warns *[]string

	// scratch buffers of the affix checks (see scratch)
	pfxWord   []byte
	pfxTwosfx []byte
	sfxWord   []byte
	sfxTwosfx []byte
}

func newAffixMgr(aff Source, dics *[]*HashMgr, key string, errs, warns *[]string) *AffixMgr {
	a := &AffixMgr{
		limits:        defaultTimeLimits(),
		alldic:        dics,
		pHMgr:         (*dics)[0],
		forbiddenword: forbiddenWord,
		cpdwordmax:    -1,
		cpdmin:        -1,
		maxngramsugs:  -1,
		maxdiff:       -1,
		maxcpdsugs:    -1,
		errs:          errs,
		warns:         warns,
	}
	if a.parseFile(aff, key) != 0 {
		*errs = append(*errs, fmt.Sprintf("Failure loading aff file %s\n", aff.name()))
	}
	// get encoding for CHECKCOMPOUNDCASE
	if !a.utf8 {
		a.csconv = getCurrentCS(a.getEncoding(), a)
		for i := 0; i <= 255; i++ {
			if a.csconv[i].cupper != a.csconv[i].clower && strings.IndexByte(a.wordchars, byte(i)) < 0 {
				a.wordchars += string([]byte{byte(i)})
			}
		}
	}
	// default BREAK definition
	if !a.parsedbreaktable {
		a.breaktable = []string{"-", "^-", "-$"}
		a.parsedbreaktable = true
	}
	if a.cpdmin == -1 {
		a.cpdmin = minCpdLen
	}
	return a
}

func (a *AffixMgr) finishFileMgr() {
	// convert affix trees to sorted list
	a.processPfxTreeToList()
	a.processSfxTreeToList()
}

// parseFile reads in the aff file and builds up the prefix and suffix
// entry objects.
func (a *AffixMgr) parseFile(src Source, key string) int {
	var dupflags []byte
	firstline := true
	afflst := src.open(key, a.errs)
	fail := func() int {
		a.finishFileMgr()
		return 1
	}
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
		ln := afflst.getlinenum()
		has := func(p string) bool { return strings.HasPrefix(line, p) }

		if has("KEY") && !parseString(a, line, &a.keystring, ln) {
			return fail()
		}
		if has("TRY") && !parseString(a, line, &a.trystring, ln) {
			return fail()
		}
		if has("SET") {
			if !parseString(a, line, &a.encoding, ln) {
				return fail()
			}
			if a.encoding == "UTF-8" {
				a.utf8 = true
			}
		}
		if has("COMPLEXPREFIXES") {
			a.complexprefixes = true
		}
		if has("COMPOUNDFLAG") && !a.parseFlag(line, &a.compoundflag, afflst) {
			return fail()
		}
		if has("COMPOUNDBEGIN") {
			target := &a.compoundbegin
			if a.complexprefixes {
				target = &a.compoundend
			}
			if !a.parseFlag(line, target, afflst) {
				return fail()
			}
		}
		if has("COMPOUNDMIDDLE") && !a.parseFlag(line, &a.compoundmiddle, afflst) {
			return fail()
		}
		if has("COMPOUNDEND") {
			target := &a.compoundend
			if a.complexprefixes {
				target = &a.compoundbegin
			}
			if !a.parseFlag(line, target, afflst) {
				return fail()
			}
		}
		if has("COMPOUNDWORDMAX") && !a.parseNum(line, &a.cpdwordmax, afflst) {
			return fail()
		}
		if has("COMPOUNDROOT") && !a.parseFlag(line, &a.compoundroot, afflst) {
			return fail()
		}
		if has("COMPOUNDPERMITFLAG") && !a.parseFlag(line, &a.compoundpermitflag, afflst) {
			return fail()
		}
		if has("COMPOUNDFORBIDFLAG") && !a.parseFlag(line, &a.compoundforbidflag, afflst) {
			return fail()
		}
		if has("COMPOUNDMORESUFFIXES") {
			a.compoundmoresuffixes = true
		}
		if has("CHECKCOMPOUNDDUP") {
			a.checkcompounddup = true
		}
		if has("CHECKCOMPOUNDREP") {
			a.checkcompoundrep = true
		}
		if has("CHECKCOMPOUNDTRIPLE") {
			a.checkcompoundtriple = true
		}
		if has("SIMPLIFIEDTRIPLE") {
			a.simplifiedtriple = true
		}
		if has("CHECKCOMPOUNDCASE") {
			a.checkcompoundcase = true
		}
		if has("NOSUGGEST") && !a.parseFlag(line, &a.nosuggest, afflst) {
			return fail()
		}
		if has("NONGRAMSUGGEST") && !a.parseFlag(line, &a.nongramsuggest, afflst) {
			return fail()
		}
		if has("FORBIDDENWORD") && !a.parseFlag(line, &a.forbiddenword, afflst) {
			return fail()
		}
		if has("LEMMA_PRESENT") && !a.parseFlag(line, &a.lemmaPresent, afflst) {
			return fail()
		}
		if has("CIRCUMFIX") && !a.parseFlag(line, &a.circumfix, afflst) {
			return fail()
		}
		if has("ONLYINCOMPOUND") && !a.parseFlag(line, &a.onlyincompound, afflst) {
			return fail()
		}
		if has("PSEUDOROOT") && !a.parseFlag(line, &a.needaffix, afflst) {
			return fail()
		}
		if has("NEEDAFFIX") && !a.parseFlag(line, &a.needaffix, afflst) {
			return fail()
		}
		if has("COMPOUNDMIN") {
			if !a.parseNum(line, &a.cpdmin, afflst) {
				return fail()
			}
			if a.cpdmin < 1 {
				a.cpdmin = 1
			}
		}
		if has("COMPOUNDSYLLABLE") && !a.parseCpdsyllable(line, afflst) {
			return fail()
		}
		if has("SYLLABLENUM") && !parseString(a, line, &a.cpdsyllablenum, ln) {
			return fail()
		}
		if has("CHECKNUM") {
			a.checknum = true
		}
		if has("WORDCHARS") && !parseArray(a, line, &a.wordchars, &a.wordcharsUTF16, a.utf8, ln) {
			return fail()
		}
		if has("IGNORE") && !parseArray(a, line, &a.ignorechars, &a.ignorecharsUTF16, a.utf8, ln) {
			return fail()
		}
		if has("ICONV") && !a.parseConvtable(line, afflst, &a.iconvtable, "ICONV") {
			return fail()
		}
		if has("OCONV") && !a.parseConvtable(line, afflst, &a.oconvtable, "OCONV") {
			return fail()
		}
		if has("PHONE") && !a.parsePhonetable(line, afflst) {
			return fail()
		}
		if has("CHECKCOMPOUNDPATTERN") && !a.parseCheckcpdtable(line, afflst) {
			return fail()
		}
		if has("COMPOUNDRULE") && !a.parseDefcpdtable(line, afflst) {
			return fail()
		}
		if has("MAP") && !a.parseMaptable(line, afflst) {
			return fail()
		}
		if has("BREAK") && !a.parseBreaktable(line, afflst) {
			return fail()
		}
		if has("LANG") {
			if !parseString(a, line, &a.lang, ln) {
				return fail()
			}
			a.langnum = getLangNum(a.lang)
		}
		if has("VERSION") {
			rest := line[7:]
			if i := strings.IndexFunc(rest, func(r rune) bool { return r != ' ' && r != '\t' }); i >= 0 {
				a.version = rest[i:]
			}
		}
		if has("MAXNGRAMSUGS") && !a.parseNum(line, &a.maxngramsugs, afflst) {
			return fail()
		}
		if has("ONLYMAXDIFF") {
			a.onlymaxdiff = true
		}
		if has("MAXDIFF") && !a.parseNum(line, &a.maxdiff, afflst) {
			return fail()
		}
		if has("MAXCPDSUGS") && !a.parseNum(line, &a.maxcpdsugs, afflst) {
			return fail()
		}
		if has("NOSPLITSUGS") {
			a.nosplitsugs = true
		}
		if has("FULLSTRIP") {
			a.fullstrip = true
		}
		if has("SUGSWITHDOTS") {
			a.sugswithdots = true
		}
		if has("KEEPCASE") && !a.parseFlag(line, &a.keepcase, afflst) {
			return fail()
		}
		if has("FORCEUCASE") && !a.parseFlag(line, &a.forceucase, afflst) {
			return fail()
		}
		if has("WARN") && !a.parseFlag(line, &a.warn, afflst) {
			return fail()
		}
		if has("FORBIDWARN") {
			a.forbidwarn = true
		}
		if has("SUBSTANDARD") && !a.parseFlag(line, &a.substandard, afflst) {
			return fail()
		}
		if has("CHECKSHARPS") {
			a.checksharps = true
		}
		// parse this affix: P - prefix, S - suffix
		ft := byte(' ')
		if has("PFX") {
			ft = 'P'
			if a.complexprefixes {
				ft = 'S'
			}
		}
		if has("SFX") {
			ft = 'S'
			if a.complexprefixes {
				ft = 'P'
			}
		}
		if ft != ' ' {
			if dupflags == nil {
				dupflags = make([]byte, contSize)
			}
			if !a.parseAffix(line, ft, afflst, dupflags) {
				return fail()
			}
		}
	}
	a.finishFileMgr()
	// affix trees are sorted now; set up the nextne and nexteq links of the
	// subset relations
	a.processPfxOrder()
	a.processSfxOrder()
	return 0
}

// buildPfxtree indexes a prefix by flag and by affix string.
func (a *AffixMgr) buildPfxtree(ep *PfxEntry) {
	key := ep.appnd
	flg := byte(ep.aflag & 0xff)
	// first index by flag which must exist
	ep.flgnxt = a.pFlag[flg]
	a.pFlag[flg] = ep
	// handle the special case of null affix string
	if key == "" {
		ep.next = a.pStart[0]
		a.pStart[0] = ep
		return
	}
	ep.nexteq = nil
	ep.nextne = nil
	sp := key[0]
	ptr := a.pStart[sp]
	if ptr == nil {
		a.pStart[sp] = ep
		return
	}
	// binary tree insertion so that a sorted list can be generated later
	for {
		pptr := ptr
		if strcmp(ep.appnd, ptr.appnd) <= 0 {
			ptr = ptr.nexteq
			if ptr == nil {
				pptr.nexteq = ep
				break
			}
		} else {
			ptr = ptr.nextne
			if ptr == nil {
				pptr.nextne = ep
				break
			}
		}
	}
}

// buildSfxtree indexes a suffix by flag and by its reversed affix string.
func (a *AffixMgr) buildSfxtree(ep *SfxEntry) {
	ep.initReverseWord()
	key := ep.rappnd
	flg := byte(ep.aflag & 0xff)
	ep.flgnxt = a.sFlag[flg]
	a.sFlag[flg] = ep
	if key == "" {
		ep.next = a.sStart[0]
		a.sStart[0] = ep
		return
	}
	ep.nexteq = nil
	ep.nextne = nil
	sp := key[0]
	ptr := a.sStart[sp]
	if ptr == nil {
		a.sStart[sp] = ep
		return
	}
	for {
		pptr := ptr
		if strcmp(ep.rappnd, ptr.rappnd) <= 0 {
			ptr = ptr.nexteq
			if ptr == nil {
				pptr.nexteq = ep
				break
			}
		} else {
			ptr = ptr.nextne
			if ptr == nil {
				pptr.nextne = ep
				break
			}
		}
	}
}

func (a *AffixMgr) processPfxTreeToList() {
	for i := 1; i < setSize; i++ {
		a.pStart[i] = processPfxInOrder(a.pStart[i], nil)
	}
}

func processPfxInOrder(ptr, nptr *PfxEntry) *PfxEntry {
	if ptr != nil {
		nptr = processPfxInOrder(ptr.nextne, nptr)
		ptr.next = nptr
		nptr = processPfxInOrder(ptr.nexteq, ptr)
	}
	return nptr
}

func (a *AffixMgr) processSfxTreeToList() {
	for i := 1; i < setSize; i++ {
		a.sStart[i] = processSfxInOrder(a.sStart[i], nil)
	}
}

func processSfxInOrder(ptr, nptr *SfxEntry) *SfxEntry {
	if ptr != nil {
		nptr = processSfxInOrder(ptr.nextne, nptr)
		ptr.next = nptr
		nptr = processSfxInOrder(ptr.nexteq, ptr)
	}
	return nptr
}

// isSubset reports whether s1 is a leading subset of the C string s2 (dots
// are for infixes).
func isSubset(s1, s2 string) bool {
	i := 0
	for i < len(s1) {
		c2 := byteAt(s2, i)
		if c2 == 0 || (s1[i] != c2 && s1[i] != '.') {
			break
		}
		i++
	}
	return i >= len(s1)
}

func (a *AffixMgr) processPfxOrder() {
	for i := 1; i < setSize; i++ {
		for ptr := a.pStart[i]; ptr != nil; ptr = ptr.next {
			nptr := ptr.next
			for ; nptr != nil; nptr = nptr.next {
				if !isSubset(ptr.appnd, nptr.appnd) {
					break
				}
			}
			ptr.nextne = nptr
			ptr.nexteq = nil
			if ptr.next != nil && isSubset(ptr.appnd, ptr.next.appnd) {
				ptr.nexteq = ptr.next
			}
		}
		for ptr := a.pStart[i]; ptr != nil; ptr = ptr.next {
			var mptr *PfxEntry
			for nptr := ptr.next; nptr != nil; nptr = nptr.next {
				if !isSubset(ptr.appnd, nptr.appnd) {
					break
				}
				mptr = nptr
			}
			if mptr != nil {
				mptr.nextne = nil
			}
		}
	}
}

func (a *AffixMgr) processSfxOrder() {
	for i := 1; i < setSize; i++ {
		for ptr := a.sStart[i]; ptr != nil; ptr = ptr.next {
			nptr := ptr.next
			for ; nptr != nil; nptr = nptr.next {
				if !isSubset(ptr.rappnd, nptr.rappnd) {
					break
				}
			}
			ptr.nextne = nptr
			ptr.nexteq = nil
			if ptr.next != nil && isSubset(ptr.rappnd, ptr.next.rappnd) {
				ptr.nexteq = ptr.next
			}
		}
		for ptr := a.sStart[i]; ptr != nil; ptr = ptr.next {
			var mptr *SfxEntry
			for nptr := ptr.next; nptr != nil; nptr = nptr.next {
				if !isSubset(ptr.rappnd, nptr.rappnd) {
					break
				}
				mptr = nptr
			}
			if mptr != nil {
				mptr.nextne = nil
			}
		}
	}
}

// debugflag adds a flag to the result for dictionary debugging.
func (a *AffixMgr) debugflag(result *strings.Builder, flag uint16) {
	result.WriteByte(msepFld)
	result.WriteString(morphFlag)
	result.WriteString(a.encodeFlag(flag))
}

// condlen counts the match positions of a condition: each [...] group counts
// as one, each other character as one.
func (a *AffixMgr) condlen(s string) int {
	l := 0
	group := false
	for i := 0; i < len(s); {
		switch {
		case s[i] == '[':
			group = true
			l++
			i++
		case s[i] == ']':
			group = false
			i++
		case group:
			i++
		default:
			l++
			if a.utf8 {
				i = utf8Next(s, i)
			} else {
				i++
			}
		}
	}
	return l
}

func (a *AffixMgr) encodeit(e *affEntry, cs string) bool {
	if cs != "." {
		n := a.condlen(cs)
		if n > 255 {
			a.warnf("error: condition length %d is over max limit\n", n)
			return false
		}
		e.numconds = n
		e.conds = cs
		if len(cs) > maxCondLen {
			e.opts |= aeLongCond
		}
	} else {
		e.numconds = 0
		e.conds = ""
	}
	return true
}

func (a *AffixMgr) lookup(word string) *hentry {
	for _, h := range *a.alldic {
		if he := h.lookup(word); he != nil {
			return he
		}
	}
	return nil
}

func (a *AffixMgr) getEncoding() string {
	if a.encoding == "" {
		a.encoding = spellEncoding
	}
	return a.encoding
}

func (a *AffixMgr) getKeyString() string {
	if a.keystring == "" {
		a.keystring = spellKeystring
	}
	return a.keystring
}

func (a *AffixMgr) getCompound() bool {
	return a.compoundflag != 0 || a.compoundbegin != 0 || len(a.defcpdtable) > 0
}

func (a *AffixMgr) getReptable() []replentry { return a.pHMgr.reptable }

func (a *AffixMgr) encodeFlag(f uint16) string { return a.pHMgr.encodeFlag(f) }

func (a *AffixMgr) parseFlag(line string, out *uint16, af lineReader) bool {
	if *out != 0 && !(*out >= defaultFlags) {
		a.warnf("error: line %d: multiple definitions of an affix file parameter\n", af.getlinenum())
		return false
	}
	var s string
	if !parseString(a, line, &s, af.getlinenum()) {
		return false
	}
	*out = a.pHMgr.decodeFlag(s)
	return true
}

func (a *AffixMgr) parseNum(line string, out *int, af lineReader) bool {
	if *out != -1 {
		a.warnf("error: line %d: multiple definitions of an affix file parameter\n", af.getlinenum())
		return false
	}
	var s string
	if !parseString(a, line, &s, af.getlinenum()) {
		return false
	}
	*out = atoi(s)
	return true
}

func (a *AffixMgr) parseCpdsyllable(line string, af lineReader) bool {
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
			a.cpdmaxsyllable = atoi(piece)
			np++
		case 2:
			if !a.utf8 {
				b := []byte(piece)
				sort.Slice(b, func(i, j int) bool { return int8(b[i]) < int8(b[j]) })
				a.cpdvowels = string(b)
			} else {
				w, _ := warnU8u16(a, piece)
				sortFlags(w)
				a.cpdvowelsUTF16 = w
			}
			np++
		}
		i++
	}
	if np < 2 {
		a.warnf("error: line %d: missing compoundsyllable information\n", af.getlinenum())
		return false
	}
	if np == 2 {
		a.cpdvowels = "AEIOUaeiou"
	}
	return true
}

func (a *AffixMgr) parseConvtable(line string, af lineReader, rl **repList, keyword string) bool {
	if *rl != nil {
		a.warnf("error: line %d: multiple table definitions\n", af.getlinenum())
		return false
	}
	i, np, numrl := 0, 0, 0
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
			numrl = atoi(piece)
			if numrl < 1 {
				a.warnf("error: line %d: incorrect entry number\n", af.getlinenum())
				return false
			}
			*rl = newRepList()
			np++
		}
		i++
	}
	if np != 2 {
		a.warnf("error: line %d: missing data\n", af.getlinenum())
		return false
	}
	for j := 0; j < numrl; j++ {
		nl, ok := af.getline()
		if !ok {
			return false
		}
		nl = mychomp(nl)
		i = 0
		var pattern, pattern2 string
		pos = 0
		for {
			piece, ok := mystrsep(nl, &pos)
			if !ok {
				break
			}
			switch i {
			case 0:
				if !strings.HasPrefix(piece, keyword) {
					a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
					*rl = nil
					return false
				}
			case 1:
				pattern = piece
			case 2:
				pattern2 = piece
			}
			i++
		}
		if pattern == "" || pattern2 == "" {
			a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
			return false
		}
		(*rl).add(pattern, pattern2)
	}
	return true
}

func (a *AffixMgr) parsePhonetable(line string, af lineReader) bool {
	if a.phone != nil {
		a.warnf("error: line %d: multiple table definitions\n", af.getlinenum())
		return false
	}
	num, ok := tableHeader(a, line, af, 1)
	if !ok {
		return false
	}
	np := &phonetable{utf8: a.utf8}
	for j := 0; j < num; j++ {
		nl, ok := af.getline()
		if !ok {
			return false
		}
		nl = mychomp(nl)
		oldSize := len(np.rules)
		pos, i := 0, 0
		for {
			piece, ok := mystrsep(nl, &pos)
			if !ok {
				break
			}
			switch i {
			case 0:
				if !strings.HasPrefix(piece, "PHONE") {
					a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
					return false
				}
			case 1:
				np.rules = append(np.rules, piece)
			case 2:
				np.rules = append(np.rules, mystrrep(piece, "_", ""))
			}
			i++
		}
		if len(np.rules) != oldSize+2 {
			a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
			return false
		}
	}
	np.rules = append(np.rules, "", "")
	initPhonetHash(np)
	a.phone = np
	return true
}

func (a *AffixMgr) parseCheckcpdtable(line string, af lineReader) bool {
	if a.parsedcheckcpd {
		a.warnf("error: line %d: multiple table definitions\n", af.getlinenum())
		return false
	}
	a.parsedcheckcpd = true
	num, ok := tableHeader(a, line, af, 1)
	if !ok {
		return false
	}
	for j := 0; j < num; j++ {
		nl, ok := af.getline()
		if !ok {
			return false
		}
		nl = mychomp(nl)
		a.checkcpdtable = append(a.checkcpdtable, patentry{})
		e := &a.checkcpdtable[len(a.checkcpdtable)-1]
		pos, i := 0, 0
		for {
			piece, ok := mystrsep(nl, &pos)
			if !ok {
				break
			}
			switch i {
			case 0:
				if !strings.HasPrefix(piece, "CHECKCOMPOUNDPATTERN") {
					a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
					a.checkcpdtable = nil
					return false
				}
			case 1:
				e.pattern = piece
				if k := strings.IndexByte(piece, '/'); k >= 0 {
					e.pattern = piece[:k]
					e.cond = a.pHMgr.decodeFlag(piece[k+1:])
				}
			case 2:
				e.pattern2 = piece
				if k := strings.IndexByte(piece, '/'); k >= 0 {
					e.pattern2 = piece[:k]
					e.cond2 = a.pHMgr.decodeFlag(piece[k+1:])
				}
			case 3:
				e.pattern3 = piece
				a.simplifiedcpd = true
			}
			i++
		}
	}
	return true
}

func (a *AffixMgr) parseDefcpdtable(line string, af lineReader) bool {
	if a.parseddefcpd {
		a.warnf("error: line %d: multiple table definitions\n", af.getlinenum())
		return false
	}
	a.parseddefcpd = true
	num, ok := tableHeader(a, line, af, 1)
	if !ok {
		return false
	}
	for j := 0; j < num; j++ {
		nl, ok := af.getline()
		if !ok {
			return false
		}
		nl = mychomp(nl)
		var rule []uint16
		pos, i := 0, 0
		for {
			piece, ok := mystrsep(nl, &pos)
			if !ok {
				break
			}
			switch i {
			case 0:
				if !strings.HasPrefix(piece, "COMPOUNDRULE") {
					a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
					a.defcpdtable = append(a.defcpdtable, rule)
					return false
				}
			case 1: // handle parenthesized flags
				if strings.IndexByte(piece, '(') >= 0 {
					for k := 0; k < len(piece); k++ {
						chb, che := k, k+1
						if piece[k] == '(' {
							if par := strings.IndexByte(piece[k:], ')'); par >= 0 {
								chb = k + 1
								che = k + par
								k = k + par
							}
						}
						if chb < len(piece) && (piece[chb] == '*' || piece[chb] == '?') {
							rule = append(rule, uint16(piece[chb]))
						} else {
							rule = a.pHMgr.decodeFlagsAppend(rule, piece[chb:che], af)
						}
					}
				} else {
					rule = a.pHMgr.decodeFlagsAppend(rule, piece, af)
				}
			}
			i++
		}
		a.defcpdtable = append(a.defcpdtable, rule)
		if len(rule) == 0 {
			a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
			return false
		}
	}
	return true
}

func (a *AffixMgr) parseMaptable(line string, af lineReader) bool {
	if a.parsedmaptable {
		a.warnf("error: line %d: multiple table definitions\n", af.getlinenum())
		return false
	}
	a.parsedmaptable = true
	num, ok := tableHeader(a, line, af, 1)
	if !ok {
		return false
	}
	for j := 0; j < num; j++ {
		nl, ok := af.getline()
		if !ok {
			return false
		}
		nl = mychomp(nl)
		var entry []string
		pos, i := 0, 0
		for {
			piece, ok := mystrsep(nl, &pos)
			if !ok {
				break
			}
			switch i {
			case 0:
				if !strings.HasPrefix(piece, "MAP") {
					a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
					return false
				}
			case 1:
				for k := 0; k < len(piece); k++ {
					chb, che := k, k+1
					if piece[k] == '(' {
						if par := strings.IndexByte(piece[k:], ')'); par >= 0 {
							chb = k + 1
							che = k + par
							k = k + par
						}
					} else if a.utf8 && piece[k]&0xc0 == 0xc0 {
						k++
						for k < len(piece) && isUTF8Cont(piece[k]) {
							k++
						}
						che = k
						k--
					}
					if chb == che {
						a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
					}
					entry = append(entry, piece[chb:che])
				}
			}
			i++
		}
		a.maptable = append(a.maptable, entry)
		if len(entry) == 0 {
			a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
			return false
		}
	}
	return true
}

func (a *AffixMgr) parseBreaktable(line string, af lineReader) bool {
	if a.parsedbreaktable {
		a.warnf("error: line %d: multiple table definitions\n", af.getlinenum())
		return false
	}
	a.parsedbreaktable = true
	i, np, numbreak := 0, 0, -1
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
			numbreak = atoi(piece)
			if numbreak < 0 {
				a.warnf("error: line %d: bad entry number\n", af.getlinenum())
				return false
			}
			if numbreak == 0 {
				return true
			}
			np++
		}
		i++
	}
	if np != 2 {
		a.warnf("error: line %d: missing data\n", af.getlinenum())
		return false
	}
	for j := 0; j < numbreak; j++ {
		nl, ok := af.getline()
		if !ok {
			return false
		}
		nl = mychomp(nl)
		pos, i := 0, 0
		for {
			piece, ok := mystrsep(nl, &pos)
			if !ok {
				break
			}
			switch i {
			case 0:
				if !strings.HasPrefix(piece, "BREAK") {
					a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
					return false
				}
			case 1:
				a.breaktable = append(a.breaktable, piece)
			}
			i++
		}
	}
	if len(a.breaktable) != numbreak {
		a.warnf("error: line %d: table is corrupt\n", af.getlinenum())
		return false
	}
	return true
}

// reverseCondition turns a condition around, so that a group keeps its
// meaning once the text it belongs to has been reversed.
// (C++ returns at once for an empty piece, which the loop leaves alone too.)
func reverseCondition(piece []byte) {
	neg := false
	for k := len(piece) - 1; k >= 0; k-- {
		switch piece[k] {
		case '[':
			if neg {
				piece[k+1] = '['
			} else {
				piece[k] = ']'
			}
		case ']':
			piece[k] = '['
			if neg {
				piece[k+1] = '^'
			}
			neg = false
		case '^':
			if k+1 < len(piece) && piece[k+1] == ']' {
				neg = true
			} else if neg {
				piece[k+1] = piece[k]
			}
		default:
			if neg {
				piece[k+1] = piece[k]
			}
		}
	}
}

func (a *AffixMgr) parseAffix(line string, at byte, af lineReader, dupflags []byte) bool {
	numents := 0
	var aflag uint16
	var ff byte
	var xprod byte
	headerline := af.getlinenum()
	var opts byte

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
			np++
			aflag = a.pHMgr.decodeFlag(piece)
			if (at == 'S' && dupflags[aflag]&dupSFX != 0) || (at == 'P' && dupflags[aflag]&dupPFX != 0) {
				a.warnf("error: line %d: multiple definitions of an affix flag\n", af.getlinenum())
			}
			if at == 'S' {
				dupflags[aflag] += dupSFX
			} else {
				dupflags[aflag] += dupPFX
			}
		case 2:
			np++
			xprod = piece[0]
			if xprod == 'Y' {
				ff = aeXProduct
			}
		case 3:
			np++
			numents = atoi(piece)
			if numents <= 0 {
				a.warnf("error: line %d: affix %s: bad entry number\n", af.getlinenum(), a.pHMgr.encodeFlag(aflag))
				return false
			}
			opts = ff
			if a.utf8 {
				opts |= aeUTF8
			}
			if a.pHMgr.isAliasf() {
				opts |= aeAliasF
			}
			if a.pHMgr.isAliasm() {
				opts |= aeAliasM
			}
		}
		i++
	}
	if np != 4 {
		a.warnf("error: line %d: affix %s: missing data\n", af.getlinenum(), a.pHMgr.encodeFlag(aflag))
		return false
	}

	entries := make([]*affEntry, 0, numents)
	var pfxs []*PfxEntry
	var sfxs []*SfxEntry
	newEntry := func() *affEntry {
		if at == 'P' {
			p := &PfxEntry{mgr: a}
			pfxs = append(pfxs, p)
			return &p.affEntry
		}
		s := &SfxEntry{mgr: a}
		sfxs = append(sfxs, s)
		return &s.affEntry
	}
	first := newEntry()
	first.opts = opts
	first.aflag = aflag
	entries = append(entries, first)
	entry := first

	for ent := 0; ent < numents; ent++ {
		nl, ok := af.getline()
		if !ok {
			return false
		}
		nl = mychomp(nl)
		ruleline := af.getlinenum()
		pos, i, np = 0, 0, 0
		for {
			start := pos
			piece, ok := mystrsep(nl, &pos)
			if !ok {
				break
			}
			_ = start
			switch i {
			case 0:
				np++
				if ent != 0 {
					entry = newEntry()
					entry.opts = first.opts & (aeXProduct | aeUTF8 | aeAliasF | aeAliasM)
					entries = append(entries, entry)
				}
			case 1:
				np++
				if a.pHMgr.decodeFlag(piece) != aflag {
					a.warnf("error: line %d: affix %s is corrupt\n", af.getlinenum(), a.pHMgr.encodeFlag(aflag))
					return false
				}
				if ent != 0 {
					entry.aflag = first.aflag
				}
			case 2:
				np++
				entry.strip = piece
				if a.complexprefixes {
					if a.utf8 {
						entry.strip = warnReversewordUTF(a, entry.strip)
					} else {
						entry.strip = reverseword(entry.strip)
					}
				}
				if entry.strip == "0" {
					entry.strip = ""
				}
			case 3:
				entry.morphcode = ""
				entry.hasMorph = false
				entry.contclass = nil
				np++
				appnd := piece
				dash := strings.IndexByte(piece, '/')
				if dash >= 0 {
					appnd = piece[:dash]
				}
				if a.ignorechars != "" && !hasNoIgnoredChars(appnd, a.ignorechars) {
					if a.utf8 {
						appnd, _ = removeIgnoredCharsUTF(appnd, a.ignorecharsUTF16)
					} else {
						appnd = removeIgnoredChars(appnd, a.ignorechars)
					}
				}
				if a.complexprefixes {
					if a.utf8 {
						appnd = warnReversewordUTF(a, appnd)
					} else {
						appnd = reverseword(appnd)
					}
				}
				entry.appnd = appnd
				if dash >= 0 {
					dashStr := piece[dash+1:]
					if a.pHMgr.isAliasf() {
						index := atoi(dashStr)
						entry.contclass = a.pHMgr.getAliasf(index, af)
						if len(entry.contclass) == 0 {
							a.warnf("error: bad affix flag alias: \"%s\"\n", dashStr)
						}
					} else {
						entry.contclass = a.pHMgr.decodeFlags(dashStr, af)
						sortFlags(entry.contclass)
					}
					a.havecontclass = true
					for _, c := range entry.contclass {
						a.contclasses[c] = true
					}
				}
				if entry.appnd == "0" {
					entry.appnd = ""
				}
			case 4:
				chunk := piece
				np++
				if a.complexprefixes {
					if a.utf8 {
						chunk = warnReversewordUTF(a, chunk)
					} else {
						chunk = reverseword(chunk)
					}
					b := []byte(chunk)
					reverseCondition(b)
					chunk = string(b)
				}
				if entry.strip != "" && chunk != "." && a.redundantCondition(at, entry.strip, chunk, af.getlinenum()) {
					chunk = "."
					entry.opts |= aeRedundantCond
				}
				if at == 'S' {
					b := []byte(reverseword(chunk))
					reverseCondition(b)
					chunk = string(b)
				}
				if !a.encodeit(entry, chunk) {
					return false
				}
			case 5:
				chunk := piece
				np++
				if a.pHMgr.isAliasm() {
					index := atoi(chunk)
					entry.morphcode, entry.hasMorph = a.pHMgr.getAliasm(index)
				} else {
					if a.complexprefixes { // XXX - fix me for morph. gen.
						if a.utf8 {
							chunk = warnReversewordUTF(a, chunk)
						} else {
							chunk = reverseword(chunk)
						}
					}
					// add the remaining of the line
					chunk += nl[pos:]
					entry.morphcode = cstr(chunk)
					entry.hasMorph = true
				}
			}
			i++
		}
		if np < 4 {
			a.warnf("error: line %d: affix %s is corrupt\n", af.getlinenum(), a.pHMgr.encodeFlag(aflag))
			return false
		}
		entry.line = ruleline
		entry.headerline = headerline
		entry.xprod = xprod
	}

	// now create SfxEntry or PfxEntry objects and use links to build an
	// ordered (sorted by affix string) list
	for _, p := range pfxs {
		a.buildPfxtree(p)
	}
	for _, s := range sfxs {
		a.buildSfxtree(s)
	}
	return true
}

func (a *AffixMgr) redundantCondition(ft byte, strip, cond string, linenum int) bool {
	stripl, condl := len(strip), len(cond)
	if ft == 'P' { // prefix
		if strip[:min(condl, stripl)] == cond {
			return true
		}
		if !a.utf8 {
			i, j := 0, 0
			for ; i < stripl && j < condl; i, j = i+1, j+1 {
				if cond[j] != '[' {
					if cond[j] != strip[i] {
						a.warnf("warning: line %d: incompatible stripping characters and condition\n", linenum)
						return false
					}
				} else {
					neg := byteAt(cond, j+1) == '^'
					in := false
					for {
						j++
						if strip[i] == byteAt(cond, j) {
							in = true
						}
						if !(j < condl-1 && cond[j] != ']') {
							break
						}
					}
					if j == condl-1 && cond[j] != ']' {
						a.warnf("error: line %d: missing ] in condition:\n%s\n", linenum, cond)
						return false
					}
					if (!neg && !in) || (neg && in) {
						a.warnf("warning: line %d: incompatible stripping characters and condition\n", linenum)
						return false
					}
				}
			}
			if j >= condl {
				return true
			}
		}
	} else { // suffix
		if stripl >= condl && strip[stripl-condl:] == cond {
			return true
		}
		if !a.utf8 {
			i, j := stripl-1, condl-1
			for ; i >= 0 && j >= 0; i, j = i-1, j-1 {
				if cond[j] != ']' {
					if cond[j] != strip[i] {
						a.warnf("warning: line %d: incompatible stripping characters and condition\n", linenum)
						return false
					}
				} else if j > 0 {
					in := false
					for {
						j--
						if strip[i] == cond[j] {
							in = true
						}
						if !(j > 0 && cond[j] != '[') {
							break
						}
					}
					if j == 0 && cond[j] != '[' {
						a.warnf("error: line: %d: missing ] in condition:\n%s\n", linenum, cond)
						return false
					}
					neg := byteAt(cond, j+1) == '^'
					if (!neg && !in) || (neg && in) {
						a.warnf("warning: line %d: incompatible stripping characters and condition\n", linenum)
						return false
					}
				}
			}
			if j < 0 {
				return true
			}
		}
	}
	return false
}

// getSuffixWords returns the words the suffixes of suff make of rootWord.
func (a *AffixMgr) getSuffixWords(suff []uint16, rootWord string) []string {
	var slst []string
	for _, start := range a.sStart {
		for ptr := start; ptr != nil; ptr = ptr.next {
			for _, f := range suff {
				if f == ptr.aflag {
					nw := rootWord + ptr.appnd
					if ptr.checkword(nw, 0, len(nw), 0, nil, 0, 0, 0, nil) != nil {
						slst = append(slst, nw)
					}
				}
			}
		}
	}
	return slst
}

// warnf records a diagnostic of the debug builds of Hunspell
// (HUNSPELL_WARNING).
func (a *AffixMgr) warnf(format string, args ...any) {
	*a.warns = append(*a.warns, fmt.Sprintf(format, args...))
}
