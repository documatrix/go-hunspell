package core

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestGoldenMalformed loads mutated affix files of the upstream tests (a
// line deleted, duplicated, truncated or with a field replaced, see
// internal/gen/golden/mutants.py) and compares the diagnostics and the API
// results with those of the C++ library.
func TestGoldenMalformed(t *testing.T) {
	files, err := filepath.Glob("../../testdata/golden/malformed/*.txt.gz")
	if err != nil || len(files) == 0 {
		t.Fatal("no golden files", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".txt.gz")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := readGzip(t, file)
			base := filepath.Join("../../testdata/hunspell", name)
			aff, err := os.ReadFile(base + ".aff")
			if err != nil {
				t.Fatal(err)
			}
			dic, err := os.ReadFile(base + ".dic")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "m.dic"), dic, 0o644); err != nil {
				t.Fatal(err)
			}
			header, body, _ := bytes.Cut(data, []byte("\n"))
			example := strings.TrimPrefix(string(header), "# example: ")
			for _, sec := range strings.Split(string(body), "=== ")[1:] {
				op, rest, _ := strings.Cut(sec, "\n")
				var warn []string
				var out strings.Builder
				var words []string
				for _, l := range strings.SplitAfter(rest, "\n") {
					if w, ok := strings.CutPrefix(l, "! "); ok {
						warn = append(warn, strings.TrimSuffix(w, "\n"))
						continue
					}
					out.WriteString(l)
					if w, ok := strings.CutPrefix(l, "> "); ok {
						words = append(words, strings.TrimSuffix(w, "\n"))
					}
				}
				mutated := mutate(t, aff, op)
				if err := os.WriteFile(filepath.Join(dir, "m.aff"), mutated, 0o644); err != nil {
					t.Fatal(err)
				}
				h := New(Source{Path: filepath.Join(dir, "m.aff")}, Source{Path: filepath.Join(dir, "m.dic")}, "")
				h.SetTimeLimits(0, 0, 0)
				var got []string
				for _, m := range append(slices.Clone(h.Errors()), h.Warnings()...) {
					m = strings.ReplaceAll(m, dir+string(filepath.Separator), "")
					got = append(got, strings.Split(strings.TrimSuffix(m, "\n"), "\n")...)
				}
				slices.Sort(got)
				slices.Sort(warn)
				if !slices.Equal(got, warn) {
					t.Errorf("%s: diagnostics\nwant %q\ngot  %q", op, warn, got)
				}
				t.Run(op, func(t *testing.T) {
					compareLines(t, out.String(), apiDump(h, words, example))
				})
			}
		})
	}
}

func readGzip(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// asciiFields splits a line at ASCII white space, like the generator.
func asciiFields(line []byte) [][]byte {
	return bytes.FieldsFunc(line, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\r' || r == '\v' || r == '\f'
	})
}

// mutate applies a mutation of mutants.py to an affix file.
func mutate(t *testing.T, aff []byte, op string) []byte {
	lines := bytes.Split(aff, []byte("\n"))
	p := strings.SplitN(op, " ", 4)
	l, _ := strconv.Atoi(p[1])
	switch p[0] {
	case "del":
		lines = slices.Delete(lines, l, l+1)
	case "dup":
		lines = slices.Insert(lines, l, lines[l])
	case "trunc":
		k, _ := strconv.Atoi(p[2])
		lines[l] = bytes.Join(asciiFields(lines[l])[:k], []byte(" "))
	case "set":
		f := asciiFields(lines[l])
		i, _ := strconv.Atoi(p[2])
		tok := ""
		if len(p) > 3 {
			tok = p[3]
		}
		f[i] = []byte(tok)
		lines[l] = bytes.Join(f, []byte(" "))
	default:
		t.Fatalf("bad mutation %q", op)
	}
	return bytes.Join(lines, []byte("\n"))
}
