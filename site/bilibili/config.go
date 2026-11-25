package bilibili

import (
	"strings"

	"github.com/Felix2yu/liuying-engine/c"
)

// streamConfig 是一次选流所需的全部输入。
//
// 存在的意义：旧实现直接从 c 包全局变量读这些值，导致任何两次并发调用互相踩参数
// （bili-history 因此不得不用一把大锁串行化下载）。把输入收成一个值之后，
// 选流逻辑变成纯函数式，调用方可以按任务传自己的配置。
//
// 注意：HTTP 层的凭据与取消信号（c.Cookie / c.Ctx）本期仍是全局的，
// 那部分由 P2 的 auth 抽象统一处理；本类型只覆盖「选流」这一段。
type streamConfig struct {
	// Resolution 是画质档位短名：8k/dolby/4k/1080p/720p/480p/360p；空表示不限。
	Resolution string
	// AudioOnly 只取音频流。
	AudioOnly bool
	// VCodecPriority 按优先级排列的编码短名；为空且 Codec 为空时不限制编码。
	VCodecPriority []string
	// Codec 是旧接口的单编码限制；优先级列表非空时被忽略。
	Codec string
	// VideoQuality 是目标 DASH 视频流 id；0 表示自动择优。
	VideoQuality int
	// AudioQuality 是目标 DASH 音频流 id；0 表示取码率最高。
	AudioQuality int
}

// currentStreamConfig 把 c 包全局变量快照成一个 streamConfig。
// 保留它是为了兼容期：旧调用方（CLI、bili-history）只设置全局变量，
// 不需要改任何代码就能走新路径。
func currentStreamConfig() streamConfig {
	return streamConfig{
		Resolution:     c.Resolution,
		AudioOnly:      c.AudioOnly,
		VCodecPriority: c.VCodecPriority,
		Codec:          c.Codec,
		VideoQuality:   c.VideoQuality,
		AudioQuality:   c.AudioQuality,
	}
}

// codecLabel 只用于错误消息，让用户看到实际尝试过的编码范围。
func (cfg streamConfig) codecLabel() string {
	if len(cfg.VCodecPriority) > 0 {
		return strings.Join(cfg.VCodecPriority, ",")
	}
	if cfg.Codec != "" {
		return cfg.Codec
	}
	return "任意"
}
