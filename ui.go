package main

import (
	"fmt"
	"image"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxn/win"
	"github.com/lxn/walk"
)

// must 是 NewXxx 双返回值构造器的便捷包装：创建失败直接 panic（GUI 启动阶段，
// 控件创建失败属于致命错误，无需恢复）。
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// tight 给布局设置紧凑的间距与边距（单位 1/96"），让控件之间更紧凑。
// NewVBoxLayout/NewHBoxLayout 均返回 *walk.BoxLayout（仅 orientation 不同）。
func tight(l walk.Layout) walk.Layout {
	if bl, ok := l.(*walk.BoxLayout); ok {
		_ = bl.SetSpacing(2)
		_ = bl.SetMargins(walk.Margins{HNear: 4, VNear: 2, HFar: 4, VFar: 2})
	}
	return l
}

// UI 持有界面状态与生成结果。walk 是事件驱动（非即时模式）框架，
// 控件状态直接保存在 walk 控件里，这里仅放置生成结果与跨控件共享状态。
type UI struct {
	mw *walk.MainWindow

	// 左栏
	qrView      *walk.ImageView
	curBmp      *walk.Bitmap
	prevBtn     *walk.PushButton
	nextBtn     *walk.PushButton
	saveImgBtn  *walk.PushButton
	pageLabel   *walk.Label
	statusLabel *walk.Label

	// 右栏参数
	ecCombo          *walk.ComboBox
	borderEdit       *walk.NumberEdit
	colorChk         *walk.CheckBox
	colorCountCombo  *walk.ComboBox
	fgEdit           *walk.LineEdit
	bgEdit           *walk.LineEdit
	autoPageChk      *walk.CheckBox
	charsEdit        *walk.NumberEdit
	markerChk        *walk.CheckBox

	// 右栏输入
	textEdit       *walk.TextEdit
	charCountLabel *walk.Label

	// 历史
	history   []HistoryEntry
	histModel *historyModel
	histTV    *walk.TableView

	// 生成状态（mutex 保护跨 goroutine 访问）
	mu           sync.Mutex
	pages        []Page
	pageCount    int
	page         int
	totalChars   int
	hasQR        bool
	curParams    Params
	lastKey      string
	genTimer     *time.Timer
	saveTimer    *time.Timer
	savedText    string
	skipSaveOnce bool
	genSeq       uint64 // 递增的生成序号，防止后台计算乱序覆盖
}

// NewUI 构造 UI 默认状态。
func NewUI() *UI {
	return &UI{
		curParams: defaultParams(),
	}
}

