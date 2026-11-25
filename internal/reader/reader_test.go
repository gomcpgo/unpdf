// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package reader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
	"github.com/gomcpgo/unpdf/internal/pdftest"
)

var ctx = context.Background()

func open(t *testing.T, data []byte, password string) *File {
	t.Helper()
	f, err := Open(ctx, bytes.NewReader(data), int64(len(data)), password, Limits{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return f
}

// check verifies that f is the document pdftest.Document(n) builds: the
// title in the information dictionary, and the content of every page.
func check(t *testing.T, f *File, n int) {
	t.Helper()
	info := f.Dict(ctx, f.Trailer["Info"])
	if title, _ := f.String(ctx, info["Title"]); string(title) != "Test Document" {
		t.Errorf("title = %q, want %q", title, "Test Document")
	}
	root := f.Dict(ctx, f.Trailer["Root"])
	pages := f.Dict(ctx, root["Pages"])
	kids := f.Array(ctx, pages["Kids"])
	if len(kids) != n {
		t.Fatalf("page tree has %d kids, want %d", len(kids), n)
	}
	for i, kid := range kids {
		page := f.Dict(ctx, kid)
		st := f.Stream(ctx, page["Contents"])
		if st == nil {
			t.Fatalf("page %d has no content stream", i+1)
		}
		data, err := f.DecodeStream(ctx, st)
		want := fmt.Sprintf("BT (Page %d) Tj ET", i+1)
		if err != nil || string(data) != want {
			t.Errorf("page %d content = %q, err %v; want %q", i+1, data, err, want)
		}
	}
}

func TestOpenEveryLayoutEncryptionAndDamage(t *testing.T) {
	layouts := map[string]pdftest.Layout{"classic": pdftest.Classic, "compressed": pdftest.Compressed}
	encryptions := map[string]*pdftest.Encryption{
		"plain":            nil,
		"rc4-40":           {R: 2, User: "user", Owner: "owner"},
		"rc4-128":          {R: 3, User: "user", Owner: "owner"},
		"aes-128":          {R: 4, User: "user", Owner: "owner"},
		"aes-256-r5":       {R: 5, User: "user", Owner: "owner"},
		"aes-256-r6":       {R: 6, User: "user", Owner: "owner"},
		"aes-128-empty-pw": {R: 4, User: "", Owner: "owner"},
		"aes-256-empty-pw": {R: 6, User: "", Owner: "owner"},
	}
	damages := map[string]func([]byte) []byte{
		"intact":          func(b []byte) []byte { return b },
		"no-startxref":    pdftest.CutStartXref,
		"bad-startxref":   pdftest.BreakStartXref,
		"shifted-offsets": pdftest.ShiftOffsets,
		"prefix-junk":     pdftest.PrefixJunk,
	}
	for lname, layout := range layouts {
		for ename, enc := range encryptions {
			for dname, damage := range damages {
				t.Run(lname+"/"+ename+"/"+dname, func(t *testing.T) {
					b := pdftest.Document(3)
					b.Layout, b.Encrypt = layout, enc
					data := damage(b.Bytes())

					passwords := []string{""}
					if enc != nil && enc.User != "" {
						passwords = []string{"user", "owner"}
					}
					for _, pw := range passwords {
						f := open(t, data, pw)
						check(t, f, 3)
						if f.Encrypted() != (enc != nil) {
							t.Errorf("Encrypted() = %v", f.Encrypted())
						}
						if f.Rebuilt() != (dname != "intact") {
							t.Errorf("Rebuilt() = %v with damage %q", f.Rebuilt(), dname)
						}
						if len(f.Warnings()) != 0 && dname == "intact" {
							t.Errorf("unexpected warnings: %v", f.Warnings())
						}
					}
					if enc != nil && enc.User != "" {
						if _, err := Open(ctx, bytes.NewReader(data), int64(len(data)), "", Limits{}); !errors.Is(err, ErrPasswordRequired) || errors.Is(err, ErrWrongPassword) {
							t.Errorf("no password: got %v, want ErrPasswordRequired", err)
						}
						if _, err := Open(ctx, bytes.NewReader(data), int64(len(data)), "nope", Limits{}); !errors.Is(err, ErrWrongPassword) || !errors.Is(err, ErrPasswordRequired) {
							t.Errorf("wrong password: got %v, want ErrWrongPassword", err)
						}
					}
				})
			}
		}
	}
}

func TestEncryptedMetadataStream(t *testing.T) {
	const xmp = "<?xpacket begin='' id='W5M0MpCehiHzreSzNTczkc9d'?><x:xmpmeta/>"
	for name, enc := range map[string]*pdftest.Encryption{
		"encrypted with the rest": {R: 4, User: "", Owner: "o"},
		"declared clear":          {R: 4, User: "", Owner: "o", ClearMetadata: true},
		"clear, revision 6":       {R: 6, User: "", Owner: "o", ClearMetadata: true},
	} {
		b := pdftest.Document(1)
		meta := b.AddStream("<< /Type /Metadata /Subtype /XML >>", []byte(xmp))
		b.Encrypt = enc
		f := open(t, b.Bytes(), "")
		obj, _ := f.Get(ctx, meta)
		data, err := f.DecodeStream(ctx, obj.(*pdfobj.Stream))
		if err != nil || string(data) != xmp {
			t.Errorf("%s: got %q, err %v", name, data, err)
		}
		check(t, f, 1)
	}
}

func TestIncrementalUpdate(t *testing.T) {
	b := pdftest.Document(2)
	extra := b.Add("(to be deleted)")
	b.NewRevision()
	b.Set(b.Info, "<< /Title (Updated Title) >>")
	b.Free(extra)
	data := b.Bytes()

	f := open(t, data, "")
	info := f.Dict(ctx, f.Trailer["Info"])
	if title, _ := f.String(ctx, info["Title"]); string(title) != "Updated Title" {
		t.Errorf("title = %q, want the updated one", title)
	}
	if obj, err := f.Get(ctx, extra); obj != nil || err != nil {
		t.Errorf("freed object = %v, %v; want nil", obj, err)
	}

	// After a rebuild the later definition must still win.
	f = open(t, pdftest.CutStartXref(data), "")
	info = f.Dict(ctx, f.Trailer["Info"])
	if title, _ := f.String(ctx, info["Title"]); string(title) != "Updated Title" {
		t.Errorf("after rebuild: title = %q, want the updated one", title)
	}
}

func TestWrongStreamLength(t *testing.T) {
	b := pdftest.New()
	content := []byte("BT (a stream with a wrong length) Tj ET")
	st := b.AddStream("<< /Length 7 >>", content)
	b.Root = b.Add("<< /Type /Catalog /Pages << /Type /Pages /Kids [] /Count 0 >> >>")
	f := open(t, b.Bytes(), "")
	obj, _ := f.Get(ctx, st)
	data, err := f.DecodeStream(ctx, obj.(*pdfobj.Stream))
	if err != nil || !bytes.Equal(data, content) {
		t.Errorf("got %q, err %v", data, err)
	}
	if ws := f.Warnings(); len(ws) != 1 || ws[0].Code != WarnStreamLength {
		t.Errorf("warnings = %v, want one %s", ws, WarnStreamLength)
	}
}

func TestIndirectStreamLength(t *testing.T) {
	b := pdftest.New()
	length := b.Reserve()
	st := b.AddStream(fmt.Sprintf("<< /Length %d 0 R >>", length), []byte("hello"))
	b.Set(length, "5")
	b.Root = b.Add("<< /Type /Catalog /Pages << /Type /Pages /Kids [] /Count 0 >> >>")
	f := open(t, b.Bytes(), "")
	obj, _ := f.Get(ctx, st)
	if data, err := f.DecodeStream(ctx, obj.(*pdfobj.Stream)); err != nil || string(data) != "hello" {
		t.Errorf("got %q, err %v", data, err)
	}
	if len(f.Warnings()) != 0 {
		t.Errorf("unexpected warnings: %v", f.Warnings())
	}
}

func TestFilterChainsAndUnsupportedFilters(t *testing.T) {
	b := pdftest.New()
	hex := fmt.Sprintf("%X>", pdftest.Flate([]byte("chained")))
	chained := b.AddStream("<< /Filter [/ASCIIHexDecode /FlateDecode] /DecodeParms [null << /Predictor 1 >>] >>", []byte(hex))
	image := b.AddStream("<< /Filter /DCTDecode >>", []byte("\xFF\xD8 not really a JPEG"))
	external := b.AddStream("<< /F (other.bin) >>", nil)
	b.Root = b.Add("<< /Type /Catalog /Pages << /Type /Pages /Kids [] /Count 0 >> >>")
	f := open(t, b.Bytes(), "")

	obj, _ := f.Get(ctx, chained)
	if data, err := f.DecodeStream(ctx, obj.(*pdfobj.Stream)); err != nil || string(data) != "chained" {
		t.Errorf("filter chain: got %q, err %v", data, err)
	}
	var unsupported *UnsupportedError
	obj, _ = f.Get(ctx, image)
	if _, err := f.DecodeStream(ctx, obj.(*pdfobj.Stream)); !errors.As(err, &unsupported) || unsupported.Feature != "filter DCTDecode" {
		t.Errorf("image filter: got %v", err)
	}
	if raw, err := f.RawStream(ctx, obj.(*pdfobj.Stream)); err != nil || !bytes.HasPrefix(raw, []byte("\xFF\xD8")) {
		t.Errorf("RawStream: got %q, err %v", raw, err)
	}
	obj, _ = f.Get(ctx, external)
	if _, err := f.DecodeStream(ctx, obj.(*pdfobj.Stream)); !errors.As(err, &unsupported) {
		t.Errorf("external file: got %v", err)
	}
}

func TestImageFilters(t *testing.T) {
	b := pdftest.New()
	jpeg := b.AddStream("<< /Filter /DCTDecode >>", []byte("a JPEG file"))
	wrapped := b.AddStream("<< /Filter [/ASCIIHexDecode /FlateDecode /CCITTFaxDecode] /DecodeParms [null null << /K -1 /Columns 99 >>] >>",
		[]byte(fmt.Sprintf("%X>", pdftest.Flate([]byte("fax data")))))
	plain := b.AddStream("<< /Filter /FlateDecode >>", pdftest.Flate([]byte("samples")))
	under := b.AddStream("<< /Filter [/DCTDecode /FlateDecode] >>", []byte("the wrong way round"))
	b.Root = b.Add("<< /Type /Catalog /Pages << /Type /Pages /Kids [] /Count 0 >> >>")
	f := open(t, b.Bytes(), "")
	stream := func(n int) *pdfobj.Stream {
		obj, _ := f.Get(ctx, n)
		return obj.(*pdfobj.Stream)
	}

	// The data comes back in the image's own format, whatever wraps it.
	for _, tt := range []struct {
		num          int
		filter, data string
	}{{jpeg, "DCTDecode", "a JPEG file"}, {wrapped, "CCITTFaxDecode", "fax data"}, {plain, "", "samples"}} {
		st := stream(tt.num)
		name, parms := f.ImageFilter(ctx, st.Dict)
		data, err := f.DecodeImage(ctx, st)
		if name != tt.filter || err != nil || string(data) != tt.data {
			t.Errorf("object %d: filter %q, data %q, err %v; want %q, %q", tt.num, name, data, err, tt.filter, tt.data)
		}
		if tt.num == wrapped && parms["Columns"] != int64(99) {
			t.Errorf("parameters of the image filter = %v", parms)
		}
	}
	var unsupported *UnsupportedError
	if _, err := f.DecodeImage(ctx, stream(under)); !errors.As(err, &unsupported) {
		t.Errorf("an image filter under another filter: got %v", err)
	}

	// The same for data that is not in a stream of the file.
	hex := pdftest.Parse("<< /Filter [/ASCIIHexDecode /DCTDecode] >>").(pdfobj.Dict)
	if data, err := f.DecodeInline(ctx, hex, []byte("FF D8>")); err != nil || string(data) != "\xFF\xD8" {
		t.Errorf("DecodeInline: got %q, err %v", data, err)
	}
	if data, err := f.DecodeInline(ctx, pdfobj.Dict{}, []byte("as it is")); err != nil || string(data) != "as it is" {
		t.Errorf("DecodeInline with no filter: got %q, err %v", data, err)
	}
	if !IsImageFilter("JPXDecode") || !IsImageFilter("JBIG2Decode") || IsImageFilter("FlateDecode") {
		t.Error("IsImageFilter gives wrong answers")
	}
}

func TestLoosePagesAreAdopted(t *testing.T) {
	// A file whose catalog and page tree are gone, as after truncation.
	b := pdftest.New()
	for i := 1; i <= 3; i++ {
		content := b.AddStream("<< >>", []byte(fmt.Sprintf("BT (Page %d) Tj ET", i)))
		b.Add(fmt.Sprintf("<< /Type /Page /MediaBox [0 0 100 100] /Contents %d 0 R >>", content))
	}
	for _, layout := range []pdftest.Layout{pdftest.Classic, pdftest.Compressed} {
		b.Layout = layout
		f := open(t, b.Bytes(), "")
		root := f.Dict(ctx, f.Trailer["Root"])
		pages := f.Dict(ctx, root["Pages"])
		if n, _ := f.Int(ctx, pages["Count"]); n != 3 {
			t.Errorf("layout %d: adopted %d pages, want 3", layout, n)
		}
		for i, kid := range f.Array(ctx, pages["Kids"]) {
			data, _ := f.DecodeStream(ctx, f.Stream(ctx, f.Dict(ctx, kid)["Contents"]))
			if want := fmt.Sprintf("BT (Page %d) Tj ET", i+1); string(data) != want {
				t.Errorf("layout %d: page %d content = %q, want %q", layout, i+1, data, want)
			}
		}
		found := false
		for _, w := range f.Warnings() {
			found = found || w.Code == WarnPageTree
		}
		if !found {
			t.Errorf("layout %d: no %s warning in %v", layout, WarnPageTree, f.Warnings())
		}
	}
}

func TestLazyRebuildOnBadOffset(t *testing.T) {
	// Corrupt one cross-reference entry that Open does not need: the reader
	// notices on first use and repairs the table.
	b := pdftest.Document(2)
	data := b.Bytes()
	info := fmt.Sprintf("%d 0 obj", b.Info)
	off := bytes.Index(data, []byte(info))
	entry := []byte(fmt.Sprintf("%010d 00000 n", off))
	if !bytes.Contains(data, entry) {
		t.Fatal("test setup: cross-reference entry not found")
	}
	data = bytes.Replace(data, entry, []byte(fmt.Sprintf("%010d 00000 n", off+3)), 1)

	f := open(t, data, "")
	if f.Rebuilt() {
		t.Fatal("rebuilt at open; the damage should not be visible yet")
	}
	check(t, f, 2)
	if !f.Rebuilt() {
		t.Error("the table was not rebuilt after a bad offset")
	}
}

func TestNotPDFAndDamaged(t *testing.T) {
	for name, data := range map[string]string{
		"empty":      "",
		"text":       "hello, world",
		"html":       "<html><body>404</body></html>",
		"late magic": strings.Repeat(" ", 5000) + "%PDF-1.4",
	} {
		if _, err := Open(ctx, strings.NewReader(data), int64(len(data)), "", Limits{}); !errors.Is(err, ErrNotPDF) {
			t.Errorf("%s: got %v, want ErrNotPDF", name, err)
		}
	}
	for name, data := range map[string]string{
		"header only":   "%PDF-1.7\n",
		"garbage":       "%PDF-1.7\n" + strings.Repeat("garbage ", 100),
		"only a string": "%PDF-1.4\n1 0 obj (hello) endobj\nstartxref\n9\n%%EOF",
	} {
		if _, err := Open(ctx, strings.NewReader(data), int64(len(data)), "", Limits{}); !errors.Is(err, ErrDamaged) {
			t.Errorf("%s: got %v, want ErrDamaged", name, err)
		}
	}
}

func TestHostileStructures(t *testing.T) {
	root := "<< /Type /Catalog /Pages << /Type /Pages /Kids [] /Count 0 >> >>"

	t.Run("reference loop", func(t *testing.T) {
		b := pdftest.New()
		a, c := b.Reserve(), b.Reserve()
		b.Set(a, fmt.Sprintf("%d 0 R", c))
		b.Set(c, fmt.Sprintf("%d 0 R", a))
		b.Root = b.Add(root)
		f := open(t, b.Bytes(), "")
		if _, err := f.Resolve(ctx, pdfobj.Ref{Num: a}); err == nil {
			t.Error("a reference loop resolved without an error")
		}
		if d := f.Dict(ctx, pdfobj.Ref{Num: a}); d != nil {
			t.Errorf("Dict on a loop = %v, want nil", d)
		}
	})

	t.Run("decompression bomb", func(t *testing.T) {
		b := pdftest.New()
		bomb := b.AddStream("<< /Filter /FlateDecode >>", pdftest.Flate(make([]byte, 4<<20)))
		b.Root = b.Add(root)
		data := b.Bytes()
		f, err := Open(ctx, bytes.NewReader(data), int64(len(data)), "", Limits{MaxStreamBytes: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		obj, _ := f.Get(ctx, bomb)
		var limit *LimitError
		if _, err := f.DecodeStream(ctx, obj.(*pdfobj.Stream)); !errors.As(err, &limit) {
			t.Errorf("got %v, want a LimitError", err)
		}
	})

	t.Run("too many objects", func(t *testing.T) {
		data := pdftest.Document(50).Bytes()
		var limit *LimitError
		if _, err := Open(ctx, bytes.NewReader(data), int64(len(data)), "", Limits{MaxObjects: 20}); !errors.As(err, &limit) {
			t.Errorf("got %v, want a LimitError", err)
		}
	})

	t.Run("deep nesting", func(t *testing.T) {
		deep := strings.Repeat("[", 500) + strings.Repeat("]", 500)
		data := []byte("%PDF-1.4\n1 0 obj " + deep + " endobj\n2 0 obj << /Type /Catalog /Pages 3 0 R >> endobj\n" +
			"3 0 obj << /Type /Pages /Kids [] /Count 0 >> endobj\ntrailer << /Root 2 0 R >>\n")
		f := open(t, data, "")
		if _, err := f.Get(ctx, 1); !errors.Is(err, pdfobj.ErrTooDeep) {
			t.Errorf("got %v, want ErrTooDeep", err)
		}
	})

	t.Run("object stream that refers to itself", func(t *testing.T) {
		// Object 5's /Length is object 6, and the cross-reference stream
		// says object 6 lives inside object stream 5.
		var buf bytes.Buffer
		buf.WriteString("%PDF-1.5\n")
		off5 := buf.Len()
		buf.WriteString("5 0 obj << /Type /ObjStm /N 1 /First 4 /Length 6 0 R >> stream\n6 0 42\nendstream endobj\n")
		off1 := buf.Len()
		buf.WriteString("1 0 obj " + root + " endobj\n")
		offX := buf.Len()
		rows := []byte{
			0, 0, 0, 0, // object 0: free
			1, 0, byte(off1), 0, // object 1
			0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // objects 2 to 4: free
			1, 0, byte(off5), 0, // object 5
			2, 0, 5, 0, // object 6: in object stream 5, index 0
			1, 0, byte(offX), 0, // object 7: this stream
		}
		fmt.Fprintf(&buf, "7 0 obj << /Type /XRef /Size 8 /W [1 2 1] /Root 1 0 R /Length %d >> stream\n", len(rows))
		buf.Write(rows)
		fmt.Fprintf(&buf, "\nendstream endobj\nstartxref\n%d\n%%%%EOF\n", offX)
		f := open(t, buf.Bytes(), "")
		// Either answer is acceptable; looping forever or crashing is not.
		f.Get(ctx, 6)
	})
}

func TestContextCancelled(t *testing.T) {
	data := pdftest.Document(2).Bytes()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for name, d := range map[string][]byte{"intact": data, "needs rebuild": pdftest.CutStartXref(data)} {
		if _, err := Open(cancelled, bytes.NewReader(d), int64(len(d)), "", Limits{}); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: got %v, want context.Canceled", name, err)
		}
	}
}

func TestXrefTableQuirks(t *testing.T) {
	// The table starts at object 1 although its first entry is the free
	// head for object 0, and the lines have irregular spacing.
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	off1 := buf.Len()
	buf.WriteString("1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n")
	off2 := buf.Len()
	buf.WriteString("2 0 obj << /Type /Pages /Kids [] /Count 0 >> endobj\n")
	start := buf.Len()
	fmt.Fprintf(&buf, "xref\n1 3\n0000000000 65535 f\n%d 0 n\r\n  %010d   00000 n \n", off1, off2)
	fmt.Fprintf(&buf, "trailer\n<< /Size 3 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", start)
	f := open(t, buf.Bytes(), "")
	if f.Rebuilt() {
		t.Error("an off-by-one table should be read without a rebuild")
	}
	if d := f.Dict(ctx, pdfobj.Ref{Num: 2}); !isType(d, "Pages") {
		t.Errorf("object 2 = %v", d)
	}
}

func TestHybridFile(t *testing.T) {
	// The table marks object 3 free; the stream named by /XRefStm says it is
	// the first object of object stream 4.
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.5\n")
	off1 := buf.Len()
	buf.WriteString("1 0 obj << /Type /Catalog /Pages 2 0 R /Extra 3 0 R >> endobj\n")
	off2 := buf.Len()
	buf.WriteString("2 0 obj << /Type /Pages /Kids [] /Count 0 >> endobj\n")
	off4 := buf.Len()
	buf.WriteString("4 0 obj << /Type /ObjStm /N 1 /First 4 /Length 13 >> stream\n3 0 (hidden)\nendstream endobj\n")
	off5 := buf.Len()
	rows := []byte{2, 4, 0} // object 3: type 2, object stream 4, index 0
	fmt.Fprintf(&buf, "5 0 obj << /Type /XRef /Size 6 /Index [3 1] /W [1 1 1] /Length %d >> stream\n", len(rows))
	buf.Write(rows)
	buf.WriteString("\nendstream endobj\n")
	start := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 6\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n0000000000 00000 f \n%010d 00000 n \n%010d 00000 n \n",
		off1, off2, off4, off5)
	fmt.Fprintf(&buf, "trailer\n<< /Size 6 /Root 1 0 R /XRefStm %d >>\nstartxref\n%d\n%%%%EOF\n", off5, start)

	f := open(t, buf.Bytes(), "")
	if f.Rebuilt() {
		t.Fatal("a hybrid file should be read without a rebuild")
	}
	if s, ok := f.String(ctx, pdfobj.Ref{Num: 3}); !ok || string(s) != "hidden" {
		t.Errorf("object 3 = %q, %v; want the object from the object stream", s, ok)
	}
	if obj, _ := f.Get(ctx, 0); obj != nil {
		t.Errorf("object 0 = %v, want nil", obj)
	}
	if len(f.Warnings()) != 0 {
		t.Errorf("unexpected warnings: %v", f.Warnings())
	}
}

func FuzzOpen(f *testing.F) {
	for _, layout := range []pdftest.Layout{pdftest.Classic, pdftest.Compressed} {
		for _, enc := range []*pdftest.Encryption{nil, {R: 3}, {R: 4}, {R: 6}} {
			b := pdftest.Document(2)
			b.Layout, b.Encrypt = layout, enc
			data := b.Bytes()
			f.Add(data)
			f.Add(pdftest.CutStartXref(data))
		}
	}
	b := pdftest.Document(2)
	b.NewRevision()
	b.Set(b.Info, "<< /Title (v2) >>")
	f.Add(b.Bytes())

	lim := Limits{MaxObjects: 4096, MaxStreamBytes: 1 << 20, MaxDepth: 32, MaxStringBytes: 1 << 16, MaxItems: 1 << 12}
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := Open(ctx, bytes.NewReader(data), int64(len(data)), "", lim)
		if err != nil {
			return
		}
		for num := 0; num < file.NumObjects() && num < 200; num++ {
			obj, err := file.Get(ctx, num)
			if err != nil {
				continue
			}
			if st, ok := obj.(*pdfobj.Stream); ok {
				if out, err := file.DecodeStream(ctx, st); err == nil && int64(len(out)) > lim.MaxStreamBytes {
					t.Fatalf("decoded %d bytes, over the limit", len(out))
				}
			}
			file.Dict(ctx, obj)
		}
		file.Warnings()
	})
}
