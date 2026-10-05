package gohunspell

// The expectations of these tests about Hunspell behavior were taken from
// the C++ Hunspell (upstream master): the hunspell tool (-a, -t, -H, -X, -n,
// -O, -1, --check-url, -p, -P, -D), its unmunch tool and a small program
// calling the Hunspell class (suggest, analyze, stem, generate,
// suffix_suggest, add, add_with_flags, add_with_affix, remove, input_conv,
// get_wordchars, get_version).

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// errReader fails every read.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failure") }

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	aff := filepath.Join(testdata, "base_utf.aff")
	dic := filepath.Join(testdata, "base_utf.dic")
	if _, err := Open(filepath.Join(dir, "none.aff"), dic); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open with a missing affix file: %v", err)
	}
	if _, err := Open(aff, filepath.Join(dir, "none.dic")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open with a missing dictionary file: %v", err)
	}
	if _, err := Open("", dic); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open without an affix file: %v", err)
	}
	if _, err := OpenReader(errReader{}, strings.NewReader("1\nx\n")); err == nil {
		t.Error("OpenReader with a failing affix reader succeeded")
	}
	if _, err := OpenReader(strings.NewReader(""), errReader{}); err == nil {
		t.Error("OpenReader with a failing dictionary reader succeeded")
	}
	// an empty dictionary file and one without a word count are not
	// dictionaries
	var le *LoadError
	_, err := OpenBytes([]byte("SET UTF-8\n"), nil)
	if !errors.As(err, &le) {
		t.Fatalf("OpenBytes with an empty dictionary: %v", err)
	}
	if want := []string{"error: empty dic file <data>", "Hash Manager Error : 2"}; !reflect.DeepEqual(le.Messages, want) {
		t.Errorf("LoadError.Messages = %q, want %q", le.Messages, want)
	}
	_, err = OpenBytes([]byte("SET UTF-8\n"), []byte("x\nfoo\n"))
	want := "gohunspell: error: <data>: line 1: missing or bad word count in the dic file; Hash Manager Error : 4"
	if err == nil || err.Error() != want {
		t.Errorf("OpenBytes with a bad word count: %v, want %s", err, want)
	}
}

