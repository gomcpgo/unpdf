// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package reader understands the structure of a PDF file: the header, the
// cross-reference data in all its forms, object streams, stream data and
// encryption. It hands out objects by number and decoded stream data, and
// rebuilds the cross-reference table by scanning when the file's own is
// missing or wrong.
package reader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
	"github.com/gomcpgo/unpdf/internal/security"
)

// Errors a caller can act on. They are re-exported by package unpdf.
var (
	ErrNotPDF           = errors.New("unpdf: not a PDF file")
	ErrPasswordRequired = errors.New("unpdf: password required")
	ErrWrongPassword    = fmt.Errorf("unpdf: wrong password: %w", ErrPasswordRequired)
	ErrDamaged          = errors.New("unpdf: file is damaged beyond repair")
)

// UnsupportedError reports a PDF feature this package does not handle.
type UnsupportedError struct {
	Feature string
}

func (e *UnsupportedError) Error() string { return "unpdf: unsupported feature: " + e.Feature }

// LimitError reports that a document exceeded one of the configured limits.
type LimitError struct {
	What  string
	Limit int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("unpdf: %s exceeds the limit of %d", e.What, e.Limit)
}

// Limits caps what a document may make the reader do. A zero field means
// "use the default".
type Limits struct {
	// MaxObjects is the highest object number accepted, and so the size of
	// the cross-reference table in memory (16 bytes per object).
	MaxObjects int
	// MaxStreamBytes caps the size of one stream, both as stored in the file
	// and after decoding.
	MaxStreamBytes int64
	// MaxDepth caps how deeply arrays and dictionaries may nest.
	MaxDepth int
	// MaxStringBytes caps the length of one string object.
	MaxStringBytes int
	// MaxItems caps the entries of one array or dictionary.
	MaxItems int
	// MaxXrefSections caps the chain of cross-reference sections that
	// incremental updates build up.
	MaxXrefSections int
	// MaxWarnings caps how many warnings are kept.
	MaxWarnings int
}

// WithDefaults fills in the zero fields.
func (l Limits) WithDefaults() Limits {
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&l.MaxObjects, 4<<20)
	def(&l.MaxDepth, 128)
	def(&l.MaxStringBytes, 32<<20)
	def(&l.MaxItems, 1<<20)
	def(&l.MaxXrefSections, 4096)
	def(&l.MaxWarnings, 200)
	if l.MaxStreamBytes <= 0 {
		l.MaxStreamBytes = 256 << 20
	}
	return l
}

// Warning is a problem that did not stop the document from being read.
type Warning struct {
	Code    string
	Message string
}

// Warning codes.
const (
	WarnXrefRebuilt  = "xref-rebuilt"
	WarnXref         = "xref"
	WarnObject       = "object"
	WarnStreamLength = "stream-length"
	WarnStreamData   = "stream-data"
	WarnLimit        = "limit"
	WarnPageTree     = "page-tree"
)

type entryKind uint8

const (
	unset entryKind = iota
	free
	inUse      // off is the byte offset of the object
	compressed // off is the number of the object stream, gen the index in it
)

type entry struct {
	off  int64
	gen  uint32
	kind entryKind
}

// File is an open PDF. It is not safe for concurrent use.
type File struct {
	src  io.ReaderAt
	size int64
	lim  Limits
	plim pdfobj.Limits

	// Version is the version in the file header, such as "1.7".
	Version string
	// Trailer holds the trailer entries: Root, Info, ID, Encrypt.
	Trailer pdfobj.Dict

	xref    []entry
	rebuilt bool
	ready   bool  // Open has finished; lazy repair is allowed
	pending []int // object streams found by a rebuild, not yet expanded

	sec        *security.Handler
	encryptObj int // object number of the encryption dictionary, 0 if direct

	// Parsed objects are cached in two generations. When the young one is
	// full it becomes the old one and the old one is dropped, so the objects
	// in use, which are found again and moved back to the young generation,
	// survive, and memory stays bounded.
	cache      map[int]pdfobj.Object
	oldCache   map[int]pdfobj.Object
	stmCache   map[int]*objStream
	stmBytes   int
	stmLoading map[int]bool

	warnings []Warning
	dropped  int
}

