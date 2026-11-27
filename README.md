# unpdf

A pure-Go library for getting the content out of PDF files, built for feeding documents to language models and search indexes.

**Status: early.** It opens, repairs and decrypts PDFs, reads metadata, and extracts text in reading order: as Markdown with headings, lists, tables and links, as plain text, or as blocks, lines and words with their positions. It finds tables and gives their cells, reads the structure tree of tagged PDFs, and returns the outline, links, comments and form fields. It lists the images on each page and extracts them as PNG or JPEG files.

- No cgo, no external programs, no dependencies outside the Go standard library.
- Safe on untrusted input: bounded memory, configurable limits, no panics on malformed files. Every parser has a fuzz target.
- Requires Go 1.25 or later.

## Install

```sh
go get github.com/gomcpgo/unpdf
```

## Use

Markdown for a whole document, which is usually what a language model wants:

```go
doc, err := unpdf.OpenFile(ctx, "report.pdf", unpdf.Options{})
if err != nil {
    return err
}
defer doc.Close()

md, err := doc.Markdown(ctx, unpdf.MarkdownOptions{Pages: "1-20"})
```

Running heads and page numbers are left out, paragraphs are unwrapped, and a comment such as `<!-- page 3 -->` precedes each page so that a passage can be traced to its page. `WriteMarkdown` streams to an `io.Writer`, and `MaxBytes` caps the output.

Page by page, as plain text:

```go
doc, err := unpdf.OpenFile(ctx, "report.pdf", unpdf.Options{})
if err != nil {
    return err
}
defer doc.Close()

info := doc.Info()
fmt.Println(info.Title, info.Author, info.Pages)

for page, err := range doc.Pages(ctx) {
    if err != nil {
        return err
    }
    text, err := page.Text(ctx)
    if err != nil {
        return err
    }
    fmt.Printf("--- page %d ---\n%s\n", page.Number, text)
}

for _, w := range doc.Warnings() {
    fmt.Println("warning:", w.Message)
}
```

`unpdf.Open` takes an `io.ReaderAt` and a size when the PDF is not a file on disk.

For positions, fonts, and to leave out running heads and page numbers, use the blocks:

```go
blocks, err := page.Blocks(ctx)
for _, b := range blocks {
    if b.Furniture {
        continue // a running head, a running foot or a page number
    }
    fmt.Println(b.Box, b.Text())
    for _, line := range b.Lines {
        for _, w := range line.Words {
            _ = w // w.Text, w.Box, w.Font, w.Size, w.Bold, w.Italic
        }
    }
}

if scanned, _ := page.Scanned(ctx); scanned {
    // an image of a page: send it to OCR or to a vision model
}
```

Each block has a `Kind` (heading, list item or paragraph) and a `Level`.

Tables, with their cells:

```go
tables, err := page.Tables(ctx)
for _, t := range tables {
    if t.Confidence < 0.5 {
        continue // not sure enough: the text is in Blocks all the same
    }
    for _, row := range t.Grid() {
        // row is the text of each cell, left to right
    }
    fmt.Println(t.Markdown())
}
```

A table carries the way it was found (`Method`) and how sure the detection is (`Confidence`). Markdown shows a table as a table from a confidence of 0.5, and as lines of text below that. `MarkdownOptions.MinTableConfidence` moves the line: set it higher to keep only the sure ones, or below zero to show every table found.

Images, each with its place on the page and its size in pixels:

```go
images, err := page.Images(ctx, unpdf.ImageOptions{})
for _, img := range images {
    data, err := img.Extract(ctx)
    if err != nil {
        continue // a colour space that cannot be converted, or an image over the limits
    }
    name := fmt.Sprintf("page-%d-%dx%d%s", page.Number, img.Width, img.Height, img.Format.Extension())
    os.WriteFile(name, data, 0o644)
}
```

A JPEG image comes back as the JPEG file the document holds. Other images are converted to PNG, with a mask as the alpha channel. JPEG 2000, JBIG2 and CCITT fax images come back as the document holds them, not decoded, with `Format` saying which. Small images, such as bullets and rules, and images that repeat from page to page, such as a logo, are left out unless `ImageOptions` asks for them.

