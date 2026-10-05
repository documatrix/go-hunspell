#!/usr/bin/env python3
"""words.py TESTS OUT N: writes OUT/<name>.words for each dictionary of the
Hunspell test folder TESTS: the dictionary words, the .good, .wrong and .sug
words and N seeded mutations (case, deleted, swapped, inserted and replaced
letters, compounds), in the encoding of the dictionary. The first line of
each file is the example word of generate()."""
import os, random, sys

T, OUT, N = sys.argv[1], sys.argv[2], int(sys.argv[3])
SLOW = {'gh1058', 'compound_wnum_overflow', 'compoundrule5', 'digits_in_words', 'checkcompoundrep2',
        'ph2', 'utfcompound', 'checkcompoundcaseutf', 'hu'}


def edge(ws):
    """Edge cases built from a few words of the dictionary: numbers, quotes,
    dots, dashes, long and oddly cased words, spaces, invalid UTF-8 and
    the spellml XML queries. (Words with NUL bytes are left out: the C++
    code mixes std::string and C string lookups on them.)"""
    ws = [w for w in ws if w] or [b'a']
    w, x = ws[0], ws[-1]
    res = [b'1', b'123', b'1,000', b'1.5', b'12-th', b'3rd', b'1st.', b'0x1F', b'-1',
           w + b"'s", w + "’s".encode('utf-8'), b"'" + w + b"'", b"l'" + w, w + b'.', w + b'..',
           b'.' + w, b'...', b'-' + w, w + b'-', w + b'--' + x, w + b'-' + x + b'-' + w,
           b'a' * 120, b'A' * 260, w.upper(), w.swapcase(), w.title(),
           w + b' ' + x, b' ' + w, w + b' ', w + b'\t' + x, b'\xff\xfe' + w, w + b'\xc3', b'\x80' + w,
           '𐐀'.encode('utf-8') + w, 'İ'.encode('utf-8') + w,
           w + 'ß'.encode('utf-8'), w.replace(b'ss', 'ß'.encode('utf-8')), w.upper() + 'SS'.encode('utf-8'),
           b'<?xml?><query type="analyze"><word>' + w + b'</word></query>',
           b'<?xml?><query type="stem"><word>' + w + b'</word></query>',
           b'<?xml?><query type="suggest"><word>' + w + b'x</word></query>',
           b'<?xml?><query type="generate"><word>' + w + b'</word><word>' + x + b'</word></query>',
           b'<?xml?><query type="generate"><word>' + w + b'</word><code><a>po:noun</a><a>is:plural</a></code></query>',
           b"<?xml?><query type='analyze'><word>" + w + b'</word></query>',
           b'<?xml?><query type="add"><word>' + w + b'</word></query>',
           b'<?xml?><query type="analyze"><word>&lt;' + w + b'&amp;</word></query>',
           b'<?xml?><query type="bad"><word>' + w + b'</word></query>',
           b'<?xml?><query>', b'<?xml?>']
    return [r for r in res if b'\n' not in r]

os.makedirs(OUT, exist_ok=True)
for fn in sorted(os.listdir(T)):
    if not fn.endswith('.dic') or not os.path.exists(os.path.join(T, fn[:-4] + '.aff')):
        continue
    n = fn[:-4]
    random.seed(n)
    aff = open(os.path.join(T, n + '.aff'), 'rb').read()
    enc = 'utf-8' if b'SET UTF-8' in aff else 'latin-1'
    def dec(b):
        try:
            return b.decode(enc)
        except UnicodeDecodeError:
            return b.decode('latin-1')
    raw = open(os.path.join(T, fn), 'rb').read().split(b'\n')[1:]
    words = []
    for line in raw:
        w = dec(line).split('\t')[0].split(' ')[0].strip()
        w = w.replace('\\/', '\x00').split('/')[0].replace('\x00', '/')
        if w:
            words.append(w)
    extra = []
    for ext in ('.good', '.wrong', '.sug'):
        p = os.path.join(T, n + ext)
        if os.path.exists(p):
            for l in open(p, 'rb').read().split(b'\n'):
                for w in dec(l).replace(',', ' ').split():
                    extra.append(w)
    letters = sorted(set(''.join(words + extra))) or list('abc')
    out = set(words) | set(extra)
    base = words + extra
    for _ in range(N):
        if not base:
            break
        w = random.choice(base)
        k = random.randrange(9)
        i = random.randrange(len(w) + 1)
        if k == 0: c = w.upper()
        elif k == 1: c = w.capitalize()
        elif k == 2: c = w[:i] + w[i + 1:]
        elif k == 3 and i < len(w) - 1: c = w[:i] + w[i + 1] + w[i] + w[i + 2:]
        elif k == 4: c = w[:i] + random.choice(letters) + w[i:]
        elif k == 5: c = w[:i] + random.choice(letters) + w[i + 1:]
        elif k == 6: c = w + random.choice(base)
        elif k == 7: c = w + '-' + random.choice(base)
        else: c = w + '.'
        out.add(c)
    out = sorted(w for w in out if w and len(w) < 60 and '\n' not in w)
    example = words[0] if words else ''
    codec = enc if enc == 'utf-8' else 'latin-1'
    def b(w):
        try:
            return w.encode(codec)
        except UnicodeEncodeError:
            return None
    lines = [b(w) for w in [example] + out]
    lines = [l for l in lines if l is not None]
    extra_edge = edge([b(w) for w in random.sample(base, min(4, len(base)))] if base else [b'a'])
    if n in SLOW:  # the long words take minutes without the time limits
        extra_edge = [e for e in extra_edge if len(e) < 100]
    lines += extra_edge
    with open(os.path.join(OUT, n + '.words'), 'wb') as f:
        for l in lines:
            f.write(l + b'\n')
