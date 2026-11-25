// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package reader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
)

// trailerKeys are the trailer entries worth keeping.
var trailerKeys = []pdfobj.Name{"Root", "Info", "ID", "Encrypt"}

// loadXref reads the cross-reference sections, newest first, following
// /Prev (incremental updates) and /XRefStm (hybrid files).
func (f *File) loadXref(ctx context.Context) error {
	start, err := f.findStartXref()
	if err != nil {
		return err
	}
	todo := []int64{start}
	seen := map[int64]bool{}
	for len(todo) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		off := todo[0]
		todo = todo[1:]
		if seen[off] {
			continue
		}
		seen[off] = true
		if len(seen) > f.lim.MaxXrefSections {
			return &LimitError{"number of cross-reference sections", int64(f.lim.MaxXrefSections)}
		}
		tr, freed, err := f.readXrefSection(ctx, off)
		if err != nil {
			return fmt.Errorf("cross-reference section at offset %d: %w", off, err)
		}
		for _, k := range trailerKeys {
			if _, have := f.Trailer[k]; !have && tr[k] != nil {
				f.Trailer[k] = tr[k]
			}
		}
		// A hybrid file keeps its compressed objects in a cross-reference
		// stream named by /XRefStm and marks them free in the table, so
		// that old readers leave them alone. The stream therefore fills in
		// the entries this table marked free, and outranks everything older.
		if v, ok := pdfobj.Int(tr["XRefStm"]); ok && !seen[v] {
			seen[v] = true
			for _, n := range freed {
				f.xref[n] = entry{}
			}
			if _, _, err := f.readXrefSection(ctx, v); err != nil {
				f.warn(WarnXref, "hybrid cross-reference stream at offset %d: %v", v, err)
			}
			for _, n := range freed {
				if f.xref[n].kind == unset {
					f.xref[n] = entry{kind: free}
				}
			}
		}
		if v, ok := pdfobj.Int(tr["Prev"]); ok {
			todo = append(todo, v)
		}
	}
	if len(f.xref) == 0 {
		return errors.New("cross-reference table is empty")
	}
	return nil
}

// findStartXref returns the offset recorded after the last "startxref".
func (f *File) findStartXref() (int64, error) {
	const window = 2048
	n := min(int64(window), f.size)
	tail := make([]byte, n)
	m, err := f.src.ReadAt(tail, f.size-n)
	if err != nil && err != io.EOF && m == 0 {
		return 0, err
	}
	tail = tail[:m]
	i := bytes.LastIndex(tail, []byte("startxref"))
	if i < 0 {
		return 0, errors.New("no startxref")
	}
	rest := tail[i+len("startxref"):]
	lex := pdfobj.NewBytesLexer(rest, f.plim)
	t, err := lex.Next()
	if err != nil || t.Kind != pdfobj.Integer || t.Int <= 0 || t.Int >= f.size {
		return 0, errors.New("startxref has no valid offset")
	}
	// Objects after the last startxref belong to an incremental update
	// whose own cross-reference section is missing. Using this section
	// would silently show the document as it was before that update.
	if bytes.Contains(rest[lex.Offset():], []byte(" obj")) {
		return 0, errors.New("objects follow the last startxref")
	}
	return t.Int, nil
}

// readXrefSection reads the table or stream at off. It returns the section's
// trailer dictionary and, for a table, the object numbers it marked free.
func (f *File) readXrefSection(ctx context.Context, off int64) (trailer pdfobj.Dict, freed []int, err error) {
	if off < 0 || off >= f.size {
		return nil, nil, errors.New("offset is outside the file")
	}
	lex := pdfobj.NewLexer(f.src, off, f.size, f.plim)
	t, err := lex.Next()
	if err != nil {
		return nil, nil, err
	}
	if t.Kind == pdfobj.Keyword && string(t.Bytes) == "xref" {
		return f.readXrefTable(ctx, lex)
	}
	if t.Kind != pdfobj.Integer {
		return nil, nil, errors.New("neither a table nor a stream")
	}
	// Start again: this should be "num gen obj << /Type /XRef ... >> stream".
	p := pdfobj.NewParser(pdfobj.NewLexer(f.src, off, f.size, f.plim))
	_, _, obj, err := p.ParseIndirect()
	if err != nil {
		return nil, nil, err
	}
	st, ok := obj.(*pdfobj.Stream)
	if !ok || st.Dict["W"] == nil {
		return nil, nil, errors.New("not a cross-reference stream")
	}
	return st.Dict, nil, f.readXrefStream(ctx, st)
}