The rest of what a document carries:

```go
outline, _ := doc.Outline(ctx)        // bookmarks, each with its page number
fields, _ := doc.FormFields(ctx)      // form field names and values
links, _ := page.Links(ctx)           // targets, with the text each link covers
comments, _ := page.Annotations(ctx)  // sticky notes and other comments
```

Boxes are in points from the top-left corner of the page as displayed.

There is also a small command-line tool:

```sh
go run github.com/gomcpgo/unpdf/cmd/unpdf info report.pdf
go run github.com/gomcpgo/unpdf/cmd/unpdf text report.pdf
go run github.com/gomcpgo/unpdf/cmd/unpdf markdown -pages 1-10 report.pdf
go run github.com/gomcpgo/unpdf/cmd/unpdf outline report.pdf
go run github.com/gomcpgo/unpdf/cmd/unpdf tables -min 0.5 report.pdf
go run github.com/gomcpgo/unpdf/cmd/unpdf images -out pictures report.pdf
```

## What works today

- Classic cross-reference tables, cross-reference streams, object streams, hybrid files and incremental updates.
- Repair of files with a missing or wrong cross-reference table, by scanning for objects. If the page tree itself is lost, the pages that survive are still returned.
- Stream filters: Flate, LZW, ASCII85, ASCIIHex and RunLength, with PNG and TIFF predictors.
- Encryption with the standard security handler: RC4 (40 to 128 bits), AES-128 and AES-256, revisions 2 to 6. Files with an empty user password open without asking.
- Metadata from the document information dictionary and from XMP.
- Pages with their media box, crop box and rotation.
- Page text, decoded:
  - Simple fonts with the standard encodings, `/Differences` and the Adobe Glyph List, including the glyph names TeX uses for mathematics and the built-in encoding of embedded Type 1 fonts.
  - Composite fonts through `/ToUnicode`, the predefined CJK CMaps (Shift-JIS, EUC, GBK, Big5, UHC and the Unicode ones), Adobe's character collections, and as a last resort the character map of an embedded TrueType font.
  - Text inside form XObjects, the values of form fields, and free-text annotations.
  - `/ActualText` replacements, Latin ligatures expanded to their letters, Arabic presentation forms turned back into letters.
- Page text, laid out:
  - Words, lines and blocks, each with its box; words with font, size, bold and italic.
  - Columns read one after the other; tables and other aligned text read row by row.
  - Arabic and Hebrew in logical order, with embedded numbers and Latin words in place.
  - Words hyphenated at a line end joined, where the hyphen is clearly for the line break. A compound broken at its hyphen keeps it ("long-" and "term" make "long-term") when the pages around, up to four each way, write it with the hyphen and never without. One they do not write again loses its hyphen.
  - Rotated pages and rotated text, each direction laid out on its own.
  - Running heads, running feet and page numbers marked, by comparing each page with its neighbours.
  - Text that is clipped away, outside the crop box, or drawn twice on top of itself is left out. Invisible text is left out too, unless it is the OCR layer of a scanned page.
  - Scanned pages flagged.
- Structure:
  - Headings, from font size and weight compared with the body text, and from the document outline where there is one. Levels follow the sizes the document uses.
  - Bulleted and numbered list items, with nesting.
  - Tagged PDFs, as Word, browsers, InDesign and accessible exports make them: the structure tree gives the reading order, the headings with their levels, the list items and the tables. A page whose tags leave out much of its text is laid out from its geometry instead.
  - Markdown built from these, with web links.
  - The outline with page numbers, link targets with their anchor text, comment annotations, and form fields with their values.
