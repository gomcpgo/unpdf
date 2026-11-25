// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gomcpgo/unpdf/internal/pdftest"
)

var ctx = context.Background()

func openBytes(t *testing.T, data []byte, opts Options) *Document {
	t.Helper()
	doc, err := Open(ctx, bytes.NewReader(data), int64(len(data)), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return doc
}

func TestOpenFile(t *testing.T) {
	tests := []struct {
		file    string
		version string
		pages   int
		title   string
		subject string
	}{
		{"testdata/classic.pdf", "1.4", 2, "unpdf classic test", "Classic cross-reference table"},
		{"testdata/compressed.pdf", "1.5", 3, "unpdf compressed test", ""},
	}
	for _, tt := range tests {
		doc, err := OpenFile(ctx, tt.file, Options{})
		if err != nil {
			t.Fatalf("%s: %v", tt.file, err)
		}
		info := doc.Info()
		if info.Version != tt.version || info.Pages != tt.pages || info.Title != tt.title || info.Subject != tt.subject {
			t.Errorf("%s: info = %+v", tt.file, info)
		}
		if info.Author != "The unpdf Authors" || !strings.HasPrefix(info.Producer, "pdfTeX") || info.Creator != "TeX" {
			t.Errorf("%s: author %q, producer %q, creator %q", tt.file, info.Author, info.Producer, info.Creator)
		}
		if want := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC); !info.Created.Equal(want) {
			t.Errorf("%s: created %v, want %v", tt.file, info.Created, want)
		}
		if info.Encrypted || info.Tagged || info.Repaired {
			t.Errorf("%s: unexpected flags in %+v", tt.file, info)
		}
		n := 0
		for p, err := range doc.Pages(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			n++
			w, h := p.Size()
			if p.Number != n || math.Abs(w-595.276) > 0.01 || math.Abs(h-841.89) > 0.01 || p.Rotate != 0 {
				t.Errorf("%s: page %d: number %d, size %.3f x %.3f, rotate %d", tt.file, n, p.Number, w, h, p.Rotate)
			}
		}
		if n != tt.pages || doc.NumPages() != tt.pages {
			t.Errorf("%s: walked %d pages, NumPages %d, want %d", tt.file, n, doc.NumPages(), tt.pages)
		}
		if ws := doc.Warnings(); len(ws) != 0 {
			t.Errorf("%s: unexpected warnings %v", tt.file, ws)
		}
		if err := doc.Close(); err != nil {
			t.Errorf("%s: Close: %v", tt.file, err)
		}
		if err := doc.Close(); err != nil {
			t.Errorf("%s: second Close: %v", tt.file, err)
		}
	}

	// classic.pdf has a modification date with a time zone.
	doc, _ := OpenFile(ctx, "testdata/classic.pdf", Options{})
	defer doc.Close()
	if _, off := doc.Info().Modified.Zone(); off != 5*3600+30*60 || doc.Info().Modified.Hour() != 13 {
		t.Errorf("modified = %v, want 13:00 at +05:30", doc.Info().Modified)
	}

	if _, err := OpenFile(ctx, "testdata/no-such-file.pdf", Options{}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: got %v", err)
	}
	if _, err := OpenFile(ctx, "testdata/README.md", Options{}); !errors.Is(err, ErrNotPDF) {
		t.Errorf("not a PDF: got %v, want ErrNotPDF", err)
	}
}

// sameInfo compares two Info values. Times are compared as instants, because
// two equal times can carry different *time.Location pointers.
func sameInfo(a, b Info) bool {
	if !a.Created.Equal(b.Created) || !a.Modified.Equal(b.Modified) {
		return false
	}
	a.Created, a.Modified, b.Created, b.Modified = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	return a == b
}

