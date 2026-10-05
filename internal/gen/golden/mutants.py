#!/usr/bin/env python3
"""mutants.py TESTS WORDS OUT APIDUMP M K: for each dictionary of the Hunspell
test folder TESTS, writes OUT/<name>.txt.gz with M seeded mutations of its affix
file (a line deleted, truncated, duplicated or with a field replaced) and,
for each, the load-time warnings (the affix file is m.aff, the dictionary
m.dic) and the API results (APIDUMP, built with
HUNSPELL_WARNING_ON) for the first K words of WORDS/<name>.words.

The mutations are described by one line that TestGoldenMalformed applies to
the original affix file:
    del L | dup L | trunc L K | set L F TOKEN
with L the 0-based line, K the number of fields kept and F the field
replaced; fields are separated by ASCII white space and joined by a space."""
import gzip, os, random, subprocess, sys, tempfile

T, W, OUT, DUMP, M, K = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], int(sys.argv[5]), int(sys.argv[6])
TOKENS = ['', '0', '1', '-1', '2', '99999999', 'x', 'A', 'AB', '/', '\\', '.', 'ő', '[', ']', '^', '0/1', 'a/b']
WS = b' \t\r\x0b\x0c'

def fields(line):
    out, cur = [], b''
    for c in line:
        if c in WS:
            if cur:
                out.append(cur)
            cur = b''
        else:
            cur += bytes([c])
    if cur:
        out.append(cur)
    return out

def apply(lines, op):
    lines = list(lines)
    p = op.split(' ', 3)
    L = int(p[1])
    if p[0] == 'del':
        del lines[L]
    elif p[0] == 'dup':
        lines.insert(L, lines[L])
    elif p[0] == 'trunc':
        lines[L] = b' '.join(fields(lines[L])[:int(p[2])])
    else:
        f = fields(lines[L])
        f[int(p[2])] = p[3].encode('utf-8') if len(p) > 3 else b''
        lines[L] = b' '.join(f)
    return lines

os.makedirs(OUT, exist_ok=True)
tmp = tempfile.mkdtemp()
for fn in sorted(os.listdir(T)):
    if not fn.endswith('.aff') or fn == 'timelimit.aff':
        continue
    n = fn[:-4]
    if not os.path.exists(os.path.join(W, n + '.words')) or not os.path.exists(os.path.join(T, n + '.dic')):
        continue
    random.seed('mutant-' + n)
    aff = open(os.path.join(T, fn), 'rb').read().split(b'\n')
    cand = [i for i, l in enumerate(aff) if fields(l) and not l.startswith(b'#')]
    if not cand:
        continue
    words = open(os.path.join(W, n + '.words'), 'rb').read().split(b'\n')
    example, words = words[0], [w for w in words[1:] if w][:K]
    open(os.path.join(tmp, 'w'), 'wb').write(b'\n'.join(words) + b'\n')
    open(os.path.join(tmp, 'm.dic'), 'wb').write(open(os.path.join(T, n + '.dic'), 'rb').read())
    res = b''
    for _ in range(M):
        L = random.choice(cand)
        nf = len(fields(aff[L]))
        k = random.randrange(5)
        if k == 0: op = 'del %d' % L
        elif k == 1: op = 'dup %d' % L
        elif k == 2: op = 'trunc %d %d' % (L, random.randrange(nf))
        else: op = 'set %d %d %s' % (L, random.randrange(nf), random.choice(TOKENS))
        open(os.path.join(tmp, 'm.aff'), 'wb').write(b'\n'.join(apply(aff, op)))
        try:
            r = subprocess.run([DUMP, 'm.aff', 'm.dic', 'w', example], cwd=tmp, capture_output=True, timeout=60)
        except subprocess.TimeoutExpired:
            continue
        if r.returncode != 0:
            continue  # the C++ library crashed: no reference
        warn = r.stderr.split(b'--- loaded\n')[0]
        res += b'=== ' + op.encode('utf-8') + b'\n'
        for l in warn.split(b'\n'):
            if l:
                res += b'! ' + l + b'\n'
        res += r.stdout
    with open(os.path.join(OUT, n + '.txt.gz'), 'wb') as f:
        with gzip.GzipFile(fileobj=f, mode='wb', mtime=0, filename='') as g:
            g.write(b'# example: ' + example + b'\n' + res)
