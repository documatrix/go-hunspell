package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/documatrix/go-hunspell/internal/core"
)

// The upstream test suite of Hunspell lives in testdata/hunspell, as copied
// from the hunspell repository (see testdata/hunspell/UPSTREAM_COMMIT).
const testDir = "../../testdata/hunspell"

// upstreamTests lists the tests of tests/Makefile.am.
func upstreamTests(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testDir, "Makefile.am"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "TESTS =")
	end := strings.Index(text[start:], "\n\n")
	block := text[start : start+end]
	re := regexp.MustCompile(`[\w.-]+\.(dic|test|sh)`)
	return re.FindAllString(block, -1)
}

func testEnv(extra map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := extra[k]; ok {
			return v
		}
		if k == "LC_ALL" {
			return "C"
		}
		return ""
	}
}

type runResult struct {
	stdout, stderr string
	code           int
}

// runHunspell runs the tool with the conventions of the C++ tool on Linux,
// which the upstream tests expect.
func runHunspell(t *testing.T, env map[string]string, stdin string, args ...string) (r runResult) {
	t.Helper()
	return runHunspellOn(t, unixPlatform, env, stdin, args...)
}

func runHunspellOn(t *testing.T, p platform, env map[string]string, stdin string, args ...string) (r runResult) {
	t.Helper()
	var out, errb bytes.Buffer
	defer func() {
		if p := recover(); p != nil {
			buf := make([]byte, 1<<16)
			n := runtime.Stack(buf, false)
			t.Fatalf("hunspell %v panicked: %v\n%s", args, p, buf[:n])
		}
	}()
	code := mainOn(p, args, testEnv(env), strings.NewReader(stdin), &out, &errb)
	return runResult{out.String(), errb.String(), code}
}

func readFile(t *testing.T, name string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testDir, name))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// shell output in $(...) loses its trailing newlines and test.sh drops the
// carriage returns
func shellOut(s string) string {
	return strings.TrimRight(strings.ReplaceAll(s, "\r", ""), "\n")
}

// runDicTest does what tests/test.sh does for one dictionary.
func runDicTest(t *testing.T, name string, encoding string, extra ...string) {
	dict := filepath.Join(testDir, name)
	base := append([]string{"-i", encoding}, extra...)
	base = append(base, "-d", dict)

	// good words
	if good, ok := readFile(t, name+".good"); ok {
		r := runHunspell(t, nil, good, append([]string{"-l"}, base...)...)
		if out := shellOut(r.stdout); out != "" {
			t.Errorf("%s.good: good words recognised as wrong:\n%s", name, out)
		}
	}
	// bad words
	if wrong, ok := readFile(t, name+".wrong"); ok {
		r := runHunspell(t, nil, wrong, append([]string{"-G"}, base...)...)
		if out := shellOut(r.stdout); out != "" {
			t.Errorf("%s.wrong: bad words recognised as good:\n%s", name, out)
		}
	}
	// roots of the good words
	if expected, ok := readFile(t, name+".root"); ok {
		good, _ := readFile(t, name+".good")
		r := runHunspell(t, nil, good, "-d", dict)
		var roots []string
		for _, l := range strings.Split(r.stdout, "\n") {
			if strings.HasPrefix(l, "+ ") {
				roots = append(roots, l[2:])
			}
		}
		if got := strings.Join(roots, "\n"); got != shellOut(expected) {
			t.Errorf("%s.root: bad analysis\nwant:\n%s\ngot:\n%s", name, shellOut(expected), got)
		}
	}
	// morphological analysis
	if expected, ok := readFile(t, name+".morph"); ok {
		var out, errb bytes.Buffer
		Analyze([]string{dict + ".aff", dict + ".dic", dict + ".good"}, &out, &errb)
		if got := shellOut(out.String()); got != shellOut(expected) {
			t.Errorf("%s.morph: bad analysis\n%s", name, lineDiff(shellOut(expected), got))
		}
	}
	// the trace of how each word was decided
	if expected, ok := readFile(t, name+".trace"); ok {
		good, _ := readFile(t, name+".good")
		wrong, _ := readFile(t, name+".wrong")
		r := runHunspell(t, nil, good+wrong, append([]string{"--trace"}, base...)...)
		if got := shellOut(r.stdout); got != shellOut(expected) {
			t.Errorf("%s.trace: bad trace\n%s", name, lineDiff(shellOut(expected), got))
		}
	}
	// suggestions
	if expected, ok := readFile(t, name+".sug"); ok {
		wrong, _ := readFile(t, name+".wrong")
		r := runHunspell(t, nil, wrong, append([]string{"-a"}, base...)...)
		var sugs []string
		for _, l := range strings.Split(r.stdout, "\n") {
			if strings.HasPrefix(l, "&") {
				if i := strings.Index(l, ": "); i >= 0 {
					l = l[i+2:]
				}
				sugs = append(sugs, l)
			}
		}
		if got := strings.Join(sugs, "\n"); got != shellOut(expected) {
			t.Errorf("%s.sug: bad suggestion\n%s", name, lineDiff(shellOut(expected), got))
		}
	}
}

