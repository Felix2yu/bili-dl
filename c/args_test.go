package c

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseYuttoArgv(t *testing.T) {
	// 流影 YuttoArgs.build 生成的典型形态。注意 --batch 后紧跟 URL，
	// 不能被误当成 batch 的值；-tp 的值含斜杠也要完整保留。
	args := []string{
		"--batch",
		"https://www.bilibili.com/video/BV1XY411o7Cv/",
		"-d", "/Users/me/Movies",
		"--no-progress", "--no-color",
		"--tmp-dir", "/tmp/work",
		"-q", "127", "-aq", "30280",
		"--vcodec", "av1:copy",
		"--download-vcodec-priority", "av1,hevc,avc",
		"--output-format", "mp4",
		"-tp", "{owner_uname}/{title}",
		"--no-danmaku", "--no-subtitle", "--no-cover",
		"--auth-file", "/Users/me/.config/yutto/auth.toml",
		"--auth-profile", "default",
	}

	Parse(args)

	if got, want := URLs, []string{"https://www.bilibili.com/video/BV1XY411o7Cv/"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("URLs = %v, want %v", got, want)
	}
	if O != "/Users/me/Movies" {
		t.Errorf("O = %q, want download dir", O)
	}
	if !Batch {
		t.Error("Batch = false, want true")
	}
	if VideoQuality != 127 || AudioQuality != 30280 {
		t.Errorf("q/aq = %d/%d, want 127/30280", VideoQuality, AudioQuality)
	}
	if VCodec != "av1:copy" || Codec != "av1" {
		t.Errorf("vcodec = %q, codec = %q", VCodec, Codec)
	}
	if got, want := VCodecPriority, []string{"av1", "hevc", "avc"}; !reflect.DeepEqual(got, want) {
		t.Errorf("priority = %v, want %v", got, want)
	}
	if SubpathTemplate != "{owner_uname}/{title}" {
		t.Errorf("tp = %q", SubpathTemplate)
	}
	if AuthFile != "/Users/me/.config/yutto/auth.toml" || AuthProfile != "default" {
		t.Errorf("auth = %q/%q", AuthFile, AuthProfile)
	}
	if DownloadDanmaku || DownloadSubtitle || SaveCover {
		t.Error("no-* flags not applied")
	}
	if !NoProgress || !NoColor {
		t.Error("no-progress/no-color not applied")
	}
}

func TestParseIgnoresUnknownFlags(t *testing.T) {
	// extraArgs 可能透传任意 yutto 参数，未知项必须静默忽略，且不能吞掉 URL。
	args := []string{
		"https://www.bilibili.com/video/BV1XY411o7Cv/",
		"--some-yutto-only-flag", "123",
		"-d", "/tmp/out",
	}
	Parse(args)
	if len(URLs) == 0 || URLs[0] != "https://www.bilibili.com/video/BV1XY411o7Cv/" {
		t.Fatalf("URL 被吞或错位: %v", URLs)
	}
	// "123" 作为未知 flag 的裸值会落进 URLs，但不影响第一个 URL 是目标。
	if O != "/tmp/out" {
		t.Errorf("O = %q", O)
	}
}

func TestParseLegacyFlags(t *testing.T) {
	Parse([]string{"-bv", "BV1uP4y1S7ps,BV1XY411o7Cv", "-o", "/tmp/x", "-c", "sess", "-j", "3", "-i"})
	if BVs != "BV1uP4y1S7ps,BV1XY411o7Cv" {
		t.Errorf("BVs = %q", BVs)
	}
	if O != "/tmp/x" || Cookie != "sess" || J != 3 || !InfoOnly {
		t.Errorf("legacy parse wrong: O=%q Cookie=%q J=%d InfoOnly=%v", O, Cookie, J, InfoOnly)
	}
}

func TestBoolExplicitFalse(t *testing.T) {
	Parse([]string{"-m=false", "--batch=false", "--no-cover=false"})
	if Merge {
		t.Error("Merge should be false")
	}
	if Batch {
		t.Error("Batch should be false")
	}
	// --no-cover=false 表示“否 不保存封面” = 保存封面。
	if !SaveCover {
		t.Error("SaveCover should be true from --no-cover=false")
	}
}

func TestDescribeHasNoCookie(t *testing.T) {
	Parse([]string{"-c", "SUPER_SECRET_SESSDATA", "-d", "/tmp"})
	if desc := Describe(); strings.Contains(desc, "SUPER_SECRET_SESSDATA") {
		t.Fatalf("Describe 泄漏了 SESSDATA: %s", desc)
	}
}

func TestParseResultJSONFlag(t *testing.T) {
	// --result-json 必须吃到一个值，否则后面的 URL 会被误当成路径。
	Parse([]string{
		"--progress-json",
		"--result-json", "/tmp/out/result.json",
		"https://www.bilibili.com/video/BV1XY411o7Cv/",
	})

	if ResultJSON != "/tmp/out/result.json" {
		t.Errorf("ResultJSON = %q, want /tmp/out/result.json", ResultJSON)
	}
	if got, want := URLs, []string{"https://www.bilibili.com/video/BV1XY411o7Cv/"}; !reflect.DeepEqual(got, want) {
		t.Errorf("URLs = %v, want %v（URL 不能被当成 result-json 的值）", got, want)
	}

	// 等号形态同样要支持。
	Parse([]string{"--result-json=/tmp/r.json", "BV1XY411o7Cv"})
	if ResultJSON != "/tmp/r.json" {
		t.Errorf("等号形态解析失败，ResultJSON = %q", ResultJSON)
	}

	// Defaults 必须把上一次的值清空，避免同进程复用（bili-history 内嵌调用）串味。
	Parse([]string{"BV1XY411o7Cv"})
	if ResultJSON != "" {
		t.Errorf("未传 flag 时应为空，实际 %q", ResultJSON)
	}
}
