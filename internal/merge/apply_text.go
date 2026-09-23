package merge

import "github.com/lisama569/merge-engine-go/internal/model"

// applyTextChange 把一条文本变更作用于文件行，返回新的行切片。
//
// 区间语义（1 起始）：
//   - 纯插入 [s, s-1]：在第 s 行之前插入 Replacement
//   - 删除   [s, e]：删除第 s..e 行，Replacement 为空
//   - 替换   [s, e]：第 s..e 行替换为 Replacement
func applyTextChange(lines []string, c model.Change) []string {
	r := c.Text.Range
	start := r.Start - 1 // 转换为 0 基下标

	// 纯插入
	if r.End == r.Start-1 {
		out := make([]string, 0, len(lines)+len(c.Text.Replacement))
		out = append(out, lines[:start]...)
		out = append(out, c.Text.Replacement...)
		out = append(out, lines[start:]...)
		return out
	}

	end := r.End // 0 基下标时为开区间端点
	out := make([]string, 0, len(lines)-(end-start)+len(c.Text.Replacement))
	out = append(out, lines[:start]...)
	out = append(out, c.Text.Replacement...)
	out = append(out, lines[end:]...)
	return out
}
