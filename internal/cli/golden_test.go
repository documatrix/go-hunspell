package cli

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The golden corpus of the command line tools lives in testdata/golden/cli:
// cases.txt lists the cases, in/ and dict/ hold the inputs, and out/ holds
// what the C++ tools printed for each case. The expected outputs are never
// made by the Go code: regenerate them from the reference binaries with
//
//	go test ./internal/cli -run TestGoldenCLI -args -golden-ref=DIR
//
// where DIR holds hunspell, analyze and chmorph built from the upstream
// sources (the hunspell tool built without curses and, so that suggestions
// are reproducible, with the time limits of the suggestion search lifted).
// The C.UTF-8 locale of glibc is built in; the locales of the locale_* cases
// are made with
//
//	localedef -i de_DE -f ISO-8859-1 $LOC/de_DE.ISO-8859-1
//	localedef -i de_DE -f ISO-8859-1 $LOC/de_DE
//	localedef -i sr_RS@latin -f UTF-8 $LOC/sr_RS.UTF-8@latin
//	localedef -i ru_RU -f KOI8-R $LOC/ru_RU.KOI8-R
//
// and handed to the C++ tools with -golden-locpath=$LOC.
const goldenDir = "../../testdata/golden/cli"

var (
	goldenRef     = flag.String("golden-ref", "", "directory of the C++ hunspell, analyze and chmorph binaries to regenerate the CLI golden files from")
	goldenLocpath = flag.String("golden-locpath", "", "LOCPATH of the C++ tools, with the locales de_DE, de_DE.ISO-8859-1, sr_RS.UTF-8@latin and ru_RU.KOI8-R the cases use")
)

// goldenCase is one run of a tool.
type goldenCase struct {
	name     string
	line     int
	tool     string            // hunspell, analyze or chmorph
	args     []string          // arguments, @HOME@ standing for the home folder
	stdin    string            // file of the standard input, if any
	env      map[string]string // the whole environment, besides HOME
	home     string            // folder in homes/ copied to a fresh HOME, "-" for an empty one
	files    []string          // files of HOME dumped after the run
	noStderr bool              // stderr is not compared (it comes from unzip)
	dicList  bool              // the -D listing is normalized
}

// splitArgs splits a line into words; single quotes keep spaces and make
// empty words.
func splitArgs(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	in, quoted := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quoted:
			if c == '\'' {
				quoted = false
			} else {
				cur.WriteByte(c)
			}
		case c == '\'':
			quoted, in = true, true
		case c == ' ' || c == '\t':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if quoted {
		return nil, fmt.Errorf("unterminated quote")
	}
	if in {
		words = append(words, cur.String())
	}
	return words, nil
}

func loadGoldenCases(t *testing.T) []*goldenCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(goldenDir, "cases.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []*goldenCase
	var c *goldenCase
	seen := map[string]bool{}
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || line[0] == '#' {
			continue
		}
		key, rest, _ := strings.Cut(line, " ")
		if key == "case" {
			if seen[rest] {
				t.Fatalf("cases.txt:%d: duplicate case %s", n+1, rest)
			}
			seen[rest] = true
			c = &goldenCase{name: rest, line: n + 1, tool: "hunspell", env: map[string]string{"LC_ALL": "C"}}
			cases = append(cases, c)
			continue
		}
		if c == nil {
			t.Fatalf("cases.txt:%d: %s outside a case", n+1, key)
		}
		switch key {
		case "tool":
			c.tool = rest
		case "args":
			if c.args, err = splitArgs(rest); err != nil {
				t.Fatalf("cases.txt:%d: %v", n+1, err)
			}
		case "stdin":
			c.stdin = rest
		case "env":
			// env replaces the default environment; "env -" empties it
			c.env = map[string]string{}
			words, err := splitArgs(rest)
			if err != nil {
				t.Fatalf("cases.txt:%d: %v", n+1, err)
			}
			for _, w := range words {
				if w == "-" {
					continue
				}
				k, v, ok := strings.Cut(w, "=")
				if !ok {
					t.Fatalf("cases.txt:%d: bad env %q", n+1, w)
				}
				c.env[k] = v
			}
		case "home":
			c.home = rest
		case "file":
			c.files = append(c.files, rest)
		case "nostderr":
			c.noStderr = true
		case "diclist":
			c.dicList = true
		default:
			t.Fatalf("cases.txt:%d: unknown key %q", n+1, key)
		}
	}
	return cases
}

// goldenResult is what a case printed.
type goldenResult struct {
	code           int
	stdout, stderr []byte
	files          [][]byte // nil for a missing file
}