// build 命令式构建整个窗口与所有控件，并绑定事件。
func (u *UI) build() (*walk.MainWindow, error) {
	mw, err := walk.NewMainWindow()
	if err != nil {
		return nil, err
	}
	u.mw = mw
	mw.SetTitle("二维码生成器 · QR Generator")
	if err := mw.SetSize(walk.Size{Width: 1000, Height: 700}); err != nil {
		return nil, err
	}
	if err := mw.SetMinMaxSize(walk.Size{Width: 760, Height: 560}, walk.Size{}); err != nil {
		return nil, err
	}
	mw.SetLayout(tight(walk.NewHBoxLayout()))

	// ===== 主水平分栏：左=二维码，右=参数+输入 =====
	hsplit := must(walk.NewHSplitter(mw))

	// --- 左栏 ---
	left := must(walk.NewComposite(hsplit))
	left.SetLayout(tight(walk.NewVBoxLayout()))

	u.qrView = must(walk.NewImageView(left))
	u.qrView.SetMode(walk.ImageViewModeZoom)
	u.qrView.SetMargin(4)

	navBar := must(walk.NewComposite(left))
	navBar.SetLayout(tight(walk.NewHBoxLayout()))
	navBar.SetMinMaxSize(walk.Size{Width: 0, Height: 34}, walk.Size{Width: 0, Height: 34})
	u.prevBtn = must(walk.NewPushButton(navBar))
	u.prevBtn.SetText("上一页")
	u.pageLabel = must(walk.NewLabel(navBar))
	u.pageLabel.SetText("第 1 / 1 页")
	u.nextBtn = must(walk.NewPushButton(navBar))
	u.nextBtn.SetText("下一页")
	must(walk.NewHSpacer(navBar)) // 弹簧：把"保存图片"推到右侧，充满中间空白
	u.saveImgBtn = must(walk.NewPushButton(navBar))
	u.saveImgBtn.SetText("保存图片")

	u.statusLabel = must(walk.NewLabel(left))
	u.statusLabel.SetMinMaxSize(walk.Size{Width: 0, Height: 20}, walk.Size{Width: 0, Height: 20})
	u.statusLabel.SetText("输入文本以生成二维码")

	// --- 右栏（垂直分栏：参数 / 输入）---
	right := must(walk.NewComposite(hsplit))
	right.SetLayout(tight(walk.NewVBoxLayout()))
	// 右栏固定为最优最小显示宽度：初始化即 300，且不再被拉伸（左栏随窗口伸缩）。
	right.SetMinMaxSize(walk.Size{Width: 300, Height: 0}, walk.Size{Width: 300, Height: 0})

	vsplit := must(walk.NewVSplitter(right))

	// 参数区：参数控件直接挂在 paramPane 上，paramPane 在 VSplitter 中按内容高度贴合
	// （非贪婪），剩余垂直空间全部交给下方输入区，避免参数区底部出现大片空白。
	paramPane := must(walk.NewComposite(vsplit))
	paramPane.SetLayout(tight(walk.NewVBoxLayout()))

	ecRow := must(walk.NewComposite(paramPane))
	ecRow.SetLayout(tight(walk.NewHBoxLayout()))
	ecLbl := must(walk.NewLabel(ecRow))
	ecLbl.SetText("容错级别")
	u.ecCombo = must(walk.NewComboBox(ecRow))
	_ = u.ecCombo.SetModel([]string{"L -- 7%", "M -- 15%", "Q -- 25%", "H -- 30%"})
	_ = u.ecCombo.SetCurrentIndex(1)

	borderRow := must(walk.NewComposite(paramPane))
	borderRow.SetLayout(tight(walk.NewHBoxLayout()))
	bLbl := must(walk.NewLabel(borderRow))
	bLbl.SetText("静区/边距(模块)")
	u.borderEdit = must(walk.NewNumberEdit(borderRow))
	_ = u.borderEdit.SetRange(0, 16)
	_ = u.borderEdit.SetValue(4)
	_ = u.borderEdit.SetSpinButtonsVisible(true)

	u.colorChk = must(walk.NewCheckBox(paramPane))
	u.colorChk.SetText("彩色二维码 (JAB Code)")
	u.colorChk.SetChecked(false)
	u.colorChk.SetAlignment(walk.AlignHNearVNear) // 左对齐，与上方参数行一致

	// 颜色数（4 / 8），仅彩色模式显示（JAB Code 仅 4/8 为 ISO 标准色数）
	colorCountRow := must(walk.NewComposite(paramPane))
	colorCountRow.SetLayout(tight(walk.NewHBoxLayout()))
	ccLbl := must(walk.NewLabel(colorCountRow))
	ccLbl.SetText("颜色数")
	u.colorCountCombo = must(walk.NewComboBox(colorCountRow))
	_ = u.colorCountCombo.SetModel([]string{"4 (4色)", "8 (8色)"})
	_ = u.colorCountCombo.SetCurrentIndex(1) // 默认 8 色
	colorCountRow.SetVisible(false)

	// 前景/背景色：仅黑白自定义时有意义，彩色（JAB）模式无效，始终不显示。
	colorPane := must(walk.NewComposite(paramPane))
	colorPane.SetLayout(tight(walk.NewHBoxLayout()))
	fgLbl := must(walk.NewLabel(colorPane))
	fgLbl.SetText("前景色")
	u.fgEdit = must(walk.NewLineEdit(colorPane))
	u.fgEdit.SetText("#000000")
	bgLbl := must(walk.NewLabel(colorPane))
	bgLbl.SetText("背景色")
	u.bgEdit = must(walk.NewLineEdit(colorPane))
	u.bgEdit.SetText("#FFFFFF")
	colorPane.SetVisible(false)

	u.autoPageChk = must(walk.NewCheckBox(paramPane))
	u.autoPageChk.SetText("自动分页（超出单码容量自动拆分）")
	u.autoPageChk.SetChecked(true)
	u.autoPageChk.SetAlignment(walk.AlignHNearVNear) // 左对齐（修正 walk 默认 AlignHVDefault 居中）
	charsPane := must(walk.NewComposite(paramPane))
	charsPane.SetLayout(tight(walk.NewHBoxLayout()))
	cLbl := must(walk.NewLabel(charsPane))
	cLbl.SetText("每页字符数")
	u.charsEdit = must(walk.NewNumberEdit(charsPane))
	_ = u.charsEdit.SetRange(1, 4000)
	_ = u.charsEdit.SetValue(300)
	_ = u.charsEdit.SetSpinButtonsVisible(true)
	charsPane.SetVisible(false)

	u.markerChk = must(walk.NewCheckBox(paramPane))
	u.markerChk.SetText("添加页码标记 [p/N]")
	u.markerChk.SetChecked(false)
	u.markerChk.SetAlignment(walk.AlignHNearVNear) // 左对齐，与上方参数行一致

	// 输入区
	inputPane := must(walk.NewComposite(vsplit))
	inputPane.SetLayout(tight(walk.NewVBoxLayout()))
	inLbl := must(walk.NewLabel(inputPane))
	inLbl.SetText("输入文本")
	inLbl.SetMinMaxSize(walk.Size{Width: 0, Height: 20}, walk.Size{Width: 0, Height: 20})
	// 关键：walk 的 TextEdit 底层是 Windows 多行 EDIT 控件，NewTextEdit 创建时
	// 默认样式只有 WS_TABSTOP|WS_VISIBLE|ES_MULTILINE|ES_WANTRETURN，**没有**
	// ES_AUTOHSCROLL / ES_AUTOVSCROLL / WS_VSCROLL。因此当文本超出可见客户区时，
	// 控件既不自动滚动、也无滚动条，光标移出可视区后新增/粘贴的内容看不见，
	// 表现为“无法输入、粘贴失败”。这里用 NewTextEditWithStyle 补上滚动样式。
	u.textEdit = must(walk.NewTextEditWithStyle(inputPane,
		win.ES_AUTOHSCROLL|win.ES_AUTOVSCROLL|win.WS_VSCROLL))
	// 同时解除 EDIT 控件默认的 ~30000 字符上限（置 0 = 无限制），避免超长文本被截断。
	u.textEdit.SetMaxLength(0)
	u.textEdit.SetAlignment(walk.AlignHNearVNear) // 左对齐并垂直充满，消除顶部空白
	u.charCountLabel = must(walk.NewLabel(inputPane))
	u.charCountLabel.SetText("字符数: 0")
	u.charCountLabel.SetMinMaxSize(walk.Size{Width: 0, Height: 20}, walk.Size{Width: 0, Height: 20})

	// 右栏底部：说明 + 管理历史按钮（放在右栏最下方）
	bottomBar := must(walk.NewComposite(right))
	bottomBar.SetLayout(tight(walk.NewHBoxLayout()))
	bottomBar.SetMinMaxSize(walk.Size{Width: 0, Height: 30}, walk.Size{Width: 0, Height: 30})
	hint := must(walk.NewLabel(bottomBar))
	hint.SetText("输入后稍作停顿即自动存入历史")
	histBtn := must(walk.NewPushButton(bottomBar))
	histBtn.SetText("管理历史记录")

	// ===== 事件绑定 =====
	u.textEdit.TextChanged().Attach(func() {
		u.updateCharCount()
		u.scheduleGenerate()
	})
	u.ecCombo.CurrentIndexChanged().Attach(u.scheduleGenerate)
	u.borderEdit.ValueChanged().Attach(u.scheduleGenerate)
	u.colorChk.CheckedChanged().Attach(func() {
		on := u.colorChk.Checked()
		colorCountRow.SetVisible(on) // 颜色数选择仅在彩色模式显示
		colorPane.SetVisible(false)  // 前景/背景色对 JAB Code 无效，始终隐藏
		u.scheduleGenerate()
	})
	u.fgEdit.TextChanged().Attach(u.scheduleGenerate)
	u.bgEdit.TextChanged().Attach(u.scheduleGenerate)
	u.autoPageChk.CheckedChanged().Attach(func() {
		charsPane.SetVisible(!u.autoPageChk.Checked())
		u.scheduleGenerate()
	})
	u.charsEdit.ValueChanged().Attach(u.scheduleGenerate)
	u.markerChk.CheckedChanged().Attach(u.scheduleGenerate)

	u.prevBtn.Clicked().Attach(func() { u.nav(-1) })
	u.nextBtn.Clicked().Attach(func() { u.nav(1) })
	u.saveImgBtn.Clicked().Attach(u.saveCurrentImage)
	histBtn.Clicked().Attach(u.showHistoryDialog)

	// 初始状态
	u.updateCharCount()
	u.updateNav()
	u.setStatus("输入文本以生成二维码")
	return mw, nil
}

