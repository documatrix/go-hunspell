// Command analyze prints the morphological analysis and the stems of the
// words of a file, or generates word forms from "word example" lines, like
// Hunspell's analyze tool.
//
//	analyze affix_file dictionary_file file_of_words_to_check
package main

import (
	"os"

	"github.com/documatrix/go-hunspell/internal/cli"
)

func main() {
	os.Exit(cli.Analyze(os.Args[1:], os.Stdout, os.Stderr))
}