func TestDiagnostics(t *testing.T) {
	// a broken affix rule fails the affix file, but the dictionary works
	dir := t.TempDir()
	aff := writeFile(t, filepath.Join(dir, "bad.aff"), "SET UTF-8\nSFX S Y 2\nSFX S 0 s .\n")
	dic := writeFile(t, filepath.Join(dir, "bad.dic"), "2\nword/S\nfoo\n")
	d, err := Open(aff, dic)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := d.Diagnostics(), []string{"Failure loading aff file " + aff}; !reflect.DeepEqual(got, want) {
		t.Errorf("Diagnostics = %q, want %q", got, want)
	}
	if !d.Spell("word") || !d.Spell("foo") || d.Spell("words") {
		t.Error("the words of a dictionary with a broken affix file")
	}
	// the returned slice is a copy
	d.Diagnostics()[0] = "changed"
	if d.Diagnostics()[0] == "changed" {
		t.Error("Diagnostics returns the internal slice")
	}
	if got := open(t, "base_utf").Diagnostics(); len(got) != 0 {
		t.Errorf("Diagnostics of a good dictionary = %q", got)
	}
	// the warnings of the C++ debug builds (HUNSPELL_WARNING_ON) follow the
	// errors
	tests := []struct {
		aff  string
		want []string
	}{
		{"SET UTF-8\nTRY\n", []string{"Failure loading aff file <data>", "error: line 2: missing data"}},
		// once for each character table Hunspell sets up
		{"SET X-UNKNOWN\n", slices.Repeat([]string{"error: unknown encoding X-UNKNOWN: using iso88591 as fallback"}, 4)},
	}
	for _, tt := range tests {
		d, err := OpenBytes([]byte(tt.aff), []byte("1\nfoo\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got := d.Diagnostics(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Diagnostics of %q = %q, want %q", tt.aff, got, tt.want)
		}
		if !d.Spell("foo") {
			t.Errorf("a warning of %q failed the dictionary", tt.aff)
		}
	}
	// the warnings of an extra dictionary
	d, err = OpenBytes([]byte("SET UTF-8\nFLAG num\n"), []byte("1\nfoo\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddDictionaryReader(strings.NewReader("1\nbar/abc\n")); err != nil {
		t.Fatal(err)
	}
	if got, want := d.Diagnostics(), []string{"error: line 2: 0 is wrong flag id"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Diagnostics after AddDictionaryReader = %q, want %q", got, want)
	}
}

func TestHzipDictionaries(t *testing.T) {
	plain := open(t, "base_utf")
	hz := filepath.Join("testdata", "hz")
	same := func(t *testing.T, d *Dictionary) {
		t.Helper()
		if got, want := slices.Sorted(d.Words()), slices.Sorted(plain.Words()); !reflect.DeepEqual(got, want) {
			t.Errorf("words of the hzip dictionary = %q, want %q", got, want)
		}
		for _, w := range []string{"looked", "created", "won’t", "İzmir"} {
			if !d.Spell(w) {
				t.Errorf("Spell(%q) = false", w)
			}
		}
		if got := d.Suggest("loooked"); !reflect.DeepEqual(got, plain.Suggest("loooked")) {
			t.Errorf("Suggest(loooked) = %q", got)
		}
	}
	// Open finds name.dic.hz for name.dic
	d, err := Open(filepath.Join(hz, "plain.aff"), filepath.Join(hz, "plain.dic"))
	if err != nil {
		t.Fatal(err)
	}
	same(t, d)
	// an encrypted dictionary needs its password
	d, err = Open(filepath.Join(hz, "secret.aff"), filepath.Join(hz, "secret.dic"), WithKey("secret"))
	if err != nil {
		t.Fatal(err)
	}
	same(t, d)
	secretHz := filepath.Join(hz, "secret.dic.hz")
	for _, key := range []string{"", "wrong"} {
		_, err := Open(filepath.Join(hz, "secret.aff"), filepath.Join(hz, "secret.dic"), WithKey(key))
		var le *LoadError
		if !errors.As(err, &le) {
			t.Fatalf("Open with the password %q: %v", key, err)
		}
		want := []string{
			"error: " + secretHz + ": missing or bad password",
			"error: empty dic file " + filepath.Join(hz, "secret.dic"),
			"Hash Manager Error : 2",
		}
		if !reflect.DeepEqual(le.Messages, want) {
			t.Errorf("Open with the password %q: %q, want %q", key, le.Messages, want)
		}
	}
	// OpenReader and OpenBytes take hzip data, too
	aff := readFile(t, filepath.Join(hz, "plain.aff"))
	d, err = OpenReader(bytes.NewReader(aff), bytes.NewReader(readFile(t, filepath.Join(hz, "plain.dic.hz"))))
	if err != nil {
		t.Fatal(err)
	}
	same(t, d)
	d, err = OpenBytes(aff, readFile(t, secretHz), WithKey("secret"))
	if err != nil {
		t.Fatal(err)
	}
	same(t, d)
	if _, err := OpenBytes(aff, readFile(t, secretHz), WithKey("wrong")); err == nil {
		t.Error("OpenBytes with a wrong password succeeded")
	}
	// AddDictionary reads extra hzip dictionaries with the password of the
	// dictionary
	d = open(t, "base_utf")
	if err := d.AddDictionary(filepath.Join(hz, "plain.dic")); err != nil {
		t.Fatal(err)
	}
	if !d.Spell("looked") {
		t.Error("AddDictionary of an hzip file")
	}
}

func TestOpenFS(t *testing.T) {
	aff := readFile(t, filepath.Join(testdata, "base_utf.aff"))
	dic := readFile(t, filepath.Join(testdata, "base_utf.dic"))
	fsys := fstest.MapFS{
		"dict/en.aff":         {Data: aff},
		"dict/en.dic":         {Data: dic},
		"hz/en.aff":           {Data: aff},
		"hz/en.dic.hz":        {Data: readFile(t, filepath.Join("testdata", "hz", "secret.dic.hz"))},
		"dir/en.aff":          {Data: aff},
		"dir/en.dic/file.txt": {Data: []byte("x")},
	}
	d, err := OpenFS(fsys, "dict/en.aff", "dict/en.dic")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Spell("looked") || d.Spell("loooked") {
		t.Error("dictionary of OpenFS")
	}
	d, err = OpenFS(fsys, "hz/en.aff", "hz/en.dic", WithKey("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Spell("looked") {
		t.Error("hzip dictionary of OpenFS")
	}
	if _, err := OpenFS(fsys, "hz/en.aff", "hz/en.dic"); err == nil {
		t.Error("OpenFS of an encrypted dictionary without password succeeded")
	}
	if _, err := OpenFS(fsys, "dict/none.aff", "dict/en.dic"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenFS with a missing affix file: %v", err)
	}
	if _, err := OpenFS(fsys, "dict/en.aff", "dict/none.dic"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenFS with a missing dictionary file: %v", err)
	}
	if _, err := OpenFS(fsys, "dir/en.aff", "dir/en.dic"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenFS with a folder as dictionary file: %v", err)
	}
}

func TestAddDictionaryErrors(t *testing.T) {
	d := open(t, "base_utf")
	dir := t.TempDir()
	if err := d.AddDictionary(filepath.Join(dir, "none.dic")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("AddDictionary of a missing file: %v", err)
	}
	if err := d.AddDictionaryReader(errReader{}); err == nil {
		t.Error("AddDictionaryReader of a failing reader succeeded")
	}
	err := d.AddDictionaryReader(strings.NewReader("many\nfoo\n"))
	var le *LoadError
	if !errors.As(err, &le) || len(le.Messages) != 2 || le.Messages[1] != "Hash Manager Error : 4" {
		t.Errorf("AddDictionaryReader of a bad word count: %v", err)
	}
	if got := d.Diagnostics(); len(got) != 2 {
		t.Errorf("Diagnostics after a failed AddDictionaryReader = %q", got)
	}
	// AddDictionary of a file with the affix flags of the dictionary
	extra := writeFile(t, filepath.Join(dir, "extra.dic"), "1\ngopher/S\n")
	if err := d.AddDictionary(extra); err != nil {
		t.Fatal(err)
	}
	if !d.Spell("gophers") || !d.HasWord("gopher") {
		t.Error("AddDictionary words")
	}
}

func TestDictionaryInfo(t *testing.T) {
	d := open(t, "base_utf")
	if d.Encoding() != "UTF-8" || d.Version() != "" || d.Lang() != "" {
		t.Errorf("Encoding, Version, Lang = %q, %q, %q", d.Encoding(), d.Version(), d.Lang())
	}
	if got := d.WordChars(); got != ".'’" {
		t.Errorf("WordChars = %q", got)
	}
	// as written, also outside the Basic Multilingual Plane
	d, err := OpenBytes([]byte("SET UTF-8\nWORDCHARS 😀-.\n"), []byte("1\nfoo\n"))
	if err != nil || d.WordChars() != "😀-." {
		t.Errorf("WordChars = %q, %v", d.WordChars(), err)
	}
	d, err = OpenBytes([]byte("SET UTF-8\nVERSION 1.2-test\nLANG de_DE\nWORDCHARS 0123456789-\n"), []byte("1\nfoo\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Version() != "1.2-test" || d.Lang() != "de_DE" || d.WordChars() != "0123456789-" {
		t.Errorf("Version, Lang, WordChars = %q, %q, %q", d.Version(), d.Lang(), d.WordChars())
	}
	// the word characters of an 8-bit dictionary hold its letters
	d = open(t, "i54633")
	want := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz" +
		"ÀÁÂÃÄÅÆÇÈÉÊËÌÍÎÏÐÑÒÓÔÕÖØÙÚÛÜÝÞàáâãäåæçèéêëìíîïðñòóôõöøùúûüýþ"
	if got := d.WordChars(); got != want {
		t.Errorf("WordChars of ISO8859-1 = %q, want %q", got, want)
	}
	if got := open(t, "base").WordChars(); got != ".'"+want {
		t.Errorf("WordChars of base = %q", got)
	}
}

func TestMorphologyAPI(t *testing.T) {
	d := open(t, "morph")
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{"GenerateMorph(drink, is:past_1)", d.GenerateMorph("drink", []string{"is:past_1"}), []string{"drank", "drank"}},
		{"GenerateMorph(drink, is:past_1, is:past_2)", d.GenerateMorph("drink", []string{"is:past_1", "is:past_2"}),
			[]string{"drank", "drank", "drunk", "drunk"}},
		{"GenerateMorph(eat, analysis)", d.GenerateMorph("eat", []string{"po:verb st:eat is:past_2"}), []string{"eaten"}},
		{"StemAnalyses(drunk)", d.StemAnalyses(d.Analyze("drunk")), []string{"drink"}},
		{"SuffixSuggest(drink)", d.SuffixSuggest("drink"), []string{"drinks"}},
		{"Generate(phenomenon, phenomena)", d.Generate("phenomenon", "phenomena"), []string{"phenomena"}},
		{"Analyze(drinks)", d.Analyze("drinks"), []string{
			" st:drink po:verb\tal:drank\tal:drunk\tts:present is:sg_3",
			" st:drink po:noun is:plur",
		}},
		{"Analyze(xyz)", d.Analyze("xyz"), nil},
		{"Stem(xyz)", d.Stem("xyz"), nil},
	}
	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
	b := open(t, "base_utf")
	if got, want := b.SuffixSuggest("look"), []string{"looked", "looking", "looker", "looks", "lookers"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SuffixSuggest(look) = %q, want %q", got, want)
	}
	if got := b.SuffixSuggest("nope"); got != nil {
		t.Errorf("SuffixSuggest(nope) = %q", got)
	}
	// the stems and analyses of an 8-bit dictionary come out as UTF-8
	e := open(t, "i54633")
	if got := e.Stem("éditer"); !reflect.DeepEqual(got, []string{"éditer"}) {
		t.Errorf("Stem(éditer) = %q", got)
	}
	if got := e.Analyze("Éditer"); !reflect.DeepEqual(got, []string{" st:éditer"}) {
		t.Errorf("Analyze(Éditer) = %q", got)
	}
}

func TestExpand(t *testing.T) {
	// the forms of the unmunch tool
	d := open(t, "base_utf")
	tests := map[string][]string{
		"look": {"look", "looked", "looker", "lookers", "looking", "looks"},
		"create": {"create", "created", "creates", "creating", "creation", "creations", "creative",
			"procreate", "procreated", "procreates", "procreating", "procreation", "procreations",
			"recreate", "recreated", "recreates", "recreating", "recreation", "recreations"},
		"text": {"text"},
		"nope": nil,
	}
	for w, want := range tests {
		got := d.Expand(w)
		slices.Sort(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Expand(%q) = %q, want %q", w, got, want)
		}
	}
}

func TestRuntimeChanges(t *testing.T) {
	d := open(t, "base_utf")
	if d.Spell("tweets") {
		t.Fatal("tweets is a word")
	}
	if err := d.AddWithFlags("tweet", "GZRDS", ""); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"tweet", "tweets", "tweeting"} {
		if r := d.Check(w); !r.Correct || (w != "tweet" && r.Root != "tweet") {
			t.Errorf("Check(%q) = %+v", w, r)
		}
	}
	// like in Hunspell, the D and R flags do not work on the added word
	if d.Spell("tweeted") || d.Spell("tweeter") {
		t.Error("tweeted or tweeter accepted")
	}
	if got, want := d.Suggest("tweetts"), []string{"tweets", "tweet"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Suggest(tweetts) = %q, want %q", got, want)
	}
	// a morphological description
	if err := d.AddWithFlags("gopher", "S", "po:noun"); err != nil {
		t.Fatal(err)
	}
	if got := d.Analyze("gophers"); len(got) != 1 || !strings.Contains(got[0], "po:noun") {
		t.Errorf("Analyze(gophers) = %q", got)
	}
	// AddWithAffix needs an example with affixes
	if err := d.AddWithAffix("blog", "nope"); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("AddWithAffix(blog, nope) = %v", err)
	}
	if err := d.AddWithAffix("blog", "text"); err == nil {
		t.Error("AddWithAffix with an example without affixes succeeded")
	}
	if d.Spell("blog") {
		t.Error("a failed AddWithAffix added the word")
	}
	if err := d.AddWithAffix("blog", "look"); err != nil || !d.Spell("blogs") {
		t.Errorf("AddWithAffix(blog, look) = %v", err)
	}
	// a removed word is forbidden, also with a capital
	if err := d.Remove("day"); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"day", "Day"} {
		if r := d.Check(w); r.Correct || !r.Forbidden {
			t.Errorf("Check(%q) after Remove = %+v", w, r)
		}
	}
	if d.HasWord("day") {
		t.Error("HasWord of a removed word")
	}
	// a word added with a capital is accepted in upper case, not in lower
	// case
	if err := d.Add("Gopherland"); err != nil {
		t.Fatal(err)
	}
	if !d.Spell("Gopherland") || !d.Spell("GOPHERLAND") || d.Spell("gopherland") {
		t.Error("case variants of an added word")
	}
}

