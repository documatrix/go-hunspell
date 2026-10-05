package core

import (
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// The tests of this file run out of time at chosen points of a search: they
// replace clock with a fake one that stands still until a chosen read, and
// then jumps two hours ahead, past every time limit. From there on the search
// has to stop the way the C++ code stops at the same place.

// clockWrappers are the functions that read clock for another one.
var clockWrappers = []string{
	"since",
	"timeExceeded",
	"(*Hunspell).expired",
	"(*AffixMgr).cpdClockExceeded",
	"(*SuggestMgr).suggestTimeout",
}

var closureName = regexp.MustCompile(`\.func\d+(\.\d+)*$`)

// clockReader returns the functions that read the clock, innermost first and
// separated by " < ", up to the first one that is not a wrapper, for example
// "since < (*SuggestMgr).suggestTimeout < (*SuggestMgr).suggest". A closure
// counts as the function it is in.
func clockReader() string {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var path []string
	for {
		f, more := frames.Next()
		name := f.Function[strings.LastIndex(f.Function, "/")+1:]
		name = closureName.ReplaceAllString(strings.TrimPrefix(name, "core."), "")
		if name == "(*fakeClock).since" {
			name = "since"
		}
		path = append(path, name)
		if !slices.Contains(clockWrappers, name) || !more {
			return strings.Join(path, " < ")
		}
	}
}

// fakeClock jumps ahead at the fireAt-th read by reader.
type fakeClock struct {
	base   time.Time
	reader string
	reads  int
	fireAt int
	fired  bool
}

// since is time.Since on the fake clock.
func (c *fakeClock) since(t time.Time) time.Duration { return c.now().Sub(t) }

func (c *fakeClock) now() time.Time {
	if !c.fired && clockReader() == c.reader {
		c.reads++
		c.fired = c.reads == c.fireAt
	}
	if c.fired {
		return c.base.Add(2 * time.Hour)
	}
	return c.base
}

// sweepClock calls run once for each of the first maxReads reads of the clock
// by reader, running out of time at that read, and passes the number of the
// read and the result of run to check. It returns the number of reads by
// reader, up to maxReads.
func sweepClock[T any](t *testing.T, reader string, maxReads int, run func() T, check func(k int, got T)) int {
	t.Helper()
	defer func(c func() time.Time, s func(time.Time) time.Duration) { clock, since = c, s }(clock, since)
	for k := 1; k <= maxReads; k++ {
		c := &fakeClock{base: time.Now(), reader: reader, fireAt: k}
		clock, since = c.now, c.since
		got := run()
		if !c.fired {
			return k - 1
		}
		check(k, got)
	}
	return maxReads
}

// subset reports whether the results of a search that ran out of time are
// among those of the full search.
func subset(part, full []string) bool {
	for _, s := range part {
		if !slices.Contains(full, s) {
			return false
		}
	}
	return true
}

// clockDict loads a dictionary of testdata/golden/extra, or one made to reach
// every step of the suggestion search (clock8 and clockutf, the latter in
// UTF-8), with compound words (cpd8 and cpdutf), without time limits.
func clockDict(t *testing.T, name string) *Hunspell {
	aff := "TRY esianrtolcdugmphbyfvkwz\nMAP 2\nMAP aá\nMAP eé\nREP 2\nREP f ph\nREP ph f\n" +
		"SFX S Y 1\nSFX S 0 s . is:pl\n"
	const dic = "8\nfoo/XS st:foo\nbar/XS st:bar\nfoobaz\nphotograph/S\nalphabet/S st:alphabet\n" +
		"information/S\nphab/X\nélan/S\n"
	if strings.HasPrefix(name, "cpd") {
		aff += "COMPOUNDFLAG X\nCOMPOUNDMIN 2\nCHECKCOMPOUNDREP\n"
	}
	if strings.HasSuffix(name, "utf") {
		aff = "SET UTF-8\n" + aff
	}
	var h *Hunspell
	switch name {
	case "clock8", "clockutf", "cpd8", "cpdutf":
		h = New(Source{Data: []byte(aff)}, Source{Data: []byte(dic)}, "")
	default:
		h = extraDict(t, name)
	}
	h.SetTimeLimits(time.Hour, time.Hour, time.Hour)
	return h
}

// TestClockSuggest runs out of time at each check of the time limits in the
// suggestion search. The C++ code returns the suggestions found so far from
// each of them (and as the clock jumps past all the limits, every later check
// stops too), so the result is a part of the full result.
func TestClockSuggest(t *testing.T) {
	type sweep struct {
		dict, word string
		readers    []string
	}
	global := []string{
		"since < (*Hunspell).expired < (*Hunspell).suggestInternal",
		"since < (*Hunspell).expired < (*Hunspell).suggest",
		"since < (*Hunspell).expired < (*Hunspell).checkword",
		"since < (*Hunspell).expired < (*Hunspell).spell",
	}
	var sweeps []sweep
	// all capitalization types, with the n-gram suggestions
	for _, w := range []string{"qwxyzzy", "Qwxyzzy", "QWXYZZY", "qwXyzzy", "QwXyzzy", "qwxyzzy.", "Qw.Xyzzy"} {
		sweeps = append(sweeps, sweep{"misc", w, global})
	}
	// the dash rule
	sweeps = append(sweeps, sweep{"cpdmore", "bar-fooo", global}, sweep{"cpdmore", "fooo-bar", global})
	// the steps of SuggestMgr.suggest, in an 8-bit and in an UTF-8 dictionary
	for _, d := range []string{"clock8", "clockutf"} {
		sweeps = append(sweeps, sweep{d, "aeaeaeaeq", []string{
			"since < (*SuggestMgr).suggestTimeout < (*SuggestMgr).mapRelated",
			"since < (*SuggestMgr).checkword",
		}})
		sweeps = append(sweeps, sweep{d, "informaton", []string{
			"since < (*SuggestMgr).suggestTimeout < (*SuggestMgr).suggest",
			"since < (*SuggestMgr).suggestTimeout < (*SuggestMgr).mapRelated",
			"since < (*SuggestMgr).suggestTimeout < (*SuggestMgr).badcharkey",
			"since < (*SuggestMgr).suggestTimeout < (*SuggestMgr).badcharkeyUTF",
			"since < (*SuggestMgr).suggestTimeout < (*SuggestMgr).swapcharUTF",
			"since < (*SuggestMgr).replchars",
			// the timers of the steps that count their candidates
			"since < (*SuggestMgr).checkword",
		}})
	}
	reads := map[string]int{}
	for _, s := range sweeps {
		h := clockDict(t, s.dict)
		full := h.Suggest(s.word)
		for _, reader := range s.readers {
			reads[reader] += sweepClock(t, reader, 60, func() []string { return h.Suggest(s.word) }, func(k int, got []string) {
				if !subset(got, full) {
					t.Errorf("%s: Suggest(%q) out of time at read %d by %s: %q, not part of %q", s.dict, s.word, k, reader, got, full)
				}
			})
		}
	}
	for reader, n := range reads {
		if n == 0 {
			t.Errorf("no clock read by %s", reader)
		}
	}
}

// TestClockCompound runs out of time at each check of the time limit of the
// compound word checks. The C++ code then gives up the compound search, so
// the word is not taken for a compound, unless the time runs out in the
// checks for word pairs and REP faults after the parts were found, which then
// do not forbid it. The trace reports the time limit once.
func TestClockCompound(t *testing.T) {
	reads := map[string]int{}
	for _, d := range []string{"cpd8", "cpdutf"} {
		h := clockDict(t, d)
		for _, reader := range []string{
			"(*AffixMgr).compoundCheck",
			"since < (*AffixMgr).cpdClockExceeded < (*AffixMgr).compoundCheck",
			"since < timeExceeded < (*AffixMgr).cpdrepCheck",
			"since < timeExceeded < (*AffixMgr).cpdwordpairCheck",
		} {
			for _, w := range []string{"foobarfoo", "barphab", "foobaz"} {
				full := h.Spell(w, nil, nil)
				n := sweepClock(t, reader, 100, func() []string {
					var tr []string
					h.SetTrace(func(depth int, line string) { tr = append(tr, line) })
					ok := h.Spell(w, nil, nil)
					h.SetTrace(nil)
					return append(tr, "result "+strings.Repeat("ok", b2i(ok)))
				}, func(k int, tr []string) {
					got := tr[len(tr)-1] == "result ok"
					if got && !full && !strings.HasSuffix(reader, "Check") {
						t.Errorf("%s: %q is a compound only when out of time at read %d by %s", d, w, k, reader)
					}
					limit := slices.DeleteFunc(tr, func(l string) bool { return !strings.HasPrefix(l, "test timelimit") })
					if len(limit) > 1 {
						t.Errorf("%s: %q: %d time limit lines", d, w, len(limit))
					}
				})
				reads[reader] += n
			}
		}
	}
	for reader, n := range reads {
		if n == 0 {
			t.Errorf("no clock read by %s", reader)
		}
	}
}

// TestClockMorph runs out of time in the analysis of compound words and in
// the generation of word forms. The C++ code returns what it has so far.
func TestClockMorph(t *testing.T) {
	reads := map[string]int{}
	for _, d := range []string{"cpd8", "cpdutf"} {
		h := clockDict(t, d)
		full := h.Analyze("foobarfoo")
		for _, reader := range []string{
			"(*AffixMgr).compoundCheckMorph",
			"since < (*AffixMgr).cpdClockExceeded < (*AffixMgr).compoundCheckMorph",
		} {
			reads[reader] += sweepClock(t, reader, 100, func() []string { return h.Analyze("foobarfoo") }, func(k int, got []string) {
				if !subset(got, full) {
					t.Errorf("%s: Analyze out of time at read %d by %s: %q, not part of %q", d, k, reader, got, full)
				}
			})
		}
		for _, c := range []struct{ word, example string }{{"foo", "bars"}, {"alphabet", "foos"}} {
			full := h.Generate(c.word, c.example)
			for _, reader := range []string{
				"since < (*Hunspell).expired < (*Hunspell).GenerateMorph",
				"since < (*SuggestMgr).suggestGen",
			} {
				reads[reader] += sweepClock(t, reader, 100, func() []string { return h.Generate(c.word, c.example) }, func(k int, got []string) {
					if !subset(got, append(full, c.word)) {
						t.Errorf("%s: Generate(%q, %q) out of time at read %d by %s: %q, not part of %q", d, c.word, c.example, k, reader, got, full)
					}
				})
			}
		}
	}
	for reader, n := range reads {
		if n == 0 {
			t.Errorf("no clock read by %s", reader)
		}
	}
}

// TestApplyNoTimeLimits sets the link time switch that raises the default
// time limits.
func TestApplyNoTimeLimits(t *testing.T) {
	defer func(g, s, c time.Duration) { timelimitGlobal, timelimitSuggestion, timelimit = g, s, c }(timelimitGlobal, timelimitSuggestion, timelimit)
	applyNoTimeLimits("")
	if l := defaultTimeLimits(); l != (timeLimits{250 * time.Millisecond, 100 * time.Millisecond, 50 * time.Millisecond}) {
		t.Errorf("default limits %v", l)
	}
	applyNoTimeLimits("1")
	if l := defaultTimeLimits(); l != (timeLimits{time.Hour, time.Hour, time.Hour}) {
		t.Errorf("limits without time limits %v", l)
	}
}
