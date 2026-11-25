package site

// Event 是一次下载过程中的事件。
//
// 它同时服务于两条通路：进程内回调（--progress-json 之外）与 stdout 的 NDJSON。
// 字段与 json tag 延续 bili-dl 既有 NDJSON 契约，只做新增不做改名，
// 因此旧消费方不受影响。
type Event struct {
	// Event 取值：start / progress / done / error / state。
	Event string `json:"event"`
	// MediaID 是站点无关的稳定 ID（BV / aweme_id / tweet_id / note_id）。
	MediaID string `json:"media_id,omitempty"`
	// BV 是哔哩哔哩专用字段，保留以兼容既有 NDJSON 消费方（不带 omitempty）。
	BV   string `json:"bv"`
	Page int    `json:"page,omitempty"`
	// Kind 取值：video / audio / cover / danmaku / subtitle / image。
	Kind     string  `json:"kind,omitempty"`
	Current  int64   `json:"current,omitempty"`
	Total    int64   `json:"total,omitempty"`
	Percent  float64 `json:"percent,omitempty"`
	SpeedBPS int64   `json:"speed_bps,omitempty"`
	Message  string  `json:"message,omitempty"`
	// Outputs 仅在 done 事件上带，用于让调用方直接拿到落盘结果。
	Outputs   []Output `json:"outputs,omitempty"`
	Timestamp int64    `json:"ts"`
}
