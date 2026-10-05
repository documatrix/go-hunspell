// Package charset converts between UTF-8 and the 8-bit character encodings
// Hunspell dictionaries can be written in.
//
// The conversions follow iconv(3) the way the hunspell command line tool uses
// it: a conversion stops at the first byte sequence it cannot convert and
// hands back what it converted up to there, together with a failure flag.
package charset

//go:generate go -C ../gen/charmaps run . -out ../../charset/maps.go

import (
	"strings"
	"unicode/utf8"
)

// Encoding is one character encoding.
type Encoding struct {
	name    string
	utf8    bool
	ascii   bool
	toUni   *[256]uint16
	fromUni map[rune]byte
}

var utf8Encoding = &Encoding{name: "UTF-8", utf8: true}
var asciiEncoding = &Encoding{name: "ASCII", ascii: true}

var aliases = map[string]string{
	"latin1":          "iso88591",
	"latin2":          "iso88592",
	"latin3":          "iso88593",
	"latin4":          "iso88594",
	"latin5":          "iso88599",
	"latin6":          "iso885910",
	"latin7":          "iso885913",
	"latin8":          "iso885914",
	"latin9":          "iso885915",
	"latin10":         "iso885916",
	"cyrillic":        "iso88595",
	"arabic":          "iso88596",
	"greek":           "iso88597",
	"hebrew":          "iso88598",
	"tis6202533":      "tis620",
	"tis62025330":     "tis620",
	"tis62025331":     "tis620",
	"microsoftcp1251": "cp1251",
	"windows1250":     "cp1250",
	"windows1251":     "cp1251",
	"windows1252":     "cp1252",
}

// Normalize maps an encoding name to the form Hunspell compares names in:
// ASCII letters lowered and everything but letters and digits dropped.
func Normalize(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 0x20)
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Lookup returns the encoding with the given name, or nil when it is unknown.
func Lookup(name string) *Encoding {
	n := Normalize(name)
	switch n {
	case "utf8":
		return utf8Encoding
	case "ascii", "usascii", "ansix341968", "ansix341986", "iso646us", "us", "c", "posix":
		return asciiEncoding
	}
	if a, ok := aliases[n]; ok {
		n = a
	}
	t, ok := tables[n]
	if !ok {
		return nil
	}
	e := &Encoding{name: name, toUni: t, fromUni: make(map[rune]byte, 256)}
	for i := 255; i >= 0; i-- {
		if t[i] != 0xFFFF {
			e.fromUni[rune(t[i])] = byte(i)
		}
	}
	return e
}

// IsUTF8 reports whether e is UTF-8.
func (e *Encoding) IsUTF8() bool { return e.utf8 }

// Name returns the name the encoding was looked up by.
func (e *Encoding) Name() string { return e.name }

// decodeUTF8 decodes one UTF-8 sequence the way glibc does, rejecting
// overlong forms, surrogates and code points past U+10FFFF.
func decodeUTF8(s string) (rune, int) {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size <= 1 {
		return utf8.RuneError, 0
	}
	return r, size
}

// ToUTF8 converts s from e to UTF-8.
func (e *Encoding) ToUTF8(s string) (string, bool) {
	switch {
	case e.utf8:
		for i := 0; i < len(s); {
			_, n := decodeUTF8(s[i:])
			if n == 0 {
				return s[:i], false
			}
			i += n
		}
		return s, true
	case e.ascii:
		for i := 0; i < len(s); i++ {
			if s[i] >= 0x80 {
				return s[:i], false
			}
		}
		return s, true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		u := e.toUni[s[i]]
		if u == 0xFFFF {
			return b.String(), false
		}
		b.WriteRune(rune(u))
	}
	return b.String(), true
}

// FromUTF8 converts the UTF-8 string s to e.
func (e *Encoding) FromUTF8(s string) (string, bool) {
	if e.utf8 {
		return e.ToUTF8(s)
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := decodeUTF8(s[i:])
		if n == 0 {
			return b.String(), false
		}
		if e.ascii {
			if r >= 0x80 {
				return b.String(), false
			}
			b.WriteByte(byte(r))
		} else {
			c, ok := e.fromUni[r]
			if !ok {
				return b.String(), false
			}
			b.WriteByte(c)
		}
		i += n
	}
	return b.String(), true
}

// Convert converts s from the encoding named from to the one named to. Names
// that compare equal byte for byte convert nothing, the way the hunspell tool
// skips iconv for them. An unknown encoding fails the conversion and leaves s
// as it was.
func Convert(s, from, to string) (string, bool) {
	if s == "" || from == "" || to == "" || from == to {
		return s, true
	}
	src := Lookup(from)
	dst := Lookup(to)
	if src == nil || dst == nil {
		return s, false
	}
	u, ok := src.ToUTF8(s)
	out, ok2 := dst.FromUTF8(u)
	return out, ok && ok2
}

// ToUTF8Lossy converts s from e to UTF-8, turning what does not convert into
// U+FFFD.
func (e *Encoding) ToUTF8Lossy(s string) string {
	if e.utf8 {
		return strings.ToValidUTF8(s, "�")
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case e.ascii:
			if s[i] < 0x80 {
				b.WriteByte(s[i])
			} else {
				b.WriteRune(utf8.RuneError)
			}
		case e.toUni[s[i]] == 0xFFFF:
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteRune(rune(e.toUni[s[i]]))
		}
	}
	return b.String()
}