// readXrefTable reads a classic table. It works token by token, not on
// fixed 20-byte lines, because real tables get the line format wrong.
func (f *File) readXrefTable(ctx context.Context, lex *pdfobj.Lexer) (pdfobj.Dict, []int, error) {
	var freed []int
	firstSection := true
	for {
		t, err := lex.Next()
		if err != nil {
			return nil, nil, err
		}
		if t.Kind == pdfobj.Keyword && string(t.Bytes) == "trailer" {
			break
		}
		t2, err := lex.Next()
		if err != nil {
			return nil, nil, err
		}
		if t.Kind != pdfobj.Integer || t2.Kind != pdfobj.Integer || t.Int < 0 || t2.Int < 0 {
			return nil, nil, errors.New("malformed subsection header")
		}
		first, count := t.Int, t2.Int
		if first+count > int64(f.lim.MaxObjects) {
			return nil, nil, &LimitError{"number of objects", int64(f.lim.MaxObjects)}
		}
		for i := int64(0); i < count; i++ {
			if i%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
			}
			a, err1 := lex.Next()
			b, err2 := lex.Next()
			c, err3 := lex.Next()
			if err1 != nil || err2 != nil || err3 != nil ||
				a.Kind != pdfobj.Integer || b.Kind != pdfobj.Integer || c.Kind != pdfobj.Keyword {
				return nil, nil, errors.New("malformed entry")
			}
			inuse := string(c.Bytes) == "n"
			// A well-known producer bug: the table starts at object 1 but
			// its first entry is the free-list head that belongs to object 0.
			if firstSection && i == 0 && first == 1 && !inuse && b.Int == 65535 {
				first = 0
			}
			e := entry{kind: free}
			if inuse && a.Int > 0 {
				e = entry{kind: inUse, off: a.Int, gen: uint32(b.Int)}
			}
			num := int(first + i)
			wasUnset := f.entry(num).kind == unset
			f.setEntry(num, e, false)
			if e.kind == free && wasUnset && f.entry(num).kind == free {
				freed = append(freed, num)
			}
		}
		firstSection = false
	}
	obj, err := pdfobj.NewParser(lex).ParseObject()
	if err != nil {
		return nil, nil, fmt.Errorf("trailer: %w", err)
	}
	d, ok := obj.(pdfobj.Dict)
	if !ok {
		return nil, nil, errors.New("trailer is not a dictionary")
	}
	return d, freed, nil
}

