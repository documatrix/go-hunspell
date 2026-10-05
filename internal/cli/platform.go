package cli

import "runtime"

// platform holds what the hunspell tool defines differently on Windows
// (the WIN32 branch of hunspell.cxx) and elsewhere.
type platform struct {
	windows     bool
	homeVar     string   // HOME: getenv("HOME") or getenv("USERPROFILE")
	homeSep     string   // what the tool puts between HOME and a file name
	dicBaseName string   // DICBASENAME, the prefix of personal dictionaries
	dirSep      byte     // DIRSEPCH
	pathSep     string   // PATHSEP
	libDir      string   // LIBDIR
	userOOODirs []string // USEROOODIR, relative to HOME
	oooDir      string   // OOODIR
	loDir       string   // LODIR
}

// unixPlatform is the non-Windows branch, with DATADIR /usr/share.
var unixPlatform = platform{
	homeVar:     "HOME",
	homeSep:     "/",
	dicBaseName: ".hunspell_",
	dirSep:      '/',
	pathSep:     ":",
	libDir: "/usr/share/hunspell:/usr/share/myspell:/usr/share/myspell/dicts:" +
		"/usr/share/hunspell:/usr/share/myspell:/usr/share/myspell/dicts:/Library/Spelling",
	userOOODirs: []string{".openoffice.org/3/user/wordbook", ".openoffice.org2/user/wordbook",
		".openoffice.org2.0/user/wordbook", ".config/libreoffice/4/user/wordbook", "Library/Spelling"},
	oooDir: "/opt/openoffice.org/basis3.0/share/dict/ooo:/usr/lib/openoffice.org/basis3.0/share/dict/ooo:" +
		"/opt/openoffice.org2.4/share/dict/ooo:/usr/lib/openoffice.org2.4/share/dict/ooo:" +
		"/opt/openoffice.org2.3/share/dict/ooo:/usr/lib/openoffice.org2.3/share/dict/ooo:" +
		"/opt/openoffice.org2.2/share/dict/ooo:/usr/lib/openoffice.org2.2/share/dict/ooo:" +
		"/opt/openoffice.org2.1/share/dict/ooo:/usr/lib/openoffice.org2.1/share/dict/ooo:" +
		"/opt/openoffice.org2.0/share/dict/ooo:/usr/lib/openoffice.org2.0/share/dict/ooo",
	loDir: "/opt/libreoffice/share/extensions:/usr/lib/libreoffice/share/extensions:" +
		"/usr/lib64/libreoffice/share/extensions",
}

// windowsPlatform is the WIN32 branch. Like the C++ tool it joins HOME
// (USERPROFILE) and a personal dictionary name without a separator.
var windowsPlatform = platform{
	windows:     true,
	homeVar:     "USERPROFILE",
	homeSep:     "",
	dicBaseName: "hunspell_",
	dirSep:      '\\',
	pathSep:     ";",
	libDir:      `C:\Hunspell\`,
	userOOODirs: []string{`AppData\Roaming\hunspell`, `Application Data\OpenOffice.org 2\user\wordbook`,
		`AppData\Roaming\LibreOffice\4\user\wordbook`},
	oooDir: `C:\Program files\OpenOffice.org 2.4\share\dict\ooo\;` +
		`C:\Program files\OpenOffice.org 2.3\share\dict\ooo\;` +
		`C:\Program files\OpenOffice.org 2.2\share\dict\ooo\;` +
		`C:\Program files\OpenOffice.org 2.1\share\dict\ooo\;` +
		`C:\Program files\OpenOffice.org 2.0\share\dict\ooo\`,
	loDir: `C:\Program Files\LibreOffice\share\extensions;C:\Program Files (x86)\LibreOffice\share\extensions`,
}

// hostPlatform is the platform the tool runs on.
var hostPlatform = platformFor(runtime.GOOS)

// platformFor returns the conventions of the hunspell tool on goos.
func platformFor(goos string) platform {
	if goos == "windows" {
		return windowsPlatform
	}
	return unixPlatform
}
