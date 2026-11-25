package engine

import "github.com/Felix2yu/liuying-engine/site"

// Request 描述一次下载任务。站点无关：适配器从 site.Media 与 Selection 里取所需信息。
type Request struct {
	// URL 是用户输入（可含分享文案、短链）。
	URL string
	// OutputDir 是产物落盘的绝对目录。
	OutputDir string
	// WorkDir 是临时目录（断点续传、分片、合并中转）。空表示由引擎在 OutputDir 下建。
	WorkDir string
	// Template 是产物路径模板，空表示各站点默认。
	Template string
	// Selection 是码流选择偏好。
	Selection site.Selection
	// Extras 控制附属资源。
	Extras ExtrasOptions
	// Auth 是本次任务可用的凭据；nil 表示未登录。
	Auth *site.Auth
	// RateLimit 是每秒字节上限，0 表示不限速。
	RateLimit int64
	// Concurrency 是并发下载数，0 表示由引擎决定（默认 1）。
	Concurrency int
}

// ExtrasOptions 控制弹幕/字幕/封面的下载。
type ExtrasOptions struct {
	Danmaku  bool
	Subtitle bool
	Cover    bool
}