func TestUnencodableWords(t *testing.T) {
	// ė has no form in ISO8859-1: such words are misspelled and have no
	// suggestions, analyses or forms, and cannot be added
	d := open(t, "i54633")
	const w = "ėditer"
	if d.Spell(w) || d.Check(w) != (Result{}) || d.HasWord(w) || d.Trace(w, func(int, string) { t.Error("trace record") }) {
		t.Error("Spell, Check, HasWord or Trace of an unencodable word")
	}
	lists := map[string][]string{
		"Suggest":                d.Suggest(w),
		"Analyze":                d.Analyze(w),
		"Stem":                   d.Stem(w),
		"StemAnalyses":           d.StemAnalyses([]string{" st:" + w}),
		"Generate(word)":         d.Generate(w, "éditer"),
		"Generate(example)":      d.Generate("éditer", w),
		"GenerateMorph(word)":    d.GenerateMorph(w, []string{"is:x"}),
		"GenerateMorph(morph)":   d.GenerateMorph("éditer", []string{"is:" + w}),
		"SuffixSuggest":          d.SuffixSuggest(w),
		"Expand":                 d.Expand(w),
		"Suggest(encodable)":     d.Suggest("xyzzyq"),
		"StemAnalyses(no stems)": d.StemAnalyses(nil),
	}
	for name, got := range lists {
		if got != nil {
			t.Errorf("%s = %q", name, got)
		}
	}
	for name, err := range map[string]error{
		"Add":                  d.Add(w),
		"AddWithFlags(word)":   d.AddWithFlags(w, "", ""),
		"AddWithFlags(flags)":  d.AddWithFlags("abc", w, ""),
		"AddWithFlags(morph)":  d.AddWithFlags("abc", "", "po:"+w),
		"AddWithAffix(word)":   d.AddWithAffix(w, "éditer"),
		"AddWithAffix(sample)": d.AddWithAffix("abc", w),
		"Remove":               d.Remove(w),
	} {
		if err == nil {
			t.Errorf("%s of an unencodable word succeeded", name)
		}
	}
	if d.Spell("abc") {
		t.Error("a failed AddWithFlags added the word")
	}
	if got := d.InputConversion(w); got != w {
		t.Errorf("InputConversion(%q) = %q", w, got)
	}
	// encodable words work through the conversion
	if err := d.Add("ça"); err != nil || !d.Spell("ça") || !d.HasWord("ça") {
		t.Errorf("Add(ça) = %v", err)
	}
	if err := d.Remove("ça"); err != nil || d.Spell("ça") {
		t.Errorf("Remove(ça) = %v", err)
	}
}

