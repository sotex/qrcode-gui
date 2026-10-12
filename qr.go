package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strconv"
	"strings"

	"github.com/skip2/go-qrcode"
)

// Params 保存一次二维码生成所需的全部参数。
// 同时用于历史记录的回放，因此所有字段都必须是可 JSON 序列化的基本类型。
type Params struct {
	Size         int    // 输出边长（像素），内部会向下取整到模块整数倍
	ECLevel      string // 容错级别：L / M / Q / H
	Border       int    // 静区（quiet zone）宽度，单位：模块
	ColorMode    bool   // 是否启用彩色（预留彩色二维码能力，默认 false = 黑白）
	FgColor      string // 前景色（十六进制，如 #000000）
	BgColor      string // 背景色（十六进制，如 #FFFFFF）
	AutoPage     bool   // true=自动分页（超出单码容量自动拆分）；false=按固定字符数手动分页
	CharsPerPage int    // 手动分页时每页字符数
	PageMarker   bool   // 是否在每页内容前添加 [p/N] 页码标记
}

func defaultParams() Params {
	return Params{
		Size: 320, ECLevel: "M", Border: 4,
		ColorMode: false, FgColor: "#000000", BgColor: "#FFFFFF",
		AutoPage: true, CharsPerPage: 300, PageMarker: true,
	}
}

// ecLevel 把字符容错级别映射为 go-qrcode 的 RecoveryLevel。
func ecLevel(s string) qrcode.RecoveryLevel {
	switch s {
	case "L":
		return qrcode.Low // 7%
	case "M":
		return qrcode.Medium // 15%
	case "Q":
		return qrcode.High // 25%
	case "H":
		return qrcode.Highest // 30%
	default:
		return qrcode.Medium
	}
}

// parseHex 解析 CSS 风格的十六进制颜色字符串（支持 #rgb / #rrggbb / #rrggbbaa，带不带 # 均可）。
// 解析失败时回退为黑色，保证渲染不会panic。
func parseHex(s string) color.NRGBA {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	switch len(s) {
	case 3:
		return color.NRGBA{R: h2b(s[0:1] + s[0:1]), G: h2b(s[1:2] + s[1:2]), B: h2b(s[2:3] + s[2:3]), A: 255}
	case 6:
		return color.NRGBA{R: h2b(s[0:2]), G: h2b(s[2:4]), B: h2b(s[4:6]), A: 255}
	case 8:
		return color.NRGBA{R: h2b(s[0:2]), G: h2b(s[2:4]), B: h2b(s[4:6]), A: h2b(s[6:8])}
	default:
		return color.NRGBA{R: 0, G: 0, B: 0, A: 255}
	}
}

func h2b(h string) uint8 {
	v, err := strconv.ParseUint(h, 16, 8)
	if err != nil {
		return 0
	}
	return uint8(v)
}

// renderPage 把一段文本渲染为二维码位图（*image.NRGBA）。
// 采用手动位图绘制，而非 go-qrcode 的高层 Image()，以便完全掌控尺寸、边距与颜色。
// 文本为空时返回 (nil, nil)；内容超过二维码最大容量时返回错误。
func renderPage(text string, p Params) (*image.NRGBA, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	qr, err := qrcode.New(text, ecLevel(p.ECLevel))
	if err != nil {
		return nil, err
	}
	bits := qr.Bitmap()
	n := len(bits)
	if n == 0 {
		return nil, fmt.Errorf("二维码数据为空")
	}

	fg := color.NRGBA{R: 0, G: 0, B: 0, A: 255}
	bg := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	if p.ColorMode {
		fg = parseHex(p.FgColor)
		bg = parseHex(p.BgColor)
	}

	total := n + 2*p.Border
	cell := 1
	if p.Size > 0 {
		cell = p.Size / total
		if cell < 1 {
			cell = 1
		}
	}
	side := total * cell
	img := image.NewNRGBA(image.Rect(0, 0, side, side))

	// 先铺背景色
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.Set(x, y, bg)
		}
	}
	// 再绘制前景色模块
	for i := 0; i < n; i++ {
		row := bits[i]
		for j := 0; j < n; j++ {
			if row[j] {
				x0 := (p.Border + j) * cell
				y0 := (p.Border + i) * cell
				for dy := 0; dy < cell; dy++ {
					base := (y0 + dy) * side
					for dx := 0; dx < cell; dx++ {
						img.Pix[base*4+(x0+dx)*4+0] = fg.R
						img.Pix[base*4+(x0+dx)*4+1] = fg.G
						img.Pix[base*4+(x0+dx)*4+2] = fg.B
						img.Pix[base*4+(x0+dx)*4+3] = fg.A
					}
				}
			}
		}
	}
	return img, nil
}

