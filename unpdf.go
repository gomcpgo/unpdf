// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/gomcpgo/unpdf/internal/font"
	"github.com/gomcpgo/unpdf/internal/pdfobj"
	"github.com/gomcpgo/unpdf/internal/reader"
)

// Errors that Open and the Document methods return. Test for them with
// errors.Is.
var (
	// ErrNotPDF means the input has no PDF header.
	ErrNotPDF = reader.ErrNotPDF
	// ErrPasswordRequired means the document is encrypted and does not open
	// with an empty password. Supply one in Options.Password.
	ErrPasswordRequired = reader.ErrPasswordRequired
	// ErrWrongPassword means Options.Password was tried and rejected. It
	// also matches ErrPasswordRequired.
	ErrWrongPassword = reader.ErrWrongPassword
	// ErrDamaged means the file is too damaged to read, even after trying
	// to rebuild its cross-reference table.
	ErrDamaged = reader.ErrDamaged
)

// UnsupportedError means the document uses a PDF feature that this package
// does not handle. Feature names it, for example "filter JBIG2Decode" or
// "encryption with security handler \"Adobe.PubSec\"".
type UnsupportedError struct {
	Feature string
}

func (e *UnsupportedError) Error() string { return "unpdf: unsupported feature: " + e.Feature }

// LimitError means the document exceeded one of the Limits. What names the
// quantity and Limit is the cap that was hit.
type LimitError struct {
	What  string
	Limit int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("unpdf: %s exceeds the limit of %d", e.What, e.Limit)
}

// publicErr converts errors from the internal packages to the exported
// error types.
func publicErr(err error) error {
	var u *reader.UnsupportedError
	if errors.As(err, &u) {
		return &UnsupportedError{Feature: u.Feature}
	}
	var l *reader.LimitError
	if errors.As(err, &l) {
		return &LimitError{What: l.What, Limit: l.Limit}
	}
	return err
}

// Limits caps the resources a document may consume. PDFs are untrusted
// input: without caps, a small hostile file can ask for gigabytes of memory.
// A zero field selects the default, which is generous enough for any
// legitimate document.
type Limits struct {
	// MaxObjects is the highest object number accepted. The in-memory
	// cross-reference table costs 16 bytes per object. Default 4,194,304.
	MaxObjects int
	// MaxStreamBytes caps the size of a single stream, stored or decoded.
	// Default 256 MiB.
	MaxStreamBytes int64
	// MaxDepth caps the nesting of arrays and dictionaries. Default 128.
	MaxDepth int
	// MaxStringBytes caps the length of one string object. Default 32 MiB.
	MaxStringBytes int
	// MaxItems caps the entries of one array or dictionary. Default
	// 1,048,576.
	MaxItems int
	// MaxPages caps the number of pages that will be enumerated. Default
	// 1,000,000.
	MaxPages int
	// MaxPageGlyphs caps the glyphs read from one page. Default 500,000.
	MaxPageGlyphs int
	// MaxWarnings caps how many warnings are kept. Default 200.
	MaxWarnings int
}

const (
	defaultMaxPages  = 1_000_000
	maxPageTreeDepth = 64
)

// Options configures Open.
type Options struct {
	// Password is tried as the user password and then as the owner
	// password, but only if the document does not open with an empty
	// password.
	Password string
	// Limits caps resource use. The zero value selects the defaults.
	Limits Limits
}

// Document is an open PDF. It is not safe for concurrent use by multiple
// goroutines.
type Document struct {
	f        *reader.File
	closer   io.Closer
	catalog  pdfobj.Dict
	info     Info
	maxPages int

	maxStream     int64
	maxPageGlyphs int
	fonts         *font.Cache
	glyphStats    map[*font.Font]map[int]glyphCount
	bandCache     map[int]map[string]bool
	recent        []*Page
}

// Open reads a PDF from r, which must stay readable until the Document is
// closed. Only the file structure is read up front; page content is read
// when it is asked for.
//
// A file with a broken cross-reference table is repaired by scanning it, and
// the repair is reported through Warnings.
func Open(ctx context.Context, r io.ReaderAt, size int64, opts Options) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lim := opts.Limits
	f, err := reader.Open(ctx, r, size, opts.Password, reader.Limits{
		MaxObjects:     lim.MaxObjects,
		MaxStreamBytes: lim.MaxStreamBytes,
		MaxDepth:       lim.MaxDepth,
		MaxStringBytes: lim.MaxStringBytes,
		MaxItems:       lim.MaxItems,
		MaxWarnings:    lim.MaxWarnings,
	})
	if err != nil {
		return nil, publicErr(err)
	}
	d := &Document{f: f, maxPages: lim.MaxPages, maxStream: lim.MaxStreamBytes, maxPageGlyphs: lim.MaxPageGlyphs}
	if d.maxPages <= 0 {
		d.maxPages = defaultMaxPages
	}
	if d.maxStream <= 0 {
		d.maxStream = 256 << 20
	}
	if d.maxPageGlyphs <= 0 {
		d.maxPageGlyphs = defaultMaxPageGlyphs
	}
	d.catalog = f.Dict(ctx, f.Trailer["Root"])
	d.info = d.readInfo(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d, nil
}

// OpenFile opens the PDF at path. Close the Document to release the file.
func OpenFile(ctx context.Context, path string, opts Options) (*Document, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	d, err := Open(ctx, file, st.Size(), opts)
	if err != nil {
		file.Close()
		return nil, err
	}
	d.closer = file
	return d, nil
}

// Close releases the resources held by the Document. It closes the
// underlying file only when the Document was opened with OpenFile.
func (d *Document) Close() error {
	if d.closer != nil {
		c := d.closer
		d.closer = nil
		return c.Close()
	}
	return nil
}

// Warning describes a problem that did not prevent reading the document,
// such as a rebuilt cross-reference table or a stream with a wrong length.
type Warning struct {
	// Code classifies the warning. See the Warn constants.
	Code string
	// Message is a human-readable description.
	Message string
}

// Warning codes.
const (
	// WarnXrefRebuilt: the cross-reference table was missing or wrong and
	// was rebuilt by scanning the file.
	WarnXrefRebuilt = reader.WarnXrefRebuilt
	// WarnXref: part of the cross-reference data was malformed.
	WarnXref = reader.WarnXref
	// WarnObject: an object could not be read and was treated as missing.
	WarnObject = reader.WarnObject
	// WarnStreamLength: a stream's /Length was missing or wrong.
	WarnStreamLength = reader.WarnStreamLength
	// WarnStreamData: a stream's data was damaged and only partly decoded.
	WarnStreamData = reader.WarnStreamData
	// WarnLimit: something was cut short by a limit.
	WarnLimit = reader.WarnLimit
	// WarnPageTree: the page tree is malformed.
	WarnPageTree = reader.WarnPageTree
	// WarnMetadata: the document metadata could not be read in full.
	WarnMetadata = "metadata"
)

// Warnings returns the problems noticed so far. The list grows as more of
// the document is read.
func (d *Document) Warnings() []Warning {
	var out []Warning
	for _, w := range d.f.Warnings() {
		out = append(out, Warning{Code: w.Code, Message: w.Message})
	}
	return append(out, d.glyphWarnings()...)
}
