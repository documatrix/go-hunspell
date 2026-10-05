// Package core is a line by line port of the Hunspell library to Go: the
// hash manager, the affix manager, the suggestion manager and the Hunspell
// class, with the C string semantics of the original where they decide the
// results.
//
// The character tables are generated from the Hunspell sources:
//
//	HUNSPELL_SRC=/path/to/hunspell/src/hunspell go generate ./internal/core
package core

//go:generate sh -c "go run ../gen/tables -src \"$HUNSPELL_SRC\" -out ."
