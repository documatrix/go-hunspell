package core

import "strings"

// repList is a conversion table (ICONV and OCONV).
type repList struct {
	dat []*replentry
	// trie does the unanchored whole-string replacement while no anchored
	// entry has been added
	trie       *convTrie
	canUseTrie bool
}

func newRepList() *repList {
	return &repList{trie: newConvTrie(), canUseTrie: true}
}

// find returns the index of the longest pattern no longer than maxLen that
// is a prefix of word, or -1.
func (r *repList) find(word string, maxLen int) int {
	p1, p2 := 0, len(r.dat)-1
	ret := -1
	for p1 <= p2 {
		m := int(uint(p1+p2) >> 1)
		pattern := r.dat[m].pattern
		cmplen := len(pattern)
		if maxLen < cmplen {
			cmplen = maxLen
		}
		c := strncmp(word, pattern, cmplen)
		switch {
		case c < 0:
			p2 = m - 1
		case c > 0:
			p1 = m + 1
		case len(pattern) <= maxLen:
			ret = m
			p1 = m + 1
		default:
			p2 = m - 1
		}
	}
	return ret
}

// strncmp compares at most n bytes of the C strings a and b.
func strncmp(a, b string, n int) int {
	for i := 0; i < n; i++ {
		ca, cb := byteAt(a, i), byteAt(b, i)
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
		if ca == 0 {
			return 0
		}
	}
	return 0
}

// strcmp compares the C strings a and b.
func strcmp(a, b string) int {
	return strings.Compare(cstr(a), cstr(b))
}

func (r *repList) replace(wordlen int, ind int, atstart bool) string {
	typ := 0
	if atstart {
		typ = 1
	}
	if wordlen == len(r.dat[ind].pattern) {
		if atstart {
			typ = 3
		} else {
			typ = 2
		}
	}
	for typ != 0 && r.dat[ind].outstrings[typ] == "" {
		if typ == 2 && !atstart {
			typ = 0
		} else {
			typ--
		}
	}
	return r.dat[ind].outstrings[typ]
}

// add adds a conversion. (C++ refuses an empty pattern or replacement here;
// the parser of the table rejects those lines before.)
func (r *repList) add(inPat1, pat2 string) int {
	// analyse word context
	typ := 0
	pat1 := inPat1
	if pat1[0] == '_' {
		pat1 = pat1[1:]
		typ = 1
	}
	if pat1 != "" && pat1[len(pat1)-1] == '_' {
		typ += 2
		pat1 = pat1[:len(pat1)-1]
	}
	pat1 = mystrrep(pat1, "_", " ")

	if typ == 0 && r.canUseTrie {
		r.trie.add(pat1, mystrrep(pat2, "_", " "))
	} else {
		r.canUseTrie = false
	}

	// find existing entry
	if m := r.find(pat1, len(pat1)); m >= 0 && r.dat[m].pattern == pat1 {
		r.dat[m].outstrings[typ] = mystrrep(pat2, "_", " ")
		return 0
	}
	e := &replentry{pattern: pat1}
	e.outstrings[typ] = mystrrep(pat2, "_", " ")
	r.dat = append(r.dat, e)
	// sort to the right place in the list
	i := len(r.dat) - 1
	for ; i > 0; i-- {
		if strcmp(e.pattern, r.dat[i-1].pattern) < 0 {
			r.dat[i] = r.dat[i-1]
		} else {
			break
		}
	}
	r.dat[i] = e
	return 0
}

// conv converts word, stopping once the result is longer than maxlen (pass a
// negative maxlen for no limit). It reports whether anything was replaced.
func (r *repList) conv(word string, maxlen int) (string, bool) {
	if maxlen < 0 {
		maxlen = int(^uint(0) >> 1)
	}
	if r.canUseTrie && r.trie.ok {
		return r.trie.transcode(word, maxlen)
	}
	var dest strings.Builder
	wordlen := len(word)
	change := false
	for i := 0; i < wordlen && dest.Len() <= maxlen; i++ {
		n := -1
		var l string
		// Find the longest matching pattern whose replacement actually
		// applies, retrying with strictly shorter patterns.
		for maxLen := wordlen - i; maxLen > 0; {
			m := r.find(word[i:], maxLen)
			if m < 0 {
				break
			}
			l = r.replace(wordlen-i, m, i == 0)
			if l != "" {
				n = m
				break
			}
			plen := len(r.dat[m].pattern)
			if plen == 0 {
				break
			}
			maxLen = plen - 1
		}
		if n < 0 {
			dest.WriteByte(word[i])
			continue
		}
		dest.WriteString(l)
		if r.dat[n].pattern != "" {
			i += len(r.dat[n].pattern) - 1
		}
		change = true
	}
	return dest.String(), change
}

// convTrie does longest prefix match transcoding.
type convTrie struct {
	root  *trieNode
	first [256]bool // the bytes a key starts with
	ok    bool
}

type trieNode struct {
	keys     []byte // the bytes of the children, in the order of children
	children []*trieNode
	value    string
	has      bool
}

func (n *trieNode) child(c byte) *trieNode {
	for i, k := range n.keys {
		if k == c {
			return n.children[i]
		}
	}
	return nil
}

func newConvTrie() *convTrie {
	return &convTrie{root: &trieNode{}, ok: true}
}

func (t *convTrie) add(key, value string) {
	if !t.ok {
		return
	}
	if len(value) > 255 {
		t.ok = false
		return
	}
	if key != "" {
		t.first[key[0]] = true
	}
	n := t.root
	for i := 0; i < len(key); i++ {
		c := n.child(key[i])
		if c == nil {
			c = &trieNode{}
			n.keys = append(n.keys, key[i])
			n.children = append(n.children, c)
		}
		n = c
	}
	n.value = value
	n.has = true
}

// transcode replaces the longest matching keys of src, copying at most
// maxlen+1 bytes.
func (t *convTrie) transcode(src string, maxlen int) (string, bool) {
	if !t.ok || src == "" {
		return "", false
	}
	i := 0
	for i < len(src) && !t.first[src[i]] {
		i++
	}
	if i == len(src) {
		if maxlen < len(src)-1 {
			return src[:maxlen+1], false
		}
		return src, false
	}
	var dst strings.Builder
	dst.Grow(len(src) + 8)
	if i > maxlen { // the copy loop stops after maxlen+1 bytes
		return src[:maxlen+1], false
	}
	dst.WriteString(src[:i])
	substituted := false
	for i < len(src) && dst.Len() <= maxlen {
		var best *trieNode
		matchLen := 0
		if t.first[src[i]] {
			n := t.root
			for j := i; j < len(src); j++ {
				if n = n.child(src[j]); n == nil {
					break
				}
				if n.has {
					best = n
					matchLen = j + 1 - i
				}
			}
		}
		if best != nil {
			dst.WriteString(best.value)
			i += matchLen
			substituted = true
		} else {
			dst.WriteByte(src[i])
			i++
		}
	}
	return dst.String(), substituted
}
