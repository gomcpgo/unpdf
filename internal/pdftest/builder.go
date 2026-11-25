// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package pdftest builds small PDF files in memory for tests: with a classic
// cross-reference table or a cross-reference stream and object stream, with
// incremental updates, and with standard-security-handler encryption.
//
// It is test support, not a PDF writer: it produces exactly the structures
// the reader has to cope with and nothing more.
package pdftest

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"sort"
	"strconv"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
)

// Layout selects how the file's cross-reference data is written.
type Layout int

const (
	// Classic writes "xref" tables and "trailer" dictionaries.
	Classic Layout = iota
	// Compressed writes a cross-reference stream, and packs every object
	// that is not a stream into one object stream.
	Compressed
)

type object struct {
	value pdfobj.Object // nil for a freed object
	data  []byte        // stream data, when value is a dictionary of a stream
	isStm bool
	freed bool
}

// Builder accumulates objects and writes them out as a PDF.
type Builder struct {
	// Version goes into the header. The default is "1.7".
	Version string
	// Layout selects the cross-reference format.
	Layout Layout
	// Encrypt, when set, encrypts the file.
	Encrypt *Encryption
	// Root and Info are the object numbers for the trailer.
	Root, Info int

	revisions []map[int]*object
	next      int
}

// New returns an empty builder.
func New() *Builder {
	return &Builder{revisions: []map[int]*object{{}}, next: 1}
}

func (b *Builder) current() map[int]*object { return b.revisions[len(b.revisions)-1] }

// Reserve returns a fresh object number without defining the object, for
// forward references.
func (b *Builder) Reserve() int {
	n := b.next
	b.next++
	return n
}

// Parse turns PDF syntax into an object. It panics on a syntax error, which
// in a test is a bug in the test.
func Parse(syntax string) pdfobj.Object {
	p := pdfobj.NewParser(pdfobj.NewBytesLexer([]byte(syntax), pdfobj.DefaultLimits()))
	obj, err := p.ParseObject()
	if err != nil {
		panic(fmt.Sprintf("pdftest: %v in %q", err, syntax))
	}
	return obj
}

// Add defines a new object from PDF syntax and returns its number.
func (b *Builder) Add(syntax string) int {
	n := b.Reserve()
	b.Set(n, syntax)
	return n
}

// Set defines object n, in the current revision, from PDF syntax.
func (b *Builder) Set(n int, syntax string) {
	b.current()[n] = &object{value: Parse(syntax)}
	b.next = max(b.next, n+1)
}

// AddStream defines a new stream object. dict is the stream dictionary
// without /Length, which is added.
func (b *Builder) AddStream(dict string, data []byte) int {
	n := b.Reserve()
	b.SetStream(n, dict, data)
	return n
}

// SetStream defines stream object n in the current revision.
func (b *Builder) SetStream(n int, dict string, data []byte) {
	d := Parse(dict).(pdfobj.Dict)
	if _, ok := d["Length"]; !ok {
		d["Length"] = int64(len(data))
	}
	b.current()[n] = &object{value: d, data: data, isStm: true}
	b.next = max(b.next, n+1)
}

// Free marks object n as deleted in the current revision.
func (b *Builder) Free(n int) {
	b.current()[n] = &object{freed: true}
}

// NewRevision starts an incremental update: objects set from now on are
// appended after the existing file with their own cross-reference section.
// Only the Classic layout supports revisions.
func (b *Builder) NewRevision() {
	b.revisions = append(b.revisions, map[int]*object{})
}

