package engine

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

// Output 是一条已经落盘的产物。
// 显式返回产出，让调用方不必扫描目录差集来判断「下到了什么」。
type Output struct {
	// Path 是绝对路径。
	Path string `json:"path"`
	// Kind 取值：video / audio / cover / danmaku / subtitle / image。
	Kind string `json:"kind"`
	// Container 是容器扩展名，不含点；非媒体文件为空。
	Container string `json:"container,omitempty"`
	// Bytes 是文件大小。
	Bytes int64 `json:"bytes"`
}

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
