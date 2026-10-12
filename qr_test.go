package main

import (
	"strings"
	"testing"
)

func TestParseHex(t *testing.T) {
	c := parseHex("#000000")
	if c.R != 0 || c.G != 0 || c.B != 0 || c.A != 255 {
		t.Fatalf("want black, got %+v", c)
	}
	c = parseHex("#FFFFFF")
	if c.R != 255 || c.G != 255 || c.B != 255 {
		t.Fatalf("want white, got %+v", c)
	}
	c = parseHex("FF0000")
	if c.R != 255 || c.G != 0 || c.B != 0 {
		t.Fatalf("want red, got %+v", c)
	}
	c = parseHex("bad-input")
	if c.R != 0 || c.G != 0 || c.B != 0 {
		t.Fatalf("want fallback black, got %+v", c)
	}
}

func TestRenderPage(t *testing.T) {
	img, err := renderPage("hello", defaultParams())
	if err != nil {
		t.Fatal(err)
	}
	if img == nil {
		t.Fatal("nil image")
	}
	if img.Bounds().Dx() <= 0 {
		t.Fatal("invalid size")
	}
}

func TestRenderPageColor(t *testing.T) {
	p := defaultParams()
	p.ColorMode = true
	p.FgColor = "#FF0000"
	p.BgColor = "#00FF00"
	img, err := renderPage("color", p)
	if err != nil {
		t.Fatal(err)
	}
	if img == nil {
		t.Fatal("nil image")
	}
}

func TestSplitShort(t *testing.T) {
	pages, err := splitPages("hello", defaultParams())
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
}

func TestSplitLongAuto(t *testing.T) {
	long := strings.Repeat("A", 8000)
	p := defaultParams()
	p.AutoPage = true
	p.PageMarker = true
	pages, err := splitPages(long, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatalf("expected multiple pages, got %d", len(pages))
	}
	for i, pg := range pages {
		if !strings.HasPrefix(pg.Text, "[") {
			t.Fatalf("page %d missing marker prefix: %q", i, pg.Text[:min(20, len(pg.Text))])
		}
		if _, err := renderPage(pg.Text, p); err != nil {
			t.Fatalf("page %d failed to render (too long?): %v", i, err)
		}
	}
}

func TestSplitLongManual(t *testing.T) {
	long := strings.Repeat("B", 8000)
	p := defaultParams()
	p.AutoPage = false
	p.CharsPerPage = 200
	p.PageMarker = true
	pages, err := splitPages(long, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatalf("expected multiple pages, got %d", len(pages))
	}
	for i, pg := range pages {
		if _, err := renderPage(pg.Text, p); err != nil {
			t.Fatalf("page %d failed to render: %v", i, err)
		}
	}
}

func TestSplitManualOverflow(t *testing.T) {
	// 不分页模式：单页字符数远超单码容量时，应标记为 Overflow 且不重新拆分。
	long := strings.Repeat("C", 5000)
	p := defaultParams()
	p.AutoPage = false
	p.CharsPerPage = 5000
	p.PageMarker = false
	pages, err := splitPages(long, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("manual mode must NOT re-split overflow; expected 1 page, got %d", len(pages))
	}
	if !pages[0].Overflow {
		t.Fatalf("expected page 0 to be marked Overflow")
	}
	if pages[0].Text != long {
		t.Fatalf("overflow page should preserve original segment, got len %d", len(pages[0].Text))
	}
}

func TestSplitChinese(t *testing.T) {
	long := strings.Repeat("二维码", 3000) // 每个汉字 3 字节，远超单码容量
	p := defaultParams()
	p.AutoPage = true
	p.PageMarker = true
	pages, err := splitPages(long, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatalf("expected multiple pages for Chinese text, got %d", len(pages))
	}
	for i, pg := range pages {
		if _, err := renderPage(pg.Text, p); err != nil {
			t.Fatalf("page %d render failed: %v", i, err)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
