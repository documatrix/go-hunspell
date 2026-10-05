// Command hunspell is a Go port of the hunspell spell checker tool. It
// supports the non-interactive modes of the original: the Ispell pipe
// interface (-a), word lists (-l, -G, -w, -L), stemming (-s), analysis (-m),
// automatic correction (-u, -U, -u2, -u3), suffixes (-S) and --trace.
package main

import (
	"os"

	"github.com/documatrix/go-hunspell/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}