func (f *File) readXrefStream(ctx context.Context, st *pdfobj.Stream) error {
	wArr, _ := st.Dict["W"].(pdfobj.Array)
	if len(wArr) < 3 {
		return errors.New("cross-reference stream has no /W")
	}
	var w [3]int
	rowLen := 0
	for i := range w {
		v, ok := pdfobj.Int(wArr[i])
		if !ok || v < 0 || v > 8 {
			return errors.New("cross-reference stream has a bad /W")
		}
		w[i] = int(v)
		rowLen += w[i]
	}
	if rowLen == 0 {
		return errors.New("cross-reference stream has an empty /W")
	}
	size, _ := pdfobj.Int(st.Dict["Size"])
	index, _ := st.Dict["Index"].(pdfobj.Array)
	if len(index) < 2 {
		index = pdfobj.Array{int64(0), size}
	}

	// A cross-reference stream is never encrypted.
	data, err := f.decode(ctx, st, false)
	if err != nil {
		return err
	}
	field := func(row []byte, start, width int) int64 {
		var v uint64
		for _, b := range row[start : start+width] {
			v = v<<8 | uint64(b)
		}
		return int64(v & (1<<63 - 1))
	}
	pos := 0
	for i := 0; i+1 < len(index); i += 2 {
		first, ok1 := pdfobj.Int(index[i])
		count, ok2 := pdfobj.Int(index[i+1])
		if !ok1 || !ok2 || first < 0 || count < 0 {
			return errors.New("cross-reference stream has a bad /Index")
		}
		for j := int64(0); j < count; j++ {
			if pos+rowLen > len(data) {
				f.warn(WarnXref, "cross-reference stream is shorter than its /Index says")
				return nil
			}
			if first+j >= int64(f.lim.MaxObjects) {
				return &LimitError{"number of objects", int64(f.lim.MaxObjects)}
			}
			row := data[pos : pos+rowLen]
			pos += rowLen
			typ := int64(1)
			if w[0] > 0 {
				typ = field(row, 0, w[0])
			}
			f1 := field(row, w[0], w[1])
			f2 := field(row, w[0]+w[1], w[2])
			var e entry
			switch typ {
			case 0:
				e = entry{kind: free}
			case 1:
				e = entry{kind: inUse, off: f1, gen: uint32(f2)}
			case 2:
				e = entry{kind: compressed, off: f1, gen: uint32(f2)}
			default:
				continue // reserved types read as references to null
			}
			f.setEntry(int(first+j), e, false)
		}
	}
	return nil
}

// rebuild reconstructs the cross-reference table by scanning the whole file
// for "num gen obj" markers. Later definitions of an object replace earlier
// ones, which is the right answer for incrementally updated files.
//
// Objects held in object streams are added afterwards by expandObjStreams,
// because reading those needs the decryption key, and that needs the trailer
// this function recovers.
func (f *File) rebuild(ctx context.Context) error {
	oldTrailer := f.Trailer
	f.rebuilt = true
	f.xref = nil
	f.Trailer = pdfobj.Dict{}
	f.pending = nil
	f.dropCache()
	clear(f.stmCache)
	f.stmBytes = 0

	type mark struct {
		off  int64
		kind byte // 't' trailer keyword, 'm' a /Type worth parsing
	}
	var (
		objOffs []int64 // offsets of object headers, ascending
		objNums []int
		marks   []mark
	)

	const (
		chunk = 1 << 20
		back  = 64 // look-behind, at least the longest "num gen " prefix
		ahead = 16 // look-ahead, at least the longest pattern
	)
	patterns := [][]byte{[]byte("trailer"), []byte("/ObjStm"), []byte("/XRef"), []byte("/Catalog")}
	buf := make([]byte, back+chunk+ahead)
	for base := int64(0); base < f.size; base += chunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		winStart := max(base-back, 0)
		winEnd := min(base+chunk+ahead, f.size)
		win := buf[:winEnd-winStart]
		n, err := f.src.ReadAt(win, winStart)
		if err != nil && err != io.EOF && n == 0 {
			return fmt.Errorf("reading the file: %w", err)
		}
		win = win[:n]
		lo := int(base - winStart)    // matches must start at or after this
		hi := min(lo+chunk, len(win)) // and before this

		for i := lo; ; {
			j := bytes.Index(win[i:min(hi+2, len(win))], []byte("obj"))
			if j < 0 {
				break
			}
			at := i + j
			i = at + 3
			if at >= hi {
				break
			}
			num, gen, start, ok := objHeaderBefore(win, at)
			if !ok {
				continue
			}
			off := winStart + int64(start)
			if len(objOffs) >= f.lim.MaxObjects {
				return &LimitError{"number of objects", int64(f.lim.MaxObjects)}
			}
			objOffs = append(objOffs, off)
			objNums = append(objNums, num)
			if num > 0 && num < f.lim.MaxObjects {
				f.setEntry(num, entry{kind: inUse, off: off, gen: uint32(gen)}, true)
			}
		}
		for pi, pat := range patterns {
			for i := lo; ; {
				j := bytes.Index(win[i:min(hi+len(pat)-1, len(win))], pat)
				if j < 0 {
					break
				}
				at := i + j
				i = at + len(pat)
				kind := byte('m')
				if pi == 0 {
					kind = 't'
				}
				if len(marks) < 1<<20 {
					marks = append(marks, mark{winStart + int64(at), kind})
				}
			}
		}
	}
	if len(objOffs) == 0 {
		return errors.New("no objects found")
	}
	sort.Slice(marks, func(i, j int) bool { return marks[i].off < marks[j].off })

	// Parse the few objects that carry structure: trailers, cross-reference
	// streams (their dictionaries double as trailers), object streams and
	// catalogs.
	var catalog pdfobj.Ref
	merge := func(d pdfobj.Dict) {
		for _, k := range trailerKeys {
			if d[k] != nil {
				f.Trailer[k] = d[k]
			}
		}
	}
	lastParsed := int64(-1)
	for _, m := range marks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.kind == 't' {
			lex := pdfobj.NewLexer(f.src, m.off+int64(len("trailer")), f.size, f.plim)
			if obj, err := pdfobj.NewParser(lex).ParseObject(); err == nil {
				if d, ok := obj.(pdfobj.Dict); ok {
					merge(d)
				}
			}
			continue
		}
		// Find the object this marker sits in.
		k := sort.Search(len(objOffs), func(i int) bool { return objOffs[i] > m.off }) - 1
		if k < 0 || objOffs[k] == lastParsed {
			continue
		}
		lastParsed = objOffs[k]
		num := objNums[k]
		p := pdfobj.NewParser(pdfobj.NewLexer(f.src, objOffs[k], f.size, f.plim))
		_, gen, obj, err := p.ParseIndirect()
		if err != nil {
			continue
		}
		live := f.entry(num).kind == inUse && f.entry(num).off == objOffs[k]
		switch v := obj.(type) {
		case *pdfobj.Stream:
			switch {
			case isType(v.Dict, "XRef"):
				merge(v.Dict)
			case isType(v.Dict, "ObjStm") && live:
				f.pending = append(f.pending, num)
			}
		case pdfobj.Dict:
			if isType(v, "Catalog") && live {
				catalog = pdfobj.Ref{Num: num, Gen: gen}
			}
		}
	}
	for _, k := range trailerKeys {
		if f.Trailer[k] == nil && oldTrailer[k] != nil && k != "Root" {
			f.Trailer[k] = oldTrailer[k]
		}
	}
	if _, ok := f.Trailer["Root"].(pdfobj.Ref); !ok && catalog.Num > 0 {
		f.Trailer["Root"] = catalog
	}
	return nil
}