const (
	maxCachedObjects   = 8192
	maxCachedStmBytes  = 64 << 20
	maxResolveHops     = 32
	headerSearchWindow = 4096
)

// Open reads the structure of the PDF in src.
func Open(ctx context.Context, src io.ReaderAt, size int64, password string, lim Limits) (*File, error) {
	lim = lim.WithDefaults()
	f := &File{
		src: src, size: size, lim: lim,
		plim:     pdfobj.Limits{MaxDepth: lim.MaxDepth, MaxString: lim.MaxStringBytes, MaxItems: lim.MaxItems},
		Trailer:  pdfobj.Dict{},
		cache:    map[int]pdfobj.Object{},
		stmCache: map[int]*objStream{},

		stmLoading: map[int]bool{},
	}
	if err := f.readHeader(); err != nil {
		return nil, err
	}

	err := f.loadXref(ctx)
	if err == nil {
		err = f.setupSecurity(ctx, password)
		if isFinal(err) {
			return nil, err
		}
		if err == nil && !f.rootOK(ctx, true) {
			err = errors.New("document catalog missing")
		}
	}
	if cerr := ctx.Err(); cerr != nil {
		return nil, cerr
	}
	if err != nil {
		// The file's own cross-reference data is unusable. Rebuild it.
		cause := err
		if err := f.rebuild(ctx); err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return nil, cerr
			}
			if isFinal(err) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %v (after: %v)", ErrDamaged, err, cause)
		}
		f.warn(WarnXrefRebuilt, "cross-reference table rebuilt by scanning the file: %v", cause)
		if err := f.setupSecurity(ctx, password); err != nil {
			if isFinal(err) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
		}
		f.expandObjStreams(ctx)
		if !f.rootOK(ctx, false) {
			f.findCatalog(ctx)
		}
		if !f.rootOK(ctx, true) {
			// The catalog or the page tree did not survive. Pages that
			// did are still worth reading.
			f.adoptLoosePages(ctx)
		}
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		if !f.rootOK(ctx, false) {
			return nil, fmt.Errorf("%w: no document catalog and no pages found", ErrDamaged)
		}
	}
	f.ready = true
	return f, nil
}

