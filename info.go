// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"strings"
	"time"

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

// decodeTextString converts a PDF text string to a Go string.
func decodeTextString(s []byte) string { return pdfobj.DecodeText(s) }

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
