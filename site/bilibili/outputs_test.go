package bilibili

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Felix2yu/bili-dl/c"
	"github.com/Felix2yu/bili-dl/site"
)

// withTempOutputDir 把 c.O 指向临时目录，测试结束后还原。
// c.O 是全局变量，因此本测试不能与其它改 c.O 的用例并行。
func withTempOutputDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	saved := c.O
	c.O = dir
	t.Cleanup(func() { c.O = saved })
	return dir
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}

func kindsOf(outs []site.Output) []string {
	out := make([]string, 0, len(outs))
	for _, o := range outs {
		out = append(out, o.Kind)
	}
	return out
}

func TestStreamOutputsReportsExistingFiles(t *testing.T) {
	dir := withTempOutputDir(t)
	s := &Stream{
		relBase: filepath.Join("某UP", "标题"),
		V:       "https://example/v",
		A:       "https://example/a",
		VCodec:  "avc1.640028",
		ACodec:  "mp4a.40.2",
		Video:   Video{BV: "BV1xx411c7mD", ActivePage: 1},
	}

	writeFile(t, s.VideoPath(), 100)
	writeFile(t, s.AudioPath(), 20)
	writeFile(t, filepath.Join(dir, s.relBase)+".cover.jpg", 7)

	outs := s.Outputs()
	if len(outs) != 3 {
		t.Fatalf("应回报 3 条产物，实际 %d: %+v", len(outs), outs)
	}
	if got := kindsOf(outs); got[0] != "video" || got[1] != "audio" || got[2] != "cover" {
		t.Errorf("产物类型顺序错误: %v", got)
	}
	if outs[0].Bytes != 100 || outs[1].Bytes != 20 {
		t.Errorf("字节数错误: %+v", outs)
	}
	if outs[0].Container != "mp4" || outs[1].Container != "m4a" {
		t.Errorf("容器错误: video=%q audio=%q", outs[0].Container, outs[1].Container)
	}
	if outs[2].Container != "" {
		t.Errorf("非媒体文件不应带容器，实际 %q", outs[2].Container)
	}

	// 合并后删除分离音轨：应自动从产出里消失，而不是留下幻影条目。
	if err := os.Remove(s.AudioPath()); err != nil {
		t.Fatalf("删除音轨失败: %v", err)
	}
	outs = s.Outputs()
	if len(outs) != 2 {
		t.Fatalf("音轨删除后应剩 2 条，实际 %d", len(outs))
	}
	for _, o := range outs {
		if o.Kind == "audio" {
			t.Error("已删除的音轨不应出现在产出里")
		}
	}
}

func TestStreamOutputsCollectsSubtitles(t *testing.T) {
	dir := withTempOutputDir(t)
	s := &Stream{relBase: filepath.Join("UP", "标题"), V: "v", VCodec: "av01.0.08M.08"}
	base := filepath.Join(dir, s.relBase)
	writeFile(t, base+".zh.srt", 1)
	writeFile(t, base+".en.srt", 1)
	writeFile(t, base+".danmaku.ass", 1)

	outs := s.Outputs()
	var subs, danmaku int
	for _, o := range outs {
		switch o.Kind {
		case "subtitle":
			subs++
		case "danmaku":
			danmaku++
		}
	}
	if subs != 2 {
		t.Errorf("应收集 2 条字幕，实际 %d", subs)
	}
	if danmaku != 1 {
		t.Errorf("应收集 1 条弹幕，实际 %d", danmaku)
	}
	if len(outs) != 3 {
		t.Errorf("总产出应为 3，实际 %d: %v", len(outs), kindsOf(outs))
	}
}

func TestStreamOutputsEmptyRelBase(t *testing.T) {
	withTempOutputDir(t)
	s := &Stream{V: "v", A: "a"}
	if outs := s.Outputs(); outs != nil {
		t.Errorf("未下载时不应有产物，实际 %+v", outs)
	}
}

func TestOutputOfSkipsMissingAndDir(t *testing.T) {
	dir := withTempOutputDir(t)
	if outs := outputOf(filepath.Join(dir, "missing.mp4"), "video"); outs != nil {
		t.Errorf("不存在的文件不应产生产出，实际 %+v", outs)
	}
	if outs := outputOf(dir, "video"); outs != nil {
		t.Errorf("目录不应产生产出，实际 %+v", outs)
	}
}

func TestNormalizeKindFallback(t *testing.T) {
	// 旧调用方可能把扩展名当 kind 传进来（downloadFile 历史行为），这里兜底修正。
	cases := map[string]string{
		".mp4": "video", ".mkv": "video",
		".m4a": "audio", ".flac": "audio",
		".jpg": "cover", ".webp": "cover",
		".ass": "danmaku", ".srt": "subtitle",
	}
	for kind, want := range cases {
		if got := normalizeKind(kind, "x"+kind); got != want {
			t.Errorf("kind=%q: want %q, got %q", kind, want, got)
		}
	}
	if got := normalizeKind("video", "/x/y.mp4"); got != "video" {
		t.Errorf("合法 kind 不应被改写，实际 %q", got)
	}
}

func TestDoneEventCarriesOutputs(t *testing.T) {
	// 锁住 NDJSON / hook 的契约：done 事件必须自带产物，调用方才不必扫目录。
	dir := withTempOutputDir(t)
	payload := []byte("0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	var mu sync.Mutex
	var events []site.Event
	SetProgressHook(func(ev site.Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	defer SetProgressHook(nil)

	path := filepath.Join(dir, "x.mp4")
	if err := downloadFileWith(srv.URL, path, downloadMeta{BV: "BV1xx411c7mD", Page: 1, Kind: "video"}); err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var done *site.Event
	for i := range events {
		if events[i].Event == "done" {
			done = &events[i]
		}
	}
	if done == nil {
		t.Fatalf("缺少 done 事件，实际事件: %+v", events)
	}
	if done.MediaID != "BV1xx411c7mD" || done.BV != "BV1xx411c7mD" {
		t.Errorf("MediaID/BV 未填充: %+v", done)
	}
	if len(done.Outputs) != 1 {
		t.Fatalf("done 应带 1 条产出，实际 %+v", done.Outputs)
	}
	o := done.Outputs[0]
	if o.Path != path || o.Kind != "video" || o.Container != "mp4" || o.Bytes != int64(len(payload)) {
		t.Errorf("产出字段错误: %+v", o)
	}
}
