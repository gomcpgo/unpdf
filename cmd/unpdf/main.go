// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Command unpdf inspects PDF files.
//
//	unpdf info [-password PW] [-json] file.pdf
package main

import (
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
