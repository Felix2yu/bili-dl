package c

import (
	"context"
	"io"
)

// 全局配置。api 包直接读取这些变量；由 args.go 的 Parse 统一填充。
// 保留 liuying-engine 原生字段名（Cookie/O/UP/...），新增 yutto 兼容字段。
var (
	// 认证：SESSDATA 来自 -c 或从 --auth-file 指向的 TOML 读取。
	// 该值绝不出现在任何日志或输出中。
	Cookie      string
	AuthFile    string
	AuthProfile string

	// 下载目标与并发
	UP     string
	O      string // 下载目录（-d 的 yutto 语义，或 -o 别名）
	TmpDir string
	WD     string
	J      int
	BVs    string
	URLs   []string // 位置参数（完整 URL / BV 号）
	FFMPEG bool

	// 输出行为
	Merge            bool // -m：是否合并音视频流
	Delete           bool // -delete：合并后删除分离的流（原 -d）
	AddBVSuffix      bool // -suffix
	DisableOverwrite bool // -no-overwrite
	InfoOnly         bool // -i
	Debug            bool

	// 画质 / 音质
	Resolution     string   // -resolution：8k/dolby/4k/1080p/...（旧接口）
	Codec          string   // -codec：av1/hevc/avc（旧接口）
	VideoQuality   int      // -q：DASH 视频流 id（如 127）
	AudioQuality   int      // -aq：DASH 音频流 id（如 30280）
	VCodec         string   // --vcodec：如 "av1:copy"
	VCodecPriority []string // --download-vcodec-priority：av1,hevc,avc
	OutputFormat   string   // --output-format（容器仍按实际编码决定）
	AudioOnly      bool

	// 路径模板与分 P
	SubpathTemplate string // -tp：{owner_uname}/{title}
	Batch           bool   // --batch：展开投稿视频全部分 P

	// 附加内容（yutto 兼容开关）
	// 默认开启，对齐 yutto 行为；流影传 --no-* 显式关闭。
	DownloadDanmaku  bool
	DownloadSubtitle bool
	SaveCover        bool

	// 番剧/剧集（PGC）
	SeasonID   string // --ss / --season-id：如 23615
	EpID       string // --ep / --ep-id：如 123456
	Collection bool   // --collection：把视频所属合集整体展开

	// 运行控制
	NoProgress     bool
	NoColor        bool
	ProgressJSON   bool            // --progress-json：向 stdout 输出 NDJSON 进度事件
	ProgressWriter io.Writer       // NDJSON 的落点，nil 表示 os.Stdout；嵌入方需要接管输出时设置
	ResultJSON     string          // --result-json：下载结束后把产物清单写到该路径
	RateLimit      string          // -rate：限速，如 "2m" / "500k"
	RateBytes      int64           // 解析后的每秒字节上限，0 表示不限速
	Ctx            context.Context // 由 main 注入，贯穿下载与合并，响应 SIGTERM
)

func Defaults() {
	J = 1
	O = "."
	// 输入侧必须清空：Parse 会向 URLs 追加、向 BVs 赋值，
	// 同进程多次调用（内嵌方复用、测试）不清就会累积上一次的目标。
	URLs = nil
	BVs = ""
	Merge = true
	Delete = true
	AddBVSuffix = true
	DisableOverwrite = true
	Batch = false
	AudioOnly = false
	InfoOnly = false
	// 对齐 yutto：默认下载弹幕/字幕/封面，--no-* 显式关闭。
	DownloadDanmaku = true
	DownloadSubtitle = true
	SaveCover = true
	NoProgress = false
	NoColor = false
	ProgressJSON = false
	ResultJSON = ""
	RateLimit = ""
	RateBytes = 0
	SeasonID = ""
	EpID = ""
	Collection = false
	VideoQuality = 0
	AudioQuality = 0
	VCodecPriority = nil
}

const (
	GetAllBV = `Array.from(document.querySelectorAll('#submit-video-list > ul.clearfix.cube-list > li')).map(e=>e.dataset['bvid']).join(',')`
)