func TestDamagedTestdataIsRepaired(t *testing.T) {
	for _, file := range []string{"testdata/classic.pdf", "testdata/compressed.pdf"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		want := openBytes(t, data, Options{}).Info()
		for name, damage := range map[string]func([]byte) []byte{
			"no startxref": pdftest.CutStartXref, "bad startxref": pdftest.BreakStartXref,
			"shifted": pdftest.ShiftOffsets, "junk prefix": pdftest.PrefixJunk,
		} {
			doc := openBytes(t, damage(data), Options{})
			got := doc.Info()
			if !got.Repaired {
				t.Errorf("%s, %s: not marked as repaired", file, name)
			}
			got.Repaired = false
			if !sameInfo(got, want) {
				t.Errorf("%s, %s: info differs after repair:\n got %+v\nwant %+v", file, name, got, want)
			}
			if ws := doc.Warnings(); len(ws) == 0 || ws[0].Code != WarnXrefRebuilt {
				t.Errorf("%s, %s: warnings = %v", file, name, ws)
			}
			n := 0
			for range doc.Pages(ctx) {
				n++
			}
			if n != want.Pages {
				t.Errorf("%s, %s: walked %d pages, want %d", file, name, n, want.Pages)
			}
		}
	}
}

func TestErrors(t *testing.T) {
	b := pdftest.Document(1)
	b.Encrypt = &pdftest.Encryption{R: 6, User: "user", Owner: "owner"}
	data := b.Bytes()

	_, err := Open(ctx, bytes.NewReader(data), int64(len(data)), Options{})
	if !errors.Is(err, ErrPasswordRequired) || errors.Is(err, ErrWrongPassword) {
		t.Errorf("no password: got %v", err)
	}
	_, err = Open(ctx, bytes.NewReader(data), int64(len(data)), Options{Password: "wrong"})
	if !errors.Is(err, ErrWrongPassword) || !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("wrong password: got %v", err)
	}
	for _, pw := range []string{"user", "owner"} {
		doc := openBytes(t, data, Options{Password: pw})
		if info := doc.Info(); !info.Encrypted || info.Title != "Test Document" {
			t.Errorf("password %q: info = %+v", pw, info)
		}
	}

	_, err = Open(ctx, strings.NewReader("not a pdf"), 9, Options{})
	if !errors.Is(err, ErrNotPDF) {
		t.Errorf("not a PDF: got %v", err)
	}
	_, err = Open(ctx, strings.NewReader("%PDF-1.7\nnothing"), 16, Options{})
	if !errors.Is(err, ErrDamaged) {
		t.Errorf("damaged: got %v", err)
	}

	big := pdftest.Document(40).Bytes()
	var limit *LimitError
	_, err = Open(ctx, bytes.NewReader(big), int64(len(big)), Options{Limits: Limits{MaxObjects: 10}})
	if !errors.As(err, &limit) || limit.Limit != 10 {
		t.Errorf("limit: got %v", err)
	}

	pubsec := pdftest.Document(1)
	enc := pubsec.Add("<< /Filter /Adobe.PubSec /V 4 >>")
	raw := pubsec.Bytes()
	raw = bytes.Replace(raw, []byte("trailer\n<<"), []byte(fmt.Sprintf("trailer\n<</Encrypt %d 0 R", enc)), 1)
	var unsupported *UnsupportedError
	_, err = Open(ctx, bytes.NewReader(raw), int64(len(raw)), Options{})
	if !errors.As(err, &unsupported) || !strings.Contains(unsupported.Feature, "Adobe.PubSec") {
		t.Errorf("public-key encryption: got %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Open(cancelled, bytes.NewReader(big), int64(len(big)), Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: got %v", err)
	}
}

