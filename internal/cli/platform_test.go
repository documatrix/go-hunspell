package cli

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWindowsSearchPath checks the search path of the WIN32 branch of the
// C++ tool: PATHSEP ";", no XDG folders, LIBDIR, USEROOODIR under
// USERPROFILE and OOODIR.
func TestWindowsSearchPath(t *testing.T) {
	env := map[string]string{
		"LC_ALL": "C", "DICPATH": `D:\dicts;;D:\more`, "USERPROFILE": `C:\Users\me`,
		"HOME": "/home/ignored", "XDG_DATA_DIRS": "/ignored", "XDG_DATA_HOME": "/ignored",
	}
	var out, errb bytes.Buffer
	mainOn(windowsPlatform, []string{"-D", "-d", "nonexistent"}, func(k string) string { return env[k] },
		strings.NewReader(""), &out, &errb)
	want := strings.Join([]string{".", "", `D:\dicts`, `D:\more`, `C:\Hunspell\`,
		`C:\Users\me\AppData\Roaming\hunspell`,
		`C:\Users\me\Application Data\OpenOffice.org 2\user\wordbook`,
		`C:\Users\me\AppData\Roaming\LibreOffice\4\user\wordbook`,
		`C:\Program files\OpenOffice.org 2.4\share\dict\ooo\`,
		`C:\Program files\OpenOffice.org 2.3\share\dict\ooo\`,
		`C:\Program files\OpenOffice.org 2.2\share\dict\ooo\`,
		`C:\Program files\OpenOffice.org 2.1\share\dict\ooo\`,
		`C:\Program files\OpenOffice.org 2.0\share\dict\ooo\`}, ";")
	lines := strings.Split(errb.String(), "\n")
	if len(lines) < 2 || lines[1] != want {
		t.Errorf("search path\n got %q\nwant %q", lines[1], want)
	}
	// without USERPROFILE only DICPATH and LIBDIR
	delete(env, "USERPROFILE")
	errb.Reset()
	mainOn(windowsPlatform, []string{"-D", "-d", "nonexistent"}, func(k string) string { return env[k] },
		strings.NewReader(""), &out, &errb)
	if got := strings.Split(errb.String(), "\n")[1]; got != `.;;D:\dicts;D:\more;C:\Hunspell\` {
		t.Errorf("search path without USERPROFILE: %q", got)
	}
}

// TestWindowsPersonalDictionary checks the personal dictionary of the WIN32
// branch: USERPROFILE and "hunspell_" + the dictionary name, joined without
// a separator as the C++ tool does.
func TestWindowsPersonalDictionary(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "me")
	abs, _ := filepath.Abs(testDir)
	t.Chdir(abs) // a bare dictionary name, found in the current folder
	dic := "base"
	env := map[string]string{"LC_ALL": "C", "USERPROFILE": profile}
	run := func(stdin string) string {
		var out, errb bytes.Buffer
		mainOn(windowsPlatform, []string{"-a", "-d", dic}, func(k string) string { return env[k] },
			strings.NewReader(stdin), &out, &errb)
		return out.String()
	}
	// *word adds the word, # saves the personal dictionary
	run("*gohunspell\n#\n")
	name := profile + "hunspell_" + dic
	data, err := os.ReadFile(name)
	if err != nil || string(data) != "gohunspell\n" {
		t.Fatalf("personal dictionary %s: %q, %v", name, data, err)
	}
	// it is loaded again
	if out := run("gohunspell\n"); !strings.Contains(out, "\n*\n") {
		t.Errorf("the saved word is not known:\n%s", out)
	}
}

// TestWindowsListing checks -D on the empty search path entry: on Windows
// FindFirstFile("*") lists the current folder, without dot files and
// folders; elsewhere opendir("") fails.
func TestWindowsListing(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"aa.dic", ".hidden.dic", "hyph_x.dic", "bb.dic.hz"} {
		os.WriteFile(filepath.Join(dir, f), nil, 0o644)
	}
	os.Mkdir(filepath.Join(dir, "cc.dic"), 0o755)
	t.Chdir(dir)
	for _, tc := range []struct {
		p    platform
		want []string
	}{
		{windowsPlatform, []string{"aa", "bb"}},
		{unixPlatform, nil},
	} {
		tl := &tool{plat: tc.p, env: func(string) string { return "" }}
		var out, errb bytes.Buffer
		tl.stdout, tl.stderr = bufio.NewWriter(&out), &errb
		tl.listdicpath("")
		got := strings.Fields(errb.String())
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("windows=%v: listing of the empty entry %q, want %q", tc.p.windows, got, tc.want)
		}
	}
}

func TestPlatformFor(t *testing.T) {
	if !platformFor("windows").windows || platformFor("linux").windows || platformFor("darwin").windows {
		t.Error("platformFor picks the wrong conventions")
	}
	if hostPlatform.windows != (runtime.GOOS == "windows") {
		t.Error("hostPlatform does not match the host")
	}
}
