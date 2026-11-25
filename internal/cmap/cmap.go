// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package cmap parses CMap programs: the ToUnicode CMaps that map character
// codes to Unicode text, and the encoding CMaps embedded in composite fonts
// that map character codes to CIDs.
//
// A CMap is a small PostScript program. Only the handful of operators that
// carry mappings are interpreted; everything else is skipped.
package cmap

import (
	"errors"
	"io"
	"unicode/utf16"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
)

// MaxEntries caps the mappings kept from one CMap.
const MaxEntries = 1 << 20

// CodeRange is one codespace range: codes of N bytes between Lo and Hi,
// where every byte must lie between the corresponding bytes of Lo and Hi.
type CodeRange struct {
	N      int
	Lo, Hi uint32
}

type cidRange struct {
	n      int
	lo, hi uint32
	cid    uint32
}

type bfRange struct {
	lo, hi uint32
	start  []uint16 // UTF-16 text for lo; the last unit counts up through the range
	list   []string // or one string per code, when the range was given as an array
}

// CMap holds the mappings read from one CMap program.
type CMap struct {
	// Name is the /CMapName, if the program defines one.
	Name string
	// WMode is 1 for vertical writing, 0 for horizontal.
	WMode int
	// UseCMap names the CMap this one builds on, if any.
	UseCMap string
	// Space is the codespace: which byte sequences form a character code.
	Space []CodeRange

	cids    []cidRange
	singles map[uint32]string
	ranges  []bfRange
	entries int
}

// Parse reads a CMap program. Damaged input yields the mappings read before
// the damage together with an error.
func Parse(data []byte) (*CMap, error) {
	lim := pdfobj.Limits{MaxDepth: 8, MaxString: 1 << 16, MaxItems: 1 << 16}
	p := pdfobj.NewParser(pdfobj.NewBytesLexer(data, lim))
	c := &CMap{singles: map[uint32]string{}}

	var stack []pdfobj.Object // operands since the last operator
	section := ""
	errs := 0
	for {
		obj, op, err := p.ContentNext()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return c, nil
		}
		if err != nil {
			// Skip the bad token and carry on; give up if the whole
			// program is like this.
			if errs++; errs > 100 {
				return c, err
			}
			stack = stack[:0]
			continue
		}
		if op == "" {
			if len(stack) < 1<<16 {
				stack = append(stack, obj)
			}
			// Inside a mapping section the operands arrive in groups of two
			// or three; consume them as soon as a group is complete, so that
			// huge sections do not pile up.
			switch section {
			case "codespace", "bfchar", "cidchar":
				if len(stack) == 2 {
					c.add(section, stack)
					stack = stack[:0]
				}
			case "bfrange", "cidrange":
				if len(stack) == 3 {
					c.add(section, stack)
					stack = stack[:0]
				}
			}
			if c.entries > MaxEntries {
				return c, errors.New("cmap: too many mappings")
			}
			continue
		}
		switch op {
		case "begincodespacerange":
			section = "codespace"
		case "beginbfchar":
			section = "bfchar"
		case "beginbfrange":
			section = "bfrange"
		case "begincidchar":
			section = "cidchar"
		case "begincidrange":
			section = "cidrange"
		case "beginnotdefchar", "beginnotdefrange":
			section = "skip"
		case "endcodespacerange", "endbfchar", "endbfrange", "endcidchar", "endcidrange", "endnotdefchar", "endnotdefrange":
			section = ""
		case "def":
			if len(stack) >= 2 {
				key, _ := stack[len(stack)-2].(pdfobj.Name)
				switch v := stack[len(stack)-1].(type) {
				case int64:
					if key == "WMode" && (v == 0 || v == 1) {
						c.WMode = int(v)
					}
				case pdfobj.Name:
					if key == "CMapName" {
						c.Name = string(v)
					}
				}
			}
		case "usecmap":
			if len(stack) >= 1 {
				if n, ok := stack[len(stack)-1].(pdfobj.Name); ok {
					c.UseCMap = string(n)
				}
			}
		}
		stack = stack[:0]
	}
}

// code converts a source code string of up to 4 bytes to its value.
func code(o pdfobj.Object) (v uint32, n int, ok bool) {
	s, isStr := o.(pdfobj.String)
	if !isStr || len(s) == 0 || len(s) > 4 {
		return 0, 0, false
	}
	for _, b := range s {
		v = v<<8 | uint32(b)
	}
	return v, len(s), true
}

