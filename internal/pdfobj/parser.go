// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package pdfobj

import (
	"errors"
	"io"
)

// ErrTooDeep is returned when arrays and dictionaries nest beyond the limit.
var ErrTooDeep = errors.New("pdf: objects nested too deeply")

// Parser builds objects from the tokens of a Lexer.
type Parser struct {
	lex *Lexer
	lim Limits
	// Lookahead. Deciding whether "12 0 R" is a reference needs two tokens
	// beyond the first integer.
	queue []queued
	buf   [4]queued // backing store for queue, so that lookahead costs no allocation
}

type queued struct {
	tok Token
	err error
}

// NewParser returns a parser reading from lex.
func NewParser(lex *Lexer) *Parser {
	return &Parser{lex: lex, lim: lex.lim}
}

func (p *Parser) next() (Token, error) {
	if len(p.queue) > 0 {
		q := p.queue[0]
		p.drop(1)
		return q.tok, q.err
	}
	return p.lex.Next()
}

// drop removes the first n tokens of the lookahead queue.
func (p *Parser) drop(n int) {
	p.queue = p.queue[n:]
	if len(p.queue) == 0 {
		p.queue = p.buf[:0]
	}
}

func (p *Parser) peek(i int) (Token, error) {
	for len(p.queue) <= i {
		if len(p.queue) == cap(p.queue) && len(p.queue) < len(p.buf) {
			// The queue has crept to the end of its store: move it back.
			n := copy(p.buf[:], p.queue)
			p.queue = p.buf[:n]
		}
		t, err := p.lex.Next()
		p.queue = append(p.queue, queued{t, err})
	}
	return p.queue[i].tok, p.queue[i].err
}

// ParseObject parses one object. It returns io.ErrUnexpectedEOF when the
// input ends before an object starts.
func (p *Parser) ParseObject() (Object, error) {
	return p.object(0)
}

func (p *Parser) object(depth int) (Object, error) {
	t, err := p.next()
	if err != nil {
		return nil, err
	}
	switch t.Kind {
	case EOF:
		return nil, io.ErrUnexpectedEOF
	case Integer:
		if t.Int >= 0 {
			t2, err2 := p.peek(0)
			if err2 == nil && t2.Kind == Integer && t2.Int >= 0 && t2.Int <= 65535 {
				t3, err3 := p.peek(1)
				if err3 == nil && t3.Kind == Keyword && string(t3.Bytes) == "R" && t.Int <= 1<<31-1 {
					p.drop(2)
					return Ref{Num: int(t.Int), Gen: int(t2.Int)}, nil
				}
			}
		}
		return t.Int, nil
	case Real:
		return t.Real, nil
	case Str:
		return String(t.Bytes), nil
	case NameTok:
		return Name(t.Bytes), nil
	case ArrayOpen:
		return p.array(depth+1, t.Offset)
	case DictOpen:
		return p.dict(depth+1, t.Offset)
	case Keyword:
		switch string(t.Bytes) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		return nil, &SyntaxError{t.Offset, "unexpected keyword " + printable(t.Bytes)}
	}
	return nil, &SyntaxError{t.Offset, "unexpected closing bracket"}
}

func (p *Parser) array(depth int, off int64) (Object, error) {
	if depth > p.lim.MaxDepth {
		return nil, ErrTooDeep
	}
	arr := Array{}
	for {
		t, err := p.peek(0)
		if err != nil {
			return nil, err
		}
		switch t.Kind {
		case ArrayClose:
			p.drop(1)
			return arr, nil
		case EOF:
			return nil, io.ErrUnexpectedEOF
		}
		if len(arr) >= p.lim.MaxItems {
			return nil, &SyntaxError{off, "array too long"}
		}
		o, err := p.object(depth)
		if err != nil {
			return nil, err
		}
		arr = append(arr, o)
	}
}

func (p *Parser) dict(depth int, off int64) (Object, error) {
	if depth > p.lim.MaxDepth {
		return nil, ErrTooDeep
	}
	d := Dict{}
	for {
		t, err := p.next()
		if err != nil {
			return nil, err
		}
		switch t.Kind {
		case DictClose:
			return d, nil
		case EOF:
			return nil, io.ErrUnexpectedEOF
		case NameTok:
			// Handled below.
		default:
			// A key that is not a name. Readers skip it and carry on.
			if t.Kind == Keyword {
				switch string(t.Bytes) {
				case "endobj", "stream", "endstream", "obj":
					return nil, &SyntaxError{t.Offset, "dictionary not closed"}
				}
			}
			continue
		}
		if len(d) >= p.lim.MaxItems {
			return nil, &SyntaxError{off, "dictionary too long"}
		}
		// A key directly followed by ">>" has no value: treat it as null.
		if v, err := p.peek(0); err == nil && v.Kind == DictClose {
			continue
		}
		o, err := p.object(depth)
		if err != nil {
			return nil, err
		}
		if o != nil {
			d[Name(t.Bytes)] = o
		}
	}
}