// updateCharCount 刷新字符计数。
func (u *UI) updateCharCount() {
	u.charCountLabel.SetText(fmt.Sprintf("字符数: %d", len([]rune(u.textEdit.Text()))))
}

// setStatus 线程安全地更新状态栏（调用方均在 UI 线程）。
func (u *UI) setStatus(s string) {
	if u.statusLabel == nil {
		return
	}
	u.statusLabel.SetText(s)
}

// readParams 从界面控件读取当前生成参数。
func (u *UI) readParams() Params {
	ec := "M"
	if t := strings.TrimSpace(u.ecCombo.Text()); t != "" {
		ec = strings.Fields(t)[0] // 选项形如 "L -- 7%"，取首词作为容错级别
	}
	border := int(u.borderEdit.Value())
	if border < 0 {
		border = 0
	}
	chars := int(u.charsEdit.Value())
	if chars < 1 {
		chars = 1
	}
	fg := strings.TrimSpace(u.fgEdit.Text())
	if fg == "" {
		fg = "#000000"
	}
	bg := strings.TrimSpace(u.bgEdit.Text())
	if bg == "" {
		bg = "#FFFFFF"
	}
	return Params{
		ECLevel:      ec,
		Border:       border,
		ColorMode:    u.colorChk.Checked(),
		ColorCount:   comboColorCount(u.colorCountCombo),
		FgColor:      fg,
		BgColor:      bg,
		AutoPage:     u.autoPageChk.Checked(),
		CharsPerPage: chars,
		PageMarker:   u.markerChk.Checked(),
	}
}