// Flate compresses data the way a /FlateDecode stream stores it.
func Flate(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

// Bytes writes the PDF.
func (b *Builder) Bytes() []byte {
	if b.Layout == Compressed && len(b.revisions) > 1 {
		panic("pdftest: revisions need the Classic layout")
	}
	var out bytes.Buffer
	version := b.Version
	if version == "" {
		version = "1.7"
	}
	fmt.Fprintf(&out, "%%PDF-%s\n%%\xE2\xE3\xCF\xD3\n", version)

	var enc *encryptor
	encNum := 0
	id := []byte("0123456789abcdef")
	if b.Encrypt != nil {
		enc = newEncryptor(*b.Encrypt, id)
		encNum = b.Reserve()
		b.revisions[0][encNum] = &object{value: enc.dict}
	}

	if b.Layout == Compressed {
		b.writeCompressed(&out, enc, encNum, id)
		return out.Bytes()
	}

	prev := int64(-1)
	for _, rev := range b.revisions {
		nums := sortedKeys(rev)
		offsets := map[int]int64{}
		for _, n := range nums {
			o := rev[n]
			if o.freed {
				continue
			}
			offsets[n] = int64(out.Len())
			b.writeObject(&out, n, o, enc, n == encNum)
		}
		start := int64(out.Len())
		out.WriteString("xref\n")
		if prev < 0 {
			// The first section lists every number from 0, as one block.
			fmt.Fprintf(&out, "0 %d\n", b.next)
			out.WriteString("0000000000 65535 f \n")
			for n := 1; n < b.next; n++ {
				if off, ok := offsets[n]; ok {
					fmt.Fprintf(&out, "%010d 00000 n \n", off)
				} else {
					out.WriteString("0000000000 00000 f \n")
				}
			}
		} else {
			for _, n := range nums {
				fmt.Fprintf(&out, "%d 1\n", n)
				if rev[n].freed {
					out.WriteString("0000000000 00001 f \n")
				} else {
					fmt.Fprintf(&out, "%010d 00000 n \n", offsets[n])
				}
			}
		}
		out.WriteString("trailer\n")
		out.Write(Serialize(b.trailer(encNum, id, prev)))
		fmt.Fprintf(&out, "\nstartxref\n%d\n%%%%EOF\n", start)
		prev = start
	}
	return out.Bytes()
}

func (b *Builder) trailer(encNum int, id []byte, prev int64) pdfobj.Dict {
	t := pdfobj.Dict{"Size": int64(b.next)}
	if b.Root > 0 {
		t["Root"] = pdfobj.Ref{Num: b.Root}
	}
	if b.Info > 0 {
		t["Info"] = pdfobj.Ref{Num: b.Info}
	}
	if encNum > 0 {
		t["Encrypt"] = pdfobj.Ref{Num: encNum}
		t["ID"] = pdfobj.Array{pdfobj.String(id), pdfobj.String(id)}
	}
	if prev >= 0 {
		t["Prev"] = prev
	}
	return t
}

func (b *Builder) writeObject(out *bytes.Buffer, n int, o *object, enc *encryptor, plain bool) {
	value := o.value
	data := o.data
	if enc != nil && !plain {
		value = enc.strings(value, n)
		if o.isStm {
			d := value.(pdfobj.Dict)
			// With ClearMetadata, metadata streams stay readable.
			clear := enc.e.ClearMetadata && enc.e.R >= 4 && d["Type"] == pdfobj.Name("Metadata")
			if !clear {
				data = enc.stream(data, n)
			}
			if l, ok := d["Length"].(int64); ok && l == int64(len(o.data)) {
				d["Length"] = int64(len(data))
			}
		}
	}
	fmt.Fprintf(out, "%d 0 obj\n", n)
	out.Write(Serialize(value))
	if o.isStm {
		out.WriteString("\nstream\n")
		out.Write(data)
		out.WriteString("\nendstream")
	}
	out.WriteString("\nendobj\n")
}

// writeCompressed writes streams and the encryption dictionary as ordinary
// objects, everything else into one object stream, and a cross-reference
// stream that uses the PNG "Up" predictor, as real producers do.
func (b *Builder) writeCompressed(out *bytes.Buffer, enc *encryptor, encNum int, id []byte) {
	rev := b.revisions[0]
	nums := sortedKeys(rev)
	stmNum := b.Reserve()
	xrefNum := b.Reserve()

	type row struct {
		typ      byte
		f1       int64
		f2       int
		assigned bool
	}
	rows := make([]row, b.next)
	rows[0] = row{0, 0, 65535, true}

	var index, body bytes.Buffer
	count := 0
	for _, n := range nums {
		o := rev[n]
		if o.freed {
			continue
		}
		if o.isStm || n == encNum {
			rows[n] = row{1, int64(out.Len()), 0, true}
			b.writeObject(out, n, o, enc, n == encNum)
			continue
		}
		fmt.Fprintf(&index, "%d %d ", n, body.Len())
		body.Write(Serialize(o.value))
		body.WriteByte('\n')
		rows[n] = row{2, int64(stmNum), count, true}
		count++
	}
	stmData := append(index.Bytes(), body.Bytes()...)
	stm := &object{
		value: pdfobj.Dict{
			"Type": pdfobj.Name("ObjStm"), "N": int64(count), "First": int64(index.Len()),
			"Filter": pdfobj.Name("FlateDecode"),
		},
		data: Flate(stmData), isStm: true,
	}
	stm.value.(pdfobj.Dict)["Length"] = int64(len(stm.data))
	rows[stmNum] = row{1, int64(out.Len()), 0, true}
	b.writeObject(out, stmNum, stm, enc, false)

	start := int64(out.Len())
	rows[xrefNum] = row{1, start, 0, true}
	// Rows are 1 + 4 + 2 bytes, each preceded by the predictor's filter byte.
	const width = 7
	var table bytes.Buffer
	prevRow := make([]byte, width)
	for _, r := range rows {
		cur := []byte{r.typ, byte(r.f1 >> 24), byte(r.f1 >> 16), byte(r.f1 >> 8), byte(r.f1), byte(r.f2 >> 8), byte(r.f2)}
		table.WriteByte(2)
		for i := range cur {
			table.WriteByte(cur[i] - prevRow[i])
		}
		prevRow = cur
	}
	d := b.trailer(encNum, id, -1)
	d["Type"] = pdfobj.Name("XRef")
	d["W"] = pdfobj.Array{int64(1), int64(4), int64(2)}
	d["Filter"] = pdfobj.Name("FlateDecode")
	d["DecodeParms"] = pdfobj.Dict{"Predictor": int64(12), "Columns": int64(width)}
	xref := &object{value: d, data: Flate(table.Bytes()), isStm: true}
	d["Length"] = int64(len(xref.data))
	// The cross-reference stream is never encrypted.
	b.writeObject(out, xrefNum, xref, nil, true)
	fmt.Fprintf(out, "startxref\n%d\n%%%%EOF\n", start)
}

func sortedKeys(m map[int]*object) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// Serialize writes an object in PDF syntax. Strings are written in
// hexadecimal, and dictionary keys in sorted order, so output is stable.
func Serialize(o pdfobj.Object) []byte {
	var b bytes.Buffer
	serialize(&b, o)
	return b.Bytes()
}

func serialize(b *bytes.Buffer, o pdfobj.Object) {
	switch v := o.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case float64:
		b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
	case pdfobj.String:
		fmt.Fprintf(b, "<%X>", []byte(v))
	case pdfobj.Name:
		b.WriteByte('/')
		for i := 0; i < len(v); i++ {
			c := v[i]
			if c <= ' ' || c >= 0x7f || c == '#' || pdfobj.IsDelim(c) {
				fmt.Fprintf(b, "#%02X", c)
			} else {
				b.WriteByte(c)
			}
		}
	case pdfobj.Ref:
		fmt.Fprintf(b, "%d %d R", v.Num, v.Gen)
	case pdfobj.Array:
		b.WriteByte('[')
		for i, x := range v {
			if i > 0 {
				b.WriteByte(' ')
			}
			serialize(b, x)
		}
		b.WriteByte(']')
	case pdfobj.Dict:
		b.WriteString("<<")
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		for _, k := range keys {
			serialize(b, pdfobj.Name(k))
			b.WriteByte(' ')
			serialize(b, v[pdfobj.Name(k)])
		}
		b.WriteString(">>")
	default:
		panic(fmt.Sprintf("pdftest: cannot serialize %T", o))
	}
}