func TestInfoSources(t *testing.T) {
	const xmp = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:xmp="http://ns.adobe.com/xap/1.0/"
  xmlns:pdf="http://ns.adobe.com/pdf/1.3/" xmp:CreatorTool="XMP Tool" pdf:Producer="XMP Producer">
 <dc:title><rdf:Alt><rdf:li xml:lang="x-default">XMP Title</rdf:li><rdf:li xml:lang="fr">Titre</rdf:li></rdf:Alt></dc:title>
 <dc:creator><rdf:Seq><rdf:li>Ann</rdf:li><rdf:li>Bob</rdf:li></rdf:Seq></dc:creator>
 <dc:description><rdf:Alt><rdf:li xml:lang="x-default">XMP Subject</rdf:li></rdf:Alt></dc:description>
 <dc:subject><rdf:Bag><rdf:li>alpha</rdf:li><rdf:li>beta</rdf:li></rdf:Bag></dc:subject>
 <xmp:CreateDate>2024-03-01T10:20:30+02:00</xmp:CreateDate>
 <xmp:ModifyDate>2024-03-02</xmp:ModifyDate>
</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`

	// XMP only: every field comes from the metadata stream.
	b := pdftest.Document(1)
	meta := b.AddStream("<< /Type /Metadata /Subtype /XML >>", []byte(xmp))
	b.Set(b.Root, fmt.Sprintf("<< /Type /Catalog /Pages 1 0 R /Metadata %d 0 R /MarkInfo << /Marked true >> /Version /2.0 >>", meta))
	b.Info = 0
	info := openBytes(t, b.Bytes(), Options{}).Info()
	want := Info{
		Version: "2.0", Pages: 1, Title: "XMP Title", Author: "Ann, Bob", Subject: "XMP Subject",
		Keywords: "alpha, beta", Creator: "XMP Tool", Producer: "XMP Producer", Tagged: true,
		Created:  time.Date(2024, 3, 1, 10, 20, 30, 0, time.FixedZone("", 2*3600)),
		Modified: time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC),
	}
	if !info.Created.Equal(want.Created) || !info.Modified.Equal(want.Modified) {
		t.Errorf("dates = %v, %v", info.Created, info.Modified)
	}
	info.Created, info.Modified, want.Created, want.Modified = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	if info != want {
		t.Errorf("XMP only:\n got %+v\nwant %+v", info, want)
	}

	// Both present: the information dictionary wins, XMP fills the gaps.
	// The title is UTF-16 with a byte order mark.
	b = pdftest.Document(1)
	meta = b.AddStream("<< /Type /Metadata /Subtype /XML >>", []byte(xmp))
	b.Set(b.Root, fmt.Sprintf("<< /Type /Catalog /Pages 1 0 R /Metadata %d 0 R >>", meta))
	b.Set(b.Info, "<< /Title <FEFF00440069006300740020005400690074006C0065002020AC> /Producer (Dict Producer) /CreationDate (D:20200102030405-08'00') >>")
	info = openBytes(t, b.Bytes(), Options{}).Info()
	if info.Title != "Dict Title €" || info.Producer != "Dict Producer" || info.Author != "Ann, Bob" || info.Version != "1.7" {
		t.Errorf("merged: %+v", info)
	}
	if want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.FixedZone("", -8*3600)); !info.Created.Equal(want) {
		t.Errorf("created = %v, want %v", info.Created, want)
	}

	// Broken XMP is a warning, not a failure.
	b = pdftest.Document(1)
	meta = b.AddStream("<< /Type /Metadata /Subtype /XML >>", []byte("<x:xmpmeta><unclosed>"))
	b.Set(b.Root, fmt.Sprintf("<< /Type /Catalog /Pages 1 0 R /Metadata %d 0 R >>", meta))
	doc := openBytes(t, b.Bytes(), Options{})
	if ws := doc.Warnings(); len(ws) != 1 || ws[0].Code != WarnMetadata || doc.Info().Title != "Test Document" {
		t.Errorf("broken XMP: warnings %v, title %q", ws, doc.Info().Title)
	}
}

func TestDecodeTextString(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain ASCII", "plain ASCII"},
		{"caf\xe9", "café"},                                // Latin-1 range
		{"\x93\x94 \x80 \x84 \xa0", "ﬁﬂ • — €"},            // PDFDocEncoding specials
		{"a\x00b\x7fc\x9fd\xade", "abcde"},                 // undefined and control bytes are dropped
		{"\xfe\xff\x00H\x00i\x20\x14", "Hi—"},              // UTF-16 big endian
		{"\xff\xfeH\x00i\x00", "Hi"},                       // UTF-16 little endian
		{"\xfe\xff\xd8\x3d\xde\x00", "\U0001F600"},         // surrogate pair
		{"\xfe\xff\x00\x1b\x00e\x00n\x00\x1b\x00A", "enA"}, // language escape markers are dropped
		{"\xef\xbb\xbfna\xc3\xafve", "naïve"},              // UTF-8 with a byte order mark
		{"\xfe\xff\x00A\x00", "A"},                         // odd length
		{"", ""},
	}
	for _, tt := range tests {
		if got := decodeTextString([]byte(tt.in)); got != tt.want {
			t.Errorf("decodeTextString(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseDate(t *testing.T) {
	utc := func(y, mo, d, h, mi, s int) time.Time { return time.Date(y, time.Month(mo), d, h, mi, s, 0, time.UTC) }
	tests := []struct {
		in   string
		want time.Time
	}{
		{"D:20240315143000Z", utc(2024, 3, 15, 14, 30, 0)},
		{"D:20240315143000+05'30'", time.Date(2024, 3, 15, 14, 30, 0, 0, time.FixedZone("", 19800))},
		{"D:20240315143000-0800", time.Date(2024, 3, 15, 14, 30, 0, 0, time.FixedZone("", -28800))},
		{"D:20240315143000+01'00", time.Date(2024, 3, 15, 14, 30, 0, 0, time.FixedZone("", 3600))},
		{"20240315", utc(2024, 3, 15, 0, 0, 0)},
		{"D:2024", utc(2024, 1, 1, 0, 0, 0)},
		{"D:202403151430", utc(2024, 3, 15, 14, 30, 0)},
		{" D:20240315143000 ", utc(2024, 3, 15, 14, 30, 0)},
		{"", time.Time{}},
		{"D:", time.Time{}},
		{"yesterday", time.Time{}},
		{"D:20241340", time.Time{}},
		{"D:0000", time.Time{}},
	}
	for _, tt := range tests {
		if got := parseDate(tt.in); !got.Equal(tt.want) {
			t.Errorf("parseDate(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// pageDoc builds a document around a page tree. build adds the tree's nodes
// and returns the object number of its root.
func pageDoc(t *testing.T, build func(b *pdftest.Builder) (pagesRoot int)) *Document {
	t.Helper()
	b := pdftest.New()
	root := build(b)
	b.Root = b.Add(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", root))
	return openBytes(t, b.Bytes(), Options{})
}

func TestPageAttributesAndInheritance(t *testing.T) {
	doc := pageDoc(t, func(b *pdftest.Builder) int {
		p1 := b.Add("<< /Type /Page >>")
		p2 := b.Add("<< /Type /Page /MediaBox [0 0 200 400] /Rotate 0 >>")
		p3 := b.Add("<< /Type /Page /CropBox [-50 10 150 9999] /Rotate -90 >>")
		p4 := b.Add("<< /Type /Page /MediaBox [300 300 0 0] /Rotate 450 >>")
		p5 := b.Add("<< /Type /Page /MediaBox [0 0 0 0] /Rotate 45 >>")
		inner := b.Add(fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R %d 0 R %d 0 R] /Count 3 /Rotate 180 >>", p3, p4, p5))
		return b.Add(fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R %d 0 R %d 0 R] /Count 5 /MediaBox [0 0 100 300] /Rotate 90 >>", p1, p2, inner))
	})
	want := []Page{
		{Number: 1, MediaBox: Rect{0, 0, 100, 300}, CropBox: Rect{0, 0, 100, 300}, Rotate: 90},   // all inherited
		{Number: 2, MediaBox: Rect{0, 0, 200, 400}, CropBox: Rect{0, 0, 200, 400}, Rotate: 0},    // own values
		{Number: 3, MediaBox: Rect{0, 0, 100, 300}, CropBox: Rect{0, 10, 100, 300}, Rotate: 270}, // crop clipped to media
		{Number: 4, MediaBox: Rect{0, 0, 300, 300}, CropBox: Rect{0, 0, 300, 300}, Rotate: 90},   // corners swapped, 450 = 90
		{Number: 5, MediaBox: Rect{0, 0, 612, 792}, CropBox: Rect{0, 0, 612, 792}, Rotate: 0},    // empty box and odd rotation
	}
	i := 0
	for p, err := range doc.Pages(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		w := want[i]
		if p.Number != w.Number || p.MediaBox != w.MediaBox || p.CropBox != w.CropBox || p.Rotate != w.Rotate {
			t.Errorf("page %d = {%v %v %d}, want {%v %v %d}", p.Number, p.MediaBox, p.CropBox, p.Rotate, w.MediaBox, w.CropBox, w.Rotate)
		}
		// Page(n) must agree with the walk.
		q, err := doc.Page(ctx, p.Number)
		if err != nil || q.MediaBox != p.MediaBox || q.CropBox != p.CropBox || q.Rotate != p.Rotate || q.Number != p.Number {
			t.Errorf("Page(%d) = %+v, %v; differs from the walk", p.Number, q, err)
		}
		i++
	}
	if i != 5 || doc.NumPages() != 5 {
		t.Errorf("walked %d pages, NumPages %d", i, doc.NumPages())
	}
	if w, h := (&Page{CropBox: Rect{0, 0, 100, 300}, Rotate: 90}).Size(); w != 300 || h != 100 {
		t.Errorf("Size of a rotated page = %v x %v, want 300 x 100", w, h)
	}
	for _, n := range []int{0, -1, 6, 1000} {
		if _, err := doc.Page(ctx, n); err == nil {
			t.Errorf("Page(%d) succeeded", n)
		}
	}
}

func TestPagesStopsEarly(t *testing.T) {
	doc := openBytes(t, pdftest.Document(100).Bytes(), Options{})
	n := 0
	for range doc.Pages(ctx) {
		n++
		if n == 3 {
			break
		}
	}
	if n != 3 {
		t.Errorf("visited %d pages", n)
	}

	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	n = 0
	var last error
	for _, err := range doc.Pages(cancelled) {
		last = err
		if err != nil {
			break
		}
		n++
		if n == 5 {
			cancel()
		}
	}
	if n != 5 || !errors.Is(last, context.Canceled) {
		t.Errorf("after cancel: %d pages, err %v", n, last)
	}
	if _, err := doc.Page(cancelled, 50); !errors.Is(err, context.Canceled) {
		t.Errorf("Page after cancel: %v", err)
	}
}

func TestMalformedPageTrees(t *testing.T) {
	t.Run("wrong counts", func(t *testing.T) {
		doc := pageDoc(t, func(b *pdftest.Builder) int {
			var kids []int
			for i := 0; i < 4; i++ {
				kids = append(kids, b.Add(fmt.Sprintf("<< /Type /Page /MediaBox [0 0 %d 10] >>", 10+i)))
			}
			a := b.Add(fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R %d 0 R] /Count 7 >>", kids[0], kids[1]))
			c := b.Add(fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R %d 0 R] >>", kids[2], kids[3]))
			return b.Add(fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R %d 0 R] /Count 4 >>", a, c))
		})
		for n := 1; n <= 4; n++ {
			p, err := doc.Page(ctx, n)
			if err != nil || p.MediaBox.X1 != float64(9+n) {
				t.Errorf("Page(%d) = %+v, %v", n, p, err)
			}
		}
	})

	t.Run("cycle", func(t *testing.T) {
		doc := pageDoc(t, func(b *pdftest.Builder) int {
			root := b.Reserve()
			page := b.Add("<< /Type /Page /MediaBox [0 0 10 10] >>")
			b.Set(root, fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R %d 0 R %d 0 R] /Count 3 >>", page, root, page))
			return root
		})
		n := 0
		for range doc.Pages(ctx) {
			n++
		}
		if n != 1 {
			t.Errorf("walked %d pages through a cyclic tree, want 1", n)
		}
		found := false
		for _, w := range doc.Warnings() {
			found = found || w.Code == WarnPageTree
		}
		if !found {
			t.Errorf("no page-tree warning in %v", doc.Warnings())
		}
	})

	t.Run("deep chain", func(t *testing.T) {
		doc := pageDoc(t, func(b *pdftest.Builder) int {
			node := b.Add("<< /Type /Page /MediaBox [0 0 10 10] >>")
			for i := 0; i < 200; i++ {
				node = b.Add(fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", node))
			}
			return node
		})
		for range doc.Pages(ctx) {
			t.Error("a page 200 levels deep should not be reached")
		}
		if _, err := doc.Page(ctx, 1); err == nil {
			t.Error("Page(1) succeeded through a 200-level chain")
		}
	})

	t.Run("missing count and missing kids", func(t *testing.T) {
		doc := pageDoc(t, func(b *pdftest.Builder) int {
			page := b.Add("<< /Type /Page /MediaBox [0 0 10 10] >>")
			return b.Add(fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R 999 0 R (junk) %d 0 R] >>", page, b.Add("<< /Type /Page /MediaBox [0 0 20 20] >>")))
		})
		if doc.NumPages() != 2 {
			t.Errorf("NumPages = %d, want 2 (counted)", doc.NumPages())
		}
	})

	t.Run("page limit", func(t *testing.T) {
		data := pdftest.Document(30).Bytes()
		doc := openBytes(t, data, Options{Limits: Limits{MaxPages: 10}})
		n := 0
		for range doc.Pages(ctx) {
			n++
		}
		if n != 10 {
			t.Errorf("walked %d pages with MaxPages 10", n)
		}
	})
}

func FuzzOpen(f *testing.F) {
	for _, name := range []string{"testdata/classic.pdf", "testdata/compressed.pdf"} {
		if data, err := os.ReadFile(name); err == nil {
			f.Add(data)
		}
	}
	for _, layout := range []pdftest.Layout{pdftest.Classic, pdftest.Compressed} {
		b := pdftest.Document(3)
		b.Layout = layout
		font := b.Add(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`)
		b.Set(2, fmt.Sprintf("<< /Type /Page /Parent 1 0 R /Contents 3 0 R /Resources << /Font << /F1 %d 0 R >> >> >>", font))
		b.SetStream(3, "<< >>", []byte("BT /F1 12 Tf 72 700 Td [(Hello) -300 (world)] TJ ET"))
		meta := b.AddStream("<< /Type /Metadata /Subtype /XML >>", []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"/>`))
		b.Set(b.Root, fmt.Sprintf("<< /Type /Catalog /Pages 1 0 R /Metadata %d 0 R >>", meta))
		f.Add(b.Bytes())
		b.Encrypt = &pdftest.Encryption{R: 4}
		f.Add(b.Bytes())
	}
	opts := Options{Limits: Limits{
		MaxObjects: 4096, MaxStreamBytes: 1 << 20, MaxDepth: 32, MaxStringBytes: 1 << 16, MaxItems: 1 << 12, MaxPages: 64,
	}}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Open(ctx, bytes.NewReader(data), int64(len(data)), opts)
		if err != nil {
			return
		}
		defer doc.Close()
		info := doc.Info()
		n := 0
		for p, err := range doc.Pages(ctx) {
			if err != nil {
				t.Fatalf("Pages failed without cancellation: %v", err)
			}
			n++
			if p.Number != n {
				t.Fatalf("page number %d at position %d", p.Number, n)
			}
			if w, h := p.Size(); !(w > 0 && h > 0) {
				t.Fatalf("page %d has size %v x %v", n, w, h)
			}
			text, err := p.Text(ctx)
			if err != nil {
				t.Fatalf("Text failed without cancellation: %v", err)
			}
			if !utf8.ValidString(text) {
				t.Fatalf("page %d: text is not valid UTF-8", n)
			}
			blocks, err := p.Blocks(ctx)
			if err != nil {
				t.Fatalf("Blocks failed without cancellation: %v", err)
			}
			for _, b := range blocks {
				if len(b.Lines) == 0 || math.IsNaN(b.Box.X0+b.Box.Y0+b.Box.X1+b.Box.Y1) {
					t.Fatalf("page %d: bad block %+v", n, b)
				}
			}
			if _, err := p.Scanned(ctx); err != nil {
				t.Fatalf("Scanned failed without cancellation: %v", err)
			}
		}
		if n > 64 {
			t.Fatalf("walked %d pages, over MaxPages", n)
		}
		for _, k := range []int{1, n, info.Pages} {
			doc.Page(ctx, k)
		}
		doc.Warnings()
	})
}

func FuzzMetadata(f *testing.F) {
	f.Add([]byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title><rdf:Alt><rdf:li>T</rdf:li></rdf:Alt></dc:title></rdf:Description></rdf:RDF></x:xmpmeta>`))
	f.Add([]byte("D:20240315143000+05'30'"))
	f.Add([]byte("\xfe\xff\x00H\x00i\xd8\x3d"))
	f.Add([]byte("2024-03-01T10:20:30+02:00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		parseXMP(data)
		parseDate(string(data))
		parseXMPDate(string(data))
		if s := decodeTextString(data); !strings.Contains(string(data), "\xef\xbb\xbf") && strings.ContainsRune(s, 0) {
			t.Fatalf("decodeTextString(%q) kept a NUL", data)
		}
	})
}
