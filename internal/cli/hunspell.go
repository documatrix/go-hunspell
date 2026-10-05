// Package cli is a port of the hunspell command line tool. It drives the
// engine the way the C++ tool does, so the upstream test suite can run
// against it unchanged.
package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/documatrix/go-hunspell/internal/charset"
	"github.com/documatrix/go-hunspell/internal/core"
	"github.com/documatrix/go-hunspell/parsers"
)

// Version is the Hunspell version the tool reports.
const Version = "1.7.2"

const (
	maxLnLen      = 8192
	pipeHeading   = "@(#) International Ispell Version 3.2.06 (but really Hunspell " + Version + ")\n"
	normalHeading = "Hunspell "
	odfExt        = "odt|ott|odp|otp|odg|otg|ods|ots"
	entityApos    = "&apos;"
	utf8Apos      = "\xe2\x80\x99"
	dmax          = 10
)

const (
	fmtText = iota
	fmtLaTeX
	fmtHTML
	fmtMan
	fmtFirst
	fmtXML
	fmtODF
)

const (
	modeNormal     = iota
	modeBadword    // print only bad words
	modeWordfilter // print only bad words from 1 word/line input
	modeBadline    // print only lines with bad words
	modeStem       // stem input words
	modeAnalyze    // analyze input words
	modePipe       // print only stars for LyX compatibility
	modeAuto0      // search typical error (based on SuggestMgr::suggest())
	modeAuto       // automatic spelling to standard output
	modeAuto2      // automatic spelling to standard output with sed log
	modeAuto3      // automatic spelling to standard output with gcc error format
	modeSuffix     // print suffixes that can be attached to a given word
	modeTrace      // check each word and print nothing of its own
)

type tool struct {
	plat            platform
	env             func(string) string
	stdout          *bufio.Writer
	stderr          io.Writer
	filterMode      int
	printgood       bool
	printtrace      bool
	nodefaultpriv   bool
	showpath        bool
	checkurl        bool
	checkapos       bool
	warn            bool
	uiEnc           string
	ioEnc           string
	dicname         string
	privdicname     string
	hasPrivdic      bool
	currentfilename string
	multipleFiles   bool
	pMS             []*core.Hunspell
	dicEnc          []string
	wordchars       string
	wordcharsUTF16  []uint16
}

// localeCodeset approximates nl_langinfo(CODESET) from the environment.
func localeCodeset(env func(string) string) string {
	loc := ""
	for _, v := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if s := env(v); s != "" {
			loc = s
			break
		}
	}
	if loc == "" || loc == "C" || loc == "POSIX" {
		return "ANSI_X3.4-1968"
	}
	if i := strings.IndexByte(loc, '.'); i >= 0 {
		cs := loc[i+1:]
		if j := strings.IndexByte(cs, '@'); j >= 0 {
			cs = cs[:j]
		}
		switch charset.Normalize(cs) {
		case "utf8":
			return "UTF-8"
		}
		return cs
	}
	return "ISO-8859-1"
}

// chenc changes the character encoding of st as iconv does, reporting
// conversion failures on stderr.
func (t *tool) chenc(st, enc1, enc2 string) string {
	return t.chencCtx(st, enc1, enc2, "")
}

// chencCtx is chenc with a context printed before the error messages.
func (t *tool) chencCtx(st, enc1, enc2, context string) string {
	if st == "" || enc1 == "" || enc2 == "" || enc1 == enc2 {
		return st
	}
	if context != "" {
		context = cstr(context) + ": "
	}
	if charset.Lookup(enc1) == nil || charset.Lookup(enc2) == nil {
		t.errf("%serror - iconv_open: %s -> %s\n", context, enc1, enc2)
		return st
	}
	out, ok := charset.Convert(st, enc1, enc2)
	if !ok {
		t.errf("%serror - iconv: %s -> %s\n", context, enc1, enc2)
	}
	return out
}

// Main runs the hunspell tool with the arguments args (without the program
// name) and returns its exit status.
func Main(args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	return mainOn(hostPlatform, args, env, stdin, stdout, stderr)
}

// mainOn is Main with the conventions of the platform p.
func mainOn(p platform, args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if env == nil {
		env = os.Getenv
	}
	// the file name of -u3 is a null pointer for the standard input
	t := &tool{plat: p, env: env, stdout: bufio.NewWriter(stdout), stderr: stderr, currentfilename: "(null)"}
	defer t.stdout.Flush()
	return t.run(args, stdin)
}

func (t *tool) errf(format string, a ...interface{}) {
	t.stdout.Flush()
	fmt.Fprintf(t.stderr, format, a...)
}