func TestInputConversion(t *testing.T) {
	d := open(t, "iconv")
	if got := d.InputConversion("şţ"); got != "șț" {
		t.Errorf("InputConversion(şţ) = %q", got)
	}
	if got := d.InputConversion("abc"); got != "abc" {
		t.Errorf("InputConversion(abc) = %q", got)
	}
	if !d.Spell("Chişinău") {
		t.Error("Spell does not apply the input conversion")
	}
	if got := d.Suggest("Chişinau"); !reflect.DeepEqual(got, []string{"Chișinău"}) {
		t.Errorf("Suggest(Chişinau) = %q", got)
	}
}

func TestEmbeddedNUL(t *testing.T) {
	// like std::string, the words may hold NUL bytes
	d := open(t, "base_utf")
	if got, want := d.Suggest("look\x00ed"), []string{"looked", "look"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Suggest(look\\x00ed) = %q, want %q", got, want)
	}
	if d.Spell("looked\x00x") || d.Stem("looked\x00x") != nil {
		t.Error("a word with a NUL byte is a word")
	}
	// the results are C strings, as the C API of Hunspell returns them
	if got := open(t, "iconv").InputConversion("şţ\x00ş"); got != "șț" {
		t.Errorf("InputConversion(şţ\\x00ş) = %q", got)
	}
	// io.ReadAll gives an empty file as a slice that is not nil, and so
	// must nonNil, as a nil Data names a file
	if b := nonNil(nil); b == nil || len(b) != 0 {
		t.Errorf("nonNil(nil) = %#v", b)
	}
}

