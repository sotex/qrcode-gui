package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"

	"github.com/CzaxStudio/proton"
	"github.com/gogpu/systray"
)

func main() {
	u := NewUI()
	_ = u.loadHistoryFile()

	app := proton.New("二维码生成器")
	app.ApplyPalette(proton.DarkPalette)
	app.Window("二维码生成器 · QR Generator", 1000, 700, u.draw)

	// 托盘在独立 goroutine 中运行（它与 proton 的主事件循环互不阻塞）。
	go runTray(makeTrayIcon(), u)

	// proton 主循环会阻塞当前（主）线程，直到窗口关闭。
	app.Run()
}

// runTray 配置并启动系统托盘：图标、右键菜单、左键点击与通知。
func runTray(icon []byte, u *UI) {
	tray := systray.New()

	menu := systray.NewMenu()
	menu.Add("显示主窗口", func() {
		tray.ShowNotification("二维码生成器", "主窗口已显示")
	})
	menu.AddSeparator()
	menu.Add("保存当前到历史", func() {
		u.requestSave.Store(true)
		u.invalidate()
	})
	menu.Add("打开历史记录", func() {
		u.showHistory.Store(true)
		u.invalidate()
	})
	menu.AddSeparator()
	menu.Add("退出", func() {
		tray.Remove()
		os.Exit(0)
	})

	tray.SetIcon(icon).
		SetTooltip("二维码生成器").
		SetMenu(menu).
		Show()

	// 左键点击托盘图标：提示主窗口已显示（proton 当前未暴露隐藏/显示窗口 API，
	// 关闭窗口即退出程序；托盘主要负责快捷操作与通知）。
	tray.OnClick(func() {
		tray.ShowNotification("二维码生成器", "主窗口已显示")
	})

	// Run 阻塞并泵送平台消息循环，直到退出。
	_ = tray.Run()
}

// makeTrayIcon 生成一张二维码作为托盘图标（PNG 字节）；失败时回退为纯色方块。
func makeTrayIcon() []byte {
	p := defaultParams()
	p.Size = 128
	p.Border = 2
	img, err := renderPage("QR Code", p)
	if err != nil || img == nil {
		img = image.NewNRGBA(image.Rect(0, 0, 16, 16))
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				img.Set(x, y, color.NRGBA{R: 80, G: 80, B: 200, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}
