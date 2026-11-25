// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package pdfobj

import (
	"fmt"
	"io"
	"math"
	"strconv"
)

// Limits bounds what the lexer and parser will accept, so that hostile input
// cannot make them allocate without limit or recurse without end.
type Limits struct {
	MaxDepth  int // nesting of arrays and dictionaries
	MaxString int // bytes in one string
	MaxItems  int // entries in one array or dictionary
}

// DefaultLimits returns limits that no sane PDF exceeds.
func DefaultLimits() Limits {
	return Limits{MaxDepth: 128, MaxString: 32 << 20, MaxItems: 1 << 20}
}

// maxToken bounds names, numbers and keywords. The specification allows 127
// bytes for a name; real files go over that, but not by much.
const maxToken = 4096

// SyntaxError reports malformed PDF syntax at a byte offset.
type SyntaxError struct {
	Offset int64
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("pdf syntax error at offset %d: %s", e.Offset, e.Msg)
}

// Kind identifies a token.
type Kind uint8

// Token kinds.
const (
	EOF Kind = iota
	Integer
	Real
	Str
	NameTok
	Keyword
	ArrayOpen
	ArrayClose
	DictOpen
	DictClose
)

// Token is one lexical element.
type Token struct {
	Kind   Kind
	Offset int64   // where the token starts
	Int    int64   // value of an Integer
	Real   float64 // value of a Real
	Bytes  []byte  // contents of a Str, NameTok or Keyword
}

// Lexer splits PDF syntax into tokens. It reads from an io.ReaderAt a chunk
// at a time, or from a byte slice held in memory.
type Lexer struct {
	src   io.ReaderAt // nil when lexing a byte slice
	limit int64       // offset where the source ends
	buf   []byte
	base  int64 // offset of buf[0] in the source
	pos   int   // next unread byte in buf
	chunk int   // size of the next read
	rerr  error // first read error other than io.EOF
	lim   Limits
}

const (
	firstChunk = 1024
	maxChunk   = 64 << 10
)

// NewLexer returns a lexer that reads src from offset off up to, but not
// including, offset limit.
func NewLexer(src io.ReaderAt, off, limit int64, lim Limits) *Lexer {
	return &Lexer{src: src, limit: limit, base: off, chunk: firstChunk, lim: lim}
}

// NewBytesLexer returns a lexer over data. The slice is not modified.
func NewBytesLexer(data []byte, lim Limits) *Lexer {
	return &Lexer{buf: data, limit: int64(len(data)), lim: lim}
}

// Offset returns the position of the next unread byte.
func (l *Lexer) Offset() int64 { return l.base + int64(l.pos) }

// ReadErr returns the first error the underlying reader reported, if any.
func (l *Lexer) ReadErr() error { return l.rerr }

// fill makes at least n unread bytes available and reports whether it could.
func (l *Lexer) fill(n int) bool {
	for len(l.buf)-l.pos < n {
		if l.src == nil {
			return false
		}
		start := l.base + int64(len(l.buf))
		if start >= l.limit {
			return false
		}
		if l.pos > 0 {
			rem := copy(l.buf, l.buf[l.pos:])
			l.buf = l.buf[:rem]
			l.base += int64(l.pos)
			l.pos = 0
		}
		want := max(l.chunk, n)
		if int64(want) > l.limit-start {
			want = int(l.limit - start)
		}
		old := len(l.buf)
		if cap(l.buf)-old < want {
			grown := make([]byte, old, old+want)
			copy(grown, l.buf)
			l.buf = grown
		}
		m, err := l.src.ReadAt(l.buf[old:old+want], start)
		l.buf = l.buf[:old+m]
		if m == 0 {
			if err != nil && err != io.EOF && l.rerr == nil {
				l.rerr = err
			}
			// Nothing more will come; do not ask again.
			l.limit = start
			return len(l.buf)-l.pos >= n
		}
		if l.chunk < maxChunk {
			l.chunk *= 2
		}
	}
	return true
}

func (l *Lexer) peek() (byte, bool) {
	if l.pos < len(l.buf) || l.fill(1) {
		return l.buf[l.pos], true
	}
	return 0, false
}

// peekAt returns the byte i positions ahead without consuming anything.
func (l *Lexer) peekAt(i int) (byte, bool) {
	if l.pos+i < len(l.buf) || l.fill(i+1) {
		return l.buf[l.pos+i], true
	}
	return 0, false
}

func (l *Lexer) readByte() (byte, bool) {
	c, ok := l.peek()
	if ok {
		l.pos++
	}
	return c, ok
}

