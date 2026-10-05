package core

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The tests of this file check edge cases that the golden files cannot hold
// or reach: the results of the C++ code for them come from small programs
// built against its sources (apidump.cxx, or a call of the function itself),
// or they check the C library semantics that a helper of the port emulates.

// TestStemMorphLimit stems an analysis with alternatives over the 4 MB limit
// of a morphological result. The C++ library (Hunspell::stem with the same
// analysis) gives one stem: the stems of the first two alternatives are the
// same, and the third is past the limit.
func TestStemMorphLimit(t *testing.T) {
	h := extraDict(t, "misc")
	big := "st:" + strings.Repeat("s", 3<<20)
	got := h.StemMorph([]string{big + " | " + big + " | st:c"})
	if len(got) != 1 || got[0] != big[3:] {
		t.Errorf("StemMorph over the limit: %d stems", len(got))
	}
}

// TestPhonetLongWord transforms words up to and over the length limit of
// phonet. The C++ phonet gives 1024 letters for 1024 letters A with the rule
// A -> B, and nothing for 1025 of them.
func TestPhonetLongWord(t *testing.T) {
	p := &phonetable{rules: []string{"A", "B", "", ""}}
	initPhonetHash(p)
	if got := phonet(strings.Repeat("A", 1024), p); got != strings.Repeat("B", 1024) {
		t.Errorf("phonet of 1024 letters: %d letters", len(got))
	}
	if got := phonet(strings.Repeat("A", 1025), p); got != "" {
		t.Errorf("phonet of 1025 letters: %d letters", len(got))
	}
}

// TestDeepCompound analyzes compounds of up to 100 parts, the maximum of the
// compound checks. The C++ library (apidump) accepts and analyzes the word of
// 99 letters a and a b, and finds nothing for 101 letters a.
func TestDeepCompound(t *testing.T) {
	h := New(Source{Data: []byte("SET UTF-8\nCOMPOUNDFLAG C\nCOMPOUNDMIN 1\n")}, Source{Data: []byte("2\na/C\nb/C\n")}, "")
	h.SetTimeLimits(0, 0, 0)
	cases := []struct {
		word string
		ana  []string
	}{
		{"aaaaa", []string{strings.Repeat(" pa:a st:a", 4) + " pa:a"}},
		{strings.Repeat("a", 99) + "b", []string{strings.Repeat(" pa:a st:a", 99) + " pa:b"}},
		{strings.Repeat("a", 101), nil},
	}
	for _, c := range cases {
		if got := h.Analyze(c.word); !slices.Equal(got, c.ana) {
			t.Errorf("Analyze(%d letters) = %q, want %q", len(c.word), got, c.ana)
		}
		if got := h.Spell(c.word, nil, nil); got != (c.ana != nil) {
			t.Errorf("Spell(%d letters) = %v", len(c.word), got)
		}
	}
}

// TestFindPositions checks the emulations of std::string::find from a
// position: a position past the end finds nothing, like npos.
func TestFindPositions(t *testing.T) {
	for _, c := range []struct {
		s, sub string
		from   int
		want   int
	}{
		{"abcabc", "bc", 0, 1},
		{"abcabc", "bc", 2, 4},
		{"abcabc", "", 6, 6},
		{"abcabc", "", 7, -1},
		{"abcabc", "c", -1, -1},
	} {
		if got := strIndexFrom(c.s, c.sub, c.from); got != c.want {
			t.Errorf("strIndexFrom(%q, %q, %d) = %d, want %d", c.s, c.sub, c.from, got, c.want)
		}
		if len(c.sub) == 1 || c.from > len(c.s) {
			b := byte('x')
			if c.sub != "" {
				b = c.sub[0]
			}
			want := c.want
			if c.from > len(c.s) {
				want = -1
			}
			if c.from >= 0 {
				if got := indexFrom(c.s, b, c.from); got != want {
					t.Errorf("indexFrom(%q, %q, %d) = %d, want %d", c.s, b, c.from, got, want)
				}
			}
		}
	}
}

