package c

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Parse 解析命令行参数，填充 c 包的全局配置。
//
// 设计要点：
//  1. 同时接受 yutto 风格（--batch <url> -d <dir> -q 127 ...）与 bili-dl 原生风格
//     （-bv BVxxx -o <dir> -c SESSDATA ...）。
//  2. 未知参数一律忽略，不返回错误——流影的 extraArgs 可能透传任意 yutto 参数，
//     Go 的 flag 包遇到未知参数会 exit 2，这里必须避免。
//  3. 布尔项（--batch、--no-progress 等）不消费后续 token；只有“有值项”才吃下一个
//     token。否则 `--batch <url>` 会把 URL 误当成 batch 的值。
//  4. 位置参数收集到 URLs。
//
// args 不含程序名（即 os.Args[1:]）。
func Parse(args []string) {
	Defaults()
	Ctx = context.Background()

	// 需要吃一个值的选项（布尔项不在此列）。
	withValue := map[string]bool{
		// yutto 兼容
		"d": true, "tmp-dir": true, "q": true, "aq": true,
		"vcodec": true, "download-vcodec-priority": true, "output-format": true,
		"tp": true, "auth-file": true, "auth-profile": true,
		// bili-dl 原生
		"c": true, "up": true, "o": true, "j": true, "bv": true,
		"resolution": true, "codec": true, "rate": true,
		// PGC 番剧/剧集
		"ss": true, "season-id": true, "ep": true, "ep-id": true,
	}

	for i := 0; i < len(args); i++ {
		raw := args[i]
		if !strings.HasPrefix(raw, "-") || raw == "-" {
			URLs = append(URLs, raw)
			continue
		}
		name := strings.TrimLeft(raw, "-")
		value := ""
		hasValue := false
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			value = name[eq+1:]
			name = name[:eq]
			hasValue = true
		}
		key := strings.ToLower(name)
		if key == "" {
			continue
		}
		if withValue[key] && !hasValue && i+1 < len(args) {
			i++
			value = args[i]
			hasValue = true
		}
		applyFlag(key, value, hasValue)
	}
}

// boolValue: 显式给值就解析，否则按“出现即置真”。
func boolValue(value string, provided bool) bool {
	if !provided || value == "" {
		return true
	}
	if v, err := strconv.ParseBool(value); err == nil {
		return v
	}
	return true
}

func applyFlag(key, value string, provided bool) {
	switch key {
	// ---- yutto 兼容 ----
	case "batch":
		Batch = boolValue(value, provided)
	case "d":
		O = value // 下载目录（yutto 语义）
	case "tmp-dir":
		TmpDir = value
	case "q":
		if n, err := strconv.Atoi(value); err == nil {
			VideoQuality = n
		}
	case "aq":
		if n, err := strconv.Atoi(value); err == nil {
			AudioQuality = n
		}
	case "vcodec":
		VCodec = value
		// "av1:copy" -> 首选 av1；同步到旧 Codec 接口
		if base := strings.SplitN(value, ":", 2)[0]; base != "" {
			Codec = base
		}
	case "download-vcodec-priority":
		VCodecPriority = splitCSV(value)
	case "output-format":
		OutputFormat = value
	case "tp":
		SubpathTemplate = value
	case "auth-file":
		AuthFile = value
	case "auth-profile":
		AuthProfile = value
	case "no-danmaku":
		DownloadDanmaku = !boolValue(value, provided)
	case "no-subtitle":
		DownloadSubtitle = !boolValue(value, provided)
	case "save-cover":
		SaveCover = boolValue(value, provided)
	case "no-cover":
		SaveCover = !boolValue(value, provided)
	case "danmaku":
		DownloadDanmaku = boolValue(value, provided)
	case "subtitle":
		DownloadSubtitle = boolValue(value, provided)
	case "cover":
		SaveCover = boolValue(value, provided)
	case "no-progress":
		NoProgress = boolValue(value, provided)
	case "no-color":
		NoColor = boolValue(value, provided)
	case "progress-json":
		ProgressJSON = boolValue(value, provided)
	case "no-progress-json":
		ProgressJSON = !boolValue(value, provided)

	// ---- 番剧/剧集（PGC）----
	case "ss", "season-id":
		SeasonID = strings.TrimSpace(value)
	case "ep", "ep-id":
		EpID = strings.TrimSpace(value)
	case "collection":
		Collection = boolValue(value, provided)

	// ---- bili-dl 原生 ----
	case "c":
		Cookie = value
	case "up":
		UP = value
	case "o":
		O = value
	case "j":
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			J = n
		}
	case "bv":
		BVs = value
	case "m":
		Merge = boolValue(value, provided)
	case "delete":
		Delete = boolValue(value, provided)
	case "suffix":
		AddBVSuffix = boolValue(value, provided)
	case "no-overwrite":
		DisableOverwrite = boolValue(value, provided)
	case "i":
		InfoOnly = boolValue(value, provided)
	case "resolution":
		Resolution = value
	case "codec":
		Codec = value
	case "audio-only":
		AudioOnly = boolValue(value, provided)
	case "rate":
		RateLimit = value
		RateBytes = ParseRate(value)
	case "debug":
		Debug = boolValue(value, provided)
		// 未知参数：静默忽略
	}
}

// ParseRate 把限速字符串解析成每秒字节数。
// 支持纯字节数、"2m"/"500k"/"1.5M" 等写法；非法或 <=0 返回 0（不限速）。
func ParseRate(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}
	unit := float64(1)
	num := s
	switch {
	case strings.HasSuffix(s, "kib"), strings.HasSuffix(s, "kb"):
		unit, num = 1024, strings.TrimSuffix(strings.TrimSuffix(s, "kib"), "kb")
	case strings.HasSuffix(s, "mib"), strings.HasSuffix(s, "mb"):
		unit, num = 1024*1024, strings.TrimSuffix(strings.TrimSuffix(s, "mib"), "mb")
	case strings.HasSuffix(s, "gib"), strings.HasSuffix(s, "gb"):
		unit, num = 1024*1024*1024, strings.TrimSuffix(strings.TrimSuffix(s, "gib"), "gb")
	case strings.HasSuffix(s, "k"):
		unit, num = 1024, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		unit, num = 1024*1024, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "g"):
		unit, num = 1024*1024*1024, strings.TrimSuffix(s, "g")
	case strings.HasSuffix(s, "b"):
		num = strings.TrimSuffix(s, "b")
	}
	num = strings.TrimSpace(num)
	n, err := strconv.ParseFloat(num, 64)
	if err != nil || n <= 0 {
		return 0
	}
	bytes := n * unit
	if bytes <= 0 || bytes > float64(^uint64(0)>>1) {
		return 0
	}
	return int64(bytes)
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Describe 返回不含凭据的配置摘要，供日志使用。SESSDATA 永不出现在这里。
func Describe() string {
	return fmt.Sprintf("dir=%s batch=%v q=%d aq=%d tp=%s codec=%s priority=%v j=%d rate=%d pgc=%s/%s json=%v extras=%v/%v/%v",
		O, Batch, VideoQuality, AudioQuality, SubpathTemplate, Codec, VCodecPriority, J,
		RateBytes, SeasonID, EpID, ProgressJSON, DownloadDanmaku, DownloadSubtitle, SaveCover)
}