func TestWords(t *testing.T) {
	d := open(t, "base_utf")
	words := slices.Collect(d.Words())
	// text is twice in base_utf.dic, but one entry
	if len(words) != 28 || d.WordCount() != 28 {
		t.Errorf("Words: %d, WordCount: %d, want 28", len(words), d.WordCount())
	}
	for _, w := range words {
		if !d.HasWord(w) {
			t.Errorf("HasWord(%q) = false", w)
		}
	}
	if !slices.Contains(words, "İzmir") || !slices.Contains(words, "can’t") {
		t.Errorf("Words = %q", words)
	}
	// an early stop
	n := 0
	for range d.Words() {
		n++
		if n == 3 {
			break
		}
	}
	if n != 3 {
		t.Errorf("early stop after %d words", n)
	}
	// HasWord does not take affixed forms or case variants
	for _, w := range []string{"looked", "LOOK", "Look", "nope"} {
		if d.HasWord(w) {
			t.Errorf("HasWord(%q) = true", w)
		}
	}
	// the words of an 8-bit dictionary are UTF-8
	if got := slices.Collect(open(t, "i54633").Words()); !reflect.DeepEqual(got, []string{"éditer"}) {
		t.Errorf("Words of i54633 = %q", got)
	}
}

func TestConcurrency(t *testing.T) {
	// without the time limits, the suggestions do not depend on the load
	// of the machine
	d, err := Open(filepath.Join(testdata, "base_utf.aff"), filepath.Join(testdata, "base_utf.dic"), WithoutTimeLimits())
	if err != nil {
		t.Fatal(err)
	}
	c := NewChecker(d, &WordList{})
	want := d.Suggest("loooked")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				switch (i + j) % 6 {
				case 0:
					if !d.Spell("looked") || d.Spell("loooked") {
						t.Error("concurrent Spell")
					}
				case 1:
					if got := d.Suggest("loooked"); !reflect.DeepEqual(got, want) {
						t.Errorf("concurrent Suggest = %q", got)
					}
				case 2:
					if len(d.CheckText("hello wrld", WithSuggestions())) != 1 {
						t.Error("concurrent CheckText")
					}
				case 3:
					if d.WordCount() < 27 || len(slices.Collect(d.Words())) < 27 {
						t.Error("concurrent Words")
					}
				case 4:
					if !c.Spell("created") || len(c.CheckText("texxt")) != 1 {
						t.Error("concurrent Checker")
					}
				case 5:
					_ = d.Add("concurrent")
					_ = d.Stem("looked")
					_ = d.Trace("looked", func(int, string) {})
				}
			}
		}(i)
	}
	wg.Wait()
	if !d.Spell("concurrent") {
		t.Error("concurrent Add")
	}
}