func (t *tool) run(args []string, stdin io.Reader) int {
	var key string
	argFiles := -1
	format := fmtText
	argstate := 0
	t.uiEnc = localeCodeset(t.env)

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case argstate == 1:
			t.dicname = a
			argstate = 0
		case argstate == 2:
			t.privdicname = a
			t.hasPrivdic = true
			argstate = 0
		case argstate == 3:
			t.ioEnc = a
			argstate = 0
		case argstate == 4:
			key = a
			argstate = 0
		case a == "-d":
			argstate = 1
		case a == "-p":
			argstate = 2
		case a == "-i":
			argstate = 3
		case a == "-P":
			argstate = 4
		case a == "-h" || a == "--help":
			t.errf("%s", usage)
			return 0
		case a == "-vv" || a == "-v" || a == "--version":
			fmt.Fprint(t.stdout, pipeHeading)
			fmt.Fprint(t.stdout, "\n")
			if a != "-vv" {
				fmt.Fprint(t.stdout, "\nCopyright (C) 2002-2022 L\303\241szl\303\263 N\303\251meth. License: MPL/GPL/LGPL.\n\n"+
					"Based on OpenOffice.org's Myspell library.\n"+
					"Myspell's copyright (C) Kevin Hendricks, 2001-2002, License: BSD.\n\n"+
					"This is free software; see the source for copying conditions.  There is NO\n"+
					"warranty; not even for MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE,\n"+
					"to the extent permitted by law.\n")
			}
			return 0
		case a == "-a":
			t.filterMode = modePipe
		case a == "-m":
			if t.filterMode != modePipe {
				t.filterMode = modeAnalyze
			}
		case a == "--trace":
			t.printtrace = true
		case a == "--no-default-personal":
			t.nodefaultpriv = true
		case a == "-s":
			if t.filterMode != modePipe {
				t.filterMode = modeStem
			}
		case a == "-S":
			if t.filterMode != modePipe {
				t.filterMode = modeSuffix
			}
		case a == "-t":
			format = fmtLaTeX
		case a == "-n":
			format = fmtMan
		case a == "-H":
			format = fmtHTML
		case a == "-X":
			format = fmtXML
		case a == "-O":
			format = fmtODF
		case a == "-l":
			t.filterMode = modeBadword
		case a == "-w":
			if t.filterMode != modePipe {
				t.filterMode = modeWordfilter
			}
		case a == "-L":
			if t.filterMode != modePipe {
				t.filterMode = modeBadline
			}
		case a == "-u":
			if t.filterMode != modePipe {
				t.filterMode = modeAuto0
			}
		case a == "-U":
			if t.filterMode != modePipe {
				t.filterMode = modeAuto
			}
		case a == "-u2":
			if t.filterMode != modePipe {
				t.filterMode = modeAuto2
			}
		case a == "-u3":
			if t.filterMode != modePipe {
				t.filterMode = modeAuto3
			}
		case a == "-G":
			t.printgood = true
		case a == "-1":
			format = fmtFirst
		case a == "-D":
			t.showpath = true
		case a == "-r":
			t.warn = true
		case a == "--check-url":
			t.checkurl = true
		case a == "--check-apostrophe":
			t.checkapos = true
		case argFiles == -1 && a != "" && a[0] != '-':
			argFiles = i
			if !exist(a) {
				t.errf("Can't open %s.\n", a)
				return 1
			}
		}
	}

	t.multipleFiles = argFiles >= 0 && len(args)-argFiles > 1

	if t.printgood && t.filterMode == modeNormal {
		t.filterMode = modeBadword
	}
	// The trace decorates whatever the other options asked for, and on its
	// own it just checks each word.
	if t.printtrace && t.filterMode == modeNormal {
		t.filterMode = modeTrace
	}

	if t.dicname == "" {
		if t.dicname = t.env("DICTIONARY"); t.dicname == "" {
			for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
				if s := t.env(v); s != "" {
					t.dicname = s
					if i := strings.IndexByte(t.dicname, '.'); i >= 0 {
						t.dicname = t.dicname[:i]
					}
					if i := strings.IndexByte(t.dicname, '@'); i >= 0 {
						t.dicname = t.dicname[:i]
					}
					break
				}
			}
			if t.dicname == "" || t.dicname == "C" || t.dicname == "POSIX" {
				t.dicname = "en_US"
			}
		}
	}

	path := t.searchPath()

	if t.showpath {
		t.errf("SEARCH PATH:\n%s\n", strings.Join(path, t.plat.pathSep))
		t.errf("AVAILABLE DICTIONARIES (path is not mandatory for -d option):\n")
		for _, dir := range path {
			t.listdicpath(dir)
		}
	}

	if !t.hasPrivdic {
		if w := t.env("WORDLIST"); w != "" {
			t.privdicname = w
			t.hasPrivdic = true
		}
	}

	names := strings.Split(t.dicname, ",")
	t.dicname = names[0]
	aff := t.search(path, names[0], ".aff")
	dic := t.search(path, names[0], ".dic")
	if aff == "" || dic == "" {
		t.errf("Can't open affix or dictionary files for dictionary named \"%s\".\n", names[0])
		return 1
	}
	if t.showpath {
		t.errf("LOADED DICTIONARY:\n%s\n%s\n", aff, dic)
	}
	t.load(aff, dic, key)
	for _, name2 := range names[1:] {
		aff = t.search(path, name2, ".aff")
		dic = t.search(path, name2, ".dic")
		if aff != "" && dic != "" {
			if len(t.pMS) < dmax {
				t.load(aff, dic, key)
				if t.showpath {
					t.errf("LOADED DICTIONARY:\n%s\n%s\n", aff, dic)
				}
			} else {
				t.errf("error - %s exceeds dictionary limit.\n", name2)
			}
		} else if dic != "" {
			h := t.pMS[len(t.pMS)-1]
			n := len(h.Errors())
			h.AddDic(core.Source{Path: dic}, "")
			for _, e := range h.Errors()[n:] {
				t.errf("%s", e)
			}
		}
	}

	if t.showpath && argFiles == -1 {
		return 0
	}

	if t.printtrace {
		for i, h := range t.pMS {
			enc := t.dicEnc[i]
			h.SetTrace(func(depth int, line string) {
				line = t.chenc(cstr(line), enc, t.uiEnc)
				fmt.Fprintf(t.stdout, "%s%s\n", strings.Repeat(" ", depth*2), line)
			})
		}
	}

	// open the private dictionaries
	if home := t.env(t.plat.homeVar); home != "" {
		base := basename(t.dicname, t.plat.dirSep)
		if !t.nodefaultpriv {
			t.loadPrivdic(home + t.plat.homeSep + t.plat.dicBaseName + base)
		}
		if !t.hasPrivdic {
			if !t.nodefaultpriv {
				t.loadPrivdic(t.plat.dicBaseName + base)
			}
		} else {
			t.loadPrivdic(home + t.plat.homeSep + t.privdicname)
		}
	}
	// a personal dictionary named with -p is loaded as given, so it is
	// honoured even when no home directory is set
	if t.hasPrivdic {
		t.loadPrivdic(t.privdicname)
	}

	if t.filterMode == modePipe {
		fmt.Fprint(t.stdout, pipeHeading)
	}

	if argFiles == -1 {
		t.pipeInterface(format, stdin, "")
	} else if t.filterMode != modeNormal {
		for _, f := range args[argFiles:] {
			fh, err := os.Open(f)
			if err != nil {
				t.errf("Can't open %s.\n", f)
				return 1
			}
			t.currentfilename = f
			ok := t.pipeInterface(format, fh, f)
			fh.Close()
			if !ok {
				return 1
			}
		}
	} else {
		t.errf("Hunspell has been compiled without Ncurses user interface.\n")
	}
	return 0
}

