package main

import (
	"fmt"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/CzaxStudio/proton"
)

// UI 持有全部界面状态与生成结果。proton 是“即时模式”框架：
// draw() 每帧被调用，控件状态存放在这里（proton 官方推荐做法）。
type UI struct {
	// —— 文本输入（右下）——
	text proton.Editor

	// —— 生成参数（右上）——
	sizeNum    proton.NumberState   // 尺寸(px)
	ecSel      proton.SelectBoxState // 容错级别 L/M/Q/H
	borderNum  proton.NumberState   // 静区(模块)
	colorOn    proton.Bool          // 彩色开关（预留彩色二维码能力）
	fgIn       proton.Editor        // 前景色 hex
	bgIn       proton.Editor        // 背景色 hex
	autoPage   proton.Bool          // 自动分页开关
	charsNum   proton.NumberState   // 每页字符数（手动分页）
	marker     proton.Bool          // 添加页码标记

	// —— 动作按钮 ——
	genBtn      proton.Clickable
	saveBtn     proton.Clickable
	histBtn     proton.Clickable
	saveImgBtn  proton.Clickable
	prevBtn     proton.Clickable
	nextBtn     proton.Clickable
	closeHistBtn proton.Clickable
	clearHistBtn proton.Clickable

	// —— 布局状态 ——
	split      proton.ResizeSplitState // 左右分栏
	paramScroll proton.Scrollable     // 参数滚动区
	histScroll  proton.Scrollable     // 历史列表滚动区
	histModal   proton.OverlayState   // 历史弹窗
	histLoad    []proton.Clickable    // 每条历史的“加载”按钮
	histDel     []proton.Clickable    // 每条历史的“删除”按钮

	// —— 生成结果 ——
	mu         sync.Mutex
	pages      []string      // 分页后的各页内容
	pageCount  int           // 页数
	page       int           // 当前页索引
	hasQR      bool          // 是否已有二维码
	qrOp       proton.ImageOp // 当前页的显示对象（由临时 PNG 加载）
	qrFile     string        // 当前页二维码的临时 PNG 路径
	renderSize int           // 当前页渲染像素边长
	lastKey    string        // 上次生成的输入指纹，用于脏检测
	status     string        // 状态栏文字

	// —— 历史记录 ——
	history []HistoryEntry

	// —— 托盘协同 ——
	ctxMu       sync.Mutex
	lastCtx     proton.Context
	showHistory atomic.Bool
	requestSave atomic.Bool
}

// NewUI 构造并初始化 UI 的默认状态，同时创建临时目录用于存放当前页二维码 PNG。
func NewUI() *UI {
	u := &UI{}
	dir, err := os.MkdirTemp("", "qrcode-gui")
	if err == nil {
		u.qrFile = filepath.Join(dir, "qr.png")
	} else {
		u.qrFile = "qr_current.png"
	}
	// 默认参数
	u.sizeNum.Value = 320
	u.borderNum.Value = 4
	u.charsNum.Value = 300
	u.ecSel.Selected = 1 // 默认容错级别 M
	u.autoPage.Value = true
	u.marker.Value = true
	u.colorOn.Value = false
	u.fgIn.SetText("#000000")
	u.bgIn.SetText("#FFFFFF")
	u.renderSize = 320
	u.status = "输入文本以生成二维码"
	return u
}

// setStatus 线程安全地更新状态栏文字。
func (u *UI) setStatus(s string) {
	u.mu.Lock()
	u.status = s
	u.mu.Unlock()
}

// readParams 从界面控件读取当前生成参数。
func (u *UI) readParams() Params {
	ec := "M"
	opts := []string{"L", "M", "Q", "H"}
	if u.ecSel.Selected >= 0 && u.ecSel.Selected < len(opts) {
		ec = opts[u.ecSel.Selected]
	}
	size := int(u.sizeNum.Value)
	if size < 16 {
		size = 16
	}
	border := int(u.borderNum.Value)
	if border < 0 {
		border = 0
	}
	chars := int(u.charsNum.Value)
	if chars < 1 {
		chars = 1
	}
	fg := strings.TrimSpace(u.fgIn.Text())
	if fg == "" {
		fg = "#000000"
	}
	bg := strings.TrimSpace(u.bgIn.Text())
	if bg == "" {
		bg = "#FFFFFF"
	}
	return Params{
		Size: size, ECLevel: ec, Border: border,
		ColorMode: u.colorOn.Value, FgColor: fg, BgColor: bg,
		AutoPage: u.autoPage.Value, CharsPerPage: chars, PageMarker: u.marker.Value,
	}
}

