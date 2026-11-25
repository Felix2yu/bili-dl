package fetch

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("解析 URL 失败: %v", err)
	}
	return u
}

func TestParseMasterPlaylistPicksBest(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360,CODECS="avc1.4d401e,mp4a.40.2"
low/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2400000,RESOLUTION=1280x720,CODECS="avc1.640020,mp4a.40.2"
high/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2400000,RESOLUTION=1920x1080
higher/index.m3u8
`)
	variants, err := ParseMasterPlaylist(body, mustURL(t, "https://cdn.example/x/master.m3u8"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(variants) != 3 {
		t.Fatalf("应解析出 3 条码流，实际 %d", len(variants))
	}
	if variants[0].Bandwidth != 800000 || variants[0].Height != 360 {
		t.Errorf("属性解析错误: %+v", variants[0])
	}
	// CODECS 里的逗号不能被当成属性分隔符。
	if variants[0].Codecs != "avc1.4d401e,mp4a.40.2" {
		t.Errorf("CODECS = %q", variants[0].Codecs)
	}
	if !strings.HasSuffix(variants[1].URI, "/high/index.m3u8") {
		t.Errorf("相对地址未解析: %q", variants[1].URI)
	}

	best, ok := BestVariant(variants)
	if !ok {
		t.Fatal("应有最优码流")
	}
	// 同码率时取分辨率更高的那条。
	if !strings.HasSuffix(best.URI, "/higher/index.m3u8") {
		t.Errorf("最优码流选择错误: %q", best.URI)
	}
}

func TestParseMediaPlaylist(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-VERSION:7
#EXT-X-TARGETDURATION:4
#EXT-X-MEDIA-SEQUENCE:10
#EXT-X-MAP:URI="init.mp4"
#EXTINF:4.0,
seg0.m4s
#EXTINF:3.5,
#EXT-X-BYTERANGE:1000@500
media.mp4
#EXTINF:3.0,
#EXT-X-BYTERANGE:500
media.mp4
`)
	pl, err := ParseMediaPlaylist(body, mustURL(t, "https://cdn.example/v/index.m3u8"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if pl.MapURI != "https://cdn.example/v/init.mp4" {
		t.Errorf("MapURI = %q", pl.MapURI)
	}
	if len(pl.Segments) != 3 {
		t.Fatalf("分片数 = %d", len(pl.Segments))
	}
	if pl.Segments[0].Seq != 10 || pl.Segments[1].Seq != 11 {
		t.Errorf("序号未从 MEDIA-SEQUENCE 起算: %+v", pl.Segments)
	}
	// 显式偏移
	if !pl.Segments[1].HasRange || pl.Segments[1].RangeStart != 500 || pl.Segments[1].RangeLen != 1000 {
		t.Errorf("显式 BYTERANGE 解析错误: %+v", pl.Segments[1])
	}
	// 省略偏移时应紧接上一片
	if !pl.Segments[2].HasRange || pl.Segments[2].RangeStart != 1500 || pl.Segments[2].RangeLen != 500 {
		t.Errorf("省略偏移的 BYTERANGE 应紧接上一片: %+v", pl.Segments[2])
	}
}

func TestParseMediaPlaylistRejectsUnsupportedEncryption(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-KEY:METHOD=SAMPLE-AES,URI="skd://x"
#EXTINF:4,
a.ts
`)
	if _, err := ParseMediaPlaylist(body, mustURL(t, "https://c/x.m3u8")); err == nil {
		t.Fatal("SAMPLE-AES 应明确报错，而不是静默产出坏文件")
	}
}

// hlsServer 起一个提供 master/variant/分片的测试服务。
type hlsServer struct {
	*httptest.Server
	segments [][]byte
	requests map[string]int
}

func newHLSServer(t *testing.T, segments [][]byte, masterBody string) *hlsServer {
	t.Helper()
	h := &hlsServer{segments: segments, requests: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		h.requests["master"]++
		_, _ = w.Write([]byte(masterBody))
	})
	mux.HandleFunc("/v/index.m3u8", func(w http.ResponseWriter, r *http.Request) {
		h.requests["index"]++
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n")
		for i := range segments {
			fmt.Fprintf(&b, "#EXTINF:4.0,\nseg%d.ts\n", i)
		}
		_, _ = w.Write([]byte(b.String()))
	})
	for i := range segments {
		idx := i
		mux.HandleFunc(fmt.Sprintf("/v/seg%d.ts", idx), func(w http.ResponseWriter, r *http.Request) {
			// 分片必须带站点要求的请求头，否则真实站点会 403。
			if r.Header.Get("Referer") == "" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write(segments[idx])
		})
	}
	h.Server = httptest.NewServer(mux)
	t.Cleanup(h.Close)
	return h
}

func TestDownloadHLSConcatenatesInOrder(t *testing.T) {
	segs := [][]byte{[]byte("AAA"), []byte("BBB"), []byte("CCC"), []byte("DDD")}
	srv := newHLSServer(t, segs, `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=640x360
v/index.m3u8
`)

	out := filepath.Join(t.TempDir(), "nested", "out.ts")
	var done, total int64
	err := DownloadHLS(context.Background(), srv.URL+"/master.m3u8", HLSOptions{
		OutPath:     out,
		Headers:     http.Header{"Referer": []string{"https://x.example/"}},
		Concurrency: 3,
		Progress: func(d, tt int64) {
			done, total = d, tt
		},
	})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读产物失败: %v", err)
	}
	if string(got) != "AAABBBCCCDDD" {
		t.Errorf("拼接顺序错误: %q", got)
	}
	if done != 4 || total != 4 {
		t.Errorf("进度回调 done=%d total=%d", done, total)
	}
	// 临时目录不应残留。
	entries, _ := os.ReadDir(filepath.Dir(out))
	if len(entries) != 1 {
		t.Errorf("输出目录残留了临时文件: %v", entries)
	}
}

func TestDownloadHLSFailsOnSegmentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v/seg") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/master.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nv/index.m3u8\n"))
		case "/v/index.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:4,\nseg0.ts\n"))
		}
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "out.ts")
	err := DownloadHLS(context.Background(), srv.URL+"/master.m3u8", HLSOptions{OutPath: out})
	if err == nil {
		t.Fatal("分片 500 应报错")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("失败时不应留下产物文件")
	}
}

func TestDownloadHLSByteRangeSegments(t *testing.T) {
	// 一个 9 字节文件，按 3 字节切三段。
	blob := []byte("123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			_, _ = w.Write([]byte(`#EXTM3U
#EXTINF:1,
#EXT-X-BYTERANGE:3@0
blob.mp4
#EXTINF:1,
#EXT-X-BYTERANGE:3@3
blob.mp4
#EXTINF:1,
#EXT-X-BYTERANGE:3@6
blob.mp4
`))
		case "/blob.mp4":
			http.ServeContent(w, r, "blob.mp4", time.Time{}, bytes.NewReader(blob))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := DownloadHLS(context.Background(), srv.URL+"/index.m3u8", HLSOptions{OutPath: out}); err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "123456789" {
		t.Errorf("字节范围拼接错误: %q", got)
	}
}

func TestDownloadHLSDecryptsAES128(t *testing.T) {
	key := []byte("0123456789abcdef")
	plain := [][]byte{[]byte("hello-"), []byte("world!")}

	// 按 HLS 规范：IV 缺省时用分片序号补零。
	encrypt := func(data []byte, seq int) []byte {
		block, _ := aes.NewCipher(key)
		iv := make([]byte, 16)
		for i := 0; i < 8; i++ {
			iv[15-i] = byte(seq >> (8 * i))
		}
		pad := aes.BlockSize - len(data)%aes.BlockSize
		padded := append(append([]byte{}, data...), bytes.Repeat([]byte{byte(pad)}, pad)...)
		out := make([]byte, len(padded))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
		return out
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/index.m3u8", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="key.bin"
#EXTINF:1,
e0.ts
#EXTINF:1,
e1.ts
`))
	})
	mux.HandleFunc("/key.bin", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(key) })
	for i := range plain {
		idx := i
		mux.HandleFunc(fmt.Sprintf("/e%d.ts", idx), func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(encrypt(plain[idx], idx))
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "out.ts")
	if err := DownloadHLS(context.Background(), srv.URL+"/index.m3u8", HLSOptions{OutPath: out}); err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "hello-world!" {
		t.Errorf("解密结果错误: %q", got)
	}
}

func TestDownloadHLSRespectsContextCancel(t *testing.T) {
	segs := [][]byte{[]byte("A"), []byte("B")}
	srv := newHLSServer(t, segs, `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000
v/index.m3u8
`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := filepath.Join(t.TempDir(), "out.ts")
	err := DownloadHLS(ctx, srv.URL+"/master.m3u8", HLSOptions{OutPath: out})
	if err == nil {
		t.Fatal("已取消的 ctx 应报错")
	}
}