- Tables:
  - From the structure tree of a tagged PDF, with merged cells and header rows as tagged.
  - From ruling lines that form a grid, with merged cells. Rows that no rule separates, as in most statistical tables, are told apart by their lines of text, and row labels that stand outside the grid are taken in.
  - From text that lines up in columns over three or more rows, with no lines drawn. Cells of more than one line, currency signs set in a column of their own and labels that wrap are put back together.
  - Each with a confidence. Forms, charts, tables of contents and numbered lists, which can look like tables, score low. So does text that lines up among the curves and slanting lines of a diagram or a chart, or on a picture: the labels of a figure, not the cells of a table.
  - A title drawn or tagged as the table's first row, and notes as its last rows, are left out of the table and read as text around it. Dot leaders are left out of cells. A line that names a group of rows is a row of its own.
  - In Markdown, a table that runs on over a page break is continued under the same header.
- Images:
  - The images of a page with their position and their size in pixels, including inline images and those drawn by forms and annotations. Images outside the visible part of the page, or clipped away, are left out.
  - JPEG images as they are. JPEG 2000, JBIG2 (with its global segments) and CCITT fax images as they are too, not decoded, with the parameters a CCITT decoder needs.
  - Every other image as PNG: grey, RGB, CMYK, indexed, Lab and ICC-based colour at 1 to 16 bits per component, `/Decode` arrays, and soft masks, stencil masks and colour-key masks as transparency. Grey and RGB keep their bit depth, and an indexed image stays a palette image.
  - Small images and images repeated in the same place on the neighbouring pages are marked, and left out by default. The strips or tiles of a picture stored in pieces are not counted as small.

## What does not work yet

- Table detection without tags is a judgement, and it errs both ways. The labels of a diagram that happen to line up in rows and columns can be taken for a table. A table without rules whose cells hold wrapped text, with nothing to mark where a row ends, is left as text. Check `Confidence`, and expect surprises in documents full of figures.
- Markdown tables have no merged cells: the text of a merged cell is in its first position and the rest are empty. Markdown has one header row, so where a table has several (`HeaderRows`), each column's heading is its headings from the top level down, joined with " / ". Where the header rows are not known, the first row is shown as the header row. They are known when the document tags them, when a ruled table rules off its header from rows of figures that no rule separates, and, in a table found by alignment, when figures or a rule show where the headings end; a heading set over several lines is then one cell.
- Without tags, a heading at body size and weight is found only if the outline names it, a list whose bullets are drawn as graphics comes out as plain lines, and text set large for other reasons, such as the names on a title page, can be taken for a heading.
- A tagged document is taken at its word. Where it tags its headings, text it tags as a paragraph is not made a heading for being bold, so a heading the author did not tag is missed.
- Tagged content inside form XObjects that number their own marked content, and alternative text for figures, are not read.
- A two-column layout whose columns hold only a word or two per line is read across, like a table.
- In right-to-left text, a number next to a Latin phrase keeps its position on the page, which is ambiguous; vowel points may be attached to the wrong side of a letter.
- Fonts whose only route to Unicode is a CFF font program's charset give U+FFFD, with a warning that names the font and counts the glyphs.
- Images are converted to PNG without colour management: an ICC profile is taken for the device space with as many components, and CMYK becomes RGB by the plain formula, so colours can be off. A spot colour (a Separation space) comes out as grey, and a DeviceN space of spot colours is not converted at all.
- JPEG 2000, JBIG2 and CCITT images are not decoded, and the mask of an image returned as it is, such as a JPEG, is not applied.
- A picture stored as strips or tiles comes back as that many images. Pictures drawn by a tiling pattern or as the glyphs of a font are not listed, and Markdown output does not mention images.

## Errors

| Error | Meaning |
|---|---|
| `ErrNotPDF` | The input has no PDF header. |
| `ErrPasswordRequired` | The document is encrypted and needs a password. |
| `ErrWrongPassword` | The password supplied was rejected. Also matches `ErrPasswordRequired`. |
| `ErrDamaged` | The file could not be read even after repair. |
| `*UnsupportedError` | The document uses a feature the library does not handle; `Feature` names it. |
| `*LimitError` | The document, or an image being converted, exceeded one of the `Limits`. |

## Licence

Apache-2.0. See [LICENSE](LICENSE). The package embeds glyph and character-mapping data from Adobe and Unicode under their own permissive licences; see [NOTICE](NOTICE).
