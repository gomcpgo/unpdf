// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package pdfobj

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func parse(t *testing.T, s string) Object {
	t.Helper()
	obj, err := NewParser(NewBytesLexer([]byte(s), DefaultLimits())).ParseObject()
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return obj
}

func TestParseObject(t *testing.T) {
	tests := []struct {
		in   string
		want Object
	}{
		// Numbers.
		{"42", int64(42)},
		{"-17", int64(-17)},
		{"+3", int64(3)},
		{"3.5", 3.5},
		{"-.5", -0.5},
		{"5.", 5.0},
		{"0.0001", 0.0001},
		{"99999999999999999999", 1e20},
		// Keywords.
		{"true", true},
		{"false", false},
		{"null", nil},
		// Names.
		{"/Name", Name("Name")},
		{"/A#20B", Name("A B")},
		{"/A#2", Name("A#2")},
		{"/", Name("")},
		// Literal strings.
		{"(hello)", String("hello")},
		{"(a (nested) b)", String("a (nested) b")},
		{`(\(\)\\)`, String(`()\`)},
		{`(\n\r\t\b\f)`, String("\n\r\t\b\f")},
		{`(\101\7\0053)`, String("A\x07\x053")},
		{"(line\\\ncontinued)", String("linecontinued")},
		{"(a\r\nb\rc)", String("a\nb\nc")},
		{`(\q)`, String("q")},
		{"()", String("")},
		// Hexadecimal strings.
		{"<48656C6C6F>", String("Hello")},
		{"<48 65 6c\n6C 6F>", String("Hello")},
		{"<484>", String("H@")},
		{"<>", String("")},
		// References.
		{"12 0 R", Ref{12, 0}},
		{"[1 2 R]", Array{Ref{1, 2}}},
		{"[1 2 3]", Array{int64(1), int64(2), int64(3)}},
		{"[1 0 R 2 0 R]", Array{Ref{1, 0}, Ref{2, 0}}},
		// Containers.
		{"[]", Array{}},
		{"[/A (b) [1] <<>>]", Array{Name("A"), String("b"), Array{int64(1)}, Dict{}}},
		{"<< /A 1 /B [2 3] /C << /D (x) >> >>", Dict{"A": int64(1), "B": Array{int64(2), int64(3)}, "C": Dict{"D": String("x")}}},
		{"<</A/B/C/D>>", Dict{"A": Name("B"), "C": Name("D")}},
		{"<< /A null /B 1 >>", Dict{"B": int64(1)}},
		{"<< /A >>", Dict{}},
		{"<< 1 /A 2 >>", Dict{"A": int64(2)}},
		// Comments and white space.
		{"% a comment\n  7", int64(7)},
		{"[1 % inside\n 2]", Array{int64(1), int64(2)}},
		{"\x00\t\f 9", int64(9)},
	}
	for _, tt := range tests {
		got := parse(t, tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parse(%q) = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	lim := Limits{MaxDepth: 4, MaxString: 8, MaxItems: 3}
	for _, in := range []string{
		"", "   ", "(unterminated", "<48", "[1 2", "<< /A 1", "]", ">>", ")", "endobj", "[1 endobj]",
		"<< /A 1 endobj", "[-1 0 R]",
		"[[[[[1]]]]]",               // too deep
		"(123456789)",               // string too long
		"<112233445566778899>",      // hex string too long
		"[1 2 3 4]",                 // too many items
		"<< /A 1 /B 2 /C 3 /D 4 >>", // too many entries
		"/" + strings.Repeat("n", 5000),
	} {
		_, err := NewParser(NewBytesLexer([]byte(in), lim)).ParseObject()
		if err == nil {
			t.Errorf("parse(%q) succeeded, want an error", in)
		}
	}
	if _, err := NewParser(NewBytesLexer(nil, lim)).ParseObject(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("empty input: got %v, want io.ErrUnexpectedEOF", err)
	}
	if _, err := NewParser(NewBytesLexer([]byte("[[[[[1]]]]]"), lim)).ParseObject(); !errors.Is(err, ErrTooDeep) {
		t.Errorf("deep nesting: got %v, want ErrTooDeep", err)
	}
}

func TestParseIndirect(t *testing.T) {
	tests := []struct {
		in       string
		num, gen int
		want     Object
		data     string // for streams: the bytes at the stream offset
	}{
		{"7 0 obj\n<< /A 1 >>\nendobj", 7, 0, Dict{"A": int64(1)}, ""},
		{"3 2 obj 42 endobj", 3, 2, int64(42), ""},
		{"3 0 obj endobj", 3, 0, nil, ""},
		{"4 0 obj [1 0 R] endobj", 4, 0, Array{Ref{1, 0}}, ""},
		{"5 0 obj << /Length 3 >> stream\nabc\nendstream endobj", 5, 0, nil, "abc"},
		{"5 0 obj << /Length 3 >> stream\r\nabc\r\nendstream", 5, 0, nil, "abc"},
		{"5 0 obj << /Length 3 >>stream \nabc", 5, 0, nil, "abc"},
		{"5 0 obj << /Length 3 >> stream\n abc", 5, 0, nil, " abc"},
	}
	for _, tt := range tests {
		p := NewParser(NewBytesLexer([]byte(tt.in), DefaultLimits()))
		num, gen, obj, err := p.ParseIndirect()
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
			continue
		}
		if num != tt.num || gen != tt.gen {
			t.Errorf("%q: got object %d %d, want %d %d", tt.in, num, gen, tt.num, tt.gen)
		}
		if tt.data != "" {
			st, ok := obj.(*Stream)
			if !ok {
				t.Errorf("%q: got %T, want a stream", tt.in, obj)
				continue
			}
			if got := tt.in[st.Offset:]; !strings.HasPrefix(got, tt.data) {
				t.Errorf("%q: stream data starts at %q, want %q", tt.in, got, tt.data)
			}
			if st.Num != tt.num {
				t.Errorf("%q: stream object number %d, want %d", tt.in, st.Num, tt.num)
			}
			continue
		}
		if !reflect.DeepEqual(obj, tt.want) {
			t.Errorf("%q: got %#v, want %#v", tt.in, obj, tt.want)
		}
	}
	for _, in := range []string{"", "obj", "1 obj", "x 0 obj 1 endobj", "1 0 R", "-1 0 obj 1 endobj", "1 70000 obj 1 endobj"} {
		if _, _, _, err := NewParser(NewBytesLexer([]byte(in), DefaultLimits())).ParseIndirect(); err == nil {
			t.Errorf("ParseIndirect(%q) succeeded, want an error", in)
		}
	}
}

// TestReaderAtMatchesBytes checks that reading through an io.ReaderAt in
// chunks gives the same objects as reading from memory, including tokens
// that straddle chunk boundaries.
func TestReaderAtMatchesBytes(t *testing.T) {
	var src bytes.Buffer
	src.WriteString("[")
	for i := 0; i < 400; i++ {
		src.WriteString(" /Name")
		src.WriteString(strings.Repeat("x", i%37))
		src.WriteString(" (")
		src.WriteString(strings.Repeat("s", i%91))
		src.WriteString(") <")
		src.WriteString(strings.Repeat("4A", i%53))
		src.WriteString("> 12345 0 R -1.25 << /K [true null] >>")
	}
	src.WriteString(" (" + strings.Repeat("long ", 30000) + ")]")
	data := src.Bytes()

	want, err := NewParser(NewBytesLexer(data, DefaultLimits())).ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewParser(NewLexer(bytes.NewReader(data), 0, int64(len(data)), DefaultLimits())).ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Error("objects read through io.ReaderAt differ from objects read from memory")
	}
	if len(want.(Array)) != 400*6+1 {
		t.Errorf("array has %d items, want %d", len(want.(Array)), 400*6+1)
	}

	// A lexer limited to part of the source must not read past its limit.
	lex := NewLexer(bytes.NewReader([]byte("1 2 3 4 5")), 2, 5, DefaultLimits())
	var seen []int64
	for {
		tok, err := lex.Next()
		if err != nil || tok.Kind == EOF {
			break
		}
		seen = append(seen, tok.Int)
	}
	if !reflect.DeepEqual(seen, []int64{2, 3}) {
		t.Errorf("limited lexer read %v, want [2 3]", seen)
	}
}

func TestNumberHelpers(t *testing.T) {
	if v, ok := Int(int64(5)); !ok || v != 5 {
		t.Error("Int(5)")
	}
	if v, ok := Int(5.0); !ok || v != 5 {
		t.Error("Int(5.0)")
	}
	if _, ok := Int(5.5); ok {
		t.Error("Int(5.5) should fail")
	}
	if _, ok := Int(Name("x")); ok {
		t.Error("Int(name) should fail")
	}
	if v, ok := Number(int64(2)); !ok || v != 2 {
		t.Error("Number(2)")
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"<< /Type /Catalog /Pages 2 0 R >>", "[1 2 3 (a\\)b) <4a4B> /N#20 true null]",
		"1 0 obj << /Length 5 >> stream\nhello\nendstream endobj", "((((", "<<<<<<", "[[[[[[[[", "1 0 R 2 0 R",
		"% comment\r(\\777\\\r\n)", "9223372036854775808 -9223372036854775808 1e5 --1 1.2.3 .",
	} {
		f.Add([]byte(s))
	}
	lim := Limits{MaxDepth: 32, MaxString: 1 << 16, MaxItems: 1 << 10}
	f.Fuzz(func(t *testing.T, data []byte) {
		a, errA := NewParser(NewBytesLexer(data, lim)).ParseObject()
		b, errB := NewParser(NewLexer(bytes.NewReader(data), 0, int64(len(data)), lim)).ParseObject()
		if (errA == nil) != (errB == nil) {
			t.Fatalf("memory and ReaderAt lexers disagree on errors: %v vs %v", errA, errB)
		}
		if errA == nil && !reflect.DeepEqual(a, b) {
			t.Fatalf("memory and ReaderAt lexers disagree: %#v vs %#v", a, b)
		}
		n1, g1, o1, err1 := NewParser(NewBytesLexer(data, lim)).ParseIndirect()
		n2, g2, o2, err2 := NewParser(NewLexer(bytes.NewReader(data), 0, int64(len(data)), lim)).ParseIndirect()
		if (err1 == nil) != (err2 == nil) || n1 != n2 || g1 != g2 {
			t.Fatalf("ParseIndirect disagrees: %d %d %v vs %d %d %v", n1, g1, err1, n2, g2, err2)
		}
		if err1 == nil && !reflect.DeepEqual(o1, o2) {
			t.Fatalf("ParseIndirect objects disagree: %#v vs %#v", o1, o2)
		}
	})
}
