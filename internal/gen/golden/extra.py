#!/usr/bin/env python3
"""Regenerates the golden files of testdata/golden/extra with apidump.

usage: extra.py APIDUMP_W DIR [FILE...]

Each DIR/<name>.api is both the input and the output: its "# key: value"
header lines (example, aff, dic, key, and warnings: load to keep only the
diagnostics of the loading), its "> word" lines and its "@op" lines
(see apidump.cxx) are kept, and the rest is replaced by the output of the C++
library: the diagnostics of apidump built with -DHUNSPELL_WARNING_ON as
"! message" lines, then the results for each word and operation. The
dictionary is <name>.aff and <name>.dic unless the header names others; a
named file ending in .gz is unpacked into a temporary directory first.
The big golden files are gzipped (<name>.api.gz).

A DIR/<name>.cases file holds many small affix files that share a .dic file
and a word list: after the header (dic, example and words, a space separated
list), each case starts with a "=== title" line, followed by the lines of its
affix file prefixed with "| ", and the output for it.
"""
import gzip
import os
import shutil
import subprocess
import sys
import tempfile


def run(apidump, d, aff, dic, items, opts):
    """runs apidump in DIR and returns the diagnostics and the output."""
    tmp = tempfile.mkdtemp()

    def path(n):
        # a gzipped file is unpacked into a temporary directory
        if not n.endswith(".gz"):
            return n
        p = os.path.join(tmp, n[:-3])
        with gzip.open(os.path.join(d, n), "rb") as f, open(p, "wb") as o:
            o.write(f.read())
        return p

    if isinstance(aff, bytes):
        p = os.path.join(tmp, "case.aff")
        with open(p, "wb") as f:
            f.write(aff)
        aff = p
    else:
        aff = path(aff)
    dic = path(dic)
    env = dict(os.environ, APIDUMP_OPS="1", APIDUMP_KEY=opts.get("key", ""))
    w = os.path.join(tmp, "words")
    with open(w, "wb") as f:
        f.write(b"".join(i + b"\n" for i in items))
    try:
        args = [apidump, aff, dic, w, opts.get("example", "").encode("utf-8", "surrogateescape")]
        p = subprocess.run(args, cwd=d, env=env, capture_output=True, timeout=600)
    finally:
        shutil.rmtree(tmp)
    if p.returncode != 0:
        sys.exit("%s: apidump failed: %r" % (aff, p.stderr[-500:]))
    stderr = p.stderr
    if opts.get("warnings") == "load":
        # only the diagnostics of the loading: the debug builds also warn
        # about malformed UTF-8 at run time, unlike the Go port
        stderr = stderr.split(b"--- loaded\n")[0]
    warn = sorted(l.replace(tmp.encode() + b"/", b"") for l in stderr.split(b"\n") if l and l != b"--- loaded")
    return b"".join(b"! " + l + b"\n" for l in warn) + p.stdout


def header_opts(header):
    opts = {}
    for l in header:
        k, _, v = l[2:].decode("utf-8", "surrogateescape").partition(": ")
        opts[k] = v
    return opts


def read(path):
    with (gzip.open if path.endswith(".gz") else open)(path, "rb") as f:
        return f.read()


def write(path, data):
    if path.endswith(".gz"):
        with open(path, "wb") as f, gzip.GzipFile(fileobj=f, mode="wb", mtime=0) as g:
            g.write(data)
    else:
        with open(path, "wb") as f:
            f.write(data)


def regen_api(apidump, d, fname):
    name = fname.split(".api")[0]
    api = os.path.join(d, fname)
    lines = read(api).split(b"\n")
    header, items = [], []
    for l in lines:
        if l.startswith(b"# ") and not items:
            header.append(l)
        elif l.startswith(b"> "):
            items.append(l[2:])
        elif l.startswith(b"@"):
            items.append(l)
    opts = header_opts(header)
    out = b"".join(l + b"\n" for l in header)
    out += run(apidump, d, opts.get("aff", name + ".aff"), opts.get("dic", name + ".dic"), items, opts)
    write(api, out)


def regen_cases(apidump, d, fname):
    name = fname.split(".cases")[0]
    path = os.path.join(d, fname)
    lines = read(path).split(b"\n")
    header, cases = [], []
    for l in lines:
        if l.startswith(b"=== "):
            cases.append((l, []))
        elif not cases and l.startswith(b"# "):
            header.append(l)
        elif cases and l.startswith(b"| "):
            cases[-1][1].append(l[2:])
    opts = header_opts(header)
    items = opts.get("words", "").encode("utf-8", "surrogateescape").split()
    out = b"".join(l + b"\n" for l in header)
    for title, aff in cases:
        out += title + b"\n" + b"".join(b"| " + l + b"\n" for l in aff)
        out += run(apidump, d, b"".join(l + b"\n" for l in aff), opts.get("dic", name + ".dic"), items, opts)
    write(path, out)


def main():
    apidump, d = sys.argv[1], sys.argv[2]
    exts = (".api", ".api.gz", ".cases", ".cases.gz")
    files = sys.argv[3:] or sorted(f for f in os.listdir(d) if f.endswith(exts))
    for f in files:
        if ".cases" in f:
            regen_cases(apidump, d, f)
        else:
            regen_api(apidump, d, f)


if __name__ == "__main__":
    main()
