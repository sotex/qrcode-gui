//go:generate rsrc -manifest app.manifest -ico qrcode.ico -o rsrc.syso

package main

import (
	"fmt"
	"image"
	"image/color"
	"os"

	"github.com/lxn/walk"
)

// 版本信息由 build.ps1 通过 -ldflags -X 注入；默认值用于本地直接 go build。
var (
	version   = "dev"
	buildTime = ""
)

// appIconResID 是 rsrc 嵌入的图标资源 ID。
// rsrc 按资源声明顺序分配 ID：1=manifest(RT_MANIFEST)，2=图标组(RT_GROUP_ICON)，
// 因此 qrcode.ico 作为程序图标的资源 ID 固定为 2。若只嵌入 manifest（未带 -ico），
// 则不存在该资源，NewIconFromResourceId(2) 会失败——所有使用处都做了错误回退，不会崩溃。
const appIconResID = 2

func main() {
	u := NewUI()
	mw, err := u.build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
		os.Exit(1)
	}
	// 设置窗口图标（使用 rsrc 嵌入的 qrcode.ico，失败不致命）
	if icon, err := walk.NewIconFromResourceId(appIconResID); err == nil {
		_ = mw.SetIcon(icon)
	}
	// 载入历史记录（失败不致命）
	_ = u.loadHistoryFile()
	// 托盘（失败不致命）
	_ = setupTray(u, mw)
	// 进入消息循环，阻塞直到窗口关闭
	mw.Run()
}

// setupTray 创建系统托盘图标与右键菜单。
func setupTray(u *UI, mw *walk.MainWindow) error {
	ni, err := walk.NewNotifyIcon(mw)
	if err != nil {
		return err
	}
	menu := ni.ContextMenu()

	aShow := walk.NewAction()
	aShow.SetText("显示主窗口")
	aShow.Triggered().Attach(func() {
		mw.Synchronize(func() {
			mw.Show()
			mw.Activate()
		})
	})
	menu.Actions().Add(aShow)
	menu.Actions().Add(walk.NewSeparatorAction())

	aSave := walk.NewAction()
	aSave.SetText("保存当前到历史")
	aSave.Triggered().Attach(func() {
		mw.Synchronize(u.saveCurrent)
	})
	menu.Actions().Add(aSave)

	aHist := walk.NewAction()
	aHist.SetText("打开历史记录")
	aHist.Triggered().Attach(func() {
		mw.Synchronize(u.showHistoryDialog)
	})
	menu.Actions().Add(aHist)
	menu.Actions().Add(walk.NewSeparatorAction())

	aExit := walk.NewAction()
	aExit.SetText("退出")
	aExit.Triggered().Attach(func() {
		ni.Dispose()
		os.Exit(0)
	})
	menu.Actions().Add(aExit)

	if icon, err := walk.NewIconFromResourceId(appIconResID); err == nil {
		ni.SetIcon(icon)
	} else if icon, err := makeTrayIcon(); err == nil {
		// 资源图标不可用时回退为生成的二维码图标
		ni.SetIcon(icon)
	}
	ni.SetToolTip("二维码生成器")
	ni.SetVisible(true)
	ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			mw.Synchronize(func() {
				mw.Show()
				mw.Activate()
			})
		}
	})
	return nil
}

// makeTrayIcon 生成一张二维码图标（仅用于托盘显示），失败时回退为纯色方块。
func makeTrayIcon() (walk.Image, error) {
	p := defaultParams()
	p.Border = 2
	img, err := renderPage("QR Code", p)
	if err != nil || img == nil {
		img = image.NewNRGBA(image.Rect(0, 0, 32, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				img.Set(x, y, color.NRGBA{R: 80, G: 80, B: 200, A: 255})
			}
		}
	}
	side := 64
	small := image.NewNRGBA(image.Rect(0, 0, side, side))
	srcW := img.Bounds().Dx()
	srcH := img.Bounds().Dy()
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			sx := x * srcW / side
			sy := y * srcH / side
			small.Set(x, y, img.At(sx, sy))
		}
	}
	return walk.NewIconFromImage(small)
}
