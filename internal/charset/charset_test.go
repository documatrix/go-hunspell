package charset

// The expected conversions were checked with the iconv tool of glibc: a
// conversion stops at the first sequence it cannot convert.

import "testing"

func TestLookup(t *testing.T) {
	tests := []struct {
		name  string
		utf8  bool
		known bool
	}{
		{"UTF-8", true, true},
		{"utf8", true, true},
		{"ASCII", false, true},
		{"US-ASCII", false, true},
		{"ANSI_X3.4-1968", false, true},
		{"ISO646-US", false, true},
		{"POSIX", false, true},
		{"ISO8859-1", false, true},
		{"iso-8859-15", false, true},
		{"latin1", false, true},
		{"Latin-2", false, true},
		{"KOI8-R", false, true},
		{"KOI8-U", false, true},
		{"microsoft-cp1251", false, true},
		{"windows-1252", false, true},
		{"TIS-620", false, true},
		{"TIS620-2533", false, true},
		{"cyrillic", false, true},
		{"ISCII-DEVANAGARI", false, false},
		{"X-UNKNOWN", false, false},
		{"", false, false},
	}
	for _, tt := range tests {
		e := Lookup(tt.name)
		if (e != nil) != tt.known {
			t.Errorf("Lookup(%q) = %v", tt.name, e)
			continue
		}
		if e == nil {
			continue
		}
		if e.IsUTF8() != tt.utf8 {
			t.Errorf("Lookup(%q).IsUTF8() = %v", tt.name, e.IsUTF8())
		}
		if !tt.utf8 && !e.ascii && e.Name() != tt.name {
			t.Errorf("Lookup(%q).Name() = %q", tt.name, e.Name())
		}
	}
	if Lookup("utf-8").Name() != "UTF-8" || Lookup("us").Name() != "ASCII" {
		t.Error("names of UTF-8 and ASCII")
	}
	if Normalize("ISO_8859-1:1987") != "iso885911987" {
		t.Errorf("Normalize = %q", Normalize("ISO_8859-1:1987"))
	}
}

type conv struct {
	in, out string
	ok      bool
}

func check(t *testing.T, what string, f func(string) (string, bool), tests []conv) {
	t.Helper()
	for _, tt := range tests {
		out, ok := f(tt.in)
		if out != tt.out || ok != tt.ok {
			t.Errorf("%s(%q) = %q, %v, want %q, %v", what, tt.in, out, ok, tt.out, tt.ok)
		}
	}
}

func TestToUTF8(t *testing.T) {
	check(t, "UTF-8 ToUTF8", Lookup("UTF-8").ToUTF8, []conv{
		{"", "", true},
		{"aéb€😀", "aéb€😀", true},
		{"a\xffb", "a", false},
		{"a\xc0\xafb", "a", false},          // overlong
		{"a\xed\xa0\x80b", "a", false},      // surrogate
		{"a\xf4\x90\x80\x80", "a", false},   // past U+10FFFF
		{"ab\xc3", "ab", false},             // incomplete
		{"\xef\xbf\xbd", "�", true},         // U+FFFD itself
		{"x\x00y", "x\x00y", true},          // NUL
		{"\xe2\x82\xac\x80", "€", false},    // stray continuation byte
		{"\xc3\xa9\xc3", "é", false},        // incomplete after a letter
		{"abc\xe2\x82", "abc", false},       // incomplete 3-byte sequence
		{"\xf0\x9f\x98\x80", "😀", true},     // 4-byte sequence
		{"\xf8\x88\x80\x80\x80", "", false}, // 5-byte form
	})
	check(t, "ASCII ToUTF8", Lookup("ASCII").ToUTF8, []conv{
		{"abc", "abc", true},
		{"ab\x80c", "ab", false},
	})
	check(t, "ISO8859-1 ToUTF8", Lookup("ISO8859-1").ToUTF8, []conv{
		{"a\xe9b\xff", "aébÿ", true},
	})
	check(t, "ISO8859-3 ToUTF8", Lookup("ISO8859-3").ToUTF8, []conv{
		{"a\xa5b", "a", false}, // 0xA5 is not a character of ISO-8859-3
	})
	check(t, "CP1252 ToUTF8", Lookup("cp1252").ToUTF8, []conv{
		{"\x80", "€", true},
		{"\x81", "", false},
	})
	check(t, "TIS-620 ToUTF8", Lookup("TIS-620").ToUTF8, []conv{
		{"\xa1", "ก", true},
		{"\xff", "", false},
	})
}

