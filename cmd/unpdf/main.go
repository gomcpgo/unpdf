// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Command unpdf inspects PDF files.
//
//	unpdf info [-password PW] [-json] file.pdf
//	unpdf text [-password PW] [-first N] [-last N] file.pdf
//	unpdf markdown [-password PW] [-pages LIST] [-furniture] [-no-page-markers] [-table-confidence N] file.pdf
//	unpdf outline [-password PW] file.pdf
//	unpdf tables [-password PW] [-first N] [-last N] [-min CONFIDENCE] file.pdf
//	unpdf images [-password PW] [-first N] [-last N] [-all] [-out DIR] file.pdf
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
	"path/filepath"
	"slices"
	"strings"
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
	case "images":
		err = images(ctx, os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "       unpdf markdown [-password PW] [-pages LIST] [-furniture] [-no-page-markers] [-table-confidence N] file.pdf")
	fmt.Fprintln(os.Stderr, "       unpdf outline [-password PW] file.pdf")
	fmt.Fprintln(os.Stderr, "       unpdf tables [-password PW] [-first N] [-last N] [-min CONFIDENCE] file.pdf")
	fmt.Fprintln(os.Stderr, "       unpdf images [-password PW] [-first N] [-last N] [-all] [-out DIR] file.pdf")
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
	confidence := fs.Float64("table-confidence", unpdf.DefaultTableConfidence, "show a table as a table from this confidence, 0 to 1; 0 shows every table found")
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
	opts := unpdf.MarkdownOptions{Pages: *pages, Furniture: *furniture, OmitPageMarkers: *noMarkers, MinTableConfidence: *confidence}
	if *confidence <= 0 {
		// In the options zero stands for the default.
		opts.MinTableConfidence = -1
	}
	err = doc.WriteMarkdown(ctx, out, opts)
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

// images lists the images of each page, one per line, and with -out writes
// each to a file in that directory.
func images(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("images", flag.ExitOnError)
	password := fs.String("password", "", "password for an encrypted file")
	first := fs.Int("first", 1, "first page")
	last := fs.Int("last", 0, "last page (0 for the last page of the document)")
	all := fs.Bool("all", false, "include small images and images repeated from page to page")
	dir := fs.String("out", "", "directory to write the images to")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	doc, err := unpdf.OpenFile(ctx, fs.Arg(0), unpdf.Options{Password: *password})
	if err != nil {
		return err
	}
	defer doc.Close()
	if *dir != "" {
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			return err
		}
	}
	failed := 0
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
		found, err := page.Images(ctx, unpdf.ImageOptions{Small: *all, Repeated: *all})
		if err != nil {
			return err
		}
		for i := range found {
			img := &found[i]
			var notes []string
			for note, set := range map[string]bool{"stencil": img.Stencil, "masked": img.Masked, "inline": img.Inline, "small": img.Small, "repeated": img.Repeated} {
				if set {
					notes = append(notes, note)
				}
			}
			slices.Sort(notes)
			space := img.ColorSpace
			if space == "" {
				space = "-"
			}
			fmt.Printf("page %d, image %d: %d x %d pixels, %s, %d bits, %s, at %.0f,%.0f to %.0f,%.0f%s\n",
				page.Number, i+1, img.Width, img.Height, space, img.BitsPerComponent, img.Format,
				img.Box.X0, img.Box.Y0, img.Box.X1, img.Box.Y1, strings.Join(append([]string{""}, notes...), ", "))
			if *dir == "" {
				continue
			}
			data, err := img.Extract(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "page %d, image %d: %v\n", page.Number, i+1, err)
				failed++
				continue
			}
			name := filepath.Join(*dir, fmt.Sprintf("page-%04d-image-%03d%s", page.Number, i+1, img.Format.Extension()))
			if err := os.WriteFile(name, data, 0o644); err != nil {
				return err
			}
		}
	}
	for _, w := range doc.Warnings() {
		fmt.Fprintf(os.Stderr, "warning [%s]: %s\n", w.Code, w.Message)
	}
	if failed > 0 {
		return fmt.Errorf("%d images could not be extracted", failed)
	}
	return nil
}