// objHeaderBefore checks whether the "obj" at win[at:] ends an object header
// "num gen obj" and returns its numbers and the index where num starts.
func objHeaderBefore(win []byte, at int) (num, gen, start int, ok bool) {
	// The keyword must end here, and must not be the tail of "endobj".
	if at+3 < len(win) && !pdfobj.IsSpace(win[at+3]) && !pdfobj.IsDelim(win[at+3]) {
		return 0, 0, 0, false
	}
	i := at - 1
	if i < 0 || !pdfobj.IsSpace(win[i]) {
		return 0, 0, 0, false
	}
	number := func(maxDigits int) (int, bool) {
		for i >= 0 && pdfobj.IsSpace(win[i]) {
			i--
		}
		end := i
		v, mul := 0, 1
		for i >= 0 && win[i] >= '0' && win[i] <= '9' && end-i < maxDigits {
			v += int(win[i]-'0') * mul
			mul *= 10
			i--
		}
		return v, end > i
	}
	gen, ok = number(5)
	if !ok || i < 0 || !pdfobj.IsSpace(win[i]) {
		return 0, 0, 0, false
	}
	num, ok = number(10)
	if !ok || (i >= 0 && win[i] >= '0' && win[i] <= '9') {
		return 0, 0, 0, false
	}
	return num, gen, i + 1, true
}

