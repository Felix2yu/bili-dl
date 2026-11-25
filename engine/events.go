package engine

import "github.com/Felix2yu/bili-dl/site"

// Event 复用 site 的事件定义，避免「引擎一套事件、站点一套事件」两处维护。
type Event = site.Event

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