// generate 根据文本与参数生成二维码。save=true 时会写入历史记录。
func (u *UI) generate(text string, p Params, save bool) {
	if strings.TrimSpace(text) == "" {
		u.mu.Lock()
		u.hasQR = false
		u.pages = nil
		u.pageCount = 0
		u.page = 0
		u.status = "请输入文本以生成二维码"
		u.mu.Unlock()
		return
	}
	pages, err := splitPages(text, p)
	if err != nil {
		u.setStatus("分页失败: " + err.Error())
		return
	}
	if len(pages) == 0 {
		u.setStatus("文本为空")
		return
	}
	if err := u.renderAndShow(0, pages, p); err != nil {
		u.setStatus("生成失败: " + err.Error())
		return
	}
	u.mu.Lock()
	u.pages = pages
	u.pageCount = len(pages)
	u.page = 0
	u.hasQR = true
	u.mu.Unlock()

	if save {
		u.addHistory(text, p, len(pages))
		u.setStatus(fmt.Sprintf("已生成并保存到历史（共 %d 页）", len(pages)))
	} else if len(pages) > 1 {
		u.setStatus(fmt.Sprintf("已生成，自动分页共 %d 页", len(pages)))
	} else {
		u.setStatus("已生成二维码")
	}
}

// renderAndShow 渲染指定页并加载为显示对象。
func (u *UI) renderAndShow(idx int, pages []string, p Params) error {
	img, err := renderPage(pages[idx], p)
	if err != nil {
		return err
	}
	f, err := os.Create(u.qrFile)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	op, err := proton.LoadImage(u.qrFile)
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.qrOp = op
	u.renderSize = img.Bounds().Dx()
	u.mu.Unlock()
	return nil
}

// nav 在分页间切换。
func (u *UI) nav(delta int) {
	u.mu.Lock()
	if u.pageCount == 0 {
		u.mu.Unlock()
		return
	}
	idx := u.page + delta
	if idx < 0 {
		idx = 0
	}
	if idx > u.pageCount-1 {
		idx = u.pageCount - 1
	}
	if idx == u.page {
		u.mu.Unlock()
		return
	}
	u.page = idx
	pages := u.pages
	u.mu.Unlock()

	if idx >= 0 && idx < len(pages) {
		p := u.readParams()
		if err := u.renderAndShow(idx, pages, p); err != nil {
			u.setStatus("渲染失败: " + err.Error())
		}
	}
}

// saveCurrent 保存当前已生成结果到历史（不重新生成）。
func (u *UI) saveCurrent() {
	u.mu.Lock()
	has := u.hasQR
	text := u.text.Text()
	pages := u.pageCount
	u.mu.Unlock()
	if !has || strings.TrimSpace(text) == "" {
		u.setStatus("暂无可保存的内容")
		return
	}
	p := u.readParams()
	u.addHistory(text, p, pages)
	u.setStatus("已保存到历史记录")
}

// saveCurrentImage 把当前页二维码 PNG 复制到 ./qr_output/ 目录。
func (u *UI) saveCurrentImage() {
	u.mu.Lock()
	has := u.hasQR
	qrFile := u.qrFile
	pg := u.page
	u.mu.Unlock()
	if !has {
		u.setStatus("暂无可保存的图片")
		return
	}
	if err := os.MkdirAll("qr_output", 0o755); err != nil {
		u.setStatus("创建目录失败: " + err.Error())
		return
	}
	out := filepath.Join("qr_output", fmt.Sprintf("qrcode_page_%d.png", pg+1))
	data, err := os.ReadFile(qrFile)
	if err != nil {
		u.setStatus("读取失败: " + err.Error())
		return
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		u.setStatus("保存失败: " + err.Error())
		return
	}
	u.setStatus("已保存: " + out)
}

// invalidate 通过缓存的 Context 请求一次重绘（供托盘线程调用）。
func (u *UI) invalidate() {
	u.ctxMu.Lock()
	c := u.lastCtx
	u.ctxMu.Unlock()
	if c != nil {
		c.Invalidate()
	}
}

