package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/documatrix/go-hunspell/parsers"
)

// TestMainDefaultEnv checks that Main reads the process environment when it
// is given none.
func TestMainDefaultEnv(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(goldenDir, "out", "version.golden"))
	if err != nil {
		t.Fatal(err)
	}
	parts, _, err := decodeParts(want)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Main([]string{"-v"}, nil, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out.String() != string(parts["stdout"]) {
		t.Errorf("-v printed %q, want %q", out.String(), parts["stdout"])
	}
}

// searchPathOf returns the search path -D prints.
func searchPathOf(t *testing.T, env map[string]string) []string {
	t.Helper()
	var out, errb bytes.Buffer
	env["LC_ALL"] = "C"
	Main([]string{"-D", "-d", "nonexistent"}, func(k string) string { return env[k] },
		strings.NewReader(""), &out, &errb)
	lines := strings.Split(errb.String(), "\n")
	if len(lines) < 2 || lines[0] != "SEARCH PATH:" {
		t.Fatalf("unexpected -D output:\n%s", errb.String())
	}
	return strings.Split(lines[1], ":")
}

// TestLibreOfficeExtensions checks the dict-* folders of the LibreOffice
// extension folders on the search path. The expectations are what the C++
// tool printed with /opt/libreoffice/share/extensions holding the folders
// dict-b, dict-a, dict-c and other, and a file dict-file.
func TestLibreOfficeExtensions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the search path is a colon separated list of POSIX paths")
	}
	ext := filepath.Join(t.TempDir(), "extensions")
	for _, d := range []string{"dict-b", "dict-a", "other", "dict-c"} {
		if err := os.MkdirAll(filepath.Join(ext, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ext, "dict-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	saved := loDirs
	defer func() { loDirs = saved }()
	loDirs = []string{ext, filepath.Join(ext, "nonexistent")}
	sub := func(s string) string { return ext + "/" + s }

	// at the end of the path, sorted, only the dict-* folders
	path := searchPathOf(t, map[string]string{"HOME": "/h"})
	if got, want := strings.Join(path[len(path)-3:], " "), sub("dict-a")+" "+sub("dict-b")+" "+sub("dict-c"); got != want {
		t.Errorf("extensions: got %s, want %s", got, want)
	}
	if got := path[len(path)-4]; got != "/usr/lib/openoffice.org2.0/share/dict/ooo" {
		t.Errorf("extensions do not follow the OpenOffice folders: %s", got)
	}
	// an extensions folder already on the path is not searched
	path = searchPathOf(t, map[string]string{"HOME": "/h", "DICPATH": ext})
	if path[2] != ext {
		t.Errorf("DICPATH is not the third entry: %v", path)
	}
	for _, p := range path {
		if strings.HasPrefix(p, ext+"/") {
			t.Errorf("%s is on the path, although its extensions folder came from DICPATH", p)
		}
	}
	// an extension folder already on the path keeps its place
	path = searchPathOf(t, map[string]string{"HOME": "/h", "DICPATH": sub("dict-a")})
	if path[2] != sub("dict-a") {
		t.Errorf("DICPATH is not the third entry: %v", path)
	}
	if got, want := strings.Join(path[len(path)-2:], " "), sub("dict-b")+" "+sub("dict-c"); got != want {
		t.Errorf("extensions: got %s, want %s", got, want)
	}
	// without a home folder there are no OpenOffice and LibreOffice folders
	path = searchPathOf(t, map[string]string{})
	if got := path[len(path)-1]; got != "/Library/Spelling" {
		t.Errorf("without HOME the path ends with %s", got)
	}
}

// TestZippedODF checks which files are unzipped: those the ODF parser reads,
// unless their extension starts with f (flat ODF). An empty extension (a file
// name ending with a dot) is not a flat one, as in is_zipped_odf of the C++
// tool, but such names are not portable, so the golden cases leave it out.
func TestZippedODF(t *testing.T) {
	odf := parsers.NewODF(parsers.ASCIILetters)
	for _, c := range []struct {
		p    parsers.Parser
		ext  string
		want bool
	}{
		{odf, "odt", true},
		{odf, "", true},
		{odf, "fodt", false},
		{odf, "f", false},
		{parsers.NewXML(parsers.ASCIILetters), "odt", false},
		{parsers.NewText(parsers.ASCIILetters), "odt", false},
	} {
		if got := isZippedODF(c.p, c.ext); got != c.want {
			t.Errorf("isZippedODF(%T, %q) = %v, want %v", c.p, c.ext, got, c.want)
		}
	}
}
