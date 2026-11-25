// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package layout

import (
	"math"
	"strings"
	"testing"
)

// run returns the glyphs of a line of text that starts at x, y, with every
// character half an em wide. Spaces become space glyphs.
func run(x, y, size float64, s string) []Glyph {
	var out []Glyph
	for _, r := range s {
		out = append(out, Glyph{Text: string(r), X: x, Y: y, UX: 1, Advance: size / 2, Size: size, Font: "Body"})
		x += size / 2
	}
	return out
}

// paragraph lays out lines of text one below the other.
func paragraph(x, y, size, leading float64, lines ...string) []Glyph {
	var out []Glyph
	for i, l := range lines {
		out = append(out, run(x, y+float64(i)*leading, size, l)...)
	}
	return out
}

func texts(blocks []Block) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = b.Text()
	}
	return out
}

func equal(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func TestWords(t *testing.T) {
	// Words separated by space glyphs, by gaps, and not by kerning.
	var g []Glyph
	g = append(g, run(100, 100, 10, "two words")...)
	g = append(g, run(200, 100, 10, "gap")...)    // a gap with no space glyph
	g = append(g, run(215.5, 100, 10, "kern")...) // half a point: kerning, same word
	blocks := Analyze(g)
	if len(blocks) != 1 || len(blocks[0].Lines) != 1 {
		t.Fatalf("got %d blocks", len(blocks))
	}
	line := blocks[0].Lines[0]
	if line.Text() != "two words gapkern" {
		t.Errorf("line = %q", line.Text())
	}
	w := line.Words[0]
	if w.Text != "two" || w.Font != "Body" || w.Size != 10 {
		t.Errorf("word = %+v", w)
	}
	// The box runs from the first glyph to the end of the last, and from
	// the ascender line to the descender line.
	want := Box{100, 100 - 8, 115, 100 + 2}
	if math.Abs(w.Box.X0-want.X0)+math.Abs(w.Box.Y0-want.Y0)+math.Abs(w.Box.X1-want.X1)+math.Abs(w.Box.Y1-want.Y1) > 1e-9 {
		t.Errorf("box = %+v, want %+v", w.Box, want)
	}

	// Glyphs drawn out of order still form one word, and a narrow space
	// glyph between two pieces keeps them apart.
	var scrambled []Glyph
	word := run(100, 100, 10, "order")
	for _, i := range []int{3, 0, 4, 1, 2} {
		scrambled = append(scrambled, word[i])
	}
	if got := texts(Analyze(scrambled)); !equal(got, []string{"order"}) {
		t.Errorf("scrambled = %q", got)
	}
	tight := append(run(100, 100, 10, "ab"), Glyph{Text: " ", X: 110, Y: 100, UX: 1, Advance: 0.5, Size: 10})
	tight = append(tight, run(110.5, 100, 10, "cd")...)
	if got := texts(Analyze(tight)); !equal(got, []string{"ab cd"}) {
		t.Errorf("narrow space = %q", got)
	}
}

func TestLinesAndBlocks(t *testing.T) {
	var g []Glyph
	g = append(g, run(72, 80, 20, "A Heading")...)
	g = append(g, paragraph(72, 110, 10, 12, "first line of the paragraph", "second line of it")...)
	g = append(g, paragraph(72, 160, 10, 12, "another paragraph")...)
	g = append(g, run(72, 122.0+4, 7, "x")...) // a subscript-sized stray on the second line
	got := texts(Analyze(g))
	want := []string{"A Heading", "first line of the paragraph\nsecond line of it", "another paragraph"}
	if len(got) != 3 || got[0] != want[0] || got[2] != want[2] || !strings.HasPrefix(got[1], "first line of the paragraph\n") {
		t.Errorf("blocks = %q, want about %q", got, want)
	}

	// Lines drawn bottom to top are still read top to bottom.
	var reversed []Glyph
	reversed = append(reversed, run(72, 124, 10, "below")...)
	reversed = append(reversed, run(72, 112, 10, "above")...)
	if got := texts(Analyze(reversed)); !equal(got, []string{"above\nbelow"}) {
		t.Errorf("reversed = %q", got)
	}
	if blocks := Analyze(nil); blocks != nil {
		t.Errorf("no glyphs gave %v", blocks)
	}
}

func prose(x, y float64, tag string, lines int) []Glyph {
	var text []string
	for i := 0; i < lines; i++ {
		text = append(text, tag+" words that fill the line "+string(rune('a'+i)))
	}
	return paragraph(x, y, 10, 12, text...)
}

func TestColumns(t *testing.T) {
	// A full-width title over two columns, with a footer below. The columns
	// share a paragraph break at the same height, which must not make the
	// reader hop between them.
	var g []Glyph
	g = append(g, run(72, 60, 16, "A title that spans both columns of the page")...)
	g = append(g, prose(72, 100, "left one", 6)...)
	g = append(g, prose(72, 190, "left two", 6)...)
	g = append(g, prose(330, 100, "right one", 6)...)
	g = append(g, prose(330, 190, "right two", 6)...)
	g = append(g, run(72, 300, 10, "a footnote that runs across the full width of the page again")...)
	got := texts(Analyze(g))
	order := ""
	for _, s := range got {
		switch {
		case strings.HasPrefix(s, "A title"):
			order += "T"
		case strings.HasPrefix(s, "left one"):
			order += "a"
		case strings.HasPrefix(s, "left two"):
			order += "b"
		case strings.HasPrefix(s, "right one"):
			order += "c"
		case strings.HasPrefix(s, "right two"):
			order += "d"
		case strings.HasPrefix(s, "a footnote"):
			order += "F"
		}
	}
	if order != "TabcdF" {
		t.Errorf("reading order %q, want TabcdF; blocks %q", order, got)
	}
	for _, s := range got {
		if strings.Contains(s, "left") && strings.Contains(s, "right") {
			t.Errorf("a line runs across both columns: %q", s)
		}
	}

	// A table has gaps between its columns too, but is read row by row.
	var table []Glyph
	for i, row := range [][3]string{{"Region", "Units", "Share"}, {"North", "1200", "40%"}, {"South", "900", "30%"},
		{"East", "600", "20%"}, {"West", "300", "10%"}, {"Total", "3000", "100%"}} {
		y := 100 + float64(i)*12
		table = append(table, run(72, y, 10, row[0])...)
		table = append(table, run(200, y, 10, row[1])...)
		table = append(table, run(300, y, 10, row[2])...)
	}
	if got := texts(Analyze(table)); len(got) != 1 || !strings.HasPrefix(got[0], "Region Units Share\nNorth 1200 40%\n") {
		t.Errorf("table = %q", got)
	}
}

func TestHyphenation(t *testing.T) {
	tests := []struct {
		lines []string
		want  string
	}{
		{[]string{"an exam-", "ple of it"}, "an example\nof it"},
		{[]string{"word-", "break"}, "wordbreak"},
		{[]string{"soft­", "Hyphen"}, "softHyphen"},
		{[]string{"the X-", "ray"}, "the X-\nray"},         // a capital before the hyphen
		{[]string{"in 2020-", "2021"}, "in 2020-\n2021"},   // a range
		{[]string{"pre-", "Columbian"}, "pre-\nColumbian"}, // a capital after it
		{[]string{"a dash -", "here"}, "a dash -\nhere"},   // a hyphen standing alone
		{[]string{"multi-", "line-", "word here"}, "multilineword\nhere"},
		{[]string{"no hyphen", "at all"}, "no hyphen\nat all"},
	}
	for _, tt := range tests {
		lines := make([][]string, len(tt.lines))
		for i, l := range tt.lines {
			lines[i] = strings.Fields(l)
		}
		if got := JoinLines(lines, make([]bool, len(lines))); got != tt.want {
			t.Errorf("%q: got %q, want %q", tt.lines, got, tt.want)
		}
	}
	// Right-to-left lines are left alone.
	if got := JoinLines([][]string{{"ab-"}, {"cd"}}, []bool{true, false}); got != "ab-\ncd" {
		t.Errorf("rtl: got %q", got)
	}
}

// visual returns glyphs for s laid out left to right as given: for
// right-to-left text, that is the reverse of reading order, as on a page.
func visual(x, y float64, s string) []Glyph { return run(x, y, 10, s) }

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func TestRightToLeft(t *testing.T) {
	const (
		shalom = "שלום" // Hebrew: shin lamed vav final-mem
		olam   = "עולם" // ayin vav lamed final-mem
	)
	// "shalom olam" as it appears on the page: the second word on the left,
	// each word's letters right to left.
	g := visual(100, 100, reverse(shalom+" "+olam))
	blocks := Analyze(g)
	if len(blocks) != 1 || !blocks[0].Lines[0].RTL || blocks[0].Text() != shalom+" "+olam {
		t.Errorf("hebrew: %q, rtl %v", blocks[0].Text(), blocks[0].Lines[0].RTL)
	}

	// A Latin phrase inside the sentence keeps its own order, and the
	// comma after it, which the page shows at its left end, follows it.
	logical := shalom + " UN Charter, " + olam
	page := reverse(olam) + " ,UN Charter " + reverse(shalom)
	if got := Analyze(visual(100, 100, page))[0].Text(); got != logical {
		t.Errorf("latin phrase: got %q, want %q", got, logical)
	}
	// So does a number.
	logical = shalom + " 1948 " + olam
	page = reverse(olam) + " 1948 " + reverse(shalom)
	if got := Analyze(visual(100, 100, page))[0].Text(); got != logical {
		t.Errorf("number: got %q, want %q", got, logical)
	}
	// A number next to a Latin phrase is ambiguous on the page: "1948 UN"
	// and "UN 1948" are displayed the same way. The page order is kept.

	// The same text drawn in logical order, each glyph placed further left.
	var drawn []Glyph
	x := 300.0
	for _, r := range shalom + " " + olam {
		x -= 5
		drawn = append(drawn, Glyph{Text: string(r), X: x, Y: 100, UX: 1, Advance: 5, Size: 10})
	}
	if got := Analyze(drawn)[0].Text(); got != shalom+" "+olam {
		t.Errorf("logical drawing order: got %q", got)
	}

	// A vowel point drawn after its letter stays with it.
	withPoint := visual(100, 100, reverse(shalom))
	withPoint = append(withPoint, Glyph{Text: "ָ", X: 116, Y: 100, UX: 1, Size: 10})
	if got := Analyze(withPoint)[0].Text(); !strings.Contains(got, "ָ") || []rune(got)[0] != 0x05e9 {
		t.Errorf("vowel point: got %+q", got)
	}

	// A Latin word set tight against a Hebrew one is two words, and is not
	// reversed.
	tight := visual(100, 100, "abc"+reverse(shalom))
	if got := Analyze(tight)[0].Text(); got != shalom+" abc" {
		t.Errorf("mixed word: got %q", got)
	}

	// A left-to-right line with one Hebrew word stays left-to-right.
	ltr := visual(100, 100, "the word "+reverse(shalom)+" means peace")
	if b := Analyze(ltr)[0]; b.Lines[0].RTL || b.Text() != "the word "+shalom+" means peace" {
		t.Errorf("ltr with hebrew: %q", b.Text())
	}
}

func TestClustersAndDuplicates(t *testing.T) {
	// One glyph carries the text of a cluster; the next has none but takes
	// up room. The word must not break there.
	g := []Glyph{
		{Text: "ab", X: 100, Y: 100, UX: 1, Advance: 0, Size: 10},
		{Text: "", X: 100, Y: 100, UX: 1, Advance: 10, Size: 10},
		{Text: "c", X: 110, Y: 100, UX: 1, Advance: 5, Size: 10},
	}
	if got := texts(Analyze(g)); !equal(got, []string{"abc"}) {
		t.Errorf("cluster = %q", got)
	}

	// The same word drawn twice, slightly offset, is one word; the same
	// word elsewhere, or at another size in the same place, is not.
	var d []Glyph
	d = append(d, run(100, 100, 10, "Bold text")...)
	d = append(d, run(100.4, 100.2, 10, "Bold text")...)
	d = append(d, run(100, 130, 10, "Bold")...)
	d = append(d, run(300, 100, 30, "Bold")...)
	got := strings.Join(texts(Analyze(d)), " | ")
	if strings.Count(got, "Bold") != 3 || strings.Count(got, "text") != 1 {
		t.Errorf("duplicates = %q", got)
	}

	// Two different words in the same place are kept apart, not shuffled
	// together letter by letter.
	var o []Glyph
	o = append(o, run(100, 100, 10, "first")...)
	o = append(o, run(101, 100, 10, "other")...)
	for _, s := range texts(Analyze(o)) {
		if !strings.Contains(s, "first") || !strings.Contains(s, "other") {
			t.Errorf("overlapping words = %q", s)
		}
	}
}

func TestDirections(t *testing.T) {
	// Upright text, a label reading up the page, and a slanted watermark.
	var g []Glyph
	g = append(g, paragraph(72, 100, 10, 12, "main text of the page", "in two lines")...)
	for i, r := range "side" {
		g = append(g, Glyph{Text: string(r), X: 40, Y: 300 - float64(i)*5, UX: 0, UY: -1, Advance: 5, Size: 10})
	}
	c, s := math.Cos(math.Pi/6), math.Sin(math.Pi/6)
	for i, r := range "DRAFT" {
		g = append(g, Glyph{Text: string(r), X: 200 + float64(i)*20*c, Y: 400 + float64(i)*20*s, UX: c, UY: s, Advance: 20, Size: 40})
	}
	blocks := Analyze(g)
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks: %q", len(blocks), texts(blocks))
	}
	if blocks[0].Angle != 0 || blocks[0].Text() != "main text of the page\nin two lines" {
		t.Errorf("main block: angle %d, %q", blocks[0].Angle, blocks[0].Text())
	}
	byAngle := map[int]Block{}
	for _, b := range blocks[1:] {
		byAngle[b.Angle] = b
	}
	side, ok := byAngle[270]
	if !ok || side.Text() != "side" {
		t.Errorf("side label: %+v", byAngle)
	}
	// The label's box, in display space, is tall and narrow.
	if w, h := side.Box.X1-side.Box.X0, side.Box.Y1-side.Box.Y0; w >= h || math.Abs(h-20) > 1e-6 {
		t.Errorf("side label box %v x %v", w, h)
	}
	if mark, ok := byAngle[30]; !ok || mark.Text() != "DRAFT" {
		t.Errorf("watermark: %+v", byAngle)
	}

	// Vertical writing: columns run down the page and follow each other
	// from right to left.
	var v []Glyph
	for col, text := range []string{"ab", "cd"} {
		for i, r := range text {
			v = append(v, Glyph{Text: string(r), X: 300 - float64(col)*20, Y: 100 + float64(i)*10, UX: 0, UY: 1, Advance: 10, Size: 10})
		}
	}
	if got := strings.Join(texts(Analyze(v)), "\n"); got != "ab\ncd" {
		t.Errorf("vertical = %q", got)
	}
}