// draw 是 proton 每帧调用的绘制函数，也是整个界面的入口。
func (u *UI) draw(win proton.Context) {
	// 记录 Context 以便托盘线程触发重绘
	u.ctxMu.Lock()
	u.lastCtx = win
	u.ctxMu.Unlock()

	// 处理来自托盘线程的请求（在绘制线程内执行，避免跨线程竞争）
	if u.requestSave.Load() {
		u.requestSave.Store(false)
		u.saveCurrent()
	}
	if u.showHistory.Load() {
		u.showHistory.Store(false)
		u.histModal.Show()
	}

	// 读取当前输入，做脏检测决定是否重新生成
	text := u.text.Text()
	p := u.readParams()
	key := fmt.Sprintf("%s|%d|%s|%d|%v|%s|%s|%v|%d|%v",
		text, p.Size, p.ECLevel, p.Border, p.ColorMode, p.FgColor, p.BgColor, p.AutoPage, p.CharsPerPage, p.PageMarker)
	if key != u.lastKey {
		u.lastKey = key
		u.generate(text, p, false)
	}

	// 主布局：左侧二维码，右侧参数+输入
	proton.ResizeSplit(win, &u.split, 0.45, u.leftPane, u.rightPane)

	// 历史管理弹窗（仅当 histModal.Visible 时绘制）
	proton.Overlay(win, &u.histModal, u.historyContent)
}

// leftPane 左栏：居中的二维码 + 翻页/状态/保存。
func (u *UI) leftPane(win proton.Context) {
	proton.GrowColumn(win,
		proton.GrowItem(win, func(win proton.Context) {
			proton.Center(win, func(win proton.Context) {
				u.mu.Lock()
				has := u.hasQR
				op := u.qrOp
				rs := u.renderSize
				u.mu.Unlock()
				if has {
					disp := float32(rs)
					if disp > 360 {
						disp = 360
					}
					if disp <= 0 {
						disp = 320
					}
					proton.Image(win, op, disp, disp)
				} else {
					proton.Muted(win, "在右侧输入文本以生成二维码")
				}
			})
		}),
		proton.FixedItem(win, func(win proton.Context) {
			u.mu.Lock()
			pg := u.page
			pc := u.pageCount
			st := u.status
			u.mu.Unlock()
			proton.Gap(win, 8)
			proton.Row(win,
				func(win proton.Context) {
					if proton.Button(win, &u.prevBtn, "‹ 上一页") {
						u.nav(-1)
					}
				},
				func(win proton.Context) {
					proton.Caption(win, fmt.Sprintf("第 %d / %d 页", pg+1, maxInt(pc, 1)))
				},
				func(win proton.Context) {
					if proton.Button(win, &u.nextBtn, "下一页 ›") {
						u.nav(1)
					}
				},
			)
			proton.Gap(win, 4)
			proton.Caption(win, st)
			proton.Gap(win, 6)
			proton.Row(win, func(win proton.Context) {
				if proton.Button(win, &u.saveImgBtn, "保存当前页图片") {
					u.saveCurrentImage()
				}
			})
		}),
	)
}

// rightPane 右栏：上=参数(可滚动)，下=文本输入(固定)。
func (u *UI) rightPane(win proton.Context) {
	proton.GrowColumn(win,
		proton.GrowItem(win, func(win proton.Context) {
			proton.Scroll(win, &u.paramScroll, u.paramsContent)
		}),
		proton.FixedItem(win, u.inputContent),
	)
}