func TestFromUTF8(t *testing.T) {
	check(t, "UTF-8 FromUTF8", Lookup("UTF-8").FromUTF8, []conv{
		{"aé", "aé", true},
		{"a\xff", "a", false},
	})
	check(t, "ASCII FromUTF8", Lookup("ASCII").FromUTF8, []conv{
		{"abc", "abc", true},
		{"abé", "ab", false},
		{"ab\xff", "ab", false},
	})
	check(t, "ISO8859-2 FromUTF8", Lookup("ISO8859-2").FromUTF8, []conv{
		{"aéb", "a\xe9b", true},
		{"xàb", "x", false}, // à is not a character of ISO-8859-2
		{"x\xc3", "x", false},
	})
	check(t, "KOI8-R FromUTF8", Lookup("KOI8-R").FromUTF8, []conv{{"Ж", "\xf6", true}})
	check(t, "KOI8-U FromUTF8", Lookup("KOI8-U").FromUTF8, []conv{{"Ґ", "\xbd", true}})
	check(t, "CP1252 FromUTF8", Lookup("windows-1252").FromUTF8, []conv{{"€", "\x80", true}})
}

func TestConvert(t *testing.T) {
	tests := []struct {
		s, from, to string
		out         string
		ok          bool
	}{
		{"", "UTF-8", "ISO8859-1", "", true},
		{"é", "", "ISO8859-1", "é", true},
		{"é", "UTF-8", "", "é", true},
		// equal names are not converted, even when the text is invalid
		{"\xff", "UTF-8", "UTF-8", "\xff", true},
		// equal encodings with different names are
		{"\xff", "UTF-8", "utf8", "", false},
		{"é", "X-UNKNOWN", "UTF-8", "é", false},
		{"é", "UTF-8", "X-UNKNOWN", "é", false},
		{"aéb", "UTF-8", "ISO8859-1", "a\xe9b", true},
		{"a\xe9b", "ISO8859-1", "UTF-8", "aéb", true},
		{"a\xe9b", "ISO8859-1", "ISO8859-2", "a\xe9b", true},
		{"x\xe0b", "ISO8859-1", "ISO8859-2", "x", false},
		{"a\x80b", "ASCII", "ISO8859-1", "a", false},
		{"\xe6\xc5", "KOI8-R", "cp1251", "\xd4\xe5", true}, // Фе
	}
	for _, tt := range tests {
		out, ok := Convert(tt.s, tt.from, tt.to)
		if out != tt.out || ok != tt.ok {
			t.Errorf("Convert(%q, %q, %q) = %q, %v, want %q, %v", tt.s, tt.from, tt.to, out, ok, tt.out, tt.ok)
		}
	}
}

func TestToUTF8Lossy(t *testing.T) {
	tests := []struct {
		enc, in, out string
	}{
		{"UTF-8", "a\xffb\xc3", "a�b�"},
		{"UTF-8", "aé", "aé"},
		{"ASCII", "a\x80b", "a�b"},
		{"ISO8859-3", "a\xa5\xe9", "a�é"},
		{"ISO8859-1", "", ""},
	}
	for _, tt := range tests {
		if got := Lookup(tt.enc).ToUTF8Lossy(tt.in); got != tt.out {
			t.Errorf("%s ToUTF8Lossy(%q) = %q, want %q", tt.enc, tt.in, got, tt.out)
		}
	}
}

func TestTables(t *testing.T) {
	// every table maps ASCII to itself and back
	for name := range tables {
		e := Lookup(name)
		for c := 0; c < 0x80; c++ {
			s := string(rune(c))
			if u, ok := e.ToUTF8(s); !ok || u != s {
				t.Errorf("%s: ToUTF8(%#x) = %q, %v", name, c, u, ok)
			}
			if b, ok := e.FromUTF8(s); !ok || b != s {
				t.Errorf("%s: FromUTF8(%#x) = %q, %v", name, c, b, ok)
			}
		}
		// and its other characters round trip
		for c := 0x80; c < 0x100; c++ {
			s := string([]byte{byte(c)})
			u, ok := e.ToUTF8(s)
			if !ok {
				continue
			}
			if b, ok := e.FromUTF8(u); !ok || b != s {
				t.Errorf("%s: %#x -> %q -> %q, %v", name, c, u, b, ok)
			}
		}
	}
}
