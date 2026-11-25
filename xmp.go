// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"time"
)

// maxXMPBytes caps the XMP packet that will be parsed. Packets are a few
// kilobytes of XML plus padding.
const maxXMPBytes = 16 << 20

const (
	nsDC  = "http://purl.org/dc/elements/1.1/"
	nsXMP = "http://ns.adobe.com/xap/1.0/"
	nsPDF = "http://ns.adobe.com/pdf/1.3/"
	nsRDF = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
)

type xmpInfo struct {
	title, author, subject, keywords string
	creator, producer                string
	created, modified                string
}

// parseXMP pulls the document-level properties out of an XMP packet. It
// reads both ways a property can be written: as a child element (possibly
// wrapping an rdf:Alt, rdf:Seq or rdf:Bag of rdf:li items) and as an
// attribute of rdf:Description. On malformed XML it returns what it had
// collected up to that point along with the error.
func parseXMP(data []byte) (xmpInfo, error) {
	var x xmpInfo
	var subjects []string

	// field returns where the property named by an element or attribute
	// should be stored, or nil when it is not one we keep.
	field := func(name xml.Name) *string {
		switch name.Space {
		case nsDC:
			switch name.Local {
			case "title":
				return &x.title
			case "creator":
				return &x.author
			case "description":
				return &x.subject
			}
		case nsPDF:
			switch name.Local {
			case "Keywords":
				return &x.keywords
			case "Producer":
				return &x.producer
			}
		case nsXMP:
			switch name.Local {
			case "CreatorTool":
				return &x.creator
			case "CreateDate":
				return &x.created
			case "ModifyDate":
				return &x.modified
			}
		}
		return nil
	}

	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var (
		current   *string // property being collected, nil outside one
		isSubject bool    // inside dc:subject, whose items become keywords
		depth     int     // element depth inside the current property
		items     []string
		text      strings.Builder
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return x, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if current == nil && !isSubject {
				if t.Name.Space == nsRDF && t.Name.Local == "Description" {
					for _, a := range t.Attr {
						if p := field(a.Name); p != nil && *p == "" {
							*p = a.Value
						}
					}
					continue
				}
				current = field(t.Name)
				isSubject = t.Name.Space == nsDC && t.Name.Local == "subject"
				depth, items = 0, nil
				text.Reset()
				continue
			}
			depth++
			if t.Name.Space == nsRDF && t.Name.Local == "li" {
				text.Reset()
			}
		case xml.CharData:
			if current != nil || isSubject {
				text.Write(t)
			}
		case xml.EndElement:
			if current == nil && !isSubject {
				continue
			}
			if depth > 0 {
				depth--
				if t.Name.Space == nsRDF && t.Name.Local == "li" {
					if s := strings.TrimSpace(text.String()); s != "" {
						items = append(items, s)
					}
					text.Reset()
				}
				continue
			}
			// The property element itself is closing.
			value := strings.TrimSpace(text.String())
			if len(items) > 0 {
				value = items[0]
				// An author list keeps every entry; a language alternative
				// keeps the first, which is the default language.
				if current == &x.author {
					value = strings.Join(items, ", ")
				}
			}
			switch {
			case isSubject:
				subjects = append(subjects, items...)
			case *current == "":
				*current = value
			}
			current, isSubject = nil, false
		}
	}
	if x.keywords == "" {
		x.keywords = strings.Join(subjects, ", ")
	}
	return x, nil
}

// parseXMPDate parses the ISO 8601 profile that XMP uses, from a bare year
// up to a full timestamp with a zone.
func parseXMPDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04Z07:00",
		"2006-01-02T15:04",
		"2006-01-02",
		"2006-01",
		"2006",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