// comboColorCount 解析颜色数下拉框（形如 "8 (8色)"），取首个整数字段；解析失败回退 8。
func comboColorCount(c *walk.ComboBox) int {
	if c == nil {
		return 8
	}
	fields := strings.Fields(c.Text())
	if len(fields) > 0 {
		if v, err := strconv.Atoi(fields[0]); err == nil {
			return v
		}
	}
	return 8
}

// fingerprint 生成输入指纹用于脏检测。
func (u *UI) fingerprint(text string, p Params) string {
	return fmt.Sprintf("%s|%s|%d|%v|%s|%s|%v|%d|%v",
		text, p.ECLevel, p.Border, p.ColorMode, p.FgColor, p.BgColor, p.AutoPage, p.CharsPerPage, p.PageMarker)
}

// scheduleGenerate 防抖重新生成二维码（120ms）。
func (u *UI) scheduleGenerate() {
	if u.genTimer != nil {
		u.genTimer.Stop()
	}
	u.genTimer = time.AfterFunc(120*time.Millisecond, func() {
		u.mw.Synchronize(u.regenerate)
	})
}

// regenerate 重新分页并渲染当前页。
// 脏检测与参数读取在 UI 线程完成（walk 控件只能在 UI 线程访问）；
// 耗时的分页/位图计算放到后台 goroutine，避免超长文本阻塞 UI 线程，
// 结果再通过 Synchronize 回到 UI 线程更新控件。genSeq 用于丢弃过期结果。
func (u *UI) regenerate() {
	text := u.textEdit.Text()
	p := u.readParams()
	key := u.fingerprint(text, p)
	u.mu.Lock()
	if key == u.lastKey {
		u.mu.Unlock()
		return
	}
	u.lastKey = key
	u.curParams = p
	myGen := u.genSeq + 1
	u.genSeq = myGen
	u.mu.Unlock()

	go func() {
		pages, err := splitPages(text, p)
		if err != nil {
			u.mw.Synchronize(func() {
				u.mu.Lock()
				stale := u.genSeq != myGen
				u.mu.Unlock()
				if stale {
					return
				}
				u.setStatus("分页失败: " + err.Error())
			})
			return
		}
		total := len([]rune(text))

		// 仅预渲染第 0 页，其余页按需（翻页时）再渲染
		var img *image.NRGBA
		var rerr error
		if len(pages) > 0 && !pages[0].Overflow {
			img, rerr = renderPage(pages[0].Text, p)
		}
		if rerr != nil {
			u.clearImage()
			u.updateNav()
			u.setStatus("渲染失败: " + rerr.Error())
			return
		}

		u.mw.Synchronize(func() {
			u.mu.Lock()
			if u.genSeq != myGen {
				u.mu.Unlock()
				return
			}
			if u.curBmp != nil {
				u.curBmp.Dispose()
				u.curBmp = nil
			}
			u.pages = pages
			u.pageCount = len(pages)
			u.totalChars = total
			u.page = 0
			u.hasQR = len(pages) > 0
			u.mu.Unlock()

			if len(pages) == 0 {
				u.clearImage()
				u.updateNav()
				u.setStatus("请输入文本以生成二维码")
				return
			}
			if len(pages) > 0 && pages[0].Overflow {
				u.clearImage()
			} else if img != nil {
				if bmp, berr := walk.NewBitmapFromImage(img); berr == nil {
					u.curBmp = bmp
					_ = u.qrView.SetImage(bmp)
				}
			}
			u.updateNav()
			u.setStatus(u.currentPageStatus(0))
			u.scheduleSave(text, p, len(pages))
		})
	}()
}