// expandObjStreams adds the objects inside the object streams that a
// rebuild found. An object defined directly, later in the file than the
// object stream, is the newer definition and is kept.
func (f *File) expandObjStreams(ctx context.Context) {
	pending := f.pending
	f.pending = nil
	sort.Slice(pending, func(i, j int) bool { return f.entry(pending[i]).off < f.entry(pending[j]).off })
	for _, stmNum := range pending {
		if ctx.Err() != nil {
			return
		}
		container := f.entry(stmNum)
		os, err := f.objStream(ctx, stmNum)
		if err != nil {
			f.warn(WarnObject, "object stream %d: %v", stmNum, err)
			continue
		}
		for idx, num := range os.nums {
			if num == stmNum {
				continue
			}
			if old := f.entry(num); old.kind != unset && f.definedAt(old) > container.off {
				continue
			}
			f.setEntry(num, entry{kind: compressed, off: int64(stmNum), gen: uint32(idx)}, true)
			delete(f.cache, num)
			delete(f.oldCache, num)
		}
	}
}

// definedAt returns the file position that defines an entry: its own offset,
// or the offset of the object stream that holds it.
func (f *File) definedAt(e entry) int64 {
	switch e.kind {
	case inUse:
		return e.off
	case compressed:
		if c := f.entry(int(e.off)); c.kind == inUse {
			return c.off
		}
	}
	return -1
}

// findCatalog is the last resort after a rebuild that recovered no /Root:
// look at every object for a dictionary of type Catalog.
func (f *File) findCatalog(ctx context.Context) {
	for num := len(f.xref) - 1; num > 0; num-- {
		if num%1024 == 0 && ctx.Err() != nil {
			return
		}
		if k := f.xref[num].kind; k != inUse && k != compressed {
			continue
		}
		obj, err := f.Get(ctx, num)
		if err != nil {
			continue
		}
		if d, ok := obj.(pdfobj.Dict); ok && isType(d, "Catalog") {
			f.Trailer["Root"] = pdfobj.Ref{Num: num, Gen: int(f.xref[num].gen)}
			return
		}
	}
}

// adoptLoosePages builds a page tree out of every page object in the file,
// in file order, for a document whose catalog or page tree is gone. The
// result is a catalog held in memory, not in the file. Pages that relied on
// attributes inherited from the lost tree nodes fall back to defaults.
func (f *File) adoptLoosePages(ctx context.Context) {
	type loose struct {
		pos   int64 // where the page is defined in the file
		index uint32
		ref   pdfobj.Ref
	}
	var pages []loose
	for num := 1; num < len(f.xref); num++ {
		if num%1024 == 0 && ctx.Err() != nil {
			return
		}
		e := f.xref[num]
		if e.kind != inUse && e.kind != compressed {
			continue
		}
		obj, err := f.Get(ctx, num)
		if err != nil {
			continue
		}
		d, ok := obj.(pdfobj.Dict)
		if !ok || !isType(d, "Page") || d["Kids"] != nil {
			continue
		}
		l := loose{pos: f.definedAt(e), ref: pdfobj.Ref{Num: num}}
		if e.kind == compressed {
			l.index = e.gen
		} else {
			l.ref.Gen = int(e.gen)
		}
		pages = append(pages, l)
	}
	if len(pages) == 0 {
		return
	}
	sort.Slice(pages, func(i, j int) bool {
		if pages[i].pos != pages[j].pos {
			return pages[i].pos < pages[j].pos
		}
		return pages[i].index < pages[j].index
	})
	kids := make(pdfobj.Array, len(pages))
	for i, p := range pages {
		kids[i] = p.ref
	}

	catalog := pdfobj.Dict{"Type": pdfobj.Name("Catalog")}
	if old, err := f.Resolve(ctx, f.Trailer["Root"]); err == nil {
		if d, ok := old.(pdfobj.Dict); ok {
			for k, v := range d {
				catalog[k] = v
			}
		}
	}
	catalog["Pages"] = pdfobj.Dict{
		"Type":  pdfobj.Name("Pages"),
		"Kids":  kids,
		"Count": int64(len(kids)),
	}
	f.Trailer["Root"] = catalog
	f.warn(WarnPageTree, "the page tree is missing; %d page objects found in the file were used in file order", len(kids))
}
