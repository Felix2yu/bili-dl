// Package api 是哔哩哔哩实现的兼容层。
//
// 历史背景：这个包原本同时是「站点逻辑」与「对外 API」，bili-history 直接依赖它
// （github.com/Felix2yu/liuying-engine/api 与 /c）。重构把站点逻辑整体迁到
// site/bilibili 之后，本包只保留导出符号的转发，确保既有调用方零改动。
//
// 约束：本文件只允许出现类型别名与一行转发，不得再写业务逻辑。
// 新代码请直接使用 site 与 engine 包。
package api

import "github.com/Felix2yu/liuying-engine/site/bilibili"

// 类型别名：用别名而非新类型，保证与 site/bilibili 完全等价，
// 调用方传值时不需要任何转换。
type (
	Video         = bilibili.Video
	Stream        = bilibili.Stream
	VideoOption   = bilibili.VideoOption
	AudioOption   = bilibili.AudioOption
	ProgressEvent = bilibili.ProgressEvent
)

// ResolveVideo 补齐视频详情（标题、UP 主、封面、全部分 P）。
func ResolveVideo(v *Video) (*Video, error) { return bilibili.ResolveVideo(v) }

// VideoForPage 返回某个分 P 对应的下载目标（1-based）。
func VideoForPage(v *Video, page int) (Video, error) { return bilibili.VideoForPage(v, page) }

// AllVideo 拉取 UP 主的全部投稿（只有 BV 号）。
func AllVideo(mid string) ([]Video, error) { return bilibili.AllVideo(mid) }

// GetStream 按当前全局配置选出要下载的码流。
func GetStream(v Video) (*Stream, error) { return bilibili.GetStream(v) }

// PrintDASHInfo 打印全部 DASH 码流信息，不下载。
func PrintDASHInfo(v Video) error { return bilibili.PrintDASHInfo(v) }

// Dl 下载 stream 指向的视频流与音频流。
func Dl(stream *Stream) error { return bilibili.Dl(stream) }

// DV 只下载视频流。
func DV(stream *Stream) error { return bilibili.DV(stream) }

// DA 只下载音频流。
func DA(stream *Stream) error { return bilibili.DA(stream) }

// VideoFromBV 由 BV 号取视频信息。
func VideoFromBV(bv string) (*Video, error) { return bilibili.VideoFromBV(bv) }

// Merge 用 ffmpeg 合并音视频流。
func Merge(stream *Stream) error { return bilibili.Merge(stream) }

// LoadSessdata 从 yutto 兼容的 auth.toml 读取指定 profile 的 SESSDATA。
func LoadSessdata(path, profile string) (string, error) {
	return bilibili.LoadSessdata(path, profile)
}

// DownloadExtras 下载弹幕、字幕、封面（best-effort）。
func DownloadExtras(stream *Stream) { bilibili.DownloadExtras(stream) }

// ListOptions 列出全部可选视频/音频码流。
func ListOptions(v Video) ([]VideoOption, []AudioOption, error) { return bilibili.ListOptions(v) }

// ParseInput 解析用户输入为 BV 号与起始分 P。
func ParseInput(input string) (bv string, page int, err error) { return bilibili.ParseInput(input) }

// ParsePGCSInput 识别番剧输入（ss/ep）。
func ParsePGCSInput(raw string) (kind, id string) { return bilibili.ParsePGCSInput(raw) }

// SeasonVideos 拉取番剧/剧集的剧集列表。
func SeasonVideos(seasonID, epID string) ([]Video, error) {
	return bilibili.SeasonVideos(seasonID, epID)
}

// ExpandPages 按分 P 与批量规则展开下载目标。
func ExpandPages(v *Video, startPage int, batch bool) []Video {
	return bilibili.ExpandPages(v, startPage, batch)
}

// ExpandUGCSeason 把投稿所属合集展开成多个视频。
func ExpandUGCSeason(base *Video) []Video { return bilibili.ExpandUGCSeason(base) }

// BuildSeasonTargets 按全局的 --ss/--ep 参数构造番剧下载目标。
func BuildSeasonTargets() ([]Video, error) { return bilibili.BuildSeasonTargets() }

// SetProgressHook 注册进程内进度回调。传 nil 取消注册。
func SetProgressHook(fn func(ProgressEvent)) { bilibili.SetProgressHook(fn) }

// RenderPath 按当前 -tp 模板渲染相对产物路径。
func RenderPath(v *Video, ext string) string { return bilibili.RenderPath(v, ext) }