func TestWithoutTimeLimits(t *testing.T) {
	d, err := Open(filepath.Join(testdata, "base_utf.aff"), filepath.Join(testdata, "base_utf.dic"), WithoutTimeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := d.Suggest("loooked"), open(t, "base_utf").Suggest("loooked"); !reflect.DeepEqual(got, want) {
		t.Errorf("Suggest without time limits = %q, want %q", got, want)
	}
}

func TestSearchPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("home", home) // Plan 9
	a, b, x, y := filepath.Join(home, "a"), filepath.Join(home, "b"), filepath.Join(home, "x"), filepath.Join(home, "y")
	sep := string(filepath.ListSeparator)
	libre := t.TempDir()
	for _, d := range []string{"dict-fr", "dict-de", "other"} {
		if err := os.Mkdir(filepath.Join(libre, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(libre, "dict-file"), "not a folder")
	saved := libreOfficeDirs
	libreOfficeDirs = []string{libre, filepath.Join(libre, "missing")}
	t.Cleanup(func() { libreOfficeDirs = saved })

	// like the hunspell tool (-D): empty and repeated entries are left out,
	// a relative XDG_DATA_HOME means its default and relative XDG_DATA_DIRS
	// entries are invalid
	t.Setenv("DICPATH", a+sep+sep+b+sep+a)
	t.Setenv("XDG_DATA_HOME", "relative")
	t.Setenv("XDG_DATA_DIRS", x+sep+"relative"+sep+y)
	got := SearchPaths()
	want := []string{a, b, filepath.Join(home, ".local", "share") + "/hunspell", x + "/hunspell", y + "/hunspell",
		"/usr/share/hunspell", "/usr/share/myspell", "/usr/share/myspell/dicts", "/Library/Spelling",
		filepath.Join(home, "Library", "Spelling"), filepath.Join(libre, "dict-de"), filepath.Join(libre, "dict-fr")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchPaths =\n%q\nwant\n%q", got, want)
	}

	// an absolute XDG_DATA_HOME and the default XDG_DATA_DIRS
	t.Setenv("DICPATH", "")
	t.Setenv("XDG_DATA_HOME", x)
	t.Setenv("XDG_DATA_DIRS", "")
	got = SearchPaths()
	want = []string{x + "/hunspell"}
	if filepath.IsAbs("/usr/local/share") {
		want = append(want, "/usr/local/share/hunspell", "/usr/share/hunspell")
	} else {
		// Windows: the Unix default folders are relative
		want = append(want, "/usr/share/hunspell")
	}
	want = append(want, "/usr/share/myspell", "/usr/share/myspell/dicts", "/Library/Spelling")
	if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
		t.Errorf("SearchPaths =\n%q\nwant it to start with\n%q", got, want)
	}
}

func TestFindLoadAvailable(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"en.aff", "en.dic", "de.aff", "fr.dic", "hyph_en.aff", "hyph_en.dic", "x.txt", ".dic", ".aff"} {
		writeFile(t, filepath.Join(dir, f), "1\nfoo\n")
	}
	writeFile(t, filepath.Join(dir, "sub.dic", "x"), "")
	writeFile(t, filepath.Join(dir, "sub.aff"), "")
	hz := filepath.Join("testdata", "hz")
	if got, want := Available(dir, hz, filepath.Join(dir, "missing"), dir), []string{"en", "plain", "secret"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Available = %q, want %q", got, want)
	}
	for _, name := range []string{"en", "en.aff", "en.dic"} {
		aff, dic, err := Find(name, filepath.Join(dir, "missing"), dir)
		if err != nil || aff != filepath.Join(dir, "en.aff") || dic != filepath.Join(dir, "en.dic") {
			t.Errorf("Find(%q) = %q, %q, %v", name, aff, dic, err)
		}
	}
	// a name with a folder is tried as it is first
	aff, dic, err := Find(filepath.Join(hz, "plain"), dir)
	if err != nil || aff != filepath.Join(hz, "plain.aff") || dic != filepath.Join(hz, "plain.dic") {
		t.Errorf("Find of an hzip dictionary = %q, %q, %v", aff, dic, err)
	}
	// an absolute name is not searched in the folders
	if _, _, err := Find(filepath.Join(dir, "missing", "en"), dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Find of a missing absolute name: %v", err)
	}
	for _, name := range []string{"de", "fr", "hyph_none"} {
		if _, _, err := Find(name, dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Find(%q): %v", name, err)
		}
	}
	d, err := Load("plain", dir, hz)
	if err != nil || !d.Spell("looked") {
		t.Errorf("Load(plain) = %v", err)
	}
	// without folders, Find, Load and Available search SearchPaths
	t.Setenv("DICPATH", hz)
	if names := Available(); !slices.Contains(names, "plain") {
		t.Errorf("Available() = %q", names)
	}
	if _, err := Load("plain"); err != nil {
		t.Errorf("Load(plain) with DICPATH: %v", err)
	}
	if _, err := Load("secret"); err == nil {
		t.Error("Load of an encrypted dictionary succeeded")
	}
}

func TestTokenize(t *testing.T) {
	d := open(t, "base_utf")
	type tok struct {
		word   string
		column int
	}
	tests := []struct {
		name string
		text string
		opts []TextOption
		want []tok
	}{
		{"text", "see http://example.com/wrld and foo@bar.com /usr/wrld wrld", nil,
			[]tok{{"see", 0}, {"and", 28}, {"wrld", 54}}},
		{"URL checking", "see http://example.com/wrld and foo@bar.com /usr/wrld wrld", []TextOption{WithURLChecking()},
			[]tok{{"see", 0}, {"http://example.com/wrld", 4}, {"and", 28}, {"foo@bar.com", 32}, {"usr/wrld", 45}, {"wrld", 54}}},
		{"LaTeX", `\section{Texxt} said $x+y$ hello \emph{wrld}`, []TextOption{WithFormat(LaTeX)},
			[]tok{{"Texxt", 9}, {"said", 16}, {"hello", 27}, {"wrld", 39}}},
		{"HTML", `<p title="Texxt">said <b>wrld</b> <code>xyzzy</code> &apos;tomorrow</p>`, []TextOption{WithFormat(HTML)},
			[]tok{{"said", 17}, {"wrld", 25}, {"tomorrow", 59}}},
		{"XML", `<doc a="xx"><p>hello wrld</p><!-- cmnt --></doc>`, []TextOption{WithFormat(XML)},
			[]tok{{"hello", 15}, {"wrld", 21}}},
		{"Man", ".TH FOO 1\n.SH NAME\nhello wrld \\fBbold\\fR", []TextOption{WithFormat(Man)},
			[]tok{{"FOO", 4}, {"NAME", 4}, {"hello", 0}, {"wrld", 6}, {"bold", 14}}},
		{"ODF", `<office:text><text:p>hello wrld</text:p><text:p>sawyer<text:span>texxt</text:span></text:p></office:text>`,
			[]TextOption{WithFormat(ODF)}, []tok{{"hello", 21}, {"wrld", 27}, {"sawyertexxt", 48}}},
		{"first field", "wrld\tfoo bar\nhello\tx\nno tab", []TextOption{WithFormat(FirstField)},
			[]tok{{"wrld", 0}, {"hello", 0}}},
		{"UTF-8", "Ünïcödé wörd\n  zweite Zeile hier", nil,
			[]tok{{"Ünïcödé", 0}, {"wörd", 8}, {"zweite", 2}, {"Zeile", 9}, {"hier", 15}}},
	}
	for _, tt := range tests {
		var got []tok
		for _, tk := range d.Tokenize(tt.text, tt.opts...) {
			got = append(got, tok{tk.Word, tk.Column})
			lines := strings.Split(tt.text, "\n")
			if tk.Line < 1 || tk.Line > len(lines) {
				t.Errorf("%s: %q on line %d", tt.name, tk.Word, tk.Line)
				continue
			}
			// the offset is the byte offset of the word in the text
			start := len(strings.Join(lines[:tk.Line-1], "\n"))
			if tk.Line > 1 {
				start++
			}
			col := len([]rune(lines[tk.Line-1][:tk.Offset-start]))
			if col != tk.Column {
				t.Errorf("%s: %q at offset %d, column %d", tt.name, tk.Word, tk.Offset, tk.Column)
			}
			if tt.name != "ODF" && !strings.HasPrefix(tt.text[tk.Offset:], tk.Word) {
				t.Errorf("%s: %q is not at offset %d", tt.name, tk.Word, tk.Offset)
			}
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: Tokenize = %v, want %v", tt.name, got, tt.want)
		}
	}
	if got := d.Tokenize(""); got != nil {
		t.Errorf("Tokenize of an empty text = %v", got)
	}
}

