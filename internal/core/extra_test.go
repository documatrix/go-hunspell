package core

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGoldenExtra compares the API results for the small dictionaries of
// testdata/golden/extra, written to reach the less common code paths, with
// the output of the Hunspell C++ library: the diagnostics of the loading and
// the results for each word and operation (see internal/gen/golden/extra.py
// and apidump.cxx).
func TestGoldenExtra(t *testing.T) {
	dir := extraDir
	files := extraFiles(t, dir, ".api")
	for _, file := range files {
		name, _, _ := strings.Cut(filepath.Base(file), ".api")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := readExtra(t, file)
			opts := map[string]string{"aff": name + ".aff", "dic": name + ".dic"}
			body := extraHeader(data, opts)
			checkExtra(t, dir, extraPath(t, dir, opts["aff"]), opts, body)
		})
	}
}

// TestGoldenExtraCases is TestGoldenExtra for the .cases files of
// testdata/golden/extra: many small affix files that share a dictionary and
// a word list (the "> " lines of the output of each case).
func TestGoldenExtraCases(t *testing.T) {
	dir := extraDir
	files := extraFiles(t, dir, ".cases")
	for _, file := range files {
		name, _, _ := strings.Cut(filepath.Base(file), ".cases")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := readExtra(t, file)
			opts := map[string]string{"dic": name + ".dic"}
			body := extraHeader(data, opts)
			for i, c := range strings.Split(body, "=== ")[1:] {
				title, rest, _ := strings.Cut(c, "\n")
				var aff, out strings.Builder
				for _, l := range strings.SplitAfter(rest, "\n") {
					if a, ok := strings.CutPrefix(l, "| "); ok {
						aff.WriteString(a)
					} else {
						out.WriteString(l)
					}
				}
				t.Run(fmt.Sprint(i), func(t *testing.T) {
					affPath := filepath.Join(t.TempDir(), "case.aff")
					if err := os.WriteFile(affPath, []byte(aff.String()), 0o644); err != nil {
						t.Fatal(err)
					}
					t.Log(title)
					checkExtra(t, dir, affPath, opts, out.String())
				})
			}
		})
	}
}

// extraDir is the directory of the extra golden files. Its paths are built
// with "/" on every system, so that the diagnostics that name a file of it
// read as those of the C++ library, which ran in that directory.
const extraDir = "../../testdata/golden/extra"

// extraFiles lists the golden files with the extension ext, gzipped or not.
func extraFiles(t *testing.T, dir, ext string) []string {
	files, err := filepath.Glob(filepath.Join(dir, "*"+ext))
	gz, err2 := filepath.Glob(filepath.Join(dir, "*"+ext+".gz"))
	files = append(files, gz...)
	if err != nil || err2 != nil || len(files) == 0 {
		t.Fatal("no golden files", err, err2)
	}
	return files
}

