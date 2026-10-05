package core

import (
	"fmt"
	"strings"
)

// TraceFunc receives one record of a trace at a time. depth is the nesting
// level of the record; the line carries no indentation of its own.
type TraceFunc func(depth int, line string)

// traceCtx collects the records of the --trace mode.
type traceCtx struct {
	callback   TraceFunc
	depth      int
	suppressed int
}

func (t *traceCtx) on() bool { return t.callback != nil && t.suppressed == 0 }

func (t *traceCtx) emit(line string) { t.callback(t.depth, line) }

// traceOn returns the context while something listens to it, else nil.
func traceOn(t *traceCtx) *traceCtx {
	if t != nil && t.on() {
		return t
	}
	return nil
}

// enter holds the nesting level one deeper until the returned function runs.
func (t *traceCtx) enter() func() {
	if t == nil {
		return func() {}
	}
	t.depth++
	return func() { t.depth-- }
}

// suppress silences the context until the returned function runs.
func suppress(t *traceCtx) func() {
	if t == nil {
		return func() {}
	}
	t.suppressed++
	return func() { t.suppressed-- }
}

func trace(t *traceCtx, format string, args ...interface{}) {
	t.emit(fmt.Sprintf(format, args...))
}

// traceFlag formats a flag in the dictionary's own syntax.
func traceFlag(a *AffixMgr, flag uint16) string {
	if flag == onlyUpcaseFlag {
		return "(onlyupcase)"
	}
	return a.encodeFlag(flag)
}

// traceFlags formats a flag list; an empty list prints as (none).
func traceFlags(a *AffixMgr, astr []uint16) string {
	if a == nil || len(astr) == 0 {
		return "(none)"
	}
	parts := make([]string, len(astr))
	for i, f := range astr {
		parts[i] = traceFlag(a, f)
	}
	return strings.Join(parts, ",")
}

func traceAffix(t *traceCtx, verb string, a *AffixMgr, e *affEntry) {
	redundant := ""
	if e.opts&aeRedundantCond != 0 {
		redundant = " redundant=Y"
	}
	xprod := ""
	if e.xprod != 0 {
		xprod = string([]byte{e.xprod})
	}
	trace(t, "%s flag=%s strip=\"%s\" add=\"%s\" cont=%s cond=\"%s\"%s at=aff:%d xprod=%s hdr=aff:%d",
		verb, traceFlag(a, e.aflag), e.strip, e.appnd, traceFlags(a, e.contclass),
		e.getCondition(verb == "sfx"), redundant, e.line, xprod, e.headerline)
}

func traceTest(t *traceCtx, name string, a *AffixMgr, flag uint16, where string, astr []uint16, outcome string) {
	trace(t, "test %s flag=%s in=%s have=%s -> %s", name, traceFlag(a, flag), where, traceFlags(a, astr), outcome)
}

func traceCircumfix(t *traceCtx, a *AffixMgr, flag uint16, pfx *PfxEntry, sfx *SfxEntry, inPrefix, inSuffix bool) {
	var outcome string
	switch {
	case inPrefix && inSuffix:
		outcome = "pass, both halves carry the flag"
	case !inPrefix && !inSuffix:
		outcome = "pass, neither affix is half of a circumfix"
	case inSuffix:
		outcome = "fail, this circumfix suffix needs its prefix"
	default:
		outcome = "fail, this circumfix prefix needs its suffix"
	}
	pfxCont := "(none)"
	if pfx != nil {
		pfxCont = traceFlags(a, pfx.contclass)
	}
	sfxCont := traceFlags(a, sfx.contclass)
	trace(t, "test circumfix flag=%s pfx-cont=%s sfx-cont=%s -> %s", traceFlag(a, flag), pfxCont, sfxCont, outcome)
}

func traceForm(t *traceCtx, a *AffixMgr, surface, stem string, pfx *PfxEntry, sfx *SfxEntry) {
	line := "form \"" + surface + "\" = "
	if pfx != nil {
		line += "pfx(" + a.encodeFlag(pfx.aflag) + ":\"" + pfx.appnd + "\") + "
	}
	line += "\"" + stem + "\""
	if sfx != nil {
		line += " + sfx(" + a.encodeFlag(sfx.aflag) + ":\"" + sfx.appnd + "\")"
	}
	t.emit(line)
}