// savePNG 把位图写到指定路径（PNG）。
func savePNG(path string, img *image.NRGBA) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// markerBudget 是页码标记的最大可能长度："[9999/9999] " 共 12 个字符。
// 自动/手动分页在容量预算中预留这一段，保证补上真实页码后仍能放入同一张二维码。
const markerBudget = 12

// splitPages 根据参数把长文本拆分为多页（每页内容都能独立编码为一张二维码）。
// 返回 nil 表示文本为空。
func splitPages(text string, p Params) ([]string, error) {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil, nil
	}
	var raw []string
	if p.AutoPage {
		raw = greedyRaw(runes, p, p.PageMarker)
	} else {
		raw = manualSplit(runes, p)
	}
	return finalizeMarkers(raw, p), nil
}

// fits 判断一段文本能否按当前容错级别编码为单张二维码。
func fits(s string, p Params) bool {
	_, err := qrcode.New(s, ecLevel(p.ECLevel))
	return err == nil
}

// greedyRaw 贪心地把 runes 拆分为尽可能大的若干页（每页都能编码成功）。
// reserveMarker=true 时在容量预算中预留 markerBudget 长度，给后续添加页码留余量。
func greedyRaw(runes []rune, p Params, reserveMarker bool) []string {
	budget := 0
	if reserveMarker {
		budget = markerBudget
	}
	var pages []string
	i := 0
	for i < len(runes) {
		lo, hi := i, len(runes)
		for lo < hi {
			mid := (lo + hi + 1) / 2
			cand := string(runes[i:mid])
			test := cand
			if budget > 0 {
				test = strings.Repeat(" ", budget) + cand
			}
			if fits(test, p) {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		if lo == i {
			// 单个字符都无法编码（理论不可能），强制包含以免死循环
			lo = i + 1
		}
		pages = append(pages, string(runes[i:lo]))
		i = lo
	}
	return pages
}

// manualSplit 按固定字符数切分；若某段仍超出容量，则对该段再用贪心细分，保证每页都能编码。
func manualSplit(runes []rune, p Params) []string {
	chunk := p.CharsPerPage
	if chunk < 1 {
		chunk = 1
	}
	var raw []string
	for i := 0; i < len(runes); i += chunk {
		end := i + chunk
		if end > len(runes) {
			end = len(runes)
		}
		seg := string(runes[i:end])
		if p.PageMarker {
			if fits(strings.Repeat(" ", markerBudget)+seg, p) {
				raw = append(raw, seg)
			} else {
				raw = append(raw, greedyRaw([]rune(seg), p, true)...)
			}
		} else {
			if fits(seg, p) {
				raw = append(raw, seg)
			} else {
				raw = append(raw, greedyRaw([]rune(seg), p, false)...)
			}
		}
	}
	return raw
}

// finalizeMarkers 在需要时为每页文本加上 [p/N] 前缀。
func finalizeMarkers(pages []string, p Params) []string {
	if !p.PageMarker {
		return pages
	}
	n := len(pages)
	out := make([]string, n)
	for i, pg := range pages {
		out[i] = fmt.Sprintf("[%d/%d] %s", i+1, n, pg)
	}
	return out
}