func TestCheckTextApostrophes(t *testing.T) {
	// a UTF-8 dictionary with typographic apostrophes: ASCII apostrophes are
	// wrong, &apos; is not a word character of the plain text
	u := open(t, "base_utf")
	text := "can’t won't can&apos;t doesn't won’t"
	var got []string
	for _, m := range u.CheckText(text) {
		got = append(got, m.Word)
	}
	if want := []string{"won't", "can", "apos", "t", "doesn't"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CheckText = %q, want %q", got, want)
	}
	// &apos; of a markup text is an apostrophe
	b := open(t, "base")
	if m := b.CheckText("<p>can&apos;t won&apos;t xx&apos;t</p>", WithFormat(HTML)); len(m) != 1 || m[0].Word != "xx&apos;t" {
		t.Errorf("CheckText of HTML apostrophes = %+v", m)
	}
	// a UTF-8 dictionary with ASCII apostrophes takes typographic ones
	d, err := OpenBytes([]byte("SET UTF-8\nWORDCHARS '’\n"), []byte("1\nl'eau\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m := d.CheckText("l'eau l’eau l’oie"); len(m) != 1 || m[0].Word != "l’oie" {
		t.Errorf("CheckText with ASCII apostrophes = %+v", m)
	}
	// 8-bit dictionaries need ASCII apostrophes; unencodable words are
	// misspelled. (The hunspell tool means to do the same, but converts the
	// word to the dictionary encoding first, where iconv stops at the
	// typographic apostrophe or at ė: it reports can’t as misspelled and
	// accepts ėditer, checked as the empty word.)
	if m := b.CheckText("can’t won’t ėditer", WithSuggestions()); len(m) != 1 || m[0].Word != "ėditer" || m[0].Suggestions != nil {
		t.Errorf("CheckText of an 8-bit dictionary = %+v", m)
	}
	// the tokenizer of an 8-bit dictionary knows its letters
	e := open(t, "i54633")
	if toks := e.Tokenize("«éditer»"); len(toks) != 1 || toks[0].Word != "éditer" || toks[0].Column != 1 {
		t.Errorf("Tokenize with an 8-bit dictionary = %+v", toks)
	}
}

func TestPersonalDictionary(t *testing.T) {
	// the personal dictionary of the hunspell tool (-p): the input
	// conversion is applied to each line and the lines that fail are
	// skipped
	d := open(t, "iconv")
	err := d.LoadPersonal(strings.NewReader("Ţara\r\n\nblog/nope\nzzz/Ţepes\n*ţ\n"))
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), `"Țepes"`) {
		t.Errorf("LoadPersonal error: %v", err)
	}
	for w, want := range map[string]bool{"Ţara": true, "Țara": true, "blog": false, "zzz": false,
		"ţ": false, "ț": false, "Ș": true} {
		if d.Spell(w) != want {
			t.Errorf("Spell(%q) = %v", w, !want)
		}
	}
	// word//example is the old syntax of word/example
	b := open(t, "base_utf")
	dir := t.TempDir()
	p := writeFile(t, filepath.Join(dir, "personal"), "blog//look\nvlog/look\nėx\n")
	if err := b.LoadPersonalFile(p); err != nil {
		t.Fatal(err)
	}
	if !b.Spell("blogs") || !b.Spell("vlogs") || !b.Spell("ėx") {
		t.Error("LoadPersonalFile words")
	}
	if err := b.LoadPersonalFile(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadPersonalFile of a missing file: %v", err)
	}
	if err := b.LoadPersonal(io.MultiReader(strings.NewReader("first\n"), errReader{})); err == nil {
		t.Error("LoadPersonal of a failing reader succeeded")
	}
	if !b.Spell("first") {
		t.Error("LoadPersonal did not apply the lines before the read error")
	}
	// unencodable words are skipped
	e := open(t, "i54633")
	if err := e.LoadPersonal(strings.NewReader("ėx\n*ėy\nėz/éditer\nça\n")); err != nil || !e.Spell("ça") {
		t.Errorf("LoadPersonal with unencodable words: %v", err)
	}
}