// prepare makes the home folder of a case and returns the arguments and
// environment with @HOME@ replaced.
func (c *goldenCase) prepare(t *testing.T) (home string, args []string, env map[string]string) {
	t.Helper()
	if c.home != "" {
		home = t.TempDir()
		if c.home != "-" {
			src := filepath.Join("homes", c.home) // relative to the corpus
			entries, err := os.ReadDir(src)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				data, err := os.ReadFile(filepath.Join(src, e.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, e.Name()), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	sub := func(s string) string { return strings.ReplaceAll(s, "@HOME@", home) }
	for _, a := range c.args {
		args = append(args, sub(a))
	}
	env = map[string]string{}
	for k, v := range c.env {
		env[k] = sub(v)
	}
	if home != "" {
		env["HOME"] = home
	}
	return home, args, env
}

func (c *goldenCase) collect(t *testing.T, home string, r *goldenResult) {
	t.Helper()
	for _, f := range c.files {
		data, err := os.ReadFile(filepath.Join(home, f))
		if err != nil {
			data = nil
		} else if data == nil {
			data = []byte{}
		}
		r.files = append(r.files, data)
	}
	if home != "" {
		h := []byte(home)
		r.stdout = bytes.ReplaceAll(r.stdout, h, []byte("@HOME@"))
		r.stderr = bytes.ReplaceAll(r.stderr, h, []byte("@HOME@"))
		for i := range r.files {
			if r.files[i] != nil {
				r.files[i] = bytes.ReplaceAll(r.files[i], h, []byte("@HOME@"))
			}
		}
	}
	if c.dicList {
		r.stderr = normalizeDicList(r.stderr)
	}
}

// normalizeDicList sorts the dictionaries -D lists, which come in the order
// of the directory entries, and drops those of the system folders and the
// LibreOffice extension folders, which depend on the machine.
func normalizeDicList(b []byte) []byte {
	lines := strings.SplitAfter(string(b), "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case l == "SEARCH PATH:\n" && i+1 < len(lines):
			out = append(out, l)
			i++
			var keep []string
			for _, p := range strings.Split(strings.TrimSuffix(lines[i], "\n"), ":") {
				if !strings.Contains(p, "/share/extensions/dict-") {
					keep = append(keep, p)
				}
			}
			out = append(out, strings.Join(keep, ":")+"\n")
		case strings.HasPrefix(l, "AVAILABLE DICTIONARIES"):
			out = append(out, l)
			var list []string
			for i+1 < len(lines) && lines[i+1] != "LOADED DICTIONARY:\n" && lines[i+1] != "" &&
				!strings.HasPrefix(lines[i+1], "Can't open") {
				i++
				if !strings.HasPrefix(lines[i], "/") {
					list = append(list, lines[i])
				}
			}
			sort.Strings(list)
			out = append(out, list...)
		default:
			out = append(out, l)
		}
	}
	return []byte(strings.Join(out, ""))
}

// runGo runs a case in-process.
func (c *goldenCase) runGo(t *testing.T) goldenResult {
	t.Helper()
	home, args, env := c.prepare(t)
	var r goldenResult
	var out, errb bytes.Buffer
	switch c.tool {
	case "hunspell":
		var stdin []byte
		if c.stdin != "" {
			var err error
			if stdin, err = os.ReadFile(c.stdin); err != nil {
				t.Fatal(err)
			}
		}
		getenv := func(k string) string { return env[k] }
		// the outputs are those of the C++ tool on Linux
		r.code = mainOn(unixPlatform, args, getenv, bytes.NewReader(stdin), &out, &errb)
	case "analyze":
		r.code = Analyze(args, &out, &errb)
	case "chmorph":
		r.code = Chmorph(args, &out, &errb)
	default:
		t.Fatalf("unknown tool %s", c.tool)
	}
	r.stdout, r.stderr = out.Bytes(), errb.Bytes()
	c.collect(t, home, &r)
	return r
}

// runRef runs a case with the C++ binary of dir.
func (c *goldenCase) runRef(t *testing.T, dir string) goldenResult {
	t.Helper()
	home, args, env := c.prepare(t)
	cmd := exec.Command(filepath.Join(dir, c.tool), args...)
	cmd.Env = []string{}
	if *goldenLocpath != "" {
		cmd.Env = append(cmd.Env, "LOCPATH="+*goldenLocpath)
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if c.stdin != "" {
		f, err := os.Open(c.stdin)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		cmd.Stdin = f
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	var r goldenResult
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		r.code = ee.ExitCode()
	}
	r.stdout, r.stderr = out.Bytes(), errb.Bytes()
	c.collect(t, home, &r)
	return r
}

// encode writes a result in the golden format: a header line before each
// part, with its length, so any byte can be in the parts.
func (c *goldenCase) encode(r goldenResult) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "exit %d\n", r.code)
	fmt.Fprintf(&b, "stdout %d\n", len(r.stdout))
	b.Write(r.stdout)
	if !c.noStderr {
		fmt.Fprintf(&b, "stderr %d\n", len(r.stderr))
		b.Write(r.stderr)
	}
	for i, f := range c.files {
		if r.files[i] == nil {
			fmt.Fprintf(&b, "file %s missing\n", f)
			continue
		}
		fmt.Fprintf(&b, "file %s %d\n", f, len(r.files[i]))
		b.Write(r.files[i])
	}
	return b.Bytes()
}

// decodeParts splits a golden file into its labelled parts.
func decodeParts(data []byte) (map[string][]byte, []string, error) {
	parts := map[string][]byte{}
	var order []string
	br := bufio.NewReader(bytes.NewReader(data))
	for {
		head, err := br.ReadString('\n')
		if head == "" && err != nil {
			return parts, order, nil
		}
		head = strings.TrimSuffix(head, "\n")
		i := strings.LastIndexByte(head, ' ')
		if i < 0 {
			return nil, nil, fmt.Errorf("bad header %q", head)
		}
		label, num := head[:i], head[i+1:]
		order = append(order, label)
		if label == "exit" || num == "missing" {
			parts[label] = []byte(num)
			continue
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			return nil, nil, fmt.Errorf("bad header %q", head)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, nil, fmt.Errorf("short part %q", label)
		}
		parts[label] = buf
	}
}

// TestGoldenCLI runs the cases of the CLI golden corpus through the Go tools
// and compares their output with what the C++ tools printed.
func TestGoldenCLI(t *testing.T) {
	cases := loadGoldenCases(t)
	abs, err := filepath.Abs(goldenDir)
	if err != nil {
		t.Fatal(err)
	}
	var ref string
	if *goldenRef != "" {
		if ref, err = filepath.Abs(*goldenRef); err != nil {
			t.Fatal(err)
		}
	}
	// the cases name their files relative to the corpus
	t.Chdir(abs)
	// the suggestions of the reference tools come without time limits
	unlimited = true
	defer func() { unlimited = false }()
	// goldenDir is relative to the package folder, so the paths below are
	// relative to the corpus from here on
	outDir := "out"
	if ref != "" {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, c := range cases {
			names[c.name+".golden"] = true
			r := c.runRef(t, ref)
			if err := os.WriteFile(filepath.Join(outDir, c.name+".golden"), c.encode(r), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// drop the golden files of removed cases
		entries, _ := os.ReadDir(outDir)
		for _, e := range entries {
			if !names[e.Name()] {
				os.Remove(filepath.Join(outDir, e.Name()))
			}
		}
		return
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(outDir, c.name+".golden"))
			if err != nil {
				t.Fatalf("cases.txt:%d: %v (regenerate the golden files)", c.line, err)
			}
			if name := missingDictionary(want); name != "" && systemHasDictionary(name) {
				t.Skipf("the golden output expects no dictionary %q, but the system folders have one", name)
			}
			got := c.encode(c.runGo(t))
			if bytes.Equal(got, want) {
				return
			}
			wp, worder, err := decodeParts(want)
			if err != nil {
				t.Fatalf("%s.golden: %v", c.name, err)
			}
			gp, _, _ := decodeParts(got)
			for _, label := range worder {
				if !bytes.Equal(wp[label], gp[label]) {
					t.Errorf("cases.txt:%d %s: %s differs (args %q)\n%s", c.line, c.name, label, c.args,
						lineDiff(string(wp[label]), string(gp[label])))
				}
			}
			if len(gp) != len(wp) {
				t.Errorf("cases.txt:%d %s: got parts that are not in the golden file", c.line, c.name)
			}
		})
	}
}

