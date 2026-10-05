package core

import (
	"bytes"
	"fmt"
	"os"
)

const (
	hzBufSize      = 65536
	hzipExtension  = ".hz"
	fileLineBufLen = hzBufSize + 50
)

// lineReader hands out the lines of a file the way FileMgr does.
type lineReader interface {
	getline() (string, bool)
	getlinenum() int
}

// fileMgr reads a plain text file line by line. A line longer than the read
// buffer ends the file the way the end of the file does.
type fileMgr struct {
	data    []byte
	pos     int
	linenum int
	hin     *hunzip
	failed  bool
}

// openFile opens path for reading lines, falling back to path.hz.
func openFile(path, key string, errs *[]string) *fileMgr {
	fm := &fileMgr{}
	if path == "" {
		fm.failed = true
		return fm
	}
	data, err := os.ReadFile(path)
	if err == nil {
		fm.data = data
		return fm
	}
	hz, herr := os.ReadFile(path + hzipExtension)
	if herr != nil {
		fm.failed = true
		*errs = append(*errs, fmt.Sprintf("error: %s: cannot open\n", path))
		return fm
	}
	fm.hin = newHunzip(path+hzipExtension, hz, key, errs)
	if !fm.hin.isOpen() {
		fm.failed = true
	}
	return fm
}

func (f *fileMgr) getline() (string, bool) {
	f.linenum++
	if f.hin != nil {
		line, ok := f.hin.getline()
		if !ok {
			f.linenum--
		}
		return line, ok
	}
	if f.failed || f.pos >= len(f.data) {
		f.linenum--
		f.failed = true
		return "", false
	}
	rest := f.data[f.pos:]
	end := bytes.IndexByte(rest, '\n')
	var line []byte
	if end < 0 {
		line = rest
		f.pos = len(f.data)
	} else {
		line = rest[:end]
		f.pos += end + 1
	}
	if len(line) > fileLineBufLen-1 {
		// istream::getline sets failbit on a line that does not fit: it
		// stores at most fileLineBufLen-1 bytes, and a line of exactly that
		// length still ends at the newline or the end of the file
		f.linenum--
		f.failed = true
		return "", false
	}
	// the C string the line is read into ends at a NUL byte
	if i := bytes.IndexByte(line, 0); i >= 0 {
		line = line[:i]
	}
	return string(line), true
}

func (f *fileMgr) getlinenum() int { return f.linenum }

// hunzip decodes the hzip format: sorted dictionaries with prefix-suffix
// encoding, 16-bit Huffman encoding and optional encryption.
type hunzip struct {
	filename string
	src      []byte
	srcPos   int
	open     bool
	bufsiz   int
	lastbit  int
	inc      int
	inbits   int
	outc     int
	dec      []hzBit
	in       []byte
	out      []byte
	line     []byte
}

type hzBit struct {
	c [2]byte
	v [2]int
}

const (
	hzCodeLen    = 65536
	hzBaseBitRec = 5000
)

func newHunzip(filename string, data []byte, key string, errs *[]string) *hunzip {
	h := &hunzip{
		filename: filename,
		src:      data,
		open:     true,
		in:       make([]byte, hzBufSize),
		out:      make([]byte, hzBufSize+1),
	}
	if h.getcode(key, errs) == -1 {
		h.bufsiz = -1
	} else {
		h.bufsiz = h.getbuf(errs)
	}
	return h
}

func (h *hunzip) read(n int) ([]byte, bool) {
	if h.srcPos+n > len(h.src) {
		h.srcPos = len(h.src)
		return nil, false
	}
	b := h.src[h.srcPos : h.srcPos+n]
	h.srcPos += n
	return b, true
}

func (h *hunzip) fail(errs *[]string, format string) int {
	*errs = append(*errs, fmt.Sprintf(format, h.filename))
	return -1
}

