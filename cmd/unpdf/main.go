// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Command unpdf inspects PDF files.
//
//	unpdf info [-password PW] [-json] file.pdf
//	unpdf text [-password PW] [-first N] [-last N] file.pdf
//	unpdf markdown [-password PW] [-pages LIST] [-furniture] [-no-page-markers] file.pdf
//	unpdf outline [-password PW] file.pdf
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/gomcpgo/unpdf"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch os.Args[1] {
	case "info":
		err = info(ctx, os.Args[2:])
	case "text":
		err = text(ctx, os.Args[2:])
	case "markdown":
		err = markdown(ctx, os.Args[2:])
	case "outline":
		err = outline(ctx, os.Args[2:])
	case "tables":
		err = tables(ctx, os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		switch {
		case errors.Is(err, unpdf.ErrPasswordRequired):
			os.Exit(3)
		case errors.Is(err, unpdf.ErrNotPDF), errors.Is(err, unpdf.ErrDamaged):
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: unpdf info [-password PW] [-json] file.pdf")
	fmt.Fprintln(os.Stderr, "       unpdf text [-password PW] [-first N] [-last N] file.pdf")
	fmt.Fprintln(os.Stderr, "       unpdf markdown [-password PW] [-pages LIST] [-furniture] [-no-page-markers] file.pdf")
	fmt.Fprintln(os.Stderr, "       unpdf outline [-password PW] file.pdf")
	fmt.Fprintln(os.Stderr, "       unpdf tables [-password PW] [-first N] [-last N] [-min CONFIDENCE] file.pdf")
	os.Exit(64)
}

type pageInfo struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Rotate int     `json:"rotate"`
}

type infoOutput struct {
	unpdf.Info
	FirstPage *pageInfo       `json:"firstPage,omitempty"`
	Warnings  []unpdf.Warning `json:"warnings,omitempty"`
}

func info(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	password := fs.String("password", "", "password for an encrypted file")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}

	doc, err := unpdf.OpenFile(ctx, fs.Arg(0), unpdf.Options{Password: *password})
	if err != nil {
		return err
	}
	defer doc.Close()

	out := infoOutput{Info: doc.Info()}
	if out.Pages > 0 {
		if p, err := doc.Page(ctx, 1); err == nil {
			w, h := p.Size()
			out.FirstPage = &pageInfo{Width: w, Height: h, Rotate: p.Rotate}
		}
	}
	out.Warnings = doc.Warnings()

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	row := func(label, value string) {
		if value != "" {
			fmt.Printf("%-14s %s\n", label+":", value)
		}
	}
	date := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format(time.RFC3339)
	}
	yesNo := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	i := out.Info
	row("Title", i.Title)
	row("Author", i.Author)
	row("Subject", i.Subject)
	row("Keywords", i.Keywords)
	row("Creator", i.Creator)
	row("Producer", i.Producer)
	row("Created", date(i.Created))
	row("Modified", date(i.Modified))
	row("Pages", fmt.Sprint(i.Pages))
	row("Encrypted", yesNo(i.Encrypted))
	row("Tagged", yesNo(i.Tagged))
	if out.FirstPage != nil {
		row("Page size", fmt.Sprintf("%g x %g pts", round2(out.FirstPage.Width), round2(out.FirstPage.Height)))
		row("Page rotation", fmt.Sprint(out.FirstPage.Rotate))
	}
	row("PDF version", i.Version)
	row("Repaired", yesNo(i.Repaired))
	for _, w := range out.Warnings {
		fmt.Printf("warning [%s]: %s\n", w.Code, w.Message)
	}
	return nil
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// text prints the text of each page, with a form feed after every page.
func text(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("text", flag.ExitOnError)
	password := fs.String("password", "", "password for an encrypted file")
	first := fs.Int("first", 1, "first page to print")
	last := fs.Int("last", 0, "last page to print (0 means the last page)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	doc, err := unpdf.OpenFile(ctx, fs.Arg(0), unpdf.Options{Password: *password})
	if err != nil {
		return err
	}
	defer doc.Close()

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for page, err := range doc.Pages(ctx) {
		if err != nil {
			return err
		}
		if page.Number < *first {
			continue
		}
		if *last > 0 && page.Number > *last {
			break
		}
		s, err := page.Text(ctx)
		if err != nil {
			return err
		}
		out.WriteString(s)
		out.WriteString("\n\f")
	}
	out.Flush()
	for _, w := range doc.Warnings() {
		fmt.Fprintf(os.Stderr, "warning [%s]: %s\n", w.Code, w.Message)
	}
	return nil
}

// markdown prints the document as Markdown.
func markdown(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("markdown", flag.ExitOnError)
	password := fs.String("password", "", "password for an encrypted file")
	pages := fs.String("pages", "", "pages to convert, such as 1-5,8,12-")
	furniture := fs.Bool("furniture", false, "keep running heads and page numbers")
	noMarkers := fs.Bool("no-page-markers", false, "leave out the comment before each page")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	doc, err := unpdf.OpenFile(ctx, fs.Arg(0), unpdf.Options{Password: *password})
	if err != nil {
		return err
	}
	defer doc.Close()
	out := bufio.NewWriter(os.Stdout)
	err = doc.WriteMarkdown(ctx, out, unpdf.MarkdownOptions{Pages: *pages, Furniture: *furniture, OmitPageMarkers: *noMarkers})
	out.Flush()
	if err != nil {
		return err
	}
	for _, w := range doc.Warnings() {
		fmt.Fprintf(os.Stderr, "warning [%s]: %s\n", w.Code, w.Message)
	}
	return nil
}

// outline prints the document's bookmarks, indented by depth.
func outline(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("outline", flag.ExitOnError)
	password := fs.String("password", "", "password for an encrypted file")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	doc, err := unpdf.OpenFile(ctx, fs.Arg(0), unpdf.Options{Password: *password})
	if err != nil {
		return err
	}
	defer doc.Close()
	items, err := doc.Outline(ctx)
	if err != nil {
		return err
	}
	var show func(items []unpdf.OutlineItem, depth int)
	show = func(items []unpdf.OutlineItem, depth int) {
		for _, it := range items {
			page := ""
			if it.Page > 0 {
				page = fmt.Sprintf("  (page %d)", it.Page)
			}
			fmt.Printf("%*s%s%s\n", 2*depth, "", it.Title, page)
			show(it.Children, depth+1)
		}
	}
	show(items, 0)
	return nil
}

// tables prints every table of the document as Markdown, each under a line
// that says where it is and how it was found.
func tables(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tables", flag.ExitOnError)
	password := fs.String("password", "", "password for an encrypted file")
	first := fs.Int("first", 1, "first page")
	last := fs.Int("last", 0, "last page (0 for the last page of the document)")
	least := fs.Float64("min", 0, "leave out tables below this confidence, 0 to 1")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	doc, err := unpdf.OpenFile(ctx, fs.Arg(0), unpdf.Options{Password: *password})
	if err != nil {
		return err
	}
	defer doc.Close()
	for page, err := range doc.Pages(ctx) {
		if err != nil {
			return err
		}
		if page.Number < *first {
			continue
		}
		if *last > 0 && page.Number > *last {
			break
		}
		found, err := page.Tables(ctx)
		if err != nil {
			return err
		}
		for i, t := range found {
			if t.Confidence < *least {
				continue
			}
			fmt.Printf("page %d, table %d: %d rows, %d columns, found from %s, confidence %.2f\n\n%s\n\n",
				page.Number, i+1, t.Rows, t.Cols, t.Method, t.Confidence, t.Markdown())
		}
	}
	return nil
}
