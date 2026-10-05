package gohunspell_test

import (
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/documatrix/go-hunspell"
)

func Example() {
	d, err := gohunspell.Open("testdata/hunspell/base.aff", "testdata/hunspell/base.dic")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(d.Spell("looked"), d.Spell("loooked"))
	fmt.Println(d.Suggest("loooked"))
	fmt.Println(d.Stem("created"))
	// Output:
	// true false
	// [looked look]
	// [created create]
}

func ExampleDictionary_Check() {
	d, _ := gohunspell.Open("testdata/hunspell/base.aff", "testdata/hunspell/base.dic")
	r := d.Check("imply")
	fmt.Printf("%+v\n", r)
	r = d.Check("implied")
	fmt.Printf("%+v\n", r)
	// Output:
	// {Correct:true Compound:false Forbidden:false Warn:false Root:}
	// {Correct:true Compound:false Forbidden:false Warn:false Root:imply}
}

func ExampleDictionary_Analyze() {
	d, _ := gohunspell.Open("testdata/hunspell/morph.aff", "testdata/hunspell/morph.dic")
	for _, a := range d.Analyze("drunk") {
		fmt.Println(strings.Join(strings.Fields(a), " "))
	}
	fmt.Println(d.Generate("eat", "drank"))
	// Output:
	// po:verb st:drink is:past_2
	// [ate]
}

func ExampleDictionary_CheckText() {
	d, _ := gohunspell.Open("testdata/hunspell/base.aff", "testdata/hunspell/base.dic")
	for _, m := range d.CheckText("The sawyer said: hello texxt!", gohunspell.WithSuggestions()) {
		fmt.Printf("%d:%d %s %v\n", m.Line, m.Column, m.Word, m.Suggestions)
	}
	// Output:
	// 1:0 The [Text]
	// 1:23 texxt [text]
}

func ExampleChecker() {
	d, _ := gohunspell.Open("testdata/hunspell/base.aff", "testdata/hunspell/base.dic")
	project, _ := gohunspell.NewWordList(strings.NewReader("Kubernetes\n*sawyer\n"))
	c := gohunspell.NewChecker(d, project)
	fmt.Println(c.Spell("Kubernetes"), c.Spell("sawyer"), c.Spell("hello"))
	// Output:
	// true false true
}

func ExampleDictionary_HasWord() {
	d, _ := gohunspell.Open("testdata/hunspell/base_utf.aff", "testdata/hunspell/base_utf.dic")
	// "looked" is correct, but only "look" is an entry of the dictionary
	fmt.Println(d.Spell("looked"), d.HasWord("looked"), d.HasWord("look"))
	// Output:
	// true false true
}

func ExampleDictionary_Words() {
	aff := "SET UTF-8\nSFX S Y 1\nSFX S 0 s .\n"
	dic := "3\ncat/S\ndog/S\nbird\n"
	d, err := gohunspell.OpenBytes([]byte(aff), []byte(dic))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(slices.Sorted(d.Words()), d.WordCount())
	for w := range d.Words() {
		if strings.HasPrefix(w, "d") {
			fmt.Println("first word with d:", w)
			break
		}
	}
	// Output:
	// [bird cat dog] 3
	// first word with d: dog
}

func ExampleDictionary_Expand() {
	d, _ := gohunspell.Open("testdata/hunspell/base_utf.aff", "testdata/hunspell/base_utf.dic")
	forms := d.Expand("look")
	slices.Sort(forms)
	fmt.Println(forms)
	// Output:
	// [look looked looker lookers looking looks]
}

func ExampleOpenFS() {
	// testdata/hz holds plain.aff and plain.dic.hz, compressed with hzip;
	// an embed.FS works the same way
	d, err := gohunspell.OpenFS(os.DirFS("testdata/hz"), "plain.aff", "plain.dic")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(d.Spell("looked"), d.Suggest("loooked"))
	// Output:
	// true [looked look]
}

func ExampleDictionary_Tokenize() {
	d, _ := gohunspell.Open("testdata/hunspell/base_utf.aff", "testdata/hunspell/base_utf.dic")
	text := `\section{Intro} Some $x+y$ text \emph{here}`
	for _, t := range d.Tokenize(text, gohunspell.WithFormat(gohunspell.LaTeX)) {
		fmt.Println(t.Line, t.Column, t.Word)
	}
	// Output:
	// 1 9 Intro
	// 1 16 Some
	// 1 27 text
	// 1 38 here
}
