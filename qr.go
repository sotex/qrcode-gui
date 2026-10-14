package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strconv"
	"strings"

	"github.com/skip2/go-qrcode"
	"github.com/srlehn/jabcode/jabenc"
)

// Params 保存一次二维码生成所需的全部参数。
// 同时用于历史记录的回放，因此所有字段都必须是可 JSON 序列化的基本类型。
type Params struct {
	ECLevel      string // 容错级别：L / M / Q / H
	Border       int    // 静区（quiet zone）宽度，单位：模块
	ColorMode    bool   // 是否启用彩色二维码（JAB Code，ISO/IEC 23634 彩色矩阵码）；false = 黑白
	ColorCount   int    // JAB Code 颜色数：4 或 8（仅 ColorMode 生效，默认 8）
	FgColor      string // 前景色（十六进制，如 #000000）——仅黑白自定义用，彩色模式无效
	BgColor      string // 背景色（十六进制，如 #FFFFFF）——仅黑白自定义用，彩色模式无效
	AutoPage     bool   // true=自动分页（超出单码容量自动拆分）；false=按固定字符数手动分页
	CharsPerPage int    // 手动分页时每页字符数
	PageMarker   bool   // 是否在每页内容前添加 [p/N] 页码标记
}

func defaultParams() Params {
	return Params{
		ECLevel: "M", Border: 4,
		ColorMode: false, ColorCount: 8, FgColor: "#000000", BgColor: "#FFFFFF",
		AutoPage: true, CharsPerPage: 300, PageMarker: false,
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
// ColorMode=true 时走 JAB Code 彩色码路径（见 renderJABPage）。
func renderPage(text string, p Params) (*image.NRGBA, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	if p.ColorMode {
		return renderJABPage(text, p)
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
	// 自适应渲染分辨率：目标边长约 1200px，保证在左侧显示区缩放下足够清晰；
	// 单模块像素下限 2、上限 40，避免过小模糊或过大浪费内存。
	cell := 4
	if total > 0 {
		c := 1200 / total
		if c < 2 {
			c = 2
		}
		if c > 40 {
			c = 40
		}
		cell = c
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

// jabModuleSize 是 JAB Code 每个模块渲染的像素边长。JAB 编码会自动选择符号版本以容纳数据，
// 这里固定模块像素大小即可获得清晰的彩色二维码（ImageViewModeZoom 会按需缩放显示）。
const jabModuleSize = 8

// jabColorBudget 是彩色模式下单页可容纳的 rune 预算（保守值）。
// JAB Code（8 色）单符号容量远高于黑白二维码；该值仅用于自动分页的拆页粒度控制。
// 若单页仍超出 JAB 最大版本，渲染时 Encode 会返回错误并在状态栏提示“渲染失败”。
const jabColorBudget = 1000

// renderJABPage 用 github.com/srlehn/jabcode 把文本渲染为 JAB Code（彩色矩阵码）位图。
// JAB Code（ISO/IEC 23634）按颜色数自动选择符号版本容纳数据，自带静区（quiet zone）。
func renderJABPage(text string, p Params) (*image.NRGBA, error) {
	n := p.ColorCount
	if n != 4 && n != 8 {
		n = 8 // 仅 4/8 为 ISO 标准色数，其它值回退到 8
	}
	enc := jabenc.New(
		jabenc.WithColors(n),
		jabenc.WithModuleSize(jabModuleSize),
	)
	img, err := enc.Encode([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("JAB Code 编码失败: %w", err)
	}
	return toNRGBA(img), nil
}

// toNRGBA 把任意 image.Image 拷贝为 *image.NRGBA（jabenc.Encode 返回的具体类型不确定，统一转换以便显示与保存）。
func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	return out
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

// Page 是一页二维码对应的原始文本片段（finalizeMarkers 后含 [p/N] 前缀）。
type Page struct {
	Text     string // 完整页文本（含 [p/N] 前缀，若启用页码）
	Start    int    // 本页内容在原始文本中的起始 rune 下标（0 基，含）
	End      int    // 本页内容在原始文本中的结束 rune 下标（0 基，不含）
	Overflow bool   // true=本页内容超出单码容量，无法生成（仅"不分页"模式出现）
}

// splitPages 根据参数把长文本拆分为多页（每页内容都能独立编码为一张二维码）。
// 返回 nil 表示文本为空。
func splitPages(text string, p Params) ([]Page, error) {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil, nil
	}
	var raw []Page
	if p.AutoPage {
		raw = greedyRaw(runes, p, p.PageMarker)
	} else {
		raw = manualSplit(runes, p)
	}
	return finalizeMarkers(raw, p), nil
}

// fits 判断一段文本能否编码为单张二维码（按当前模式选择容量判定）。
// 黑白模式：用 go-qrcode 的容错级别判定；彩色（JAB Code）模式：按 jabColorBudget 保守预算判定
// （JAB 编码本身会自动选版本容纳数据，预算仅用于自动分页粒度，真正溢出在渲染时由 Encode 报错）。
func fits(s string, p Params) bool {
	if p.ColorMode {
		return len([]rune(s)) <= jabColorBudget
	}
	_, err := qrcode.New(s, ecLevel(p.ECLevel))
	return err == nil
}

// greedyRaw 贪心地把 runes 拆分为尽可能大的若干页（每页都能编码成功）。
// reserveMarker=true 时在容量预算中预留 markerBudget 长度，给后续添加页码留余量。
func greedyRaw(runes []rune, p Params, reserveMarker bool) []Page {
	budget := 0
	if reserveMarker {
		budget = markerBudget
	}
	var pages []Page
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
		pages = append(pages, Page{Start: i, End: lo, Text: string(runes[i:lo])})
		i = lo
	}
	return pages
}

// manualSplit 按固定字符数切分为若干页；"不分页"模式下若某页超出单码容量，
// 仅标记 Overflow（不重新拆分），符合"文本超长且不分页则不显示"的要求。
func manualSplit(runes []rune, p Params) []Page {
	chunk := p.CharsPerPage
	if chunk < 1 {
		chunk = 1
	}
	var pages []Page
	for i := 0; i < len(runes); i += chunk {
		end := i + chunk
		if end > len(runes) {
			end = len(runes)
		}
		seg := string(runes[i:end])
		pg := Page{Start: i, End: end, Text: seg}
		test := seg
		if p.PageMarker {
			test = strings.Repeat(" ", markerBudget) + seg
		}
		if !fits(test, p) {
			pg.Overflow = true
		}
		pages = append(pages, pg)
	}
	return pages
}

// finalizeMarkers 在需要时为每页文本加上 [p/N] 前缀（保留原始文本区间 Start/End）。
func finalizeMarkers(pages []Page, p Params) []Page {
	if !p.PageMarker {
		return pages
	}
	n := len(pages)
	out := make([]Page, n)
	for i, pg := range pages {
		out[i] = pg
		out[i].Text = fmt.Sprintf("[%d/%d] %s", i+1, n, pg.Text)
	}
	return out
}
