package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

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
	ecCombo     *walk.ComboBox
	borderEdit  *walk.NumberEdit
	colorChk    *walk.CheckBox
	fgEdit      *walk.LineEdit
	bgEdit      *walk.LineEdit
	autoPageChk *walk.CheckBox
	charsEdit   *walk.NumberEdit
	markerChk   *walk.CheckBox

	// 右栏输入
	textEdit       *walk.TextEdit
	charCountLabel *walk.Label

	// 历史
	history   []HistoryEntry
	histModel *historyModel
	histTV    *walk.TableView

	// 生成状态（mutex 保护跨 goroutine 访问）
	mu           sync.Mutex
	pages        []string
	pageCount    int
	page         int
	hasQR        bool
	curParams    Params
	lastKey      string
	genTimer     *time.Timer
	saveTimer    *time.Timer
	savedText    string
	skipSaveOnce bool
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
	mw.SetLayout(walk.NewHBoxLayout())

	// ===== 主水平分栏：左=二维码，右=参数+输入 =====
	hsplit := must(walk.NewHSplitter(mw))

	// --- 左栏 ---
	left := must(walk.NewComposite(hsplit))
	left.SetLayout(walk.NewVBoxLayout())

	u.qrView = must(walk.NewImageView(left))
	u.qrView.SetMode(walk.ImageViewModeZoom)
	u.qrView.SetMargin(8)

	navBar := must(walk.NewComposite(left))
	navBar.SetLayout(walk.NewHBoxLayout())
	navBar.SetMinMaxSize(walk.Size{Width: 0, Height: 34}, walk.Size{Width: 0, Height: 34})
	u.prevBtn = must(walk.NewPushButton(navBar))
	u.prevBtn.SetText("上一页")
	u.pageLabel = must(walk.NewLabel(navBar))
	u.pageLabel.SetText("第 1 / 1 页")
	u.nextBtn = must(walk.NewPushButton(navBar))
	u.nextBtn.SetText("下一页")
	u.saveImgBtn = must(walk.NewPushButton(navBar))
	u.saveImgBtn.SetText("保存图片")

	u.statusLabel = must(walk.NewLabel(left))
	u.statusLabel.SetMinMaxSize(walk.Size{Width: 0, Height: 20}, walk.Size{Width: 0, Height: 20})
	u.statusLabel.SetText("输入文本以生成二维码")

	// --- 右栏（垂直分栏：参数 / 输入）---
	right := must(walk.NewComposite(hsplit))
	right.SetLayout(walk.NewVBoxLayout())
	right.SetMinMaxSize(walk.Size{Width: 300, Height: 0}, walk.Size{Width: 440, Height: 0})

	vsplit := must(walk.NewVSplitter(right))

	// 参数区（可滚动）
	paramPane := must(walk.NewComposite(vsplit))
	paramPane.SetLayout(walk.NewVBoxLayout())
	paramPane.SetMinMaxSize(walk.Size{Width: 0, Height: 200}, walk.Size{Width: 0, Height: 440})
	paramScroll := must(walk.NewScrollView(paramPane))
	paramScroll.SetLayout(walk.NewVBoxLayout())

	title := must(walk.NewLabel(paramScroll))
	title.SetText("生成参数")

	ecRow := must(walk.NewComposite(paramScroll))
	ecRow.SetLayout(walk.NewHBoxLayout())
	ecLbl := must(walk.NewLabel(ecRow))
	ecLbl.SetText("容错级别")
	u.ecCombo = must(walk.NewComboBox(ecRow))
	_ = u.ecCombo.SetModel([]string{"L", "M", "Q", "H"})
	_ = u.ecCombo.SetCurrentIndex(1)

	borderRow := must(walk.NewComposite(paramScroll))
	borderRow.SetLayout(walk.NewHBoxLayout())
	bLbl := must(walk.NewLabel(borderRow))
	bLbl.SetText("静区/边距(模块)")
	u.borderEdit = must(walk.NewNumberEdit(borderRow))
	_ = u.borderEdit.SetRange(0, 16)
	_ = u.borderEdit.SetValue(4)
	_ = u.borderEdit.SetSpinButtonsVisible(true)

	u.colorChk = must(walk.NewCheckBox(paramScroll))
	u.colorChk.SetText("启用彩色二维码（预留）")
	colorPane := must(walk.NewComposite(paramScroll))
	colorPane.SetLayout(walk.NewHBoxLayout())
	fgLbl := must(walk.NewLabel(colorPane))
	fgLbl.SetText("前景色")
	u.fgEdit = must(walk.NewLineEdit(colorPane))
	u.fgEdit.SetText("#000000")
	bgLbl := must(walk.NewLabel(colorPane))
	bgLbl.SetText("背景色")
	u.bgEdit = must(walk.NewLineEdit(colorPane))
	u.bgEdit.SetText("#FFFFFF")
	colorPane.SetVisible(false)

	u.autoPageChk = must(walk.NewCheckBox(paramScroll))
	u.autoPageChk.SetText("自动分页（超出单码容量自动拆分）")
	u.autoPageChk.SetChecked(true)
	charsPane := must(walk.NewComposite(paramScroll))
	charsPane.SetLayout(walk.NewHBoxLayout())
	cLbl := must(walk.NewLabel(charsPane))
	cLbl.SetText("每页字符数")
	u.charsEdit = must(walk.NewNumberEdit(charsPane))
	_ = u.charsEdit.SetRange(1, 4000)
	_ = u.charsEdit.SetValue(300)
	_ = u.charsEdit.SetSpinButtonsVisible(true)
	charsPane.SetVisible(false)

	u.markerChk = must(walk.NewCheckBox(paramScroll))
	u.markerChk.SetText("添加页码标记 [p/N]")
	u.markerChk.SetChecked(false)

	histBtn := must(walk.NewPushButton(paramScroll))
	histBtn.SetText("管理历史记录")
	hint := must(walk.NewLabel(paramScroll))
	hint.SetText("输入后稍作停顿即自动存入历史")

	// 输入区
	inputPane := must(walk.NewComposite(vsplit))
	inputPane.SetLayout(walk.NewVBoxLayout())
	inLbl := must(walk.NewLabel(inputPane))
	inLbl.SetText("输入文本")
	inLbl.SetMinMaxSize(walk.Size{Width: 0, Height: 20}, walk.Size{Width: 0, Height: 20})
	u.textEdit = must(walk.NewTextEdit(inputPane))
	u.charCountLabel = must(walk.NewLabel(inputPane))
	u.charCountLabel.SetText("字符数: 0")
	u.charCountLabel.SetMinMaxSize(walk.Size{Width: 0, Height: 20}, walk.Size{Width: 0, Height: 20})

	// ===== 事件绑定 =====
	u.textEdit.TextChanged().Attach(func() {
		u.updateCharCount()
		u.scheduleGenerate()
	})
	u.ecCombo.CurrentIndexChanged().Attach(u.scheduleGenerate)
	u.borderEdit.ValueChanged().Attach(u.scheduleGenerate)
	u.colorChk.CheckedChanged().Attach(func() {
		colorPane.SetVisible(u.colorChk.Checked())
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
	opts := []string{"L", "M", "Q", "H"}
	idx := u.ecCombo.CurrentIndex()
	if idx >= 0 && idx < len(opts) {
		ec = opts[idx]
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
		FgColor:      fg,
		BgColor:      bg,
		AutoPage:     u.autoPageChk.Checked(),
		CharsPerPage: chars,
		PageMarker:   u.markerChk.Checked(),
	}
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
	u.mu.Unlock()

	pages, err := splitPages(text, p)
	if err != nil {
		u.setStatus("分页失败: " + err.Error())
		return
	}
	u.mu.Lock()
	u.pages = pages
	u.pageCount = len(pages)
	u.page = 0
	u.hasQR = len(pages) > 0
	u.mu.Unlock()

	if len(pages) == 0 {
		u.clearImage()
		u.updateNav()
		u.setStatus("请输入文本以生成二维码")
		return
	}
	if err := u.showImage(0); err != nil {
		u.setStatus("生成失败: " + err.Error())
		return
	}
	u.updateNav()
	if len(pages) > 1 {
		u.setStatus(fmt.Sprintf("已生成，自动分页共 %d 页", len(pages)))
	} else {
		u.setStatus("已生成二维码")
	}
	u.scheduleSave(text, p, len(pages))
}

// showImage 渲染第 idx 页并显示到 ImageView。
func (u *UI) showImage(idx int) error {
	u.mu.Lock()
	pages := u.pages
	p := u.curParams
	u.mu.Unlock()
	if idx < 0 || idx >= len(pages) {
		return nil
	}
	img, err := renderPage(pages[idx], p)
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
	img, err := renderPage(pages[pg], p)
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
	dlg.SetLayout(walk.NewVBoxLayout())

	bar := must(walk.NewComposite(dlg))
	bar.SetLayout(walk.NewHBoxLayout())
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
		idx := tv.CurrentIndex()
		if idx < 0 {
			return
		}
		u.deleteHistory(idx)
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