// TestPortableFileNames checks the file names of the repository for
// macOS and Windows: no two paths may differ only in case, and no name may
// be a reserved device name of Windows (nul.txt, con, ...).
func TestPortableFileNames(t *testing.T) {
	reserved := regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\.|$)`)
	seen := map[string]string{}
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testSubDir") {
			return filepath.SkipDir
		}
		if reserved.MatchString(d.Name()) || strings.ContainsAny(d.Name(), `<>:"|?*\`) {
			t.Errorf("%s: not a valid file name on Windows", path)
		}
		k := strings.ToLower(path)
		if prev, ok := seen[k]; ok {
			t.Errorf("%s and %s differ only in case", prev, path)
		}
		seen[k] = path
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// missingDictionary returns the dictionary a golden output reports it
// cannot open, if any.
func missingDictionary(golden []byte) string {
	const msg = "Can't open affix or dictionary files for dictionary named \""
	_, rest, ok := bytes.Cut(golden, []byte(msg))
	if !ok {
		return ""
	}
	name, _, _ := bytes.Cut(rest, []byte("\""))
	return string(name)
}

// systemHasDictionary reports whether the system folders the hunspell tool
// always searches hold the dictionary: then the outputs recorded on a
// machine without it do not apply.
func systemHasDictionary(name string) bool {
	if strings.ContainsAny(name, "/\\") {
		return false
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/usr/share/hunspell", "/usr/local/share/hunspell", "/usr/share/myspell",
		"/usr/share/myspell/dicts", "/Library/Spelling", filepath.Join(home, "Library", "Spelling"),
		filepath.Join(home, ".local", "share", "hunspell")} {
		if _, err := os.Stat(filepath.Join(dir, name+".dic")); err == nil {
			return true
		}
	}
	return false
}