func cstr(s string) string {
	if i := strings.IndexByte(s, 0); i >= 0 {
		return s[:i]
	}
	return s
}

// unlimited lifts the time limits of the suggestion search and of the
// generation, so that the tests get all the suggestions the reference tools
// built without them give.
var unlimited bool

func newHunspell(aff, dic, key string) *core.Hunspell {
	h := core.New(core.Source{Path: aff}, core.Source{Path: dic}, key)
	if unlimited {
		h.SetTimeLimits(0, 0, 0)
	}
	return h
}

func (t *tool) load(aff, dic, key string) {
	h := newHunspell(aff, dic, key)
	for _, e := range h.Errors() {
		t.errf("%s", e)
	}
	t.pMS = append(t.pMS, h)
	t.dicEnc = append(t.dicEnc, h.Encoding())
}

func exist(name string) bool {
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func (t *tool) searchPath() []string {
	p := t.plat
	path := []string{".", ""}
	var dirs []string
	addDirs := func(list, suffix string, absoluteOnly bool) {
		for _, d := range strings.Split(list, p.pathSep) {
			if d == "" || (absoluteOnly && d[0] != '/') {
				continue
			}
			d += suffix
			if !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	if d := t.env("DICPATH"); d != "" {
		addDirs(d, "", false)
	}
	home := t.env(p.homeVar)
	if !p.windows {
		// XDG Base Directory Specification: relative entries are invalid,
		// and an unset or empty variable means its default
		if x := t.env("XDG_DATA_HOME"); x != "" && x[0] == '/' {
			addDirs(x, "/hunspell", false)
		} else if home != "" {
			addDirs(home+"/.local/share", "/hunspell", false)
		}
		if x := t.env("XDG_DATA_DIRS"); x != "" {
			addDirs(x, "/hunspell", true)
		} else {
			addDirs("/usr/local/share:/usr/share", "/hunspell", true)
		}
	}
	addDirs(p.libDir, "", false)
	if home != "" {
		for _, d := range p.userOOODirs {
			addDirs(home+string(p.dirSep)+d, "", false)
		}
		addDirs(p.oooDir, "", false)
		// LibreOffice installs each dictionary extension in its own
		// share/extensions/dict-XX; a folder already on the path is not
		// searched for them
		first := len(dirs)
		addDirs(t.loDir(), "", false)
		lodirs := append([]string(nil), dirs[first:]...)
		dirs = dirs[:first]
		for _, lodir := range lodirs {
			entries, err := os.ReadDir(lodir)
			if err != nil {
				continue
			}
			var subdirs []string
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "dict-") && isDir(lodir+string(p.dirSep)+e.Name()) {
					subdirs = append(subdirs, lodir+string(p.dirSep)+e.Name())
				}
			}
			sort.Strings(subdirs)
			for _, s := range subdirs {
				addDirs(s, "", false)
			}
		}
	}
	return append(path, dirs...)
}

// loDirs replaces LODIR in tests.
var loDirs []string

func (t *tool) loDir() string {
	if loDirs != nil {
		return strings.Join(loDirs, t.plat.pathSep)
	}
	return t.plat.loDir
}

// isDir reports whether path is a folder, following symbolic links like
// stat(2).
func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func (t *tool) exist2(dir, name, ext string) string {
	buf := name + ext
	if dir != "" {
		buf = dir + string(t.plat.dirSep) + buf
	}
	if exist(buf) || exist(buf+".hz") {
		return buf
	}
	return ""
}

func (t *tool) search(path []string, name, ext string) string {
	for _, dir := range path {
		if r := t.exist2(dir, name, ext); r != "" {
			return r
		}
	}
	return ""
}

func (t *tool) listdicpath(dir string) {
	buf, open := "", "."
	if dir != "" {
		buf = dir + string(t.plat.dirSep)
		open = buf
	} else if !t.plat.windows {
		return // an empty path entry is not opened (opendir("") fails)
	}
	// on Windows FindFirstFile(buf + "*") lists the current folder for the
	// empty entry
	entries, err := os.ReadDir(open)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "hyph_") {
			continue
		}
		if t.plat.windows && (name[0] == '.' || e.IsDir() || isHidden(filepath.Join(open, name))) {
			continue // hidden files, dot files and folders are skipped
		}
		switch {
		case len(name) > 4 && strings.HasSuffix(name, ".dic"):
			t.errf("%s%s\n", buf, name[:len(name)-4])
		case len(name) > 7 && strings.HasSuffix(name, ".dic.hz"):
			t.errf("%s%s\n", buf, name[:len(name)-7])
		}
	}
}