// TestCStringHelpers checks the helpers that emulate C strings: strncmp ends
// at a NUL in both strings, std::string::resize pads with NULs, and an empty
// string has no first character.
func TestCStringHelpers(t *testing.T) {
	for _, c := range []struct {
		a, b string
		n    int
		want int
	}{
		{"ab\x00x", "ab\x00y", 4, 0}, // equal C strings
		{"ab", "ab", 5, 0},           // the terminating NULs
		{"abc", "abd", 3, -1},
		{"abd", "abc", 2, 0},
		{"b", "a", 1, 1},
	} {
		if got := strncmp(c.a, c.b, c.n); got != c.want {
			t.Errorf("strncmp(%q, %q, %d) = %d, want %d", c.a, c.b, c.n, got, c.want)
		}
	}
	var buf []byte
	if got := scratchResized(&buf, "abc", 1, 4, "xy"); got != "bc\x00\x00xy" {
		t.Errorf("scratchResized = %q", got)
	}
	if got := scratchResized(&buf, "abc", 0, 2, ""); got != "ab" {
		t.Errorf("scratchResized = %q", got)
	}
	if c, n := FirstUTF16(""); c != 0 || n != 0 {
		t.Errorf("FirstUTF16 of an empty string = %d, %d", c, n)
	}
}

// TestNULLookup looks up a word with a NUL in a hash bucket that holds the
// word before the NUL. The C++ code hashes all the bytes but compares C
// strings, so it finds that word. This is a Go only case: the C++ API takes C
// strings.
func TestNULLookup(t *testing.T) {
	h := New(Source{Data: []byte("")}, Source{Data: []byte("3\nfoo\nbar\nbaz\n")}, "")
	m := h.hmgrs[0]
	found := false
	for i := 0; i < 1000000 && !found; i++ {
		w := "foo\x00" + strconv.Itoa(i)
		if m.hash(w) != m.hash("foo") {
			continue
		}
		found = true
		if e := m.lookup(w); e == nil || e.word != "foo" {
			t.Errorf("lookup(%q) = %v", w, e)
		}
	}
	if !found {
		t.Fatal("no word with a NUL in the bucket of foo")
	}
	// a NUL word that is not in that bucket is not found
	if e := m.lookup("qux\x00"); e != nil {
		t.Errorf("lookup of qux = %v", e)
	}
}

// TestNULSuggest asks for suggestions for a word that starts with a NUL,
// which the n-gram search reads as an empty C string: only empty guesses (from
// empty ph: fields) score above its threshold. A Go only case, without a
// panic.
func TestNULSuggest(t *testing.T) {
	for _, aff := range []string{"", "SET UTF-8\n"} {
		h := New(Source{Data: []byte(aff + "SFX S Y 1\nSFX S 0 s .\n")}, Source{Data: []byte("2\nabc ph:\nab ph:\n")}, "")
		for _, w := range []string{"\x00abc", "\x00"} {
			for _, s := range h.Suggest(w) {
				if s != "ab" && s != "abc" {
					t.Errorf("Suggest(%q): %q", w, s)
				}
			}
		}
	}
}

// hzEncode packs the byte stream of the prefix-suffix encoding of hzip (the
// output of prefixcompress in hzip.cxx) into a hz0 file: a code table and the
// coded byte pairs, with the terminal code last, like encode_file does. The
// codes are of a fixed length instead of the Huffman codes of hzip.
func hzEncode(stream []byte) []byte {
	index := map[[2]byte]int{}
	var syms [][2]byte
	var codes []int
	for i := 0; i+1 < len(stream); i += 2 {
		s := [2]byte{stream[i], stream[i+1]}
		if _, ok := index[s]; !ok {
			index[s] = len(syms)
			syms = append(syms, s)
		}
		codes = append(codes, index[s])
	}
	// the terminal code carries the odd last byte
	term := [2]byte{}
	if len(stream)%2 == 1 {
		term = [2]byte{1, stream[len(stream)-1]}
	}
	syms = append(syms, term)
	codes = append(codes, len(syms)-1)
	l := 1
	for 1<<l < len(syms) {
		l++
	}
	bits := func(dst []byte, pos, code int) []byte {
		for j := 0; j < l; j++ {
			if (pos+j)/8 >= len(dst) {
				dst = append(dst, 0)
			}
			if code>>(l-1-j)&1 == 1 {
				dst[(pos+j)/8] |= 1 << (7 - (pos+j)%8)
			}
		}
		return dst
	}
	out := []byte{'h', 'z', '0', byte(len(syms) >> 8), byte(len(syms))}
	for i, s := range syms {
		out = append(out, s[0], s[1], byte(l))
		out = append(out, bits(make([]byte, l/8+1), 0, i)...)
	}
	var data []byte
	for i, c := range codes {
		data = bits(data, i*l, c)
	}
	// encode_file writes bits/8+1 bytes: the decoder needs a bit past the
	// terminal code to see it end
	if n := len(codes) * l; n/8+1 > len(data) {
		data = append(data, 0)
	}
	return append(out, data...)
}

