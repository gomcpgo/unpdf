# unpdf

A pure-Go library for getting the content out of PDF files, built for feeding documents to language models and search indexes.

**Status: early.** It opens, repairs and decrypts PDFs, reads metadata and pages, and extracts the text of each page in the order the page draws it. Layout analysis (columns, reading order, right-to-left text, joining hyphenated words) is next; Markdown, structure, tables and images follow.

- No cgo, no external programs, no dependencies outside the Go standard library.
- Safe on untrusted input: bounded memory, configurable limits, no panics on malformed files. Every parser has a fuzz target.
- Requires Go 1.25 or later.

## Install

```sh
go get github.com/gomcpgo/unpdf
```

## Use

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

There is also a small command-line tool:

```sh
go run github.com/gomcpgo/unpdf/cmd/unpdf info report.pdf
go run github.com/gomcpgo/unpdf/cmd/unpdf text report.pdf
```

## What works today

- Classic cross-reference tables, cross-reference streams, object streams, hybrid files and incremental updates.
- Repair of files with a missing or wrong cross-reference table, by scanning for objects. If the page tree itself is lost, the pages that survive are still returned.
- Stream filters: Flate, LZW, ASCII85, ASCIIHex and RunLength, with PNG and TIFF predictors.
- Encryption with the standard security handler: RC4 (40 to 128 bits), AES-128 and AES-256, revisions 2 to 6. Files with an empty user password open without asking.
- Metadata from the document information dictionary and from XMP.
- Pages with their media box, crop box and rotation.
- Page text:
  - Simple fonts with the standard encodings, `/Differences` and the Adobe Glyph List, including the glyph names TeX uses for mathematics and the built-in encoding of embedded Type 1 fonts.
  - Composite fonts through `/ToUnicode`, the predefined CJK CMaps (Shift-JIS, EUC, GBK, Big5, UHC and the Unicode ones) and Adobe's character collections.
  - Text inside form XObjects, the values of form fields, and free-text annotations.
  - `/ActualText` replacements, Latin ligatures expanded to their letters.
  - Text outside the crop box and text drawn twice to fake bold are left out.

## What does not work yet

- Reading order is the drawing order. Multi-column pages usually come out right; tables often do not.
- Arabic and Hebrew come out in visual order, reversed.
- Words hyphenated at a line end are not joined.
- Fonts whose only route to Unicode is the embedded font program's own tables (TrueType `cmap`, CFF charset) give U+FFFD, with a warning that names the font and counts the glyphs.

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
