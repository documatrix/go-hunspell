// Package gohunspell is a pure Go port of Hunspell, the spell checker of
// LibreOffice, Firefox, Chrome and macOS.
//
// It reads Hunspell .aff/.dic dictionaries (also hzip compressed .hz files)
// and supports everything they can express: all affix and compounding
// options, the 8-bit encodings, morphological analysis, stemming and
// generation, and Hunspell's suggestion algorithms. The engine is a faithful
// port of the C++ code and passes the complete upstream test suite.
//
// Basic use:
//
//	d, err := gohunspell.Open("en_US.aff", "en_US.dic")
//	if err != nil {
//		log.Fatal(err)
//	}
//	d.Spell("hello")        // true
//	d.Suggest("helo")       // [hello help hell halo ...]
//	d.Stem("walked")        // [walk]
//
// Words are UTF-8 in and out, whatever the encoding of the dictionary.
// CheckText and Tokenize split text (plain, LaTeX, HTML, XML, man, ODF) the
// way the hunspell tool does. A Checker adds word lists on top of a
// dictionary without changing it.
package gohunspell
