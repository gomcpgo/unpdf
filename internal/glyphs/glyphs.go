// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package glyphs turns what a simple font says about a character, a code in
// one of the standard encodings or a glyph name, into Unicode text.
//
// The glyph names come from the Adobe Glyph List, and the Standard, Symbol
// and ZapfDingbats encodings from the mapping tables Unicode publishes. The
// data files are embedded as distributed, with their notices, and parsed the
// first time they are needed.
package glyphs

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

//go:embed data/glyphlist.txt
var glyphListData string

//go:embed data/zapfdingbats.txt
var zapfListData string

//go:embed data/stdenc.txt
var standardData string

//go:embed data/symbol.txt
var symbolData string

//go:embed data/zdingbat.txt
var zapfData string

var (
	loadOnce  sync.Once
	glyphList map[string]string
	zapfList  map[string]string
	encodings map[string]*[256]rune
)

func load() {
	loadOnce.Do(func() {
		glyphList = parseGlyphList(glyphListData)
		zapfList = parseGlyphList(zapfListData)
		encodings = map[string]*[256]rune{
			"StandardEncoding":     parseEncoding(standardData),
			"SymbolEncoding":       parseEncoding(symbolData),
			"ZapfDingbatsEncoding": parseEncoding(zapfData),
			"WinAnsiEncoding":      winAnsi(),
			"MacRomanEncoding":     macRoman(),
		}
		// MacExpertEncoding holds small capitals and old-style figures. It
		// is rare, and the Standard table at least gets its ASCII right.
		encodings["MacExpertEncoding"] = encodings["StandardEncoding"]
	})
}

// parseGlyphList reads "name;XXXX XXXX" lines.
func parseGlyphList(data string) map[string]string {
	m := make(map[string]string, 4400)
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		name, hex, ok := strings.Cut(line, ";")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		var b strings.Builder
		for _, h := range strings.Fields(hex) {
			if v, err := strconv.ParseUint(h, 16, 32); err == nil {
				b.WriteRune(rune(v))
			}
		}
		if b.Len() > 0 {
			m[name] = b.String()
		}
	}
	return m
}

// parseEncoding reads the Unicode mapping tables: tab-separated lines of
// Unicode value and code, both in hexadecimal. When a code is listed more
// than once, the first line wins.
func parseEncoding(data string) *[256]rune {
	var t [256]rune
	for line := range strings.SplitSeq(data, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		u, err1 := strconv.ParseUint(strings.TrimSpace(f[0]), 16, 32)
		c, err2 := strconv.ParseUint(strings.TrimSpace(f[1]), 16, 8)
		if err1 == nil && err2 == nil && t[c] == 0 {
			t[c] = rune(u)
		}
	}
	return &t
}

// winAnsi builds WinAnsiEncoding: Windows code page 1252, with the two
// changes the PDF specification makes to it.
func winAnsi() *[256]rune {
	var t [256]rune
	for i := 0x20; i < 0x100; i++ {
		t[i] = rune(i)
	}
	high := [32]rune{
		0x20AC, 0x2022, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x2022, 0x017D, 0x2022,
		0x2022, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x2022, 0x017E, 0x0178,
	}
	copy(t[0x80:], high[:])
	t[0x7F] = 0x2022 // unused codes show a bullet
	t[0xA0] = 0x0020 // "space", not a no-break space
	t[0xAD] = 0x002D // "hyphen", not a soft hyphen
	return &t
}