func lineDiff(want, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, c string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			c = g[i]
		}
		if a != c {
			fmt.Fprintf(&b, "line %d:\n  want %q\n  got  %q\n", i+1, a, c)
		}
	}
	return b.String()
}

// TestUpstream runs every test of Hunspell's test suite in-process.
func TestUpstream(t *testing.T) {
	for _, test := range upstreamTests(t) {
		test := test
		t.Run(test, func(t *testing.T) {
			switch {
			case strings.HasSuffix(test, ".dic"):
				runDicTest(t, strings.TrimSuffix(test, ".dic"), "UTF-8")
			case strings.HasSuffix(test, ".test"), strings.HasSuffix(test, ".sh"):
				runScriptTest(t, test)
			}
		})
	}
}

// runScriptTest replays the scripted tests (*.test, *.sh).
func runScriptTest(t *testing.T, test string) {
	name := strings.TrimSuffix(strings.TrimSuffix(test, ".test"), ".sh")
	dict := filepath.Join(testDir, name)
	noCrash := func(stdin string, args ...string) runResult {
		r := runHunspell(t, nil, stdin, args...)
		if r.code != 0 {
			t.Errorf("%s exited with %d: %s", test, r.code, r.stderr)
		}
		return r
	}
	switch name {
	case "utf8_nonbmp":
		runDicTest(t, name, "utf-8", "-1")
	case "oconv2":
		runDicTest(t, name, "utf-8")
	case "compound_wnum_overflow":
		noCrash(strings.Repeat("a", 131)+"-\n", "-d", dict, "-a")
	case "gh-hunzip-overflow", "gh1116":
		r := noCrash("test\n", "-d", dict, "-a")
		if name == "gh1116" && !strings.Contains(r.stdout+r.stderr, "missing or bad word count") {
			t.Errorf("gh1116 did not reject inflated tablesize:\n%s", r.stderr)
		}
	case "gh1018":
		noCrash("zuri\n", "-d", dict, "-a")
	case "gh1095":
		noCrash("lag\n", "-d", dict, "-a")
	case "ofz51432", "ofz5627151457255424":
		noCrash("  \n", "-d", dict, "-a")
	case "gh100":
		r := runHunspell(t, nil, "~latin1\naaa\n", "-a", "-d", dict)
		var lines []string
		for _, l := range strings.Split(r.stdout, "\n") {
			if l != "" && strings.ContainsRune("-+*&#", rune(l[0])) {
				lines = append(lines, l)
			}
		}
		if got := strings.Join(lines, "\n"); got != "*" {
			t.Errorf("gh100 unexpected output: %q", got)
		}
	case "gh1032":
		testGh1032(t, dict)
	case "gh1058":
		testGh1058(t, dict)
	case "gh1044":
		var out, errb bytes.Buffer
		if code := Analyze([]string{dict + ".aff", dict + ".dic", dict + ".words"}, &out, &errb); code != 0 {
			t.Errorf("gh1044 exited with %d", code)
		}
	case "gh1086":
		noCrash("", "-d", dict, "-U", dict+".txt")
	case "gh646":
		done := make(chan struct{})
		go func() {
			defer close(done)
			noCrash("", "-d", dict, "-u3", dict+".txt")
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("gh646 hung")
		}
	case "gh910":
		r := runHunspell(t, nil, "", "-d", dict, "-p", dict+".pers", "-l", dict+".txt")
		if r.stdout+r.stderr != "" {
			t.Errorf("gh910: -p personal dictionary not honoured with HOME unset: %s", r.stdout+r.stderr)
		}
	case "gh489":
		r := runHunspell(t, nil, "foobar\n", "-s", "-d", dict)
		if got := shellOut(r.stdout); got != "foobar foobar" {
			t.Errorf("gh489 stem foobar: want 'foobar foobar' got %q", got)
		}
	case "gh303":
		in := filepath.Join(t.TempDir(), "in.txt")
		os.WriteFile(in, []byte("drinks\n"), 0o644)
		morph := filepath.Join(testDir, "morph")
		for _, c := range [][3]string{{"is:sg_3", "is:past_1", "drank"}, {"is:sg_3", "is:past_2", "drunk"}} {
			var out, errb bytes.Buffer
			Chmorph([]string{morph + ".aff", morph + ".dic", in, c[0], c[1]}, &out, &errb)
			if got := shellOut(out.String()); got != c[2] {
				t.Errorf("gh303 chmorph drinks %s %s: want %q got %q", c[0], c[1], c[2], got)
			}
		}
	case "listdicpath":
		dir := filepath.Join(t.TempDir(), "listdicpath")
		os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
		for _, f := range []string{"top.dic", "hyph_top.dic", "sub/nested.dic"} {
			os.WriteFile(filepath.Join(dir, f), nil, 0o644)
		}
		// real paths: the conventions of the host (on Windows C:\...)
		r := runHunspellOn(t, hostPlatform, map[string]string{"DICPATH": dir}, "", "-D", "-a", os.DevNull)
		sep := string(hostPlatform.dirSep)
		lines := strings.Split(r.stderr, "\n")
		has := func(pred func(string) bool) bool {
			for _, l := range lines {
				if pred(l) {
					return true
				}
			}
			return false
		}
		if !has(func(l string) bool { return l == dir+sep+"top" }) {
			t.Errorf("%s/top is not listed:\n%s", dir, r.stderr)
		}
		if has(func(l string) bool { return strings.HasPrefix(l, dir+sep+"sub") }) {
			t.Errorf("a dictionary in a subfolder is listed:\n%s", r.stderr)
		}
		if has(func(l string) bool { return strings.HasPrefix(l, dir+sep+"hyph_") }) {
			t.Errorf("a hyphenation table is listed as a dictionary:\n%s", r.stderr)
		}
	case "gh211":
		t.Skip("gh211 tests the makealias awk tool, which is not part of the library")
	case "abi-check":
		t.Skip("abi-check checks the symbols of the C++ shared library")
	default:
		t.Fatalf("no replay for %s", test)
	}
}

