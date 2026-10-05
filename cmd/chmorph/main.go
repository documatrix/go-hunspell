// Command chmorph changes affixes by morphological analysis and generation,
// like Hunspell's chmorph tool.
//
//	chmorph affix_file dictionary_file file_to_convert STRING1 STRING2
package main

import (
	"os"

	"github.com/documatrix/go-hunspell/internal/cli"
)

func main() {
	os.Exit(cli.Chmorph(os.Args[1:], os.Stdout, os.Stderr))
}