func TestMark(t *testing.T) {
	var g []Glyph
	g = append(g, paragraph(72, 30, 10, 12, "Running head", "first line of body", "second line of body")...)
	g = append(g, run(72, 760, 10, "Page 7")...)
	art := run(400, 30, 10, "Confidential")
	for i := range art {
		art[i].Artifact = true
	}
	g = append(g, art...)
	blocks := Analyze(g)
	// The artifact is furniture already, and kept apart from the head.
	foundArtifact := false
	for _, b := range blocks {
		if b.Furniture {
			foundArtifact = foundArtifact || b.Text() == "Confidential"
		}
	}
	if !foundArtifact {
		t.Errorf("artifact not marked: %q", texts(blocks))
	}

	asked := 0
	marked := Mark(blocks, func(text string, box Box) bool {
		asked++
		return text == "Running head" || strings.HasPrefix(text, "Page ")
	})
	var furniture, body []string
	for _, b := range marked {
		if b.Furniture {
			furniture = append(furniture, b.Text())
		} else {
			body = append(body, b.Text())
		}
	}
	if !equal(body, []string{"first line of body\nsecond line of body"}) {
		t.Errorf("body = %q", body)
	}
	if len(furniture) != 3 {
		t.Errorf("furniture = %q", furniture)
	}
	// The original blocks are untouched, and furniture is not asked about twice.
	for _, b := range blocks {
		if b.Furniture && b.Text() != "Confidential" {
			t.Errorf("Mark changed its input: %q", b.Text())
		}
	}
	if asked == 0 {
		t.Error("the test function was never called")
	}
}