// readExtra reads a golden file, unpacking a gzipped one.
func readExtra(t *testing.T, file string) string {
	if strings.HasSuffix(file, ".gz") {
		return string(readGzip(t, file))
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// extraHeader reads the "# key: value" header lines into opts and returns
// the rest.
func extraHeader(data string, opts map[string]string) string {
	for strings.HasPrefix(data, "# ") {
		line, rest, _ := strings.Cut(data, "\n")
		k, v, _ := strings.Cut(line[2:], ": ")
		opts[k] = v
		data = rest
	}
	return data
}

// checkExtra loads a dictionary and compares the diagnostics ("! " lines of
// body) and the results for the words ("> " lines) and operations ("@"
// lines) with body.
func checkExtra(t *testing.T, dir, aff string, opts map[string]string, body string) {
	t.Helper()
	var warn, items []string
	var want strings.Builder
	for _, l := range strings.SplitAfter(body, "\n") {
		line := strings.TrimSuffix(l, "\n")
		if w, ok := strings.CutPrefix(line, "! "); ok {
			warn = append(warn, w)
			continue
		}
		want.WriteString(l)
		if w, ok := strings.CutPrefix(line, "> "); ok {
			items = append(items, w)
		} else if strings.HasPrefix(line, "@") {
			items = append(items, "\x00"+line)
		}
	}
	dic := extraPath(t, dir, opts["dic"])
	h := New(Source{Path: aff}, Source{Path: dic}, opts["key"])
	h.SetTimeLimits(0, 0, 0)
	msgs := func() []string { return append(slices.Clone(h.Errors()), h.Warnings()...) }
	loadMsgs := msgs()
	example := opts["example"]
	exmorph := h.Analyze(example)
	var got strings.Builder
	for _, it := range items {
		if op, ok := strings.CutPrefix(it, "\x00"); ok {
			runOp(&got, h, dir, op)
		} else {
			dumpWord(&got, h, it, example, exmorph)
		}
	}
	if opts["warnings"] != "load" {
		loadMsgs = msgs()
	}
	var diags []string
	for _, m := range loadMsgs {
		m = strings.ReplaceAll(m, dir+"/", "")
		m = strings.ReplaceAll(m, filepath.Dir(aff)+string(filepath.Separator), "")
		if strings.HasSuffix(opts["dic"], ".gz") {
			// unpacked into a temporary directory, like extra.py does
			m = strings.ReplaceAll(m, filepath.Dir(dic)+string(filepath.Separator), "")
		}
		diags = append(diags, strings.Split(strings.TrimSuffix(m, "\n"), "\n")...)
	}
	slices.Sort(diags)
	if !slices.Equal(diags, warn) {
		t.Errorf("diagnostics\nwant %q\ngot  %q", warn, diags)
	}
	compareLines(t, want.String(), got.String())
}

// extraPath returns the path of a file of the extra directory; a gzipped
// file is unpacked into a temporary directory first.
func extraPath(t *testing.T, dir, name string) string {
	if name == "" {
		return ""
	}
	if !strings.HasSuffix(name, ".gz") {
		return dir + "/" + name
	}
	p := filepath.Join(t.TempDir(), strings.TrimSuffix(name, ".gz"))
	if err := os.WriteFile(p, readGzip(t, dir+"/"+name), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// cxxFields splits off the first n fields of s at white space like
// istringstream >>, and returns the rest without its leading white space.
func cxxFields(s string, n int) ([]string, string) {
	isSpace := func(c byte) bool { return c == ' ' || (c >= '\t' && c <= '\r') }
	var f []string
	for len(f) < n {
		for s != "" && isSpace(s[0]) {
			s = s[1:]
		}
		if s == "" {
			break
		}
		i := 0
		for i < len(s) && !isSpace(s[i]) {
			i++
		}
		f = append(f, s[:i])
		s = s[i:]
	}
	for s != "" && isSpace(s[0]) {
		s = s[1:]
	}
	for len(f) < n {
		f = append(f, "")
	}
	return f, s
}

// runOp runs an operation line like apidump.cxx.
func runOp(b *strings.Builder, h *Hunspell, dir, line string) {
	fmt.Fprintf(b, "%s\n", line)
	f, c := cxxFields(line, 3)
	cmd, a, a2 := f[0], f[1], f[2]
	if a2 == "" {
		c = ""
	}
	switch cmd {
	case "@add":
		fmt.Fprintf(b, "  ret %d\n", h.Add(a))
	case "@addflags":
		fmt.Fprintf(b, "  ret %d\n", h.AddWithFlags(a, a2, c))
	case "@addaff":
		fmt.Fprintf(b, "  ret %d\n", h.AddWithAffix(a, a2))
	case "@remove":
		fmt.Fprintf(b, "  ret %d\n", h.Remove(a))
	case "@adddic":
		fmt.Fprintf(b, "  ret %d\n", h.AddDic(Source{Path: dir + "/" + a}, a2))
	case "@trace":
		h.SetTrace(func(depth int, line string) {
			fmt.Fprintf(b, "  trace %d %s\n", depth, line)
		})
		ok := h.Spell(a, nil, nil)
		h.SetTrace(nil)
		fmt.Fprintf(b, "  spell %d\n", b2i(ok))
	case "@gen", "@stem":
		n := 1
		if cmd == "@gen" {
			n = 2
		}
		f, rest := cxxFields(line, n)
		var pl []string
		if rest != "" {
			pl = strings.Split(rest, "|")
		}
		var r []string
		if cmd == "@gen" {
			r = h.GenerateMorph(f[1], pl)
		} else {
			r = h.StemMorph(pl)
		}
		for _, s := range r {
			fmt.Fprintf(b, "  %s %s\n", cmd[1:], s)
		}
	case "@iconv":
		dest, ok := h.InputConv(a)
		fmt.Fprintf(b, "  iconv %d %s\n", b2i(ok), dest)
	case "@info":
		fmt.Fprintf(b, "  enc %s\n", h.Encoding())
		fmt.Fprintf(b, "  wordchars %s\n", h.WordChars())
		fmt.Fprintf(b, "  wordchars16")
		for _, c := range h.WordCharsUTF16() {
			fmt.Fprintf(b, " %d", c)
		}
		fmt.Fprintf(b, "\n")
		fmt.Fprintf(b, "  version %s\n", h.Version())
		fmt.Fprintf(b, "  langnum %d\n", h.LangNum())
	default:
		fmt.Fprintf(b, "  unknown\n")
	}
}
