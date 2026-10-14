package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// HistoryEntry 单条历史记录。
type HistoryEntry struct {
	ID      string `json:"id"`
	Time    int64  `json:"time"`    // Unix 秒
	Text    string `json:"text"`    // 原始输入文本
	Params  Params `json:"params"`  // 生成参数（用于回放）
	Pages   int    `json:"pages"`   // 分页数量
	Preview string `json:"preview"` // 文本预览
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
	u.deleteHistories([]int{i})
}

// removeByIndexes 从切片中删除指定索引的元素，返回新切片（不修改入参）。
// 重复索引自动去重，越界与负索引自动忽略，索引顺序无关（内部从大到小处理）。
func removeByIndexes[T any](src []T, idxs []int) []T {
	seen := make(map[int]struct{}, len(idxs))
	order := make([]int, 0, len(idxs))
	for _, i := range idxs {
		if i < 0 {
			continue
		}
		if _, ok := seen[i]; ok {
			continue
		}
		seen[i] = struct{}{}
		order = append(order, i)
	}
	if len(order) == 0 {
		return src
	}
	// 从大到小删除，保证每个待删索引在删除时仍指向正确的原始行
	sort.Sort(sort.Reverse(sort.IntSlice(order)))
	out := append([]T(nil), src...)
	for _, i := range order {
		if i < len(out) {
			out = append(out[:i], out[i+1:]...)
		}
	}
	return out
}

// deleteHistories 按多个索引批量删除记录（用于历史对话框的多选删除）。
func (u *UI) deleteHistories(idxs []int) {
	u.mu.Lock()
	if len(u.history) == 0 {
		u.mu.Unlock()
		return
	}
	next := removeByIndexes(u.history, idxs)
	removed := len(u.history) - len(next)
	u.history = next
	list := u.history
	u.mu.Unlock()
	if removed > 0 {
		u.persist(list)
		u.setStatus(fmt.Sprintf("已删除 %d 条历史记录", removed))
	}
}

// clearHistory 清空全部历史记录。
func (u *UI) clearHistory() {
	u.mu.Lock()
	u.history = nil
	u.mu.Unlock()
	u.persist(nil)
	u.setStatus("已清空历史记录")
}

// loadHistory 把第 i 条记录回填到界面控件，借助各控件的 Changed 事件自动触发重新生成。
func (u *UI) loadHistory(i int) {
	u.mu.Lock()
	if i < 0 || i >= len(u.history) {
		u.mu.Unlock()
		return
	}
	e := u.history[i]
	params := e.Params
	u.mu.Unlock()

	opts := []string{"L", "M", "Q", "H"}
	idx := 1
	for k, v := range opts {
		if v == params.ECLevel {
			idx = k
		}
	}
	u.ecCombo.SetCurrentIndex(idx)
	u.borderEdit.SetValue(float64(params.Border))
	u.colorChk.SetChecked(params.ColorMode)
	u.fgEdit.SetText(params.FgColor)
	u.bgEdit.SetText(params.BgColor)
	u.autoPageChk.SetChecked(params.AutoPage)
	u.charsEdit.SetValue(float64(params.CharsPerPage))
	u.markerChk.SetChecked(params.PageMarker)
	u.textEdit.SetText(e.Text)

	u.lastKey = ""        // 强制下一轮重新生成
	u.skipSaveOnce = true // 回填内容不重复写入历史
	u.setStatus("已加载历史记录")
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
