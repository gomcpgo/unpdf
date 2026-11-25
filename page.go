// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"fmt"
	"iter"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
)

// Rect is a rectangle in PDF user space: points (1/72 inch), with the origin
// at the bottom left. X0,Y0 is the lower-left corner and X1,Y1 the
// upper-right.
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// Width returns the width of the rectangle.
func (r Rect) Width() float64 { return r.X1 - r.X0 }

// Height returns the height of the rectangle.
func (r Rect) Height() float64 { return r.Y1 - r.Y0 }

// Page is one page of a document.
type Page struct {
	// Number is the page's position in the document, starting at 1.
	Number int
	// MediaBox is the full extent of the page.
	MediaBox Rect
	// CropBox is the visible region of the page. It equals MediaBox when
	// the page does not set one.
	CropBox Rect
	// Rotate is the clockwise rotation applied when the page is shown: 0,
	// 90, 180 or 270.
	Rotate int

	doc       *Document
	dict      pdfobj.Dict
	resources pdfobj.Object
}

// Size returns the width and height of the page as displayed, in points:
// the crop box, with the sides swapped when the page is rotated by 90 or
// 270 degrees.
func (p *Page) Size() (width, height float64) {
	w, h := p.CropBox.Width(), p.CropBox.Height()
	if p.Rotate == 90 || p.Rotate == 270 {
		return h, w
	}
	return w, h
}

// NumPages returns the number of pages the document declares. A damaged
// document may hold fewer; Pages yields the pages that are really there.
func (d *Document) NumPages() int { return d.info.Pages }

// inherited holds the page attributes that a page takes from its ancestors
// in the page tree when it does not set them itself.
type inherited struct {
	mediaBox, cropBox, rotate, resources pdfobj.Object
}

func (in inherited) with(node pdfobj.Dict) inherited {
	if v := node["MediaBox"]; v != nil {
		in.mediaBox = v
	}
	if v := node["CropBox"]; v != nil {
		in.cropBox = v
	}
	if v := node["Rotate"]; v != nil {
		in.rotate = v
	}
	if v := node["Resources"]; v != nil {
		in.resources = v
	}
	return in
}

// declaredPages returns the page count from the root of the page tree, or
// counts the pages when that entry is missing or absurd.
func (d *Document) declaredPages(ctx context.Context) int {
	root := d.f.Dict(ctx, d.catalog["Pages"])
	if root == nil {
		d.f.Warn(WarnPageTree, "the document has no page tree")
		return 0
	}
	if n, ok := d.f.Int(ctx, root["Count"]); ok && n >= 0 && n <= int64(d.maxPages) {
		return int(n)
	}
	d.f.Warn(WarnPageTree, "the page tree has no usable /Count; pages were counted")
	n := 0
	d.walk(ctx, func(*Page) bool { n++; return true })
	return n
}

// Pages returns an iterator over the pages of the document, in order. Each
// page is read from the file as the iteration reaches it, so stopping early
// costs nothing for the pages not visited.
//
// The iterator yields a non-nil error, and stops, when ctx is cancelled.
func (d *Document) Pages(ctx context.Context) iter.Seq2[*Page, error] {
	return func(yield func(*Page, error) bool) {
		stopped := false
		d.walk(ctx, func(p *Page) bool {
			if !yield(p, nil) {
				stopped = true
				return false
			}
			return true
		})
		if err := ctx.Err(); err != nil && !stopped {
			yield(nil, err)
		}
	}
}

