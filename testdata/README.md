# Test data

Every file here was generated for this repository and may be redistributed
under the repository's licence.

| File | What it is | Source |
|---|---|---|
| `classic.pdf` | Two pages, PDF 1.4, classic cross-reference table, no object streams | `src/classic.tex`, built with pdfTeX |
| `compressed.pdf` | Three pages, PDF 1.5, cross-reference stream and object streams | `src/compressed.tex`, built with pdfTeX |

The embedded fonts are subsets of Computer Modern, which is distributed under
the SIL Open Font License.

Files with other structures (encryption, incremental updates, damage) are
built in memory by `internal/pdftest`.