func (t *tool) putdic(inWord string, h *core.Hunspell, context string) int {
	// the word is converted to the encoding of the first dictionary, whichever
	// dictionary it goes to
	word := t.chencCtx(inWord, t.uiEnc, t.dicEnc[0], context)
	word, _ = h.InputConv(word)
	if word == "" {
		return 0
	}
	w := -1
	if k := strings.IndexByte(word[1:], '/'); k >= 0 {
		w = k + 1
	}
	if w < 0 {
		if word[0] == '*' {
			return h.Remove(word[1:])
		}
		return h.Add(word)
	}
	affix := word[w+1:]
	word = word[:w]
	if affix != "" && affix[0] == '/' { // word//pattern (back comp.)
		affix = affix[1:]
	}
	return h.AddWithAffix(word, affix) // word/pattern
}

func (t *tool) loadPrivdic(filename string) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return
	}
	// the lines as std::getline reads them
	text := string(data)
	for linenum := 1; text != ""; linenum++ {
		line := text
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			line, text = text[:i], text[i+1:]
		} else {
			text = ""
		}
		ctx := fmt.Sprintf("%s:%d: '%s'", filename, linenum, line)
		t.putdic(line, t.pMS[0], ctx)
	}
}

func (t *tool) saveDicwords(w *[]string) int {
	if !t.hasPrivdic && t.nodefaultpriv {
		*w = nil
		return 1
	}
	home := t.env(t.plat.homeVar)
	if home == "" {
		t.errf("error - missing HOME variable\n")
		return -1
	}
	sbuf := home + t.plat.homeSep
	offset := len(sbuf)
	if !t.hasPrivdic {
		sbuf += t.plat.dicBaseName + basename(t.dicname, t.plat.dirSep)
	} else {
		sbuf += t.privdicname
	}
	filename := sbuf[offset:]
	target := sbuf
	if exist(filename) {
		target = filename
	}
	f, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0
	}
	for _, s := range *w {
		fmt.Fprintf(f, "%s\n", t.chenc(s, t.ioEnc, t.uiEnc))
	}
	f.Close()
	*w = nil
	return 1
}