// showImage 渲染第 idx 页并显示到 ImageView。Overflow 页（不分页模式下超长）不显示，仅清空。
func (u *UI) showImage(idx int) error {
	u.mu.Lock()
	pages := u.pages
	p := u.curParams
	u.mu.Unlock()
	if idx < 0 || idx >= len(pages) {
		return nil
	}
	pg := pages[idx]
	if pg.Overflow {
		u.clearImage()
		return nil
	}
	img, err := renderPage(pg.Text, p)
	if err != nil {
		return err
	}
	bmp, err := walk.NewBitmapFromImage(img)
	if err != nil {
		return err
	}
	if u.curBmp != nil {
		u.curBmp.Dispose()
	}
	u.curBmp = bmp
	return u.qrView.SetImage(bmp)
}

// clearImage 清空当前二维码显示。
func (u *UI) clearImage() {
	if u.curBmp != nil {
		u.curBmp.Dispose()
		u.curBmp = nil
	}
	_ = u.qrView.SetImage(nil)
}

// updateNav 刷新翻页按钮与页码。
func (u *UI) updateNav() {
	u.mu.Lock()
	pc := u.pageCount
	pg := u.page
	u.mu.Unlock()
	u.pageLabel.SetText(fmt.Sprintf("第 %d / %d 页", pg+1, maxInt(pc, 1)))
	u.prevBtn.SetEnabled(pg > 0)
	u.nextBtn.SetEnabled(pg < pc-1)
}

