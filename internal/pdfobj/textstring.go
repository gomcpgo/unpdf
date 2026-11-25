// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package pdfobj

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// pdfDocEncoding maps the bytes where PDFDocEncoding differs from Latin-1.
// Zero means the byte is undefined.
var pdfDocEncoding = map[byte]rune{
	0x18: 0x02D8, 0x19: 0x02C7, 0x1A: 0x02C6, 0x1B: 0x02D9, 0x1C: 0x02DD, 0x1D: 0x02DB, 0x1E: 0x02DA, 0x1F: 0x02DC,
	0x7F: 0,
	0x80: 0x2022, 0x81: 0x2020, 0x82: 0x2021, 0x83: 0x2026, 0x84: 0x2014, 0x85: 0x2013, 0x86: 0x0192, 0x87: 0x2044,
	0x88: 0x2039, 0x89: 0x203A, 0x8A: 0x2212, 0x8B: 0x2030, 0x8C: 0x201E, 0x8D: 0x201C, 0x8E: 0x201D, 0x8F: 0x2018,
	0x90: 0x2019, 0x91: 0x201A, 0x92: 0x2122, 0x93: 0xFB01, 0x94: 0xFB02, 0x95: 0x0141, 0x96: 0x0152, 0x97: 0x0160,
	0x98: 0x0178, 0x99: 0x017D, 0x9A: 0x0131, 0x9B: 0x0142, 0x9C: 0x0153, 0x9D: 0x0161, 0x9E: 0x017E, 0x9F: 0,
	0xA0: 0x20AC, 0xAD: 0,
}

// DecodeText converts a PDF text string to a Go string. Text strings
// are UTF-16 with a byte order mark, UTF-8 with a byte order mark (PDF 2.0),
// or PDFDocEncoding.
func DecodeText(s []byte) string {
	switch {
	case len(s) >= 2 && s[0] == 0xFE && s[1] == 0xFF:
		return decodeUTF16(s[2:], true)
	case len(s) >= 2 && s[0] == 0xFF && s[1] == 0xFE:
		return decodeUTF16(s[2:], false)
	case len(s) >= 3 && s[0] == 0xEF && s[1] == 0xBB && s[2] == 0xBF:
		return strings.ToValidUTF8(string(s[3:]), "�")
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range s {
		r, special := pdfDocEncoding[c]
		switch {
		case !special:
			if c >= 0x20 || c == '\t' || c == '\n' || c == '\r' {
				b.WriteRune(rune(c))
			}
		case r != 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func decodeUTF16(s []byte, bigEndian bool) string {
	units := make([]uint16, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		if bigEndian {
			units = append(units, uint16(s[i])<<8|uint16(s[i+1]))
		} else {
			units = append(units, uint16(s[i+1])<<8|uint16(s[i]))
		}
	}
	runes := utf16.Decode(units)
	out := make([]byte, 0, len(runes))
	for _, r := range runes {
		// Language tags are embedded as escape sequences: ESC, code, ESC.
		if r == 0 || r == 0x1B {
			continue
		}
		out = utf8.AppendRune(out, r)
	}
	return string(out)
}
