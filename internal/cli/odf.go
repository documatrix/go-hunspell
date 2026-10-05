package cli

import (
	"archive/zip"
	"io"
	"regexp"

	"github.com/documatrix/go-hunspell/parsers"
)

// isZippedODF reports whether a file is read with the ODF parser, but it is
// not flat ODF (.fodt etc.).
func isZippedODF(p parsers.Parser, extension string) bool {
	x, ok := p.(*parsers.XMLParser)
	return ok && x.IsODF() && (extension == "" || extension[0] != 'f')
}

// secureFilename reports whether a file name is safe in the shell command
// the hunspell tool extracts content.xml with.
func secureFilename(filename string) bool {
	for i := 0; i < len(filename); i++ {
		c := filename[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '.' || c == '-' ||
			c == '_' || c == '/' || c == ' ' || c == '~') {
			return false
		}
	}
	return true
}

var (
	odfBreak = regexp.MustCompile(`(</text:p>|</style:style>)(.)`)
	// sed works line by line, so a tag is not removed across lines
	odfSpan = regexp.MustCompile(`</?text:span[^>\n]*>`)
)

// readODFContent reads content.xml of an ODF document, with its one-line XML
// broken at </style:style> and </text:p>, and the text:span tags removed,
// as the hunspell tool does with unzip and sed. Like unzip -p, it gives
// nothing for a broken archive or a missing content.xml.
func readODFContent(filename string) []byte {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return nil
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name != "content.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		// unzip -p writes what it decompressed even when the checksum
		// turns out to be wrong
		data, _ := io.ReadAll(rc)
		rc.Close()
		data = odfBreak.ReplaceAll(data, []byte("$1\n$2"))
		return odfSpan.ReplaceAll(data, nil)
	}
	return nil
}