// isFinal reports whether err should be returned to the caller as it is,
// because rebuilding the cross-reference table cannot change it.
func isFinal(err error) bool {
	var unsupported *UnsupportedError
	var limit *LimitError
	return errors.Is(err, ErrPasswordRequired) || errors.As(err, &unsupported) || errors.As(err, &limit) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (f *File) readHeader() error {
	head := make([]byte, min(int64(headerSearchWindow), f.size))
	n, err := f.src.ReadAt(head, 0)
	if err != nil && err != io.EOF && n == 0 {
		return fmt.Errorf("unpdf: reading header: %w", err)
	}
	head = head[:n]
	i := bytes.Index(head, []byte("%PDF-"))
	if i < 0 {
		return ErrNotPDF
	}
	// The version is "major.minor"; anything else is left empty.
	v := head[i+5:]
	end := 0
	for end < len(v) && end < 8 && (v[end] == '.' || (v[end] >= '0' && v[end] <= '9')) {
		end++
	}
	f.Version = string(v[:end])
	return nil
}

// Rebuilt reports whether the cross-reference table had to be reconstructed.
func (f *File) Rebuilt() bool { return f.rebuilt }

// Encrypted reports whether the document is encrypted.
func (f *File) Encrypted() bool { return f.sec != nil }

// Size returns the size of the file in bytes.
func (f *File) Size() int64 { return f.size }

// NumObjects returns one more than the highest object number in the
// cross-reference table.
func (f *File) NumObjects() int { return len(f.xref) }

// Warnings returns the problems noticed so far.
func (f *File) Warnings() []Warning {
	out := append([]Warning(nil), f.warnings...)
	if f.dropped > 0 {
		out = append(out, Warning{WarnLimit, fmt.Sprintf("%d further warnings were dropped", f.dropped)})
	}
	return out
}

// Warn records a problem on behalf of a higher layer.
func (f *File) Warn(code, format string, args ...any) { f.warn(code, format, args...) }

func (f *File) warn(code, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	for _, w := range f.warnings {
		if w.Code == code && w.Message == msg {
			return
		}
	}
	if len(f.warnings) >= f.lim.MaxWarnings {
		f.dropped++
		return
	}
	f.warnings = append(f.warnings, Warning{code, msg})
}

func (f *File) entry(num int) entry {
	if num < 0 || num >= len(f.xref) {
		return entry{}
	}
	return f.xref[num]
}

// setEntry stores e for object num. With overwrite false an existing entry
// is kept, which is how newer cross-reference sections take precedence over
// older ones.
func (f *File) setEntry(num int, e entry, overwrite bool) {
	if num < 0 || num >= f.lim.MaxObjects {
		f.warn(WarnLimit, "object numbers above %d are ignored", f.lim.MaxObjects)
		return
	}
	if num >= len(f.xref) {
		if num < cap(f.xref) {
			f.xref = f.xref[:num+1]
		} else {
			grown := make([]entry, num+1, max(num+1, 2*cap(f.xref)))
			copy(grown, f.xref)
			f.xref = grown
		}
	}
	if overwrite || f.xref[num].kind == unset {
		f.xref[num] = e
	}
}

// Get returns object num, or nil when the document has no such object. A
// reference to a missing object is, by definition, a reference to null.
func (f *File) Get(ctx context.Context, num int) (pdfobj.Object, error) {
	if o, ok := f.cache[num]; ok {
		return o, nil
	}
	if o, ok := f.oldCache[num]; ok {
		f.store(num, o)
		return o, nil
	}
	e := f.entry(num)
	var obj pdfobj.Object
	var err error
	switch e.kind {
	case inUse:
		obj, err = f.loadAt(num, e)
		if err != nil && f.ready && !f.rebuilt {
			// One bad offset usually means the whole table is off.
			cause := err
			if rerr := f.rebuild(ctx); rerr == nil {
				f.warn(WarnXrefRebuilt, "cross-reference table rebuilt by scanning the file: %v", cause)
				f.expandObjStreams(ctx)
				return f.Get(ctx, num)
			}
		}
	case compressed:
		obj, err = f.loadCompressed(ctx, num, e)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f.store(num, obj)
	return obj, nil
}

// store puts an object in the young generation of the cache.
func (f *File) store(num int, obj pdfobj.Object) {
	if len(f.cache) >= maxCachedObjects {
		f.oldCache, f.cache = f.cache, make(map[int]pdfobj.Object, maxCachedObjects/4)
	}
	f.cache[num] = obj
}

// dropCache forgets every cached object.
func (f *File) dropCache() {
	clear(f.cache)
	f.oldCache = nil
}

func (f *File) loadAt(num int, e entry) (pdfobj.Object, error) {
	if e.off < 0 || e.off >= f.size {
		return nil, fmt.Errorf("object %d: offset %d is outside the file", num, e.off)
	}
	p := pdfobj.NewParser(pdfobj.NewLexer(f.src, e.off, f.size, f.plim))
	n, gen, obj, err := p.ParseIndirect()
	if err != nil {
		return nil, fmt.Errorf("object %d: %w", num, err)
	}
	if n != num {
		return nil, fmt.Errorf("object %d: found object %d at its offset", num, n)
	}
	if f.sec != nil && num != f.encryptObj {
		if st, ok := obj.(*pdfobj.Stream); !ok || !isType(st.Dict, "XRef") {
			obj = f.decryptStrings(obj, num, gen)
		}
	}
	return obj, nil
}

// decryptStrings decrypts every string inside obj, in place.
func (f *File) decryptStrings(obj pdfobj.Object, num, gen int) pdfobj.Object {
	switch v := obj.(type) {
	case pdfobj.String:
		return pdfobj.String(f.sec.DecryptString(num, gen, v))
	case pdfobj.Array:
		for i := range v {
			v[i] = f.decryptStrings(v[i], num, gen)
		}
	case pdfobj.Dict:
		for k, x := range v {
			v[k] = f.decryptStrings(x, num, gen)
		}
	case *pdfobj.Stream:
		f.decryptStrings(v.Dict, num, gen)
	}
	return obj
}

// Resolve follows indirect references until it reaches a direct object.
func (f *File) Resolve(ctx context.Context, o pdfobj.Object) (pdfobj.Object, error) {
	for hops := 0; ; hops++ {
		ref, ok := o.(pdfobj.Ref)
		if !ok {
			return o, nil
		}
		if hops >= maxResolveHops {
			return nil, fmt.Errorf("object %d: reference chain too long", ref.Num)
		}
		var err error
		if o, err = f.Get(ctx, ref.Num); err != nil {
			return nil, err
		}
	}
}

// The helpers below resolve a value and convert it, returning the zero value
// when it is missing, of another type, or unreadable. An unreadable object
// is recorded as a warning: one broken object should not sink the document.

func (f *File) resolveQuiet(ctx context.Context, o pdfobj.Object) pdfobj.Object {
	v, err := f.Resolve(ctx, o)
	if err != nil {
		if ctx.Err() == nil {
			f.warn(WarnObject, "%v", err)
		}
		return nil
	}
	return v
}

// Dict resolves o to a dictionary. A stream yields its dictionary.
func (f *File) Dict(ctx context.Context, o pdfobj.Object) pdfobj.Dict {
	switch v := f.resolveQuiet(ctx, o).(type) {
	case pdfobj.Dict:
		return v
	case *pdfobj.Stream:
		return v.Dict
	}
	return nil
}

// Array resolves o to an array.
func (f *File) Array(ctx context.Context, o pdfobj.Object) pdfobj.Array {
	a, _ := f.resolveQuiet(ctx, o).(pdfobj.Array)
	return a
}

// Stream resolves o to a stream.
func (f *File) Stream(ctx context.Context, o pdfobj.Object) *pdfobj.Stream {
	s, _ := f.resolveQuiet(ctx, o).(*pdfobj.Stream)
	return s
}

// Name resolves o to a name.
func (f *File) Name(ctx context.Context, o pdfobj.Object) pdfobj.Name {
	n, _ := f.resolveQuiet(ctx, o).(pdfobj.Name)
	return n
}

// String resolves o to a string.
func (f *File) String(ctx context.Context, o pdfobj.Object) (pdfobj.String, bool) {
	s, ok := f.resolveQuiet(ctx, o).(pdfobj.String)
	return s, ok
}

// Int resolves o to an integer.
func (f *File) Int(ctx context.Context, o pdfobj.Object) (int64, bool) {
	return pdfobj.Int(f.resolveQuiet(ctx, o))
}

// Number resolves o to a number.
func (f *File) Number(ctx context.Context, o pdfobj.Object) (float64, bool) {
	return pdfobj.Number(f.resolveQuiet(ctx, o))
}

// Bool resolves o to a boolean.
func (f *File) Bool(ctx context.Context, o pdfobj.Object) (value, ok bool) {
	value, ok = f.resolveQuiet(ctx, o).(bool)
	return value, ok
}

func isType(d pdfobj.Dict, name pdfobj.Name) bool {
	t, _ := d["Type"].(pdfobj.Name)
	return t == name
}

// rootOK reports whether the trailer's /Root leads to a usable catalog. With
// strict set, the catalog must also have a page tree.
func (f *File) rootOK(ctx context.Context, strict bool) bool {
	root, err := f.Resolve(ctx, f.Trailer["Root"])
	if err != nil {
		return false
	}
	cat, ok := root.(pdfobj.Dict)
	if !ok {
		return false
	}
	if !strict {
		return true
	}
	pages, err := f.Resolve(ctx, cat["Pages"])
	if err != nil {
		return false
	}
	_, ok = pages.(pdfobj.Dict)
	return ok
}