func (c *CMap) add(section string, args []pdfobj.Object) {
	switch section {
	case "codespace":
		lo, n, ok1 := code(args[0])
		hi, n2, ok2 := code(args[1])
		if ok1 && ok2 && n == n2 && len(c.Space) < 256 {
			c.Space = append(c.Space, CodeRange{N: n, Lo: lo, Hi: hi})
		}
	case "bfchar":
		src, _, ok := code(args[0])
		dst, isStr := args[1].(pdfobj.String)
		if ok && isStr {
			c.singles[src] = utf16String(dst)
			c.entries++
		}
	case "bfrange":
		lo, _, ok1 := code(args[0])
		hi, _, ok2 := code(args[1])
		if !ok1 || !ok2 || hi < lo {
			return
		}
		switch dst := args[2].(type) {
		case pdfobj.String:
			if lo == hi {
				c.singles[lo] = utf16String(dst)
			} else {
				c.ranges = append(c.ranges, bfRange{lo: lo, hi: hi, start: utf16Units(dst)})
			}
			c.entries++
		case pdfobj.Array:
			r := bfRange{lo: lo, hi: hi}
			for _, x := range dst {
				s, _ := x.(pdfobj.String)
				r.list = append(r.list, utf16String(s))
			}
			c.ranges = append(c.ranges, r)
			c.entries += len(dst)
		}
	case "cidchar":
		src, n, ok := code(args[0])
		cid, isInt := args[1].(int64)
		if ok && isInt && cid >= 0 && cid <= 1<<24 {
			c.cids = append(c.cids, cidRange{n: n, lo: src, hi: src, cid: uint32(cid)})
			c.entries++
		}
	case "cidrange":
		lo, n, ok1 := code(args[0])
		hi, n2, ok2 := code(args[1])
		cid, isInt := args[2].(int64)
		if ok1 && ok2 && isInt && n == n2 && hi >= lo && cid >= 0 && cid <= 1<<24 {
			c.cids = append(c.cids, cidRange{n: n, lo: lo, hi: hi, cid: uint32(cid)})
			c.entries++
		}
	}
}

func utf16Units(s []byte) []uint16 {
	units := make([]uint16, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		units = append(units, uint16(s[i])<<8|uint16(s[i+1]))
	}
	if len(s) == 1 {
		// A one-byte destination: producers mean the character itself.
		units = append(units, uint16(s[0]))
	}
	return units
}

func utf16String(s []byte) string {
	return string(utf16.Decode(utf16Units(s)))
}

// HasUnicode reports whether the CMap has any code-to-Unicode mappings.
func (c *CMap) HasUnicode() bool { return len(c.singles) > 0 || len(c.ranges) > 0 }

// HasCIDs reports whether the CMap has any code-to-CID mappings.
func (c *CMap) HasCIDs() bool { return len(c.cids) > 0 }

// Unicode returns the text that a character code maps to. When mappings
// overlap, the one defined last wins.
func (c *CMap) Unicode(code uint32) (string, bool) {
	if s, ok := c.singles[code]; ok {
		return s, true
	}
	for i := len(c.ranges) - 1; i >= 0; i-- {
		r := &c.ranges[i]
		if code < r.lo || code > r.hi {
			continue
		}
		off := code - r.lo
		if r.list != nil {
			if int(off) < len(r.list) {
				return r.list[off], true
			}
			return "", false
		}
		if len(r.start) == 0 {
			return "", false
		}
		units := append([]uint16(nil), r.start...)
		last := len(units) - 1
		if last >= 1 && utf16.IsSurrogate(rune(units[last-1])) && utf16.IsSurrogate(rune(units[last])) {
			// The range counts through code points outside the BMP.
			rn := utf16.DecodeRune(rune(units[last-1]), rune(units[last])) + rune(off)
			return string(utf16.Decode(units[:last-1])) + string(rn), true
		}
		units[last] += uint16(off)
		return string(utf16.Decode(units)), true
	}
	return "", false
}

// CID returns the CID that an n-byte character code maps to.
func (c *CMap) CID(code uint32, n int) (uint32, bool) {
	for i := len(c.cids) - 1; i >= 0; i-- {
		r := &c.cids[i]
		if r.n == n && code >= r.lo && code <= r.hi {
			return r.cid + (code - r.lo), true
		}
	}
	return 0, false
}

// Next splits the next character code off s using the codespace ranges. It
// returns the code and its length in bytes. When no range matches, it
// consumes the shortest code length the codespace allows, so that decoding
// stays in step as well as it can.
func Next(space []CodeRange, s []byte) (code uint32, n int) {
	if len(s) == 0 {
		return 0, 0
	}
	shortest := 0
	for _, r := range space {
		if shortest == 0 || r.N < shortest {
			shortest = r.N
		}
	}
	var v uint32
	for n := 1; n <= 4 && n <= len(s); n++ {
		v = v<<8 | uint32(s[n-1])
		for _, r := range space {
			if r.N == n && inRange(v, r) {
				return v, n
			}
		}
	}
	n = min(max(shortest, 1), len(s))
	v = 0
	for _, b := range s[:n] {
		v = v<<8 | uint32(b)
	}
	return v, n
}

// inRange checks v against a codespace range byte by byte.
func inRange(v uint32, r CodeRange) bool {
	for i := 0; i < r.N; i++ {
		shift := uint(8 * i)
		b, lo, hi := byte(v>>shift), byte(r.Lo>>shift), byte(r.Hi>>shift)
		if b < lo || b > hi {
			return false
		}
	}
	return true
}