// currentPageStatus 生成左下角状态栏文案：显示当前页对应的原始文本字符区间。
func (u *UI) currentPageStatus(idx int) string {
	u.mu.Lock()
	pages := u.pages
	total := u.totalChars
	u.mu.Unlock()
	n := len(pages)
	if n == 0 || idx < 0 || idx >= n {
		return "请输入文本以生成二维码"
	}
	pg := pages[idx]
	if pg.Overflow {
		return fmt.Sprintf("第 %d/%d 页 · 当前页文本过长（共 %d 字），请开启右侧\"自动分页\"", idx+1, n, total)
	}
	rng := fmt.Sprintf("字符 %d–%d", pg.Start+1, pg.End)
	if n == 1 {
		return fmt.Sprintf("已生成二维码 · %s（共 %d 字）", rng, total)
	}
	return fmt.Sprintf("自动分页 · 第 %d/%d 页 · %s（共 %d 字）", idx+1, n, rng, total)
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
	u.mu.Unlock()
	if err := u.showImage(idx); err != nil {
		u.setStatus("渲染失败: " + err.Error())
		return
	}
	u.updateNav()
	u.setStatus(u.currentPageStatus(idx))
}

// scheduleSave 防抖自动写入历史（700ms 停顿后）。
func (u *UI) scheduleSave(text string, p Params, pages int) {
	if u.saveTimer != nil {
		u.saveTimer.Stop()
	}
	u.saveTimer = time.AfterFunc(700*time.Millisecond, func() {
		u.mw.Synchronize(func() {
			u.maybeSaveHistory(text, p, pages)
		})
	})
}

// maybeSaveHistory 去重后写入历史。
func (u *UI) maybeSaveHistory(text string, p Params, pages int) {
	u.mu.Lock()
	if u.skipSaveOnce {
		u.skipSaveOnce = false
		u.savedText = text
		u.mu.Unlock()
		return
	}
	if strings.TrimSpace(text) == "" || text == u.savedText {
		u.mu.Unlock()
		return
	}
	u.savedText = text
	u.mu.Unlock()
	u.addHistory(text, p, pages)
	u.setStatus(fmt.Sprintf("已保存到历史（共 %d 页）", pages))
}

// saveCurrent 把当前已生成结果存入历史（不重新生成）。
func (u *UI) saveCurrent() {
	u.mu.Lock()
	has := u.hasQR
	text := u.textEdit.Text()
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

// saveCurrentImage 把当前页二维码保存为用户指定路径的 PNG。
func (u *UI) saveCurrentImage() {
	u.mu.Lock()
	has := u.hasQR
	pg := u.page
	pages := u.pages
	p := u.curParams
	u.mu.Unlock()
	if !has || pg < 0 || pg >= len(pages) {
		u.setStatus("暂无可保存的图片")
		return
	}
	if pages[pg].Overflow {
		u.setStatus("当前页文本过长，无法保存图片，请开启右侧\"自动分页\"")
		return
	}
	img, err := renderPage(pages[pg].Text, p)
	if err != nil {
		u.setStatus("渲染失败: " + err.Error())
		return
	}
	dlg := new(walk.FileDialog)
	dlg.Title = "保存二维码图片"
	dlg.Filter = "PNG 图片 (*.png)|*.png"
	dlg.FilePath = fmt.Sprintf("qrcode_page_%d.png", pg+1)
	if ok, err := dlg.ShowSave(u.mw); err != nil {
		u.setStatus("保存失败: " + err.Error())
		return
	} else if !ok {
		return
	}
	if err := savePNG(dlg.FilePath, img); err != nil {
		u.setStatus("保存失败: " + err.Error())
		return
	}
	u.setStatus("已保存: " + dlg.FilePath)
}

// historyRow 是历史表格的一行展示数据。
type historyRow struct {
	Time    string
	Preview string
	Params  string
	Pages   int
}

// historyModel 实现 walk.TableModel 接口（自定义 Value，非反射）。
type historyModel struct {
	walk.TableModelBase
	rows []historyRow
}

func (m *historyModel) RowCount() int { return len(m.rows) }

func (m *historyModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.rows) {
		return nil
	}
	r := m.rows[row]
	switch col {
	case 0:
		return r.Time
	case 1:
		return r.Preview
	case 2:
		return r.Params
	case 3:
		return r.Pages
	}
	return nil
}