// check checks a token in the dictionaries, starting with the one that
// knew the last word.
func (t *tool) check(d *int, token string, info *int, root *string) bool {
	for i := 0; i < len(t.pMS); i++ {
		buf := t.chenc(token, t.ioEnc, t.dicEnc[*d])
		buf = strings.ReplaceAll(buf, entityApos, "'")
		if t.checkapos && strings.IndexByte(buf, '\'') >= 0 {
			return false
		}
		// 8-bit encoded dictionaries need ASCII apostrophes (eg. English dictionaries)
		if t.dicEnc[*d] != "UTF-8" {
			buf = strings.ReplaceAll(buf, utf8Apos, "'")
		}
		h := t.pMS[*d]
		if h.Spell(buf, info, root) && !(t.warn && *info&core.SpellWarn != 0) {
			return true
		}
		// UTF-8 encoded dictionaries with ASCII apostrophes, but without
		// ICONV support, need also ASCII apostrophes (eg. French dictionaries)
		if t.dicEnc[*d] == "UTF-8" && strings.Contains(buf, utf8Apos) &&
			h.Spell(strings.ReplaceAll(buf, utf8Apos, "'"), info, root) && !(t.warn && *info&core.SpellWarn != 0) {
			return true
		}
		*d++
		if *d == len(t.pMS) {
			*d = 0
		}
	}
	return false
}

func (t *tool) getParser(format int, extension string) parsers.Parser {
	h := t.pMS[0]
	denc := h.Encoding()
	ioUTF8 := false
	if t.ioEnc != "" {
		switch t.ioEnc {
		case "UTF-8", "utf-8", "UTF8", "utf8":
			ioUTF8 = true
			t.ioEnc = "UTF-8"
		}
	} else {
		// nl_langinfo always names a codeset, so the I/O encoding is the
		// locale's, never the dictionary's
		t.ioEnc = t.uiEnc
		ioUTF8 = t.uiEnc == "UTF-8"
	}

	if ioUTF8 {
		t.wordcharsUTF16 = h.WordCharsUTF16()
		if denc != "UTF-8" && h.WordChars() != "" {
			conv, ok := convertWordchars(h.WordChars(), denc)
			if !ok {
				t.errf("error - iconv_open: %s -> UTF-8\n", denc)
				t.wordcharsUTF16 = nil
			} else {
				w := core.UTF16(conv)
				sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
				t.wordcharsUTF16 = w
			}
		}
	} else {
		// 8-bit input encoding: detect letters by unicodeisalpha() for tokenization
		var letters []byte
		enc := charset.Lookup(t.ioEnc)
		if enc == nil {
			t.errf("error - iconv_open: %s -> UTF-8\n", t.ioEnc)
		} else {
			for i := 32; i < 256; i++ {
				u, ok := enc.ToUTF8(string([]byte{byte(i)}))
				if ok {
					c, _ := core.FirstUTF16(u)
					if core.UnicodeIsAlpha(c) {
						letters = append(letters, byte(i))
					}
				}
			}
		}
		// UTF-8 wordchars -> 8 bit wordchars
		wc := h.WordChars()
		if l := len(wc); l > 0 {
			if denc == "UTF-8" {
				l = len(h.WordCharsUTF16())
			}
			src := wc
			if l+1 < len(src) {
				src = src[:l+1]
			}
			if charset.Lookup(t.ioEnc) == nil || charset.Lookup(denc) == nil {
				t.errf("error - iconv_open: %s -> %s\n", denc, t.ioEnc)
			} else {
				conv, _ := charset.Convert(src, denc, t.ioEnc)
				letters = append(letters, cstr(conv)...)
			}
		}
		t.wordchars = string(letters)
	}

	var p parsers.Parser
	switch format {
	case fmtLaTeX:
		if ioUTF8 {
			p = parsers.NewLaTeXUTF8(t.wordcharsUTF16)
		} else {
			p = parsers.NewLaTeX(t.wordchars)
		}
	case fmtHTML:
		if ioUTF8 {
			p = parsers.NewHTMLUTF8(t.wordcharsUTF16)
		} else {
			p = parsers.NewHTML(t.wordchars)
		}
	case fmtMan:
		if ioUTF8 {
			p = parsers.NewManUTF8(t.wordcharsUTF16)
		} else {
			p = parsers.NewMan(t.wordchars)
		}
	case fmtXML:
		if ioUTF8 {
			p = parsers.NewXMLUTF8(t.wordcharsUTF16)
		} else {
			p = parsers.NewXML(t.wordchars)
		}
	case fmtODF:
		if ioUTF8 {
			p = parsers.NewODFUTF8(t.wordcharsUTF16)
		} else {
			p = parsers.NewODF(t.wordchars)
		}
	case fmtFirst:
		p = parsers.NewFirst(t.wordchars)
	}
	if p == nil && extension != "" {
		switch {
		case extension == "html" || extension == "htm" || extension == "xhtml":
			if ioUTF8 {
				p = parsers.NewHTMLUTF8(t.wordcharsUTF16)
			} else {
				p = parsers.NewHTML(t.wordchars)
			}
		case extension == "xml":
			if ioUTF8 {
				p = parsers.NewXMLUTF8(t.wordcharsUTF16)
			} else {
				p = parsers.NewXML(t.wordchars)
			}
		case (len(extension) == 3 && strings.Contains(odfExt, extension)) ||
			(len(extension) == 4 && extension[0] == 'f' && strings.Contains(odfExt, extension[1:])):
			if ioUTF8 {
				p = parsers.NewODFUTF8(t.wordcharsUTF16)
			} else {
				p = parsers.NewODF(t.wordchars)
			}
		case extension[0] > '0' && extension[0] <= '9':
			if ioUTF8 {
				p = parsers.NewManUTF8(t.wordcharsUTF16)
			} else {
				p = parsers.NewMan(t.wordchars)
			}
		case extension == "tex":
			if ioUTF8 {
				p = parsers.NewLaTeXUTF8(t.wordcharsUTF16)
			} else {
				p = parsers.NewLaTeX(t.wordchars)
			}
		}
	}
	if p == nil {
		if ioUTF8 {
			p = parsers.NewTextUTF8(t.wordcharsUTF16)
		} else {
			p = parsers.NewText(t.wordchars)
		}
	}
	p.SetURLChecking(t.checkurl)
	return p
}