// hzLines decodes a hz0 file and returns its lines and whether it reads to
// its end.
func hzLines(t *testing.T, data []byte) []string {
	t.Helper()
	var errs []string
	h := newHunzip("x.hz", data, "", &errs)
	if len(errs) > 0 {
		t.Fatalf("errors %q", errs)
	}
	var lines []string
	for {
		l, ok := h.getline()
		if !ok {
			return lines
		}
		lines = append(lines, l)
	}
}

// TestHunzipEdges decodes hzip streams made to reach the less common paths of
// Hunzip::getline: escapes and suffix codes at the end of the 64 KB output
// buffer, and the corrupt suffix and prefix codes that end the file (getline
// returns false) or that point past the previous line (strcpy to line + left
// leaves the line at the NUL of the previous one). The hunzip tool of the C++
// library gives the same lines for these streams, followed by 80 KB of more
// lines (it loses the last block of a file).
func TestHunzipEdges(t *testing.T) {
	// 16383 lines "abc" end at byte 65532 of the stream
	filler := []byte(strings.Repeat("abc\x00", 16383))
	fillerLines := strings.Split(strings.Repeat("abc\n", 16383), "\n")
	fillerLines = fillerLines[:len(fillerLines)-1]
	for i := range fillerLines {
		fillerLines[i] += "\n"
	}
	cases := []struct {
		name   string
		stream []byte
		want   []string
	}{
		// the escaped "-" after the end of the buffer
		{"escape", append(slices.Clone(filler), "abc\x1f-\x00end\x00"...), append(slices.Clone(fillerLines), "abc-\n", "end\n")},
		// the prefix length after the end of the buffer: "xyz" and the last 3
		// characters of the previous line and its newline
		{"suffix", append(slices.Clone(filler), "xyz\x22\x00end\x00"...), append(slices.Clone(fillerLines), "xyzabc\n", "end\n")},
		// a suffix longer than the previous line
		{"long suffix", []byte("ab\x00x\x2e\x00cd\x00"), []string{"ab\n"}},
		// a line too long with its prefix
		{"long line", []byte("abc\x00" + strings.Repeat("a", 65400) + "\x21\xc8cd\x00"), []string{"abc\n"}},
		// a prefix longer than the previous line: the previous line again,
		// and the next lines take their prefixes from it
		{"long prefix", []byte("abcd\x00xy\x21\x10ef\x02gh\x00"), []string{"abcd\n", "abcd\n", "abef\n", "gh\n"}},
		// an escaped NUL ends the line like a C string
		{"nul", []byte("ab\x1f\x00cd\x00ef\x00"), []string{"ab", "ef\n"}},
	}
	for _, c := range cases {
		if got := hzLines(t, hzEncode(c.stream)); !slices.Equal(got, c.want) {
			if len(got) > 3 && len(c.want) > 3 {
				t.Errorf("%s: %d lines, ending %q, want %d, ending %q", c.name, len(got), got[len(got)-3:], len(c.want), c.want[len(c.want)-3:])
			} else {
				t.Errorf("%s: %q, want %q", c.name, got, c.want)
			}
		}
	}
}
