# unpdf

A pure-Go library for getting the content out of PDF files, built for feeding documents to language models and search indexes.

**Status: early.** It opens, repairs and decrypts PDFs, reads metadata, and extracts text in reading order: as Markdown with headings, lists and links, as plain text, or as blocks, lines and words with their positions. It also returns the outline, links, comments and form fields. Tables, tagged-PDF structure and images are next.

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
  - Words hyphenated at a line end joined, where the hyphen is clearly for the line break.
  - Rotated pages and rotated text, each direction laid out on its own.
  - Running heads, running feet and page numbers marked, by comparing each page with its neighbours.
  - Text that is clipped away, outside the crop box, or drawn twice on top of itself is left out. Invisible text is left out too, unless it is the OCR layer of a scanned page.
  - Scanned pages flagged.
- Structure:
  - Headings, from font size and weight compared with the body text, and from the document outline where there is one. Levels follow the sizes the document uses.
  - Bulleted and numbered list items, with nesting.
  - Markdown built from these, with web links.
  - The outline with page numbers, link targets with their anchor text, comment annotations, and form fields with their values.

## What does not work yet

- Tables are read row by row but not recognised as tables: there are no cells, and in Markdown each row is a line of text.
- Structure is inferred from layout only. Tagged PDFs are not yet read through their structure tree, so a list whose bullets are drawn as graphics, as browsers draw them, comes out as plain lines.
- A heading at body size and weight is found only if the outline names it. Text set large for other reasons, such as the names on a title page, can be taken for a heading.
- A two-column layout whose columns hold only a word or two per line is read across, like a table.
- In right-to-left text, a number next to a Latin phrase keeps its position on the page, which is ambiguous; vowel points may be attached to the wrong side of a letter.
- Fonts whose only route to Unicode is a CFF font program's charset give U+FFFD, with a warning that names the font and counts the glyphs.

## Errors

| Error | Meaning |
|---|---|
| `ErrNotPDF` | The input has no PDF header. |
| `ErrPasswordRequired` | The document is encrypted and needs a password. |
| `ErrWrongPassword` | The password supplied was rejected. Also matches `ErrPasswordRequired`. |
| `ErrDamaged` | The file could not be read even after repair. |
| `*UnsupportedError` | The document uses a feature the library does not handle; `Feature` names it. |
| `*LimitError` | The document exceeded one of the `Limits`. |

## Licence

Apache-2.0. See [LICENSE](LICENSE). The package embeds glyph and character-mapping data from Adobe and Unicode under their own permissive licences; see [NOTICE](NOTICE).