// paramsContent 右上：生成参数面板。
func (u *UI) paramsContent(win proton.Context) {
	proton.Pad(win, 12, func(win proton.Context) {
		proton.H3(win, "生成参数")
		proton.Gap(win, 8)

		proton.LabeledDivider(win, "基本设置")
		proton.Gap(win, 6)
		u.rowLabelControl(win, "尺寸 (px)",
			proton.FixedItem(win, func(win proton.Context) {
				proton.NumberInput(win, &u.sizeNum, 64, 1024, 16)
			}))
		u.rowLabelControl(win, "容错级别",
			proton.GrowItem(win, func(win proton.Context) {
				proton.SelectBox(win, &u.ecSel, []string{"L", "M", "Q", "H"})
			}))
		u.rowLabelControl(win, "静区/边距 (模块)",
			proton.FixedItem(win, func(win proton.Context) {
				proton.NumberInput(win, &u.borderNum, 0, 16, 1)
			}))

		proton.Gap(win, 8)
		proton.LabeledDivider(win, "彩色二维码（预留）")
		proton.Gap(win, 6)
		proton.Toggle(win, &u.colorOn, "启用彩色二维码")
		proton.If(win, u.colorOn.Value, func(win proton.Context) {
			u.rowLabelControl(win, "前景色",
				proton.GrowItem(win, func(win proton.Context) {
					proton.Input(win, &u.fgIn, "#000000")
				}))
			u.rowLabelControl(win, "背景色",
				proton.GrowItem(win, func(win proton.Context) {
					proton.Input(win, &u.bgIn, "#FFFFFF")
				}))
		})

		proton.Gap(win, 8)
		proton.LabeledDivider(win, "分页设置")
		proton.Gap(win, 6)
		proton.Toggle(win, &u.autoPage, "自动分页（超出单码容量自动拆分多页）")
		proton.If(win, !u.autoPage.Value, func(win proton.Context) {
			u.rowLabelControl(win, "每页字符数",
				proton.FixedItem(win, func(win proton.Context) {
					proton.NumberInput(win, &u.charsNum, 50, 4000, 50)
				}))
		})
		proton.Checkbox(win, &u.marker, "添加页码标记 [p/N]")

		proton.Gap(win, 10)
		proton.Row(win,
			func(win proton.Context) {
				if proton.Button(win, &u.genBtn, "生成并保存历史") {
					p := u.readParams()
					u.generate(u.text.Text(), p, true)
				}
			},
			func(win proton.Context) {
				if proton.Button(win, &u.saveBtn, "仅保存历史") {
					u.saveCurrent()
				}
			},
		)
		proton.Gap(win, 8)
		if proton.Button(win, &u.histBtn, "管理历史记录") {
			u.histModal.Show()
		}
	})
}

// rowLabelControl 一行：左侧标签 + 右侧控件（控件用 GrowRow 的 FlexItem 控制伸缩）。
func (u *UI) rowLabelControl(win proton.Context, label string, control proton.FlexItem) {
	proton.GrowRow(win,
		proton.FixedItem(win, func(win proton.Context) {
			proton.Label(win, label)
		}),
		control,
	)
	proton.Gap(win, 8)
}

// inputContent 右下：文本输入 + 字数统计。
func (u *UI) inputContent(win proton.Context) {
	proton.Pad(win, 12, func(win proton.Context) {
		proton.Label(win, "输入文本")
		proton.Gap(win, 4)
		proton.TextArea(win, &u.text, "在此输入要生成二维码的文本…")
		proton.Gap(win, 4)
		proton.Caption(win, fmt.Sprintf("字符数: %d", len([]rune(u.text.Text()))))
	})
}

// historyContent 历史管理弹窗内容。
func (u *UI) historyContent(win proton.Context) {
	cardBg := color.NRGBA{R: 0x1e, G: 0x1e, B: 0x2e, A: 255}
	proton.MaxWidth(win, 620, func(win proton.Context) {
		proton.Card(win, cardBg, 12, 20, func(win proton.Context) {
			proton.H3(win, "历史记录")
			proton.Gap(win, 6)
			u.mu.Lock()
			hist := u.history
			u.mu.Unlock()
			if len(hist) == 0 {
				proton.Muted(win, "暂无历史记录")
			} else {
				u.ensureHistBtns(len(hist))
				proton.List(win, &u.histScroll, len(hist), func(win proton.Context, i int) {
					e := hist[i]
					proton.GrowRow(win,
						proton.GrowItem(win, func(win proton.Context) {
							proton.Column(win,
								func(win proton.Context) { proton.Caption(win, e.TimeStr()) },
								func(win proton.Context) { proton.Label(win, previewOr(e.Preview)) },
								func(win proton.Context) {
									proton.Caption(win, fmt.Sprintf("参数: %s / %dpx / 边距%d / %d页", e.Params.ECLevel, e.Params.Size, e.Params.Border, e.Pages))
								},
							)
						}),
						proton.FixedItem(win, func(win proton.Context) {
							if proton.Button(win, &u.histLoad[i], "加载") {
								u.loadHistory(i)
							}
						}),
						proton.FixedItem(win, func(win proton.Context) {
							if proton.Button(win, &u.histDel[i], "删除") {
								u.deleteHistory(i)
							}
						}),
					)
					proton.Divider(win)
				})
			}
			proton.Gap(win, 10)
			proton.Row(win,
				func(win proton.Context) {
					if proton.Button(win, &u.clearHistBtn, "清空历史") {
						u.clearHistory()
					}
				},
				func(win proton.Context) {
					if proton.Button(win, &u.closeHistBtn, "关闭") {
						u.histModal.Hide()
					}
				},
			)
		})
	})
}

func previewOr(s string) string {
	if s == "" {
		return "(空)"
	}
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