// Document returns a builder holding a complete document of n pages. Page i
// has the content stream "BT (Page i) Tj ET", compressed with Flate. The
// information dictionary has a title and an author.
func Document(n int) *Builder {
	b := New()
	pages := b.Reserve()
	kids := ""
	for i := 1; i <= n; i++ {
		content := b.AddStream("<< /Filter /FlateDecode >>", Flate([]byte(fmt.Sprintf("BT (Page %d) Tj ET", i))))
		page := b.Add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /Contents %d 0 R >>", pages, content))
		kids += fmt.Sprintf("%d 0 R ", page)
	}
	b.Set(pages, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d /MediaBox [0 0 612 792] >>", kids, n))
	b.Root = b.Add(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pages))
	b.Info = b.Add("<< /Title (Test Document) /Author (unpdf) >>")
	return b
}

// The functions below damage a finished file the way real files get
// damaged.

// CutStartXref removes the final "startxref" and everything after it.
func CutStartXref(data []byte) []byte {
	return data[:bytes.LastIndex(data, []byte("startxref"))]
}

// BreakStartXref makes the final startxref offset point 137 bytes too far.
func BreakStartXref(data []byte) []byte {
	i := bytes.LastIndex(data, []byte("startxref")) + len("startxref\n")
	j := i + bytes.IndexByte(data[i:], '\n')
	n, _ := strconv.Atoi(string(data[i:j]))
	return append(append(append([]byte(nil), data[:i]...), strconv.Itoa(n+137)...), data[j:]...)
}

// ShiftOffsets inserts a comment after the header line, which makes every
// offset in the cross-reference data wrong.
func ShiftOffsets(data []byte) []byte {
	i := bytes.IndexByte(data, '\n') + 1
	return append(append(append([]byte(nil), data[:i]...), "% an inserted comment line\n"...), data[i:]...)
}

// PrefixJunk puts bytes before the %PDF header.
func PrefixJunk(data []byte) []byte {
	return append([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/pdf\r\n\r\n"), data...)
}
