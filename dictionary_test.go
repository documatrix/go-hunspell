package gohunspell

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const testdata = "testdata/hunspell"

func open(t *testing.T, name string) *Dictionary {
	t.Helper()
	d, err := Open(filepath.Join(testdata, name+".aff"), filepath.Join(testdata, name+".dic"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSpellAndSuggest(t *testing.T) {
	d := open(t, "base")
	for _, w := range []string{"created", "uncreate", "NASA", "Hunspell", "looked", "speech", "won't"} {
		if !d.Spell(w) {
			t.Errorf("Spell(%q) = false", w)
		}
	}
	for _, w := range []string{"loooked", "texxt", "Nasa"} {
		if d.Spell(w) {
			t.Errorf("Spell(%q) = true", w)
		}
	}
	if got := d.Suggest("loooked"); len(got) == 0 || got[0] != "looked" {
		t.Errorf("Suggest(loooked) = %v", got)
	}
	r := d.Check("looked")
	if !r.Correct || r.Root != "look" {
		t.Errorf("Check(looked) = %+v", r)
	}
}

func TestEightBitDictionary(t *testing.T) {
	// i54633 is an ISO8859-1 dictionary; words are UTF-8 at the API
	d := open(t, "i54633")
	if d.Encoding() != "ISO8859-1" {
		t.Fatalf("encoding %q", d.Encoding())
	}
	for _, w := range []string{"éditer", "Éditer"} {
		if !d.Spell(w) {
			t.Errorf("Spell(%q) = false", w)
		}
	}
	if d.Spell("editer") {
		t.Error("Spell(editer) = true")
	}
	if got := d.Suggest("editer"); !reflect.DeepEqual(got, []string{"éditer"}) {
		t.Errorf("Suggest(editer) = %q", got)
	}
	// a word that has no form in the dictionary encoding
	if d.Spell("ėditer") {
		t.Error("Spell of an unencodable word = true")
	}
}

func TestMorphology(t *testing.T) {
	d := open(t, "morph")
	if got := d.Stem("drinks"); !reflect.DeepEqual(got, []string{"drink"}) {
		t.Errorf("Stem(drinks) = %q", got)
	}
	an := d.Analyze("drank")
	if len(an) != 1 || !strings.Contains(an[0], "is:past_1") {
		t.Errorf("Analyze(drank) = %q", an)
	}
	if got := d.Generate("drink", "ate"); !reflect.DeepEqual(got, []string{"drank"}) {
		t.Errorf("Generate(drink, ate) = %q", got)
	}
	if got := d.GenerateMorph("eat", []string{"is:past_2"}); !reflect.DeepEqual(got, []string{"eaten"}) {
		t.Errorf("GenerateMorph(eat, is:past_2) = %q", got)
	}
	if got := d.StemAnalyses(d.Analyze("phenomena")); !reflect.DeepEqual(got, []string{"phenomenon"}) {
		t.Errorf("StemAnalyses = %q", got)
	}
}

func TestRuntimeWords(t *testing.T) {
	d := open(t, "base")
	if d.Spell("gohunspell") {
		t.Fatal("unexpected word")
	}
	if err := d.Add("gohunspell"); err != nil {
		t.Fatal(err)
	}
	if !d.Spell("gohunspell") || !d.Spell("Gohunspell") {
		t.Error("added word not accepted")
	}
	if err := d.AddWithAffix("tweet", "look"); err != nil {
		t.Fatal(err)
	}
	if !d.Spell("tweeted") {
		t.Error("tweeted not accepted after AddWithAffix(tweet, look)")
	}
	if err := d.Remove("speech"); err != nil {
		t.Fatal(err)
	}
	if d.Spell("speech") {
		t.Error("removed word still accepted")
	}
	if err := d.LoadPersonal(strings.NewReader("gopher\n*hello\nblog/look\n")); err != nil {
		t.Fatal(err)
	}
	if !d.Spell("gopher") || d.Spell("hello") {
		t.Error("personal dictionary not applied")
	}
	if !d.Spell("blogs") {
		t.Error("blog/look did not take the affixes of look")
	}
}

func TestOpenReaderAndErrors(t *testing.T) {
	aff, _ := os.ReadFile(filepath.Join(testdata, "base.aff"))
	dic, _ := os.ReadFile(filepath.Join(testdata, "base.dic"))
	d, err := OpenBytes(aff, dic)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Spell("created") {
		t.Error("OpenBytes dictionary does not work")
	}
	if _, err := Open("nope.aff", "nope.dic"); err == nil {
		t.Error("Open of missing files succeeded")
	}
	if _, err := OpenBytes(aff, []byte("x\nword\n")); err == nil {
		t.Error("a bad word count was accepted")
	}
}

func TestAddDictionary(t *testing.T) {
	d := open(t, "base")
	if err := d.AddDictionaryReader(strings.NewReader("1\ngopher/S\n")); err != nil {
		t.Fatal(err)
	}
	if !d.Spell("gophers") {
		t.Error("extra dictionary not used with the affix rules")
	}
}

func TestCheckText(t *testing.T) {
	d := open(t, "base")
	text := "Hello texxt, said the sawyer.\nIt's tomorrow seven days."
	got := d.CheckText(text, WithSuggestions())
	var words []string
	for _, m := range got {
		words = append(words, m.Word)
	}
	if !reflect.DeepEqual(words, []string{"texxt", "the", "It's", "days."}) {
		t.Fatalf("misspellings %q", words)
	}
	m := got[0]
	if m.Offset != 6 || m.Line != 1 || m.Column != 6 || text[m.Offset:m.Offset+len(m.Word)] != "texxt" {
		t.Errorf("position %+v", m.Token)
	}
	if len(m.Suggestions) == 0 || m.Suggestions[0] != "text" {
		t.Errorf("suggestions %q", m.Suggestions)
	}
	if got[2].Line != 2 || got[2].Column != 0 {
		t.Errorf("line %d", got[2].Line)
	}
	html := d.CheckText(`<p title="x">texxt <code>texxt</code></p>`, WithFormat(HTML))
	if len(html) != 1 {
		t.Errorf("HTML misspellings %+v", html)
	}
}

func TestChecker(t *testing.T) {
	d := open(t, "base")
	project, _ := NewWordList(strings.NewReader("# project words\nKubernetes\ngopher\n*sawyer\n"))
	c := NewChecker(d, project)
	for w, want := range map[string]bool{"Kubernetes": true, "gopher": true, "Gopher": true, "GOPHER": true,
		"sawyer": false, "text": true, "Gophr": false} {
		if got := c.Spell(w); got != want {
			t.Errorf("Checker.Spell(%q) = %v", w, got)
		}
	}
	if got := c.Suggest("gopherr"); len(got) == 0 || got[len(got)-1] != "gopher" {
		t.Errorf("Checker.Suggest(gopherr) = %q", got)
	}
	doc := &WordList{}
	doc.Add("texxt")
	c.AddWordList(doc)
	if len(c.CheckText("texxt sawyer")) != 1 {
		t.Error("document word list not used")
	}
	c.RemoveWordList(doc)
	if c.Spell("texxt") {
		t.Error("removed word list still used")
	}
}

func TestTrace(t *testing.T) {
	d := open(t, "trace_basic")
	var lines []string
	ok := d.Trace("own", func(depth int, line string) {
		lines = append(lines, strings.Repeat("  ", depth)+line)
	})
	want := []string{
		`word "own"`,
		`  lookup "own" -> entry "own" flags=Z`,
		`  test needaffix flag=Z in=dic have=Z -> fail, on to the next homonym`,
		`  lookup "own" -> entry "own" flags=(none)`,
		`  result correct`,
	}
	if !ok || !reflect.DeepEqual(lines, want) {
		t.Errorf("trace:\n%s", strings.Join(lines, "\n"))
	}
}

func TestConcurrentUse(t *testing.T) {
	d := open(t, "base")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if !d.Spell("created") || len(d.Suggest("creatd")) == 0 {
					t.Error("concurrent use failed")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestFindAndAvailable(t *testing.T) {
	aff, dic, err := Find("base", testdata)
	if err != nil || filepath.Base(aff) != "base.aff" || filepath.Base(dic) != "base.dic" {
		t.Fatalf("Find = %q %q %v", aff, dic, err)
	}
	names := Available(testdata)
	if len(names) < 100 {
		t.Errorf("Available found %d dictionaries", len(names))
	}
	if _, err := Load("no_such_dictionary", testdata); err == nil {
		t.Error("Load of a missing dictionary succeeded")
	}
}
