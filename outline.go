// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"bytes"
	"context"
	"strings"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
)

// OutlineItem is one entry of the document outline, also called bookmarks.
type OutlineItem struct {
	// Title is the entry's text.
	Title string
	// Page is the number of the page the entry leads to, from 1. It is 0
	// when the entry has no destination in this document: it may open a
	// web address or another file, or its destination may be broken.
	Page int
	// Children are the entries nested under this one.
	Children []OutlineItem
}

const (
	maxOutlineItems  = 20000
	maxOutlineDepth  = 32
	maxNameTreeDepth = 32
)

// Outline returns the document outline: the table of contents that viewers
// show beside the pages. It is empty when the document has none.
func (d *Document) Outline(ctx context.Context) ([]OutlineItem, error) {
	f := d.f
	root := f.Dict(ctx, d.catalog["Outlines"])
	if root == nil {
		return nil, ctx.Err()
	}
	seen := map[int]bool{}
	count := 0
	var level func(first pdfobj.Object, depth int) []OutlineItem
	level = func(first pdfobj.Object, depth int) []OutlineItem {
		var items []OutlineItem
		for cur := first; cur != nil && ctx.Err() == nil; {
			// Entries are linked through /Next; a damaged file can link
			// them in a circle.
			if ref, ok := cur.(pdfobj.Ref); ok {
				if seen[ref.Num] {
					break
				}
				seen[ref.Num] = true
			}
			node := f.Dict(ctx, cur)
			if node == nil || count >= maxOutlineItems {
				break
			}
			count++
			title, _ := f.String(ctx, node["Title"])
			item := OutlineItem{Title: strings.TrimSpace(pdfobj.DecodeText(title))}
			dest := node["Dest"]
			if dest == nil {
				if action := f.Dict(ctx, node["A"]); action != nil && f.Name(ctx, action["S"]) == "GoTo" {
					dest = action["D"]
				}
			}
			item.Page = d.destinationPage(ctx, dest)
			if depth < maxOutlineDepth {
				item.Children = level(node["First"], depth+1)
			}
			items = append(items, item)
			cur = node["Next"]
		}
		return items
	}
	items := level(root["First"], 0)
	return items, ctx.Err()
}

// destinationPage returns the page number a destination leads to, or 0. A
// destination is an array starting with a page, or the name of one.
func (d *Document) destinationPage(ctx context.Context, dest pdfobj.Object) int {
	f := d.f
	for hops := 0; hops < 4 && dest != nil; hops++ {
		switch v := resolveQuiet(ctx, f, dest).(type) {
		case pdfobj.Array:
			if len(v) == 0 {
				return 0
			}
			if ref, ok := v[0].(pdfobj.Ref); ok {
				return d.pageNumber(ctx, ref.Num)
			}
			// A page index, as destinations in other files are written.
			if n, ok := pdfobj.Int(v[0]); ok && n >= 0 && n < int64(d.NumPages()) {
				return int(n) + 1
			}
			return 0
		case pdfobj.Dict:
			dest = v["D"] // a named destination may be wrapped in a dictionary
		case pdfobj.Name:
			dest = f.Dict(ctx, d.catalog["Dests"])[v]
		case pdfobj.String:
			names := f.Dict(ctx, d.catalog["Names"])
			budget := treeBudget
			dest = d.nameTree(ctx, f.Dict(ctx, names["Dests"]), v, 0, &budget)
		default:
			return 0
		}
	}
	return 0
}

// nameTree looks a key up in a name tree: a tree whose leaves hold sorted
// key and value pairs and whose inner nodes give the range of keys below
// them.
func (d *Document) nameTree(ctx context.Context, node pdfobj.Dict, key []byte, depth int, budget *int) pdfobj.Object {
	f := d.f
	if node == nil || depth > maxNameTreeDepth || *budget <= 0 || ctx.Err() != nil {
		return nil
	}
	*budget--
	if names := f.Array(ctx, node["Names"]); names != nil {
		for i := 0; i+1 < len(names); i += 2 {
			if k, ok := f.String(ctx, names[i]); ok && bytes.Equal(k, key) {
				return names[i+1]
			}
		}
	}
	for _, kid := range f.Array(ctx, node["Kids"]) {
		kd := f.Dict(ctx, kid)
		if limits := f.Array(ctx, kd["Limits"]); len(limits) >= 2 {
			lo, _ := f.String(ctx, limits[0])
			hi, _ := f.String(ctx, limits[1])
			if bytes.Compare(key, lo) < 0 || bytes.Compare(key, hi) > 0 {
				continue
			}
		}
		if v := d.nameTree(ctx, kd, key, depth+1, budget); v != nil {
			return v
		}
	}
	return nil
}

// pageNumber returns the number of the page whose object number is objNum,
// or 0. The first call walks the page tree once to build the index.
func (d *Document) pageNumber(ctx context.Context, objNum int) int {
	if d.pageNumbers == nil {
		index := map[int]int{}
		d.walk(ctx, func(p *Page) bool {
			if p.objNum != 0 {
				index[p.objNum] = p.Number
			}
			return true
		})
		if ctx.Err() != nil {
			return 0
		}
		d.pageNumbers = index
	}
	return d.pageNumbers[objNum]
}