// testGh1032: HashMgr::remove double delete[] when add_with_affix shares
// flags with aliased flags (AF directive present).
func testGh1032(t *testing.T, base string) {
	h := core.New(core.Source{Path: base + ".aff"}, core.Source{Path: base + ".dic"}, "")
	spell := func(w string) bool { return h.Spell(w, nil, nil) }
	if !spell("test") || !spell("tests") {
		t.Fatal("'test' or 'tests' not in dictionary")
	}
	h.AddWithAffix("x", "test")
	if !spell("x") || !spell("xs") {
		t.Fatal("'x' or 'xs' not recognized after add_with_affix")
	}
	h.Remove("test")
	if !spell("x") {
		t.Fatal("'x' not recognized after removing 'test'")
	}
	h.Remove("x")
}

// testGh1058: suggest() must not return the input word as one of its
// suggestions.
func testGh1058(t *testing.T, base string) {
	h := core.New(core.Source{Path: base + ".aff"}, core.Source{Path: base + ".dic"}, "")
	if !h.Spell("banana", nil, nil) {
		t.Fatal("'banana' not in dictionary")
	}
	for _, s := range h.Suggest("banana") {
		if s == "banana" {
			t.Fatal("suggest returned the input word as a suggestion")
		}
	}
}

// TestUpstreamScripts runs the unmodified test.sh and *.test scripts of the
// upstream suite against freshly built hunspell, analyze and chmorph binaries.
func TestUpstreamScripts(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the upstream scripts need a POSIX environment")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	bin := t.TempDir()
	for _, cmd := range []string{"hunspell", "analyze", "chmorph"} {
		build := exec.Command("go", "build", "-o", filepath.Join(bin, cmd), "../../cmd/"+cmd)
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("building %s: %v\n%s", cmd, err, out)
		}
	}
	// test.sh runs the tools through libtool --mode=execute
	libtool := filepath.Join(bin, "libtool")
	os.WriteFile(libtool, []byte("#!/bin/sh\nshift\nexec \"$@\"\n"), 0o755)
	// macOS has no timeout(1): run the command without a limit
	if _, err := exec.LookPath("timeout"); err != nil {
		os.WriteFile(filepath.Join(bin, "timeout"), []byte("#!/bin/sh\nshift\nexec \"$@\"\n"), 0o755)
	}
	abs, _ := filepath.Abs(testDir)
	for _, test := range upstreamTests(t) {
		test := test
		switch test {
		case "gh1032.test", "gh1058.test": // C++ API programs, see TestUpstream
			continue
		case "gh211.test", "abi-check.sh": // makealias tool, C++ ABI
			continue
		}
		t.Run(test, func(t *testing.T) {
			var cmd *exec.Cmd
			if strings.HasSuffix(test, ".dic") {
				cmd = exec.Command(bash, "./test.sh", test)
			} else {
				cmd = exec.Command(bash, "./"+test)
			}
			cmd.Dir = abs
			cmd.Env = append(os.Environ(),
				"HUNSPELL="+filepath.Join(bin, "hunspell"),
				"ANALYZE="+filepath.Join(bin, "analyze"),
				"CHMORPH="+filepath.Join(bin, "chmorph"),
				"LIBTOOL="+libtool,
				"TMPDIR="+t.TempDir(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("%s: %v\n%s", test, err, out)
			}
		})
	}
}
