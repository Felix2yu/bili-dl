package engine

import "github.com/Felix2yu/liuying-engine/site"

// State 是一次下载的终态。
type State string

const (
	// StateSucceeded 全部 Part 成功。
	StateSucceeded State = "succeeded"
	// StatePartial 部分 Part 成功（如多 P 里某个分 P 失败）。
	StatePartial State = "partial"
	// StateFailed 全部失败。
	StateFailed State = "failed"
	// StateCanceled 被取消。
	StateCanceled State = "canceled"
)

// Output 复用 site 的产物定义，保证站点适配器可以直接返回、无需转换。
type Output = site.Output

// Result 是一次下载的完整结果。
type Result struct {
	// MediaID 是站点内稳定 ID；多条目时为第一条。
	MediaID string `json:"media_id,omitempty"`
	State   State  `json:"state"`
	// Outputs 按落盘顺序排列。
	Outputs []Output `json:"outputs,omitempty"`
	// Warnings 是「不影响成功判定」的告警，如弹幕下载失败。
	Warnings []string `json:"warnings,omitempty"`
}
