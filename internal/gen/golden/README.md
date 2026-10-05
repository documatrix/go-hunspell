# Golden reference outputs

`testdata/golden/api/<name>.api` holds the results of the Hunspell C++ API
for words of the upstream test dictionary `<name>`: the dictionary words, the
`.good`, `.wrong` and `.sug` words and 150 seeded mutations of them. For each
word it lists spell (with the info flags and the root), suggest, analyze,
stem, stem of the analyses, suffix_suggest and generate. `TestGoldenAPI`
(internal/core) checks that the Go port gives the same output.

To regenerate (with the Hunspell sources in `$H`):

```sh
# raise the time limits, so the output does not depend on the machine
sed -i 's/milliseconds([0-9]*)/milliseconds(3600000)/' $H/src/hunspell/atypes.hxx
g++ -O1 -std=c++17 -DHUNSPELL_STATIC -I$H/src/hunspell -include config.h \
    -o apidump internal/gen/golden/apidump.cxx $H/src/hunspell/*.cxx
python3 internal/gen/golden/words.py testdata/hunspell /tmp/words 150
cd testdata/hunspell
for f in /tmp/words/*.words; do
  n=$(basename $f .words)
  [ $n = timelimit ] && continue
  { printf '# example: '; head -1 $f; tail -n +2 $f > /tmp/w; ../../apidump $n.aff $n.dic /tmp/w "$(head -1 $f)"; } \
    > ../golden/api/$n.api
done
```

`config.h` is the one of a configured Hunspell build (it only needs the
version and `HAVE_*` macros).

## Extra cases

`testdata/golden/extra` holds small dictionaries written to reach the less
common code paths (PHONE rules, Hungarian compounding, CHECKCOMPOUNDPATTERN,
COMPOUNDRULE with affixes, COMPLEXPREFIXES, the XML API, malformed affix and
dictionary files, hzip files, ...). `TestGoldenExtra` and
`TestGoldenExtraCases` (internal/core) compare them with the C++ library.
Each `<name>.api` (gzipped when big) is its own input: the header names the
example word and optionally the files and the key, `> word` lines are
dumped as above and `@op` lines run the operations of `apidump.cxx` (add,
remove, add_dic, a traced spell, generate and stem with a list, input_conv
and the dictionary info). A `<name>.cases` file holds many small affix files
that share a dictionary and a word list. The diagnostics come from apidump
built with `-DHUNSPELL_WARNING_ON` (the debug builds of the library print
them on stderr):

```sh
g++ -O1 -std=c++17 -DHUNSPELL_STATIC -DHUNSPELL_WARNING_ON -I$H/src/hunspell \
    -include config.h -o apidump_w internal/gen/golden/apidump.cxx $H/src/hunspell/*.cxx
python3 internal/gen/golden/extra.py ./apidump_w testdata/golden/extra   # all files
python3 internal/gen/golden/extra.py ./apidump_w testdata/golden/extra hu.api
```

To add a case, write the dictionary, list the words and operations in a new
`.api` file (or a case in a `.cases` file) and run `extra.py` on it.
`testdata/golden/extra/hz` holds files made by the `hzip` tool, which
`TestHunzipRoundTrip` decompresses (the C++ reader is no reference there).