// IsSpace reports whether c is PDF white space.
func IsSpace(c byte) bool {
	switch c {
	case 0, '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// IsDelim reports whether c is a PDF delimiter character.
func IsDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// skipSpace consumes white space and comments.
func (l *Lexer) skipSpace() {
	for {
		c, ok := l.peek()
		switch {
		case !ok:
			return
		case IsSpace(c):
			l.pos++
		case c == '%':
			for {
				c, ok := l.readByte()
				if !ok || c == '\n' || c == '\r' {
					break
				}
			}
		default:
			return
		}
	}
}

// SkipStreamEOL consumes the end-of-line that follows the "stream" keyword,
// leaving the lexer on the first byte of stream data. Spaces before the line
// end are tolerated.
func (l *Lexer) SkipStreamEOL() {
	for {
		c, ok := l.peek()
		if !ok || (c != ' ' && c != '\t') {
			break
		}
		// Only skip blanks when a line end follows; otherwise they are data.
		n := 1
		for {
			c2, ok2 := l.peekAt(n)
			if !ok2 || (c2 != ' ' && c2 != '\t') {
				break
			}
			n++
		}
		if c2, ok2 := l.peekAt(n); !ok2 || (c2 != '\r' && c2 != '\n') {
			return
		}
		l.pos += n
	}
	if c, ok := l.peek(); ok && c == '\r' {
		l.pos++
		if c, ok := l.peek(); ok && c == '\n' {
			l.pos++
		}
	} else if ok && c == '\n' {
		l.pos++
	}
}

// Next returns the next token. At the end of input it returns a token of
// kind EOF and a nil error.
func (l *Lexer) Next() (Token, error) {
	l.skipSpace()
	off := l.Offset()
	c, ok := l.readByte()
	if !ok {
		return Token{Kind: EOF, Offset: off}, nil
	}
	switch c {
	case '[':
		return Token{Kind: ArrayOpen, Offset: off}, nil
	case ']':
		return Token{Kind: ArrayClose, Offset: off}, nil
	case '{', '}':
		return Token{Kind: Keyword, Offset: off, Bytes: []byte{c}}, nil
	case '(':
		b, err := l.literalString(off)
		return Token{Kind: Str, Offset: off, Bytes: b}, err
	case ')':
		return Token{}, &SyntaxError{off, "unexpected ')'"}
	case '<':
		if c2, ok := l.peek(); ok && c2 == '<' {
			l.pos++
			return Token{Kind: DictOpen, Offset: off}, nil
		}
		b, err := l.hexString(off)
		return Token{Kind: Str, Offset: off, Bytes: b}, err
	case '>':
		if c2, ok := l.peek(); ok && c2 == '>' {
			l.pos++
			return Token{Kind: DictClose, Offset: off}, nil
		}
		return Token{}, &SyntaxError{off, "unexpected '>'"}
	case '/':
		b, err := l.name(off)
		return Token{Kind: NameTok, Offset: off, Bytes: b}, err
	}

	// A number or a keyword: read up to the next white space or delimiter.
	word := []byte{c}
	for {
		c, ok := l.peek()
		if !ok || IsSpace(c) || IsDelim(c) {
			break
		}
		if len(word) >= maxToken {
			return Token{}, &SyntaxError{off, "token too long"}
		}
		word = append(word, c)
		l.pos++
	}
	if isInt, i, f, ok := parseNumber(word); ok {
		if isInt {
			return Token{Kind: Integer, Offset: off, Int: i}, nil
		}
		return Token{Kind: Real, Offset: off, Real: f}, nil
	}
	return Token{Kind: Keyword, Offset: off, Bytes: word}, nil
}

// parseNumber parses a PDF number: an optional sign, digits, and at most one
// decimal point. It reports whether b is a number at all.
func parseNumber(b []byte) (isInt bool, i int64, f float64, ok bool) {
	p := 0
	neg := false
	if b[0] == '+' || b[0] == '-' {
		neg = b[0] == '-'
		p++
	}
	var whole, frac, scale float64 = 0, 0, 1
	var n int64
	digits, fracDigits, dot, overflow := 0, 0, false, false
	for ; p < len(b); p++ {
		c := b[p]
		switch {
		case c >= '0' && c <= '9':
			d := int64(c - '0')
			if dot {
				if fracDigits < 18 {
					frac = frac*10 + float64(d)
					scale *= 10
					fracDigits++
				}
			} else {
				if n > (math.MaxInt64-d)/10 {
					overflow = true
				} else {
					n = n*10 + d
				}
				whole = whole*10 + float64(d)
			}
			digits++
		case c == '.' && !dot:
			dot = true
		default:
			return false, 0, 0, false
		}
	}
	if digits == 0 {
		return false, 0, 0, false
	}
	if !dot && !overflow {
		if neg {
			n = -n
		}
		return true, n, 0, true
	}
	if digits > 15 {
		// Too many digits for the sums above to be exact.
		v, err := strconv.ParseFloat(string(b), 64)
		return false, 0, v, err == nil
	}
	v := whole + frac/scale
	if neg {
		v = -v
	}
	return false, 0, v, true
}

func (l *Lexer) literalString(off int64) ([]byte, error) {
	out := []byte{}
	depth := 1
	for {
		c, ok := l.readByte()
		if !ok {
			return out, &SyntaxError{off, "unterminated string"}
		}
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return out, nil
			}
		case '\r':
			// A line end inside a string reads as a single line feed.
			if c2, ok := l.peek(); ok && c2 == '\n' {
				l.pos++
			}
			c = '\n'
		case '\\':
			e, ok := l.readByte()
			if !ok {
				return out, &SyntaxError{off, "unterminated string"}
			}
			switch e {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case '\r':
				// Backslash at a line end: the string continues.
				if c2, ok := l.peek(); ok && c2 == '\n' {
					l.pos++
				}
				continue
			case '\n':
				continue
			case '0', '1', '2', '3', '4', '5', '6', '7':
				v := int(e - '0')
				for i := 0; i < 2; i++ {
					d, ok := l.peek()
					if !ok || d < '0' || d > '7' {
						break
					}
					v = v*8 + int(d-'0')
					l.pos++
				}
				c = byte(v)
			default:
				// Covers \( \) \\ and, leniently, any other character.
				c = e
			}
		}
		if len(out) >= l.lim.MaxString {
			return out, &SyntaxError{off, "string too long"}
		}
		out = append(out, c)
	}
}