// showHistoryDialog 弹出历史记录管理对话框。
func (u *UI) showHistoryDialog() {
	dlg, err := walk.NewDialog(u.mw)
	if err != nil {
		u.setStatus("打开历史失败: " + err.Error())
		return
	}
	dlg.SetTitle("历史记录")
	dlg.SetLayout(tight(walk.NewVBoxLayout()))

	bar := must(walk.NewComposite(dlg))
	bar.SetLayout(tight(walk.NewHBoxLayout()))
	loadBtn := must(walk.NewPushButton(bar))
	loadBtn.SetText("加载选中")
	delBtn := must(walk.NewPushButton(bar))
	delBtn.SetText("删除选中")
	clearBtn := must(walk.NewPushButton(bar))
	clearBtn.SetText("清空")
	closeBtn := must(walk.NewPushButton(bar))
	closeBtn.SetText("关闭")

	tv := must(walk.NewTableView(dlg))
	tv.SetLastColumnStretched(true)
	_ = tv.SetMultiSelection(true) // 允许历史表格多选，配合“删除选中”批量删除
	c0 := walk.NewTableViewColumn()
	c0.SetName("Time")
	_ = c0.SetTitle("时间")
	_ = c0.SetWidth(150)
	tv.Columns().Add(c0)
	c1 := walk.NewTableViewColumn()
	c1.SetName("Preview")
	_ = c1.SetTitle("预览")
	_ = c1.SetWidth(280)
	tv.Columns().Add(c1)
	c2 := walk.NewTableViewColumn()
	c2.SetName("Params")
	_ = c2.SetTitle("参数")
	_ = c2.SetWidth(180)
	tv.Columns().Add(c2)
	c3 := walk.NewTableViewColumn()
	c3.SetName("Pages")
	_ = c3.SetTitle("页数")
	_ = c3.SetWidth(50)
	tv.Columns().Add(c3)
	u.histTV = tv
	u.populateHistory(tv)

	loadBtn.Clicked().Attach(func() {
		idx := tv.CurrentIndex()
		if idx < 0 {
			return
		}
		u.loadHistory(idx)
		dlg.Accept()
	})
	delBtn.Clicked().Attach(func() {
		idxs := tv.SelectedIndexes()
		if len(idxs) == 0 {
			return
		}
		u.deleteHistories(idxs)
		u.populateHistory(tv)
	})
	clearBtn.Clicked().Attach(func() {
		u.clearHistory()
		u.populateHistory(tv)
	})
	closeBtn.Clicked().Attach(func() {
		dlg.Cancel()
	})
	tv.ItemActivated().Attach(func() {
		idx := tv.CurrentIndex()
		if idx < 0 {
			return
		}
		u.loadHistory(idx)
		dlg.Accept()
	})

	dlg.SetMinMaxSize(walk.Size{Width: 720, Height: 480}, walk.Size{})
	dlg.Run()
}

// populateHistory 把当前历史填充到表格。
func (u *UI) populateHistory(tv *walk.TableView) {
	u.mu.Lock()
	hist := u.history
	u.mu.Unlock()
	rows := make([]historyRow, 0, len(hist))
	for _, e := range hist {
		rows = append(rows, historyRow{
			Time:    e.TimeStr(),
			Preview: previewOr(e.Preview),
			Params:  fmt.Sprintf("%s / 边距%d / %d页", e.Params.ECLevel, e.Params.Border, e.Pages),
			Pages:   e.Pages,
		})
	}
	m := &historyModel{rows: rows}
	u.histModel = m
	tv.SetModel(m)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func previewOr(s string) string {
	if s == "" {
		return "(空)"
	}
	return s
}
