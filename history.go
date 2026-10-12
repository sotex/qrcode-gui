package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CzaxStudio/proton"
)

// HistoryEntry 单条历史记录。
type HistoryEntry struct {
	ID      string  `json:"id"`
	Time    int64   `json:"time"`    // Unix 秒
	Text    string  `json:"text"`    // 原始输入文本
	Params  Params  `json:"params"`  // 生成参数（用于回放）
	Pages   int     `json:"pages"`   // 分页数量
	Preview string  `json:"preview"` // 文本预览
}

// TimeStr 返回可读的时间字符串。
func (e HistoryEntry) TimeStr() string {
	return time.Unix(e.Time, 0).Format("2006-01-02 15:04:05")
}

// historyPath 返回历史记录 JSON 文件的完整路径（位于用户配置目录）。
func historyPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	dir := filepath.Join(base, "qrcode-gui")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "history.json"), nil
}

// loadHistoryFile 从磁盘载入历史记录到 u.history。文件不存在或损坏时静默返回空列表。
func (u *UI) loadHistoryFile() error {
	path, err := historyPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []HistoryEntry
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	u.mu.Lock()
	u.history = list
	u.mu.Unlock()
	return nil
}

// persist 把给定的历史列表原子写入磁盘。
func (u *UI) persist(list []HistoryEntry) {
	path, err := historyPath()
	if err != nil {
		return
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// addHistory 追加一条历史记录并落盘。
func (u *UI) addHistory(text string, p Params, pages int) {
	e := HistoryEntry{
		ID:      fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(u.history)),
		Time:    time.Now().Unix(),
		Text:    text,
		Params:  p,
		Pages:   pages,
		Preview: previewOf(text),
	}
	u.mu.Lock()
	u.history = append(u.history, e)
	list := u.history
	u.mu.Unlock()
	u.persist(list)
}

// deleteHistory 按索引删除一条记录。
func (u *UI) deleteHistory(i int) {
	u.mu.Lock()
	if i < 0 || i >= len(u.history) {
		u.mu.Unlock()
		return
	}
	u.history = append(u.history[:i], u.history[i+1:]...)
	list := u.history
	u.mu.Unlock()
	u.persist(list)
	u.setStatus("已删除一条历史记录")
}

// clearHistory 清空全部历史记录。
func (u *UI) clearHistory() {
	u.mu.Lock()
	u.history = nil
	u.mu.Unlock()
	u.persist(nil)
	u.setStatus("已清空历史记录")
}

// loadHistory 把第 i 条记录的内容与参数回填到界面，并强制重新生成。
func (u *UI) loadHistory(i int) {
	u.mu.Lock()
	if i < 0 || i >= len(u.history) {
		u.mu.Unlock()
		return
	}
	e := u.history[i]
	params := e.Params
	u.mu.Unlock()

	u.text.SetText(e.Text)
	u.sizeNum.Value = float64(params.Size)
	idx := 1
	opts := []string{"L", "M", "Q", "H"}
	for k, v := range opts {
		if v == params.ECLevel {
			idx = k
		}
	}
	u.ecSel.Selected = idx
	u.borderNum.Value = float64(params.Border)
	u.colorOn.Value = params.ColorMode
	u.fgIn.SetText(params.FgColor)
	u.bgIn.SetText(params.BgColor)
	u.autoPage.Value = params.AutoPage
	u.charsNum.Value = float64(params.CharsPerPage)
	u.marker.Value = params.PageMarker

	u.histModal.Hide()
	u.lastKey = "" // 触发下一帧重新生成
	u.setStatus("已加载历史记录")
}

// ensureHistBtns 保证历史列表的加载/删除按钮切片足够长。
func (u *UI) ensureHistBtns(n int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for len(u.histLoad) < n {
		u.histLoad = append(u.histLoad, proton.Clickable{})
	}
	for len(u.histDel) < n {
		u.histDel = append(u.histDel, proton.Clickable{})
	}
}

// previewOf 生成历史预览：取前 40 个字符，换行替换为空格。
func previewOf(text string) string {
	r := []rune(text)
	if len(r) > 40 {
		r = r[:40]
	}
	s := string(r)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}
