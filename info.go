// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
)

// Info describes a document as a whole.
type Info struct {
	// Version is the PDF version, such as "1.7". The catalog's /Version
	// entry overrides the file header when it is newer.
	Version string
	// Pages is the number of pages the document declares.
	Pages int

	Title    string
	Author   string
	Subject  string
	Keywords string
	// Creator is the application that made the original document, and
	// Producer the one that converted it to PDF.
	Creator  string
	Producer string
	// Created and Modified are zero when the document does not say.
	Created  time.Time
	Modified time.Time

	// Encrypted reports whether the document is encrypted. Documents with
	// an empty user password are encrypted too, and open without asking.
	Encrypted bool
	// Tagged reports whether the document declares itself a tagged PDF,
	// which means it carries a structure tree.
	Tagged bool
	// Repaired reports whether the cross-reference table had to be rebuilt.
	Repaired bool
}

// Info returns the document's metadata. Fields come from the document
// information dictionary; whatever that leaves empty is filled from the XMP
// metadata stream.
func (d *Document) Info() Info { return d.info }

func (d *Document) readInfo(ctx context.Context) Info {
	f := d.f
	info := Info{
		Version:   f.Version,
		Encrypted: f.Encrypted(),
		Repaired:  f.Rebuilt(),
	}
	if v := string(f.Name(ctx, d.catalog["Version"])); v > info.Version && len(v) == len(info.Version) {
		info.Version = v
	}
	if mark := f.Dict(ctx, d.catalog["MarkInfo"]); mark != nil {
		info.Tagged, _ = f.Bool(ctx, mark["Marked"])
	}

	if dict := f.Dict(ctx, f.Trailer["Info"]); dict != nil {
		text := func(key pdfobj.Name) string {
			s, _ := f.String(ctx, dict[key])
			return strings.TrimSpace(decodeTextString(s))
		}
		info.Title = text("Title")
		info.Author = text("Author")
		info.Subject = text("Subject")
		info.Keywords = text("Keywords")
		info.Creator = text("Creator")
		info.Producer = text("Producer")
		info.Created = parseDate(text("CreationDate"))
		info.Modified = parseDate(text("ModDate"))
	}

	if st := f.Stream(ctx, d.catalog["Metadata"]); st != nil {
		data, err := f.DecodeStream(ctx, st)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				f.Warn(WarnMetadata, "XMP metadata could not be read: %v", err)
			}
		case len(data) > maxXMPBytes:
			f.Warn(WarnMetadata, "XMP metadata is larger than %d bytes and was ignored", maxXMPBytes)
		default:
			x, err := parseXMP(data)
			if err != nil {
				f.Warn(WarnMetadata, "XMP metadata is not well formed: %v", err)
			}
			fill := func(dst *string, src string) {
				if *dst == "" {
					*dst = strings.TrimSpace(src)
				}
			}
			fill(&info.Title, x.title)
			fill(&info.Author, x.author)
			fill(&info.Subject, x.subject)
			fill(&info.Keywords, x.keywords)
			fill(&info.Creator, x.creator)
			fill(&info.Producer, x.producer)
			if info.Created.IsZero() {
				info.Created = parseXMPDate(x.created)
			}
			if info.Modified.IsZero() {
				info.Modified = parseXMPDate(x.modified)
			}
		}
	}

	info.Pages = d.declaredPages(ctx)
	return info
}

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

// decodeTextString converts a PDF text string to a Go string. Text strings
// are UTF-16 with a byte order mark, UTF-8 with a byte order mark (PDF 2.0),
// or PDFDocEncoding.
func decodeTextString(s []byte) string {
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

// parseDate parses a PDF date, "D:YYYYMMDDHHmmSSOHH'mm'", where everything
// after the year is optional. It returns the zero time when s is not a date.
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "D:")
	digits := func(n int) (int, bool) {
		if len(s) < n {
			return 0, false
		}
		v := 0
		for i := 0; i < n; i++ {
			if s[i] < '0' || s[i] > '9' {
				return 0, false
			}
			v = v*10 + int(s[i]-'0')
		}
		s = s[n:]
		return v, true
	}
	year, ok := digits(4)
	if !ok || year < 1 {
		return time.Time{}
	}
	fields := [5]int{1, 1, 0, 0, 0} // month, day, hour, minute, second
	for i := range fields {
		v, ok := digits(2)
		if !ok {
			break
		}
		fields[i] = v
	}
	month, day, hour, minute, sec := fields[0], fields[1], fields[2], fields[3], fields[4]
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || sec > 60 {
		return time.Time{}
	}

	loc := time.UTC
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		sign := 1
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
		if h, ok := digits(2); ok && h <= 23 {
			s = strings.TrimPrefix(s, "'")
			m, _ := digits(2)
			if m > 59 {
				m = 0
			}
			if off := sign * (h*3600 + m*60); off != 0 {
				loc = time.FixedZone("", off)
			}
		}
	}
	return time.Date(year, time.Month(month), day, hour, minute, sec, 0, loc)
}
