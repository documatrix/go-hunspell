package gohunspell

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// libreOfficeDirs are the folders LibreOffice installs its dictionary
// extensions in, each in a dict-* subfolder.
var libreOfficeDirs = []string{"/opt/libreoffice/share/extensions", "/usr/lib/libreoffice/share/extensions",
	"/usr/lib64/libreoffice/share/extensions"}

// SearchPaths returns the folders the hunspell tool searches for
// dictionaries: the folders of the DICPATH environment variable, the XDG
// data folders and the usual system folders of Hunspell, MySpell and
// LibreOffice dictionaries.
func SearchPaths() []string {
	var dirs []string
	add := func(list, suffix string, absoluteOnly bool) {
		for _, d := range filepath.SplitList(list) {
			if d == "" || (absoluteOnly && !filepath.IsAbs(d)) {
				continue
			}
			d += suffix
			for _, x := range dirs {
				if x == d {
					d = ""
					break
				}
			}
			if d != "" {
				dirs = append(dirs, d)
			}
		}
	}
	add(os.Getenv("DICPATH"), "", false)
	home, _ := os.UserHomeDir()
	if x := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(x) {
		add(x, "/hunspell", false)
	} else if home != "" {
		add(filepath.Join(home, ".local", "share"), "/hunspell", false)
	}
	if x := os.Getenv("XDG_DATA_DIRS"); x != "" {
		add(x, "/hunspell", true)
	} else {
		add("/usr/local/share"+string(filepath.ListSeparator)+"/usr/share", "/hunspell", true)
	}
	// the fixed folders of the hunspell tool, searched even when
	// XDG_DATA_DIRS leaves out /usr/share
	for _, d := range []string{"/usr/share/hunspell", "/usr/share/myspell", "/usr/share/myspell/dicts", "/Library/Spelling"} {
		add(d, "", false)
	}
	if home != "" {
		add(filepath.Join(home, "Library", "Spelling"), "", false)
	}
	for _, lodir := range libreOfficeDirs {
		entries, err := os.ReadDir(lodir)
		if err != nil {
			continue
		}
		var sub []string
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "dict-") {
				continue
			}
			// like stat(2) in the hunspell tool, follow symbolic links
			p := filepath.Join(lodir, e.Name())
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				sub = append(sub, p)
			}
		}
		sort.Strings(sub)
		for _, s := range sub {
			add(s, "", false)
		}
	}
	return dirs
}

// Find returns the affix and dictionary file of the dictionary called name
// (for example "en_US") in the given folders, or in SearchPaths when none
// are given. A name with a folder in it is tried as it is first. An .aff or
// .dic extension of name is ignored.
func Find(name string, paths ...string) (aff, dic string, err error) {
	if len(paths) == 0 {
		paths = SearchPaths()
	}
	name = strings.TrimSuffix(strings.TrimSuffix(name, ".aff"), ".dic")
	candidates := append([]string{""}, paths...)
	for _, dir := range candidates {
		base := name
		if dir != "" {
			if filepath.IsAbs(name) {
				break
			}
			base = filepath.Join(dir, name)
		}
		if fileExists(base+".aff") && fileExists(base+".dic") {
			return base + ".aff", base + ".dic", nil
		}
	}
	return "", "", fmt.Errorf("gohunspell: dictionary %q not found: %w", name, os.ErrNotExist)
}

// Load finds the dictionary called name (see Find) and opens it.
func Load(name string, paths ...string) (*Dictionary, error) {
	aff, dic, err := Find(name, paths...)
	if err != nil {
		return nil, err
	}
	return Open(aff, dic)
}

// Available lists the names of the dictionaries in the given folders, or in
// SearchPaths when none are given. Hyphenation tables are left out.
func Available(paths ...string) []string {
	if len(paths) == 0 {
		paths = SearchPaths()
	}
	seen := map[string]bool{}
	var names []string
	for _, dir := range paths {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, "hyph_") || e.IsDir() {
				continue
			}
			var base string
			switch {
			case strings.HasSuffix(n, ".dic.hz"):
				base = strings.TrimSuffix(n, ".dic.hz")
			case strings.HasSuffix(n, ".dic"):
				base = strings.TrimSuffix(n, ".dic")
			default:
				continue
			}
			if base == "" || !fileExists(filepath.Join(dir, base+".aff")) || seen[base] {
				continue
			}
			seen[base] = true
			names = append(names, base)
		}
	}
	sort.Strings(names)
	return names
}