func (h *hunzip) getcode(key string, errs *[]string) int {
	const msgFormat = "error: %s: not in hzip format\n"
	const msgKey = "error: %s: missing or bad password\n"
	allocatedbit := hzBaseBitRec
	magic, ok := h.read(3)
	if !ok || !(string(magic) == "hz0" || string(magic) == "hz1") {
		return h.fail(errs, msgFormat)
	}
	enc := 0
	encrypted := string(magic) == "hz1"
	if encrypted {
		if key == "" {
			return h.fail(errs, msgKey)
		}
		c, ok := h.read(1)
		if !ok {
			return h.fail(errs, msgFormat)
		}
		var cs byte
		for i := 0; i < len(key); i++ {
			cs ^= key[i]
		}
		if cs != c[0] {
			return h.fail(errs, msgKey)
		}
	} else {
		key = ""
	}
	next := func() byte {
		enc++
		if enc >= len(key) {
			enc = 0
		}
		return key[enc]
	}
	cb, ok := h.read(2)
	if !ok {
		return h.fail(errs, msgFormat)
	}
	c := [2]byte{cb[0], cb[1]}
	if key != "" {
		c[0] ^= key[enc]
		c[1] ^= next()
	}
	n := int(c[0])<<8 + int(c[1])
	h.dec = make([]hzBit, hzBaseBitRec)
	for i := 0; i < n; i++ {
		cb, ok := h.read(2)
		if !ok {
			return h.fail(errs, msgFormat)
		}
		c = [2]byte{cb[0], cb[1]}
		if key != "" {
			c[0] ^= next()
			c[1] ^= next()
		}
		lb, ok := h.read(1)
		if !ok {
			return h.fail(errs, msgFormat)
		}
		l := lb[0]
		if key != "" {
			l ^= next()
		}
		code, ok := h.read(int(l>>3) + 1)
		if !ok {
			return h.fail(errs, msgFormat)
		}
		in := append([]byte(nil), code...)
		if key != "" {
			for j := 0; j <= int(l>>3); j++ {
				in[j] ^= next()
			}
		}
		p := 0
		for j := 0; j < int(l); j++ {
			b := 0
			if in[j>>3]&(1<<(7-(j&7))) != 0 {
				b = 1
			}
			oldp := p
			p = h.dec[p].v[b]
			if p == 0 {
				h.lastbit++
				if h.lastbit == allocatedbit {
					allocatedbit += hzBaseBitRec
					grown := make([]hzBit, allocatedbit)
					copy(grown, h.dec)
					h.dec = grown
				}
				h.dec[h.lastbit].v = [2]int{}
				h.dec[oldp].v[b] = h.lastbit
				p = h.lastbit
			}
		}
		h.dec[p].c = c
	}
	return 0
}

func (h *hunzip) getbuf(errs *[]string) int {
	p := 0
	o := 0
	for {
		if h.inc == 0 {
			n := copy(h.in, h.src[h.srcPos:])
			h.srcPos += n
			h.inbits = n << 3
		}
		for ; h.inc < h.inbits; h.inc++ {
			b := 0
			if h.in[h.inc>>3]&(1<<(7-(h.inc&7))) != 0 {
				b = 1
			}
			oldp := p
			p = h.dec[p].v[b]
			if p == 0 {
				if oldp == h.lastbit {
					h.open = false
					// add last odd byte
					if h.dec[h.lastbit].c[0] != 0 {
						h.out[o] = h.dec[h.lastbit].c[1]
						o++
					}
					return o
				}
				h.out[o] = h.dec[oldp].c[0]
				h.out[o+1] = h.dec[oldp].c[1]
				o += 2
				if o >= hzBufSize {
					return o
				}
				p = h.dec[p].v[b]
			}
		}
		h.inc = 0
		if h.inbits != hzBufSize*8 {
			break
		}
	}
	return h.fail(errs, "error: %s: not in hzip format\n")
}

// isOpen reports whether the file decoded. Unlike the C++ code this does not
// depend on the input stream still being open, which made small files that
// decode in one go unreadable there.
func (h *hunzip) isOpen() bool { return h.bufsiz != -1 }

func (h *hunzip) getline() (string, bool) {
	linebuf := make([]byte, hzBufSize)
	l, eol, left, right := 0, false, 0, 0
	if h.bufsiz == -1 {
		return "", false
	}
	var noErrs []string
	for l < h.bufsiz && l < hzBufSize-1 && !eol {
		linebuf[l] = h.out[h.outc]
		l++
		switch h.out[h.outc] {
		case '\t', ' ':
		case 31: // escape
			h.outc++
			if h.outc == h.bufsiz {
				h.bufsiz = h.getbuf(&noErrs)
				h.outc = 0
			}
			linebuf[l-1] = h.out[h.outc]
		default:
			if h.out[h.outc] < 47 {
				if h.out[h.outc] > 32 {
					right = int(h.out[h.outc]) - 31
					h.outc++
					if h.outc == h.bufsiz {
						h.bufsiz = h.getbuf(&noErrs)
						h.outc = 0
					}
				}
				if h.out[h.outc] == 30 {
					left = 9
				} else {
					left = int(h.out[h.outc])
				}
				linebuf[l-1] = '\n'
				eol = true
			}
		}
		h.outc++
		if h.outc == h.bufsiz {
			h.outc = 0
			if h.open {
				h.bufsiz = h.getbuf(&noErrs)
			} else {
				h.bufsiz = -1
			}
		}
	}
	// append suffix from previous line
	var cur []byte
	if right != 0 {
		prev := h.line
		n := right + 1
		if len(prev) < n || l+n >= hzBufSize {
			return "", false
		}
		cur = append(append([]byte(nil), linebuf[:l-1]...), prev[len(prev)-n:]...)
	} else {
		cur = append([]byte(nil), linebuf[:l]...)
	}
	// copy into line with left-offset from previous line preserved
	if left+len(cur) >= hzBufSize+50 {
		return "", false
	}
	nl := make([]byte, 0, left+len(cur))
	if left > len(h.line) {
		nl = append(nl, h.line...)
		nl = append(nl, make([]byte, left-len(h.line))...)
	} else {
		nl = append(nl, h.line[:left]...)
	}
	nl = append(nl, cur...)
	if i := bytes.IndexByte(nl, 0); i >= 0 {
		nl = nl[:i]
	}
	h.line = nl
	return string(nl), true
}
