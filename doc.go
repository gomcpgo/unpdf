// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package unpdf reads PDF files: their metadata, pages and text today, and
// their structure, tables and images as the package grows. It is written in
// pure Go, with no cgo and no external programs.
//
// PDFs are treated as untrusted input. Every parser checks bounds, and the
// Limits in Options cap how much memory a document can make the package
// use. Malformed input produces an error or a Warning, never a panic.
//
// # Opening a document
//
// Open takes an io.ReaderAt, so a file, a byte slice or anything that can be
// read at an offset will do. Only the file structure is read up front; page
// content is read when asked for.
//
//	doc, err := unpdf.OpenFile(ctx, "report.pdf", unpdf.Options{})
//	if err != nil {
//		return err
//	}
//	defer doc.Close()
//
//	info := doc.Info()
//	fmt.Println(info.Title, info.Pages)
//
//	for page, err := range doc.Pages(ctx) {
//		if err != nil {
//			return err
//		}
//		text, err := page.Text(ctx)
//		if err != nil {
//			return err
//		}
//		fmt.Println(page.Number, text)
//	}
//
// # Text
//
// Document.Markdown converts a document, or some of its pages, to Markdown
// with headings, lists, tables and links: the form that suits a language
// model or a search index best. Page.Text returns a page's plain text in
// reading order. Page.Blocks returns the same text as blocks of lines of
// words, each with its position and its kind (heading, list item or
// paragraph), and with running heads and page numbers marked so that they
// can be left out. Page.Scanned says whether the page is an image of a
// page, to be sent to OCR.
//
// Page.Tables returns the tables on a page with their cells. A table comes
// from the document's own tags, from ruling lines that form a grid, or from
// text that lines up in columns; each says which, and how confident the
// detection is.
//
// A tagged PDF describes its own structure: its reading order, headings,
// lists and tables. Where a page is tagged, that description is used in
// place of what the layout suggests.
//
// Document.Outline, Document.FormFields, Page.Links and Page.Annotations
// return the bookmarks, the form fields with their values, the links and
// the comments.
//
// Glyphs that a font gives no Unicode mapping for appear as U+FFFD; the
// document's warnings then say which font and how many glyphs, so a caller
// can decide whether the text is trustworthy.
//
// # Damaged and encrypted files
//
// A file whose cross-reference table is missing or wrong is repaired by
// scanning it for objects, the way PDF viewers do. Info.Repaired and
// Document.Warnings report that it happened.
//
// Encrypted files open without a password when their user password is
// empty, which is the common case. Otherwise set Options.Password; without
// the right one, Open fails with ErrPasswordRequired or ErrWrongPassword.
//
// # Errors
//
// Errors that a caller can act on have a fixed identity: ErrNotPDF,
// ErrPasswordRequired, ErrWrongPassword and ErrDamaged match with errors.Is,
// and *UnsupportedError and *LimitError with errors.As. Problems that do not
// prevent reading are collected as warnings instead.
//
// A Document is not safe for concurrent use.
package unpdf
