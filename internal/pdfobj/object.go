// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package pdfobj holds the PDF object model and the lexer and parser that
// read it. It knows nothing about file structure: cross-reference tables,
// object streams and encryption live in package reader.
package pdfobj

// Object is any PDF object. The dynamic type is one of:
//
//	nil      the null object
//	bool     a boolean
//	int64    an integer
//	float64  a real number
//	String   a string (literal or hexadecimal)
//	Name     a name
//	Array    an array
//	Dict     a dictionary
//	*Stream  a stream
//	Ref      an indirect reference
type Object = any

// String is a PDF string: raw bytes after unescaping. Its meaning (text,
// date, binary data) depends on where it appears.
type String []byte

// Name is a PDF name, without the leading slash and with #xx escapes decoded.
type Name string

// Array is a PDF array.
type Array []Object

// Dict is a PDF dictionary.
type Dict map[Name]Object

// Ref is an indirect reference to an object.
type Ref struct {
	Num int
	Gen int
}

// Stream is a stream object. Only the dictionary is held in memory; the data
// stays in the file and is read on demand.
type Stream struct {
	Dict Dict
	// Offset is the position of the first byte of stream data in the source.
	Offset int64
	// Num and Gen identify the object the stream belongs to. They are needed
	// to decrypt the data. Num is zero when unknown.
	Num, Gen int
}

// Int returns o as an integer. Reals with an integral value are accepted,
// because real-world files write counts and lengths either way.
func Int(o Object) (int64, bool) {
	switch v := o.(type) {
	case int64:
		return v, true
	case float64:
		if v == float64(int64(v)) && v > -1e15 && v < 1e15 {
			return int64(v), true
		}
	}
	return 0, false
}

// Number returns o as a float64 when it is an integer or a real.
func Number(o Object) (float64, bool) {
	switch v := o.(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}