func TestWordLists(t *testing.T) {
	if _, err := NewWordList(errReader{}); err == nil {
		t.Error("NewWordList of a failing reader succeeded")
	}
	if _, err := NewWordListFromDic(errReader{}); err == nil {
		t.Error("NewWordListFromDic of a failing reader succeeded")
	}
	if _, err := NewWordListFile(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("NewWordListFile of a missing file: %v", err)
	}
	wl, err := NewWordList(strings.NewReader("  # comment\nalpha\n* \n*beta\n  gamma  \n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(slices.Values(wl.Words())); !reflect.DeepEqual(got, []string{"alpha", "gamma"}) {
		t.Errorf("Words = %q", got)
	}
	if !wl.IsForbidden("beta") || wl.IsForbidden("") || wl.HasWord("beta") {
		t.Error("forbidden words")
	}
	// Add and Forbid override each other
	wl.Forbid("alpha")
	wl.Add("beta")
	if wl.HasWord("alpha") || !wl.IsForbidden("alpha") || !wl.HasWord("beta") || wl.IsForbidden("beta") {
		t.Error("Add and Forbid")
	}
	var empty WordList
	empty.Forbid("x")
	if !empty.IsForbidden("x") || len(empty.Words()) != 0 {
		t.Error("Forbid of a zero WordList")
	}
	// a .dic file without count line, with a byte order mark, escaped
	// slashes, morphological fields, tabs and comments
	wl, err = NewWordListFromDic(strings.NewReader("\uFEFFfirst/A\n# comment\n\nTCP\\/IP/S\n/slash\n" +
		"word po:noun\nother\tst:x\nspaced word\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/slash", "TCP/IP", "first", "other", "spaced word", "word"}
	if got := slices.Sorted(slices.Values(wl.Words())); !reflect.DeepEqual(got, want) {
		t.Errorf("NewWordListFromDic = %q, want %q", got, want)
	}
	wl, err = NewWordListFromDic(strings.NewReader("\uFEFF2\nfirst/A\n"))
	if err != nil || !reflect.DeepEqual(wl.Words(), []string{"first"}) {
		t.Errorf("NewWordListFromDic with a count line = %q, %v", wl.Words(), err)
	}
}

func TestCheckerSuggestAndText(t *testing.T) {
	d := open(t, "base_utf")
	project, err := NewWordList(strings.NewReader("Kubernetes\ngopher\ngoph\nbackend\nmicroservice\nsuperlongword\n*looks\n"))
	if err != nil {
		t.Fatal(err)
	}
	c := NewChecker(d, project)
	if c.Dictionary() != d {
		t.Error("Checker.Dictionary")
	}
	// the dictionary's suggestions without the forbidden words, then the
	// word list words by edit distance: 1 for up to 4 letters, 2 for up to
	// 8, 3 above
	if got := d.Suggest("lookz"); !slices.Contains(got, "looks") {
		t.Fatalf("dictionary suggestions %q", got)
	}
	tests := map[string][]string{
		"lookz":         {"look"},
		"gophr":         {"goph", "gopher"},
		"gofr":          nil,
		"backnd":        {"backend"},
		"microsrvce":    {"microservice"},
		"superlngwrd":   {"superlongword"},
		"superlngwrdxx": nil,
		"Kubernetes":    nil,
		"bakend":        {"backend"},
		"mcroservce":    {"microservice"},
		"suprlngwordxx": nil,
	}
	for w, want := range tests {
		got := c.Suggest(w)
		var extra []string
		for _, s := range got {
			if s == "looks" {
				t.Errorf("Suggest(%q) has the forbidden word", w)
			}
			if !slices.Contains(d.Suggest(w), s) {
				extra = append(extra, s)
			}
		}
		if w == "lookz" {
			extra = got
		}
		if !reflect.DeepEqual(extra, want) {
			t.Errorf("Checker.Suggest(%q) = %q, list words %q, want %q", w, got, extra, want)
		}
	}
	// equal distances in alphabetical order
	c2 := NewChecker(d, &WordList{})
	l := &WordList{}
	for _, w := range []string{"abcd", "abce", "abcf", "abc"} {
		l.Add(w)
	}
	c2.AddWordList(l)
	if got := c2.Suggest("abcx"); !reflect.DeepEqual(got[len(got)-3:], []string{"abcd", "abce", "abcf"}) {
		t.Errorf("Suggest(abcx) = %q", got)
	}
	// CheckText with suggestions
	m := c.CheckText("Kubernetes lokks gophr looks", WithSuggestions())
	if len(m) != 3 || m[0].Word != "lokks" || m[1].Word != "gophr" || m[2].Word != "looks" {
		t.Fatalf("CheckText = %+v", m)
	}
	if !slices.Contains(m[1].Suggestions, "gopher") || slices.Contains(m[0].Suggestions, "looks") {
		t.Errorf("CheckText suggestions = %q, %q", m[0].Suggestions, m[1].Suggestions)
	}
	c.RemoveWordList(&WordList{}) // not a list of the checker
	if !c.Spell("Kubernetes") {
		t.Error("RemoveWordList of an unknown list")
	}
}