func (l *Lexer) hexString(off int64) ([]byte, error) {
	out := []byte{}
	hi, have := byte(0), false
	for {
		c, ok := l.readByte()
		if !ok {
			return out, &SyntaxError{off, "unterminated hex string"}
		}
		if c == '>' {
			if have {
				out = append(out, hi<<4)
			}
			return out, nil
		}
		v, isHex := unhex(c)
		if !isHex {
			continue // white space, or junk that readers ignore
		}
		if !have {
			hi, have = v, true
			continue
		}
		if len(out) >= l.lim.MaxString {
			return out, &SyntaxError{off, "string too long"}
		}
		out = append(out, hi<<4|v)
		have = false
	}
}

func (l *Lexer) name(off int64) ([]byte, error) {
	out := []byte{}
	for {
		c, ok := l.peek()
		if !ok || IsSpace(c) || IsDelim(c) {
			return out, nil
		}
		l.pos++
		if c == '#' {
			h1, ok1 := l.peekAt(0)
			h2, ok2 := l.peekAt(1)
			v1, x1 := unhex(h1)
			v2, x2 := unhex(h2)
			if ok1 && ok2 && x1 && x2 {
				c = v1<<4 | v2
				l.pos += 2
			}
		}
		if len(out) >= maxToken {
			return out, &SyntaxError{off, "name too long"}
		}
		out = append(out, c)
	}
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// skipInlineImage scans forward for the EI that ends inline image data. The
// data is binary and may contain the letters "EI" by chance, so a candidate
// only counts when it stands alone as a token and what follows looks like
// content-stream text.
func (l *Lexer) skipInlineImage() int {
	// One white-space byte separates ID from the data.
	if c, ok := l.peek(); ok && IsSpace(c) {
		l.pos++
	}
	skipped := 0
	prevSpace := true
	for {
		c, ok := l.readByte()
		if !ok {
			return skipped
		}
		if c == 'E' && prevSpace {
			if c2, ok := l.peek(); ok && c2 == 'I' {
				after, more := l.peekAt(1)
				if !more || IsSpace(after) {
					plausible := true
					for i := 2; i < 12; i++ {
						b, ok := l.peekAt(i)
						if !ok {
							break
						}
						if b >= 0x7f || (b < 0x20 && !IsSpace(b)) {
							plausible = false
							break
						}
					}
					if plausible {
						l.pos++ // the I
						return skipped
					}
				}
			}
		}
		prevSpace = IsSpace(c)
		skipped++
	}
}