// convertWordchars converts the 8-bit word characters of a dictionary to
// UTF-8, keeping what converted before a failure, as iconv does.
func convertWordchars(wc, denc string) (string, bool) {
	if charset.Lookup(denc) == nil {
		return "", false
	}
	out, _ := charset.Convert(wc, denc, "UTF-8")
	return out, true
}

// lineReader reads lines the way fgets does with a fixed buffer: a long
// line comes in several pieces.
type lineReader struct {
	r *bufio.Reader
}

func (l *lineReader) next() (string, bool) {
	var b []byte
	for len(b) < maxLnLen-1 {
		c, err := l.r.ReadByte()
		if err != nil {
			if len(b) == 0 {
				return "", false
			}
			break
		}
		b = append(b, c)
		if c == '\n' {
			break
		}
	}
	line := string(b)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line, true
}

func basename(s string, c byte) string {
	if i := strings.LastIndexByte(s, c); i >= 0 {
		return s[i+1:]
	}
	return s
}

// pipeInterface checks the input; it reports false when the tool has to
// exit with an error.
func (t *tool) pipeInterface(format int, in io.Reader, filename string) bool {
	var dicwords []string
	lineno := 0
	terseMode := false
	verboseMode := false
	d := 0
	// decided before the parser settles the I/O encoding, so the offsets of
	// the first input are byte offsets unless -i named UTF-8 exactly
	ioIsUTF8 := t.ioEnc == "UTF-8"
	prefix := ""
	if t.multipleFiles {
		prefix = filename + ": "
	}
	extension := ""
	if filename != "" {
		extension = basename(filename, '.')
	}
	parser := t.getParser(format, extension)

	if filename != "" && isZippedODF(parser, extension) {
		if !secureFilename(filename) {
			t.errf("Can't open %s.\n", filename)
			return false
		}
		in = bytes.NewReader(readODFContent(filename))
	}

	if t.filterMode == modeNormal {
		fmt.Fprint(t.stdout, normalHeading+Version)
		if v := t.pMS[0].Version(); v != "" {
			fmt.Fprintf(t.stdout, " - %s", v)
		}
		fmt.Fprint(t.stdout, "\n")
	}

	lr := &lineReader{r: bufio.NewReader(in)}
nextline:
	for {
		raw, ok := lr.next()
		if !ok {
			break
		}
		buf := cstr(raw)
		lineno++
		bad := false
		pos := 0
		// execute commands
		if t.filterMode == modePipe {
			pos = -1
			switch byteAt(buf, 0) {
			case '%':
				verboseMode = false
				terseMode = false
			case '!':
				terseMode = true
			case '`':
				verboseMode = true
			case '+':
				parser = t.getParser(fmtLaTeX, "")
				parser.SetURLChecking(t.checkurl)
			case '-':
				parser = t.getParser(format, "")
			case '@':
				t.putdic(buf[1:], t.pMS[d], "")
			case '*':
				word := buf[1:]
				dicwords = append(dicwords, word)
				t.putdic(word, t.pMS[d], "")
			case '#':
				if t.saveDicwords(&dicwords) < 0 {
					continue
				}
			case '^':
				pos = 1
			case '~':
				// The ispell line that sets the string character type from a
				// filename is accepted and ignored, so it is not checked as a word.
			default:
				pos = 0
			}
		}
		if pos < 0 {
			continue
		}
		parser.PutLine(buf[pos:])
		prevByteOffset, prevCharOffset := 0, 0
		for {
			token, ok := parser.NextToken()
			if !ok {
				break
			}
			token = parser.Word(token)
			token = strings.ReplaceAll(token, entityApos, "'")
			switch t.filterMode {
			case modeBadword:
				var info int
				if !t.check(&d, token, &info, nil) {
					bad = true
					if !t.printgood {
						fmt.Fprintf(t.stdout, "%s%s\n", prefix, token)
					}
				} else if t.printgood {
					fmt.Fprintf(t.stdout, "%s%s\n", prefix, token)
				}
			case modeWordfilter:
				var info int
				if !t.check(&d, parser.Word(token), &info, nil) {
					if !t.printgood {
						fmt.Fprintf(t.stdout, "%s\n", buf)
					}
				} else if t.printgood {
					fmt.Fprintf(t.stdout, "%s\n", buf)
				}
				continue nextline
			case modeBadline:
				var info int
				if !t.check(&d, parser.Word(token), &info, nil) {
					bad = true
				}
			case modeAuto0, modeAuto, modeAuto2, modeAuto3:
				var f io.Writer = t.stdout
				if t.filterMode == modeAuto {
					f = t.stderr
					t.stdout.Flush()
				}
				var info int
				if !t.check(&d, parser.Word(token), &info, nil) {
					bad = true
					wlst := t.pMS[d].Suggest(t.chenc(parser.Word(token), t.ioEnc, t.dicEnc[d]))
					if len(wlst) > 0 {
						bestIO := t.chenc(wlst[0], t.dicEnc[d], t.ioEnc)
						origToken := token
						parser.ChangeToken(bestIO)
						// consume the replacement token to avoid re-checking it
						parser.NextToken()
						switch t.filterMode {
						case modeAuto3:
							fmt.Fprintf(f, "%s:%d: Locate: %s | Try: %s\n", t.currentfilename, lineno,
								t.chenc(origToken, t.ioEnc, t.uiEnc), t.chenc(wlst[0], t.dicEnc[d], t.uiEnc))
						case modeAuto2:
							fmt.Fprintf(f, "%ds/%s/%s/g; # %s\n", lineno, origToken, bestIO, buf)
						default:
							fmt.Fprintf(f, "Line %d: %s -> ", lineno, t.chenc(origToken, t.ioEnc, t.uiEnc))
							fmt.Fprintf(f, "%s\n", t.chenc(wlst[0], t.dicEnc[d], t.uiEnc))
						}
					} else if t.filterMode == modeAuto3 {
						fmt.Fprintf(f, "%s:%d: Locate: %s\n", t.currentfilename, lineno, t.chenc(token, t.ioEnc, t.uiEnc))
					}
				}
			case modeStem, modeAnalyze:
				fn := t.pMS[d].Stem
				if t.filterMode == modeAnalyze {
					fn = t.pMS[d].Analyze
				}
				result := fn(t.chenc(token, t.ioEnc, t.dicEnc[d]))
				if len(result) == 0 && token != "" && token[len(token)-1] == '.' {
					token = token[:len(token)-1]
					result = fn(t.chenc(token, t.ioEnc, t.dicEnc[d]))
				}
				tokenUI := t.chenc(token, t.ioEnc, t.uiEnc)
				for _, r := range result {
					fmt.Fprintf(t.stdout, "%s %s\n", tokenUI, cstr(t.chenc(r, t.dicEnc[d], t.uiEnc)))
				}
				if len(result) == 0 {
					fmt.Fprintf(t.stdout, "%s\n", tokenUI)
				}
				fmt.Fprint(t.stdout, "\n")
			case modeSuffix:
				for _, j := range t.pMS[d].SuffixSuggest(t.chenc(token, t.ioEnc, t.dicEnc[d])) {
					fmt.Fprintf(t.stdout, "Suffix Suggestions are %s \n", t.chenc(j, t.dicEnc[d], t.uiEnc))
				}
			case modeTrace:
				t.pMS[d].Spell(t.chenc(token, t.ioEnc, t.dicEnc[d]), nil, nil)
			case modePipe, modeNormal:
				var info int
				var root string
				if t.check(&d, parser.Word(token), &info, &root) {
					if t.filterMode == modePipe {
						if !terseMode {
							if verboseMode {
								fmt.Fprintf(t.stdout, "* %s\n", token)
							} else {
								fmt.Fprint(t.stdout, "*\n")
							}
						}
					} else {
						switch {
						case info&core.SpellCompound != 0:
							fmt.Fprint(t.stdout, "-\n")
						case root != "":
							fmt.Fprintf(t.stdout, "+ %s\n", cstr(t.chenc(root, t.dicEnc[d], t.uiEnc)))
						default:
							fmt.Fprint(t.stdout, "*\n")
						}
					}
				} else {
					byteOffset := parser.TokenPos() + pos
					charOffset := prevCharOffset
					if ioIsUTF8 {
						for i := prevByteOffset; i < byteOffset; i++ {
							if byteAt(buf, i)&0xc0 != 0x80 {
								charOffset++
							}
						}
					} else {
						charOffset = byteOffset
					}
					prevByteOffset = byteOffset
					prevCharOffset = charOffset
					wlst := t.pMS[d].Suggest(t.chenc(token, t.ioEnc, t.dicEnc[d]))
					outEnc := t.ioEnc
					tok := token
					if t.filterMode == modeNormal {
						outEnc = t.uiEnc
						tok = t.chenc(token, t.ioEnc, t.uiEnc)
					}
					for j := range wlst {
						wlst[j] = cstr(t.chenc(wlst[j], t.dicEnc[d], outEnc))
					}
					if len(wlst) == 0 {
						fmt.Fprintf(t.stdout, "# %s %d", tok, charOffset)
					} else {
						fmt.Fprintf(t.stdout, "& %s %d %d: %s", tok, len(wlst), charOffset, wlst[0])
					}
					for _, s := range wlst[min(1, len(wlst)):] {
						fmt.Fprintf(t.stdout, ", %s", s)
					}
					fmt.Fprint(t.stdout, "\n")
				}
			}
		}
		switch t.filterMode {
		case modeAuto:
			fmt.Fprintf(t.stdout, "%s\n", parser.Line())
		case modeBadline:
			if (t.printgood && !bad) || (!t.printgood && bad) {
				fmt.Fprintf(t.stdout, "%s\n", buf)
			}
		case modePipe, modeNormal:
			fmt.Fprint(t.stdout, "\n")
		}
		t.stdout.Flush()
	}
	return true
}

