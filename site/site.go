// Package site 定义站点适配器契约与站点无关的媒体模型。
//
// 设计要点：适配器分两类能力，按需实现。
//
//   - Adapter：必备。认领 URL 并解析成站点无关的 Media。
//   - StreamProvider：可选。能给出直链，由 fetch 层搬运（哔哩哔哩走这条）。
//
// 本包不 import 任何具体站点实现，避免引擎被单个站点的依赖绑架。
package site

import (
	"context"
	"net/http"
	"time"
)

// Adapter 是站点适配器的必备能力。
type Adapter interface {
	// Site 返回站点标识，如 "bilibili"、"douyin"、"x"、"xiaohongshu"。
	Site() string
	// Match 判断该 URL 是否归本站点处理。实现应只做字符串/域名判断，不发请求。
	Match(rawURL string) bool
	// Resolve 把 URL 解析成一条或多条待下载媒体（合集、多 P、图集会展开成多条或一条多 Part）。
	// 只做元数据解析，不下载、不选流。
	Resolve(ctx context.Context, rawURL string, opt ResolveOptions) ([]*Media, error)
}

// StreamProvider 是站点适配器的可选能力：能直接把可选码流列成直链。
// 由 fetch 层负责实际搬运、续传、限速与进度上报。
type StreamProvider interface {
	Adapter
	Streams(ctx context.Context, m *Media, sel Selection) (*StreamSet, error)
}

// ResolveOptions 控制解析行为。零值表示「默认解析」。
type ResolveOptions struct {
	// ExpandCollections 展开合集/播放列表的全部条目。
	// 不展开时只返回 URL 直接指向的那一条媒体。
	ExpandCollections bool
	// Auth 是本次解析可用的凭据；nil 表示未登录。
	Auth *Auth
	// Client 允许调用方注入 HTTP 客户端（代理、超时）；nil 由实现使用默认客户端。
	Client *http.Client
}

// Auth 是一次请求可用的凭据集合。
// 各站点自行决定用哪些 Cookie/Header，未用到的字段忽略。
type Auth struct {
	Cookies []Cookie
	Headers map[string]string
}

// Cookie 是单条 Cookie。Expires 为零值表示会话 Cookie。
type Cookie struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	Expires  time.Time
	Secure   bool
	HTTPOnly bool
}

// Author 是内容作者。ID 为站点内稳定标识（bilibili 的 mid、抖音的 sec_uid 等）。
type Author struct {
	ID   string
	Name string
}

// Media 是一条待下载媒体，已抹平站点差异。
type Media struct {
	Site     string
	ID       string // 站点内稳定 ID：bvid / aweme_id / tweet_id / note_id
	Title    string
	Author   Author
	Cover    string
	Duration time.Duration
	// Parts 是分 P / 剧集 / 播放列表条目。单条媒体时长度为 1，Index 从 1 开始。
	Parts []Part
	// Extra 是站点透传字段（cid、ep_id、合集归属等），供适配器自己在 Streams 阶段读回。
	Extra map[string]string
}

// Part 是 Media 的一个可独立下载单元。
type Part struct {
	Index    int // 1-based
	ID       string
	Title    string
	Duration time.Duration
	// URL 是该 Part 的可再解析地址（可选）。
	URL string
}

// Selection 是码流选择偏好。零值表示「自动选最优」。
type Selection struct {
	// PartIndex 指定要选流的分 P（1-based）；0 表示第一 P。
	PartIndex int
	// VideoQuality 是站点原生画质 ID（bilibili 的 DASH id，如 127）。
	VideoQuality int
	// VCodecPriority 按优先级排列的编码短名（av1/hevc/avc）；空表示不限制。
	VCodecPriority []string
	// AudioQuality 是站点原生音质 ID（bilibili 的 30280 等）。
	AudioQuality int
	// MaxHeight 是上限高度（像素），0 表示不限。
	MaxHeight int
	// AudioOnly 只取音频流。
	AudioOnly bool
	// Container 指定容器偏好（mp4/mkv/mov/m4a/flac）；空表示由编码决定。
	Container string
	// ExtraArgs 是站点透传参数，未识别时忽略。
	ExtraArgs []string
}

// Stream 是一条可选码流。
type Stream struct {
	ID        int
	Codec     string // 完整编码串，如 av01.0.08M.08、avc1.640028
	Width     int
	Height    int
	Bandwidth int64
	URL       string
	// Headers 是该码流下载时必须带的请求头（Referer/UA 等）。
	Headers http.Header
	// Container 是该码流落盘的容器扩展名，不含点。
	Container string
	// HLS 为 true 表示 URL 指向 m3u8，需由 fetch 层走 HLS 分片路径。
	HLS bool
	// Kind 是产物类型：video（默认）/ audio / image。图集站点用它区分单张图片。
	Kind string
}

// Extra 是随主媒体一起下载的附属资源（封面、弹幕、字幕）。
type Extra struct {
	Kind string // cover / danmaku / subtitle
	URL  string
	// Headers 同 Stream.Headers。
	Headers http.Header
	// Filename 是附属资源的文件名后缀片段，如 ".cover.jpg"、".danmaku.ass"。
	Filename string
	// Bytes 是已知大小，0 表示未知。
	Bytes int64
}

// StreamSet 是一个 Part 的全部可选码流。
type StreamSet struct {
	Videos []Stream
	Audios []Stream
	Extras []Extra
}