// ParseIndirect parses "num gen obj ... endobj". When the object is a
// stream, the result is a *Stream whose Offset is the first byte of its data;
// the data itself is not read.
func (p *Parser) ParseIndirect() (num, gen int, obj Object, err error) {
	t1, err := p.next()
	if err != nil {
		return 0, 0, nil, err
	}
	t2, err := p.next()
	if err != nil {
		return 0, 0, nil, err
	}
	t3, err := p.next()
	if err != nil {
		return 0, 0, nil, err
	}
	if t1.Kind != Integer || t2.Kind != Integer || t3.Kind != Keyword || string(t3.Bytes) != "obj" ||
		t1.Int < 0 || t1.Int > 1<<31-1 || t2.Int < 0 || t2.Int > 65535 {
		if t1.Kind == EOF {
			return 0, 0, nil, io.ErrUnexpectedEOF
		}
		return 0, 0, nil, &SyntaxError{t1.Offset, "expected an object header"}
	}
	num, gen = int(t1.Int), int(t2.Int)

	// An empty object ("1 0 obj endobj") is null.
	if t, err := p.peek(0); err == nil && t.Kind == Keyword && string(t.Bytes) == "endobj" {
		return num, gen, nil, nil
	}
	obj, err = p.object(0)
	if err != nil {
		return num, gen, nil, err
	}
	if d, ok := obj.(Dict); ok {
		if t, err := p.peek(0); err == nil && t.Kind == Keyword && string(t.Bytes) == "stream" && len(p.queue) == 1 {
			p.queue = p.buf[:0]
			p.lex.SkipStreamEOL()
			return num, gen, &Stream{Dict: d, Offset: p.lex.Offset(), Num: num, Gen: gen}, nil
		}
	}
	return num, gen, obj, nil
}

func printable(b []byte) string {
	if len(b) > 20 {
		b = b[:20]
	}
	out := make([]byte, 0, len(b)+2)
	out = append(out, '"')
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			c = '?'
		}
		out = append(out, c)
	}
	return string(append(out, '"'))
}

// ContentNext reads the next element of a content stream. An operand comes
// back as (object, "", nil) and an operator as (nil, name, nil). At the end
// of the stream it returns io.EOF.
//
// Content streams have no indirect objects, so the only keywords that are
// operands are true, false and null; every other keyword is an operator.
func (p *Parser) ContentNext() (Object, string, error) {
	t, err := p.peek(0)
	if err != nil {
		p.drop(1)
		return nil, "", err
	}
	switch t.Kind {
	case EOF:
		return nil, "", io.EOF
	case Keyword:
		switch string(t.Bytes) {
		case "true", "false", "null":
		default:
			p.drop(1)
			if op, ok := operators[string(t.Bytes)]; ok {
				return nil, op, nil // no allocation for the names seen millions of times
			}
			return nil, string(t.Bytes), nil
		}
	case ArrayClose, DictClose:
		// A stray closing bracket: skip it.
		p.drop(1)
		return nil, "", &SyntaxError{t.Offset, "unexpected closing bracket"}
	}
	obj, err := p.object(0)
	return obj, "", err
}

// InlineImage is called after the ID operator of an inline image. It moves
// past the image data and its closing EI operator, and returns the data: a
// part of the slice being parsed, or nil when the parser reads from a file.
// An end of line between the data and EI is left out.
//
// Binary data can hold the letters EI by chance, so the end of the data is
// a guess unless its length is known. Pass the length when the image
// dictionary gives it away, as it does for an image with no filter, and -1
// otherwise.
func (p *Parser) InlineImage(size int) []byte {
	p.queue = p.buf[:0]
	return p.lex.inlineImage(size)
}

// operators holds the content stream operators as ready-made strings.
var operators = func() map[string]string {
	m := map[string]string{}
	for _, op := range []string{
		"b", "B", "b*", "B*", "BDC", "BI", "BMC", "BT", "BX", "c", "cm", "CS", "cs", "d", "d0", "d1", "Do", "DP",
		"EI", "EMC", "ET", "EX", "f", "F", "f*", "G", "g", "gs", "h", "i", "ID", "j", "J", "K", "k", "l", "m", "M",
		"MP", "n", "q", "Q", "re", "RG", "rg", "ri", "s", "S", "SC", "sc", "SCN", "scn", "sh", "T*", "Tc", "Td",
		"TD", "Tf", "Tj", "TJ", "TL", "Tm", "Tr", "Ts", "Tw", "Tz", "v", "w", "W", "W*", "y", "'", "\"",
	} {
		m[op] = op
	}
	return m
}()
