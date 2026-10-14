package main

import (
	"testing"
)

// TestRenderJABPage 验证 JAB Code 彩色码渲染可产出有效位图。
func TestRenderJABPage(t *testing.T) {
	p := defaultParams()
	p.ColorMode = true
	p.ColorCount = 8

	img, err := renderJABPage("https://example.com/二维码彩色测试 ABC123", p)
	if err != nil {
		t.Fatalf("renderJABPage error: %v", err)
	}
	if img == nil {
		t.Fatal("renderJABPage returned nil image")
	}
	b := img.Bounds()
	if b.Dx() < 50 || b.Dy() < 50 {
		t.Fatalf("JAB image too small: %dx%d", b.Dx(), b.Dy())
	}
	t.Logf("JAB 8-color image size: %dx%d", b.Dx(), b.Dy())

	// 4 色也应可渲染
	p2 := defaultParams()
	p2.ColorMode = true
	p2.ColorCount = 4
	img4, err := renderJABPage("hello jabcode 你好", p2)
	if err != nil {
		t.Fatalf("renderJABPage(4-color) error: %v", err)
	}
	if img4 == nil || img4.Bounds().Dx() < 50 {
		t.Fatal("renderJABPage(4-color) produced invalid image")
	}
	t.Logf("JAB 4-color image size: %dx%d", img4.Bounds().Dx(), img4.Bounds().Dy())
}
