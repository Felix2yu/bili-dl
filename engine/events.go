package engine

// Event 是一次下载过程中的事件。字段同时服务于进程内回调与 NDJSON 输出，
// 因此 json tag 与 bili-dl 既有的 --progress-json 保持一致，只做新增不做改名。
type Event struct {
	// Event 取值：start / progress / done / error / state。
	Event string `json:"event"`
	// MediaID 是站点无关的稳定 ID（BV / aweme_id / tweet_id / note_id）。
	MediaID string `json:"media_id,omitempty"`
	// BV 是哔哩哔哩专用字段，保留以兼容既有 NDJSON 消费方。
	BV   string `json:"bv,omitempty"`
	Page int    `json:"page,omitempty"`
	// Kind 取值：video / audio / cover / danmaku / subtitle / image。
	Kind      string   `json:"kind,omitempty"`
	Current   int64    `json:"current,omitempty"`
	Total     int64    `json:"total,omitempty"`
	Percent   float64  `json:"percent,omitempty"`
	SpeedBPS  int64    `json:"speed_bps,omitempty"`
	Message   string   `json:"message,omitempty"`
	Outputs   []Output `json:"outputs,omitempty"`
	Timestamp int64    `json:"ts"`
}

// EventSink 接收下载事件。实现必须快速返回，不得阻塞下载。
// nil sink 表示调用方不关心事件。
type EventSink func(Event)

// Emit 在 sink 为 nil 时安全退化为空操作。
func (s EventSink) Emit(ev Event) {
	if s == nil {
		return
	}
	s(ev)
}