// Page returns page n, counting from 1.
func (d *Document) Page(ctx context.Context, n int) (*Page, error) {
	if n < 1 {
		return nil, fmt.Errorf("unpdf: page %d: page numbers start at 1", n)
	}
	if p := d.seek(ctx, n); p != nil {
		return p, nil
	}
	// The page counts in the tree did not lead anywhere. Count for real.
	var found *Page
	d.walk(ctx, func(p *Page) bool {
		if p.Number == n {
			found = p
			return false
		}
		return true
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("unpdf: page %d: the document has fewer pages", n)
	}
	return found, nil
}

// seek finds page n by descending the page tree, using each node's /Count to
// skip whole subtrees. It returns nil when the counts are missing or wrong.
func (d *Document) seek(ctx context.Context, n int) *Page {
	f := d.f
	node := f.Dict(ctx, d.catalog["Pages"])
	in := inherited{}
	remaining := int64(n)
	for depth := 0; node != nil && depth < maxPageTreeDepth; depth++ {
		if ctx.Err() != nil {
			return nil
		}
		in = in.with(node)
		kids := f.Array(ctx, node["Kids"])
		var next pdfobj.Dict
		for _, kid := range kids {
			kd := f.Dict(ctx, kid)
			if kd == nil {
				return nil
			}
			if kd["Kids"] == nil {
				if remaining == 1 {
					return d.newPage(ctx, n, kd, in.with(kd))
				}
				remaining--
				continue
			}
			count, ok := f.Int(ctx, kd["Count"])
			if !ok || count < 0 {
				return nil
			}
			if remaining > count {
				remaining -= count
				continue
			}
			next = kd
			break
		}
		node = next
	}
	return nil
}

// walk visits every page in document order until visit returns false. It
// survives malformed trees: loops are cut, depth is capped, and nodes that
// cannot be read are skipped with a warning.
func (d *Document) walk(ctx context.Context, visit func(*Page) bool) {
	f := d.f
	type frame struct {
		kids pdfobj.Array
		next int
		in   inherited
	}
	stack := []frame{{kids: pdfobj.Array{d.catalog["Pages"]}}}
	seen := map[int]bool{}
	n := 0
	for len(stack) > 0 {
		if ctx.Err() != nil {
			return
		}
		top := &stack[len(stack)-1]
		if top.next >= len(top.kids) {
			stack = stack[:len(stack)-1]
			continue
		}
		kid := top.kids[top.next]
		top.next++
		if ref, ok := kid.(pdfobj.Ref); ok {
			if seen[ref.Num] {
				f.Warn(WarnPageTree, "the page tree refers to object %d more than once", ref.Num)
				continue
			}
			seen[ref.Num] = true
		}
		node := f.Dict(ctx, kid)
		if node == nil {
			f.Warn(WarnPageTree, "a page tree node is missing or unreadable")
			continue
		}
		in := top.in.with(node)
		if node["Kids"] != nil {
			if len(stack) >= maxPageTreeDepth {
				f.Warn(WarnPageTree, "the page tree is nested more than %d levels deep", maxPageTreeDepth)
				continue
			}
			stack = append(stack, frame{kids: f.Array(ctx, node["Kids"]), in: in})
			continue
		}
		n++
		if !visit(d.newPage(ctx, n, node, in)) {
			return
		}
		if n >= d.maxPages {
			f.Warn(WarnLimit, "stopped after %d pages", d.maxPages)
			return
		}
	}
}

func (d *Document) newPage(ctx context.Context, n int, dict pdfobj.Dict, in inherited) *Page {
	p := &Page{Number: n, doc: d, dict: dict, resources: in.resources}
	media, ok := d.rect(ctx, in.mediaBox)
	if !ok {
		// US Letter is what viewers fall back to.
		media = Rect{0, 0, 612, 792}
		d.f.Warn(WarnPageTree, "page %d has no usable /MediaBox; assumed 612 x 792", n)
	}
	p.MediaBox = media
	p.CropBox = media
	if crop, ok := d.rect(ctx, in.cropBox); ok {
		// The crop box cannot extend beyond the media box.
		crop.X0, crop.Y0 = max(crop.X0, media.X0), max(crop.Y0, media.Y0)
		crop.X1, crop.Y1 = min(crop.X1, media.X1), min(crop.Y1, media.Y1)
		if crop.X1 > crop.X0 && crop.Y1 > crop.Y0 {
			p.CropBox = crop
		}
	}
	if r, ok := d.f.Int(ctx, in.rotate); ok && r%90 == 0 {
		p.Rotate = int(((r % 360) + 360) % 360)
	}
	return p
}

// rect reads a rectangle array and normalises it so that X0,Y0 is the
// lower-left corner.
func (d *Document) rect(ctx context.Context, o pdfobj.Object) (Rect, bool) {
	arr := d.f.Array(ctx, o)
	if len(arr) < 4 {
		return Rect{}, false
	}
	var v [4]float64
	for i := range v {
		n, ok := d.f.Number(ctx, arr[i])
		if !ok {
			return Rect{}, false
		}
		v[i] = n
	}
	r := Rect{min(v[0], v[2]), min(v[1], v[3]), max(v[0], v[2]), max(v[1], v[3])}
	return r, r.X1 > r.X0 && r.Y1 > r.Y0
}