func byteAt(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

const usage = `Usage: hunspell [OPTION]... [FILE]...
Check spelling of each FILE. Without FILE, check standard input.

  -1		check only first field in lines (delimiter = tabulator)
  -a		Ispell's pipe interface
  --check-url	check URLs, e-mail addresses and directory paths
  --check-apostrophe	check Unicode typographic apostrophe
  -d d[,d2,...]	use d (d2 etc.) dictionaries
  -D		show available dictionaries
  -G		print only correct words or lines
  -h, --help	display this help and exit
  -H		HTML input file format
  -i enc	input encoding
  -l		print misspelled words
  -L		print lines with misspelled words
  -m 		analyze the words of the input text
  -n		nroff/troff input file format
  -O		OpenDocument (ODF or Flat ODF) input file format
  -p dict	set dict custom dictionary
  --no-default-personal
		don't use the default personal dictionary
  -r		warn of the potential mistakes (rare words)
  -P password	set password for encrypted dictionaries
  -s 		stem the words of the input text
  -S 		suffix words of the input text
  -t		TeX/LaTeX input file format
  --trace	report how each input word was decided
  -u		print the line number, misspelled word and first suggestion
  -U		print the input text with each misspelled word replaced by its
		first suggestion
  -u2		print the replacements that -U would make, as a sed script
  -u3		print the file name and line number with each misspelled word
  -v, --version	print version number
  -vv		print Ispell compatible version number
  -w		print misspelled words (= lines) from one word/line input.
  -X		XML input file format

Example: hunspell -d en_US file.txt    # interactive spelling
         hunspell -i utf-8 file.txt    # check UTF-8 encoded file
         hunspell -l *.odt             # print misspelled words of ODF files

         # Quick fix of ODF documents by personal dictionary creation

         # 1 Make a reduced list from misspelled and unknown words:

         hunspell -l *.odt | sort | uniq >words

         # 2 Delete misspelled words of the file by a text editor.
         # 3 Use this personal dictionary to fix the deleted words:

         hunspell -p words *.odt

Bug reports: http://hunspell.github.io/
`