// macRoman builds MacRomanEncoding from the Mac OS Roman character set.
func macRoman() *[256]rune {
	var t [256]rune
	for i := 0x20; i < 0x7F; i++ {
		t[i] = rune(i)
	}
	high := [128]rune{
		0x00C4, 0x00C5, 0x00C7, 0x00C9, 0x00D1, 0x00D6, 0x00DC, 0x00E1, 0x00E0, 0x00E2, 0x00E4, 0x00E3, 0x00E5, 0x00E7, 0x00E9, 0x00E8,
		0x00EA, 0x00EB, 0x00ED, 0x00EC, 0x00EE, 0x00EF, 0x00F1, 0x00F3, 0x00F2, 0x00F4, 0x00F6, 0x00F5, 0x00FA, 0x00F9, 0x00FB, 0x00FC,
		0x2020, 0x00B0, 0x00A2, 0x00A3, 0x00A7, 0x2022, 0x00B6, 0x00DF, 0x00AE, 0x00A9, 0x2122, 0x00B4, 0x00A8, 0x2260, 0x00C6, 0x00D8,
		0x221E, 0x00B1, 0x2264, 0x2265, 0x00A5, 0x00B5, 0x2202, 0x2211, 0x220F, 0x03C0, 0x222B, 0x00AA, 0x00BA, 0x03A9, 0x00E6, 0x00F8,
		0x00BF, 0x00A1, 0x00AC, 0x221A, 0x0192, 0x2248, 0x2206, 0x00AB, 0x00BB, 0x2026, 0x0020, 0x00C0, 0x00C3, 0x00D5, 0x0152, 0x0153,
		0x2013, 0x2014, 0x201C, 0x201D, 0x2018, 0x2019, 0x00F7, 0x25CA, 0x00FF, 0x0178, 0x2044, 0x20AC, 0x2039, 0x203A, 0xFB01, 0xFB02,
		0x2021, 0x00B7, 0x201A, 0x201E, 0x2030, 0x00C2, 0x00CA, 0x00C1, 0x00CB, 0x00C8, 0x00CD, 0x00CE, 0x00CF, 0x00CC, 0x00D3, 0x00D4,
		0xF8FF, 0x00D2, 0x00DA, 0x00DB, 0x00D9, 0x0131, 0x02C6, 0x02DC, 0x00AF, 0x02D8, 0x02D9, 0x02DA, 0x00B8, 0x02DD, 0x02DB, 0x02C7,
	}
	copy(t[0x80:], high[:])
	return &t
}

// Encoding returns the code-to-Unicode table of a named base encoding:
// StandardEncoding, WinAnsiEncoding, MacRomanEncoding, MacExpertEncoding,
// SymbolEncoding or ZapfDingbatsEncoding. Undefined codes are zero.
func Encoding(name string) (*[256]rune, bool) {
	load()
	t, ok := encodings[name]
	return t, ok
}

// Name converts a glyph name to Unicode text, following the rules of the
// Adobe Glyph List specification: the name is cut at the first period, split
// into components at underscores, and each component is looked up in the
// glyph list or read as a "uniXXXX" or "uXXXX" name. zapf selects the
// ZapfDingbats list, for that font's own names such as "a100".
//
// It returns false when any component is unknown.
func Name(name string, zapf bool) (string, bool) {
	load()
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "", false
	}
	if s, ok := component(name, zapf); ok {
		return s, true
	}
	if !strings.Contains(name, "_") {
		return "", false
	}
	var b strings.Builder
	for part := range strings.SplitSeq(name, "_") {
		s, ok := component(part, zapf)
		if !ok {
			return "", false
		}
		b.WriteString(s)
	}
	return b.String(), true
}

func component(name string, zapf bool) (string, bool) {
	if zapf {
		if s, ok := zapfList[name]; ok {
			return s, true
		}
	}
	if r, ok := greek[name]; ok {
		return string(r), true
	}
	if s, ok := glyphList[name]; ok {
		// A few names in the list map to the private use area, a relic of
		// fonts that predate the characters' encoding. Prefer better.
		if r := []rune(s); len(r) != 1 || r[0] < 0xE000 || r[0] > 0xF8FF {
			return s, true
		}
		if t, ok := texName(name); ok {
			return t, true
		}
		return s, true
	}
	if s, ok := texName(name); ok {
		return s, true
	}
	// "uni" followed by groups of four hexadecimal digits.
	if rest, ok := strings.CutPrefix(name, "uni"); ok && len(rest) >= 4 && len(rest)%4 == 0 {
		var b strings.Builder
		for i := 0; i < len(rest); i += 4 {
			r, ok := hexRune(rest[i : i+4])
			if !ok {
				return "", false
			}
			b.WriteRune(r)
		}
		return b.String(), true
	}
	// "u" followed by four to six hexadecimal digits.
	if rest, ok := strings.CutPrefix(name, "u"); ok && len(rest) >= 4 && len(rest) <= 6 {
		if r, ok := hexRune(rest); ok {
			return string(r), true
		}
	}
	return "", false
}

// hexRune parses upper-case hexadecimal digits as a Unicode scalar value.
func hexRune(s string) (rune, bool) {
	var v rune
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | rune(c-'0')
		case c >= 'A' && c <= 'F':
			v = v<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	if v > 0x10FFFF || (v >= 0xD800 && v <= 0xDFFF) || v == 0 {
		return 0, false
	}
	return v, true
}

// greek overrides three entries of the glyph list, which maps these names to
// look-alike symbols: the ohm sign, the increment sign and the micro sign.
// In running text and in mathematics the glyphs are the Greek letters.
var greek = map[string]rune{"Omega": 0x03A9, "Delta": 0x0394, "mu": 0x03BC}