func FuzzAnalyze(f *testing.F) {
	f.Add([]byte("hello world\nsecond line"), uint8(10), int16(72), int16(100), uint8(0))
	f.Add([]byte("שלום 12 ab,"), uint8(12), int16(300), int16(50), uint8(3))
	f.Add([]byte("a\x00\xff b-\nc"), uint8(0), int16(-5), int16(0), uint8(7))
	f.Fuzz(func(t *testing.T, data []byte, size uint8, x0, y0 int16, mode uint8) {
		var glyphs []Glyph
		x, y := float64(x0), float64(y0)
		dirs := [][2]float64{{1, 0}, {0, 1}, {-1, 0}, {0, -1}, {0.6, 0.8}, {0, 0}}
		for i, r := range string(data) {
			if i > 2000 {
				break
			}
			d := dirs[(int(mode)+i/40)%len(dirs)]
			g := Glyph{Text: string(r), X: x, Y: y, UX: d[0], UY: d[1], Advance: float64(size) / 2, Size: float64(size)}
			switch {
			case r == '\n':
				y += float64(size) * 1.2
				x = float64(x0)
				continue
			case mode&1 == 1 && i%7 == 0:
				g.Text = ""
			case mode&2 == 2 && i%5 == 0:
				x -= float64(size) // draw backwards now and then
			}
			glyphs = append(glyphs, g)
			x += g.Advance
		}
		blocks := Analyze(glyphs)
		blocks = Mark(blocks, func(text string, box Box) bool { return len(text)%2 == 0 })
		Text(blocks)
		for _, b := range blocks {
			for _, l := range b.Lines {
				for _, w := range l.Words {
					if math.IsNaN(w.Box.X0+w.Box.Y0+w.Box.X1+w.Box.Y1) || w.Text == "" {
						t.Fatalf("bad word %+v", w)
					}
				}
			}
		}
	})
}
