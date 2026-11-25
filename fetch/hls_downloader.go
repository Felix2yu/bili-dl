package fetch

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// HLSOptions 控制一次 HLS 下载。
type HLSOptions struct {
	// OutPath 是最终产物路径，父目录会被创建。
	OutPath string
	// Headers 会附加到清单与分片请求上（Referer/UA 等，站点强校验时必须带）。
	Headers http.Header
	// Client 为空时用 http.DefaultClient。
	Client *http.Client
	// Concurrency 是分片并发数，<=0 时取 4。
	Concurrency int
	// TempDir 是分片落盘目录，空则由系统临时目录创建。
	TempDir string
	// Progress 在每个分片完成后回调；total 为分片总数。
	Progress func(done, total int64)
}

// DownloadHLS 下载一个 HLS 点播源到单个文件。
//
// 只支持点播（VOD）：直播清单会持续增长，语义不同，这里不做。
// 不支持 DRM（SAMPLE-AES / 密钥系统）：解析阶段就会明确报错，而不是产出坏文件。
func DownloadHLS(ctx context.Context, playlistURL string, opt HLSOptions) error {
	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}
	base, err := url.Parse(playlistURL)
	if err != nil {
		return fmt.Errorf("清单地址无效: %w", err)
	}

	// master → variant：最多下钻两层，防止清单互相引用导致死循环。
	body, err := getBytes(ctx, client, playlistURL, opt.Headers, 0, 0)
	if err != nil {
		return err
	}
	level := 0
	for IsMaster(body) && level < 2 {
		variants, err := ParseMasterPlaylist(body, base)
		if err != nil {
			return err
		}
		best, ok := BestVariant(variants)
		if !ok {
			return fmt.Errorf("没有可用码流")
		}
		if base, err = url.Parse(best.URI); err != nil {
			return err
		}
		if body, err = getBytes(ctx, client, best.URI, opt.Headers, 0, 0); err != nil {
			return err
		}
		level++
	}

	playlist, err := ParseMediaPlaylist(body, base)
	if err != nil {
		return err
	}

	var keyBytes []byte
	if playlist.Key.Method == "AES-128" {
		keyBytes, err = getBytes(ctx, client, playlist.Key.URI, opt.Headers, 0, 0)
		if err != nil {
			return fmt.Errorf("取解密密钥失败: %w", err)
		}
		if len(keyBytes) != 16 {
			return fmt.Errorf("AES-128 密钥长度不是 16 字节（%d）", len(keyBytes))
		}
	}

	return downloadSegments(ctx, client, playlist, keyBytes, opt)
}

func downloadSegments(
	ctx context.Context,
	client *http.Client,
	playlist *MediaPlaylist,
	key []byte,
	opt HLSOptions,
) error {
	dir, err := os.MkdirTemp(opt.TempDir, "hls-")
	if err != nil {
		return fmt.Errorf("创建分片目录失败: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if err := os.MkdirAll(filepath.Dir(opt.OutPath), 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	// 先落初始化段（fMP4 必需），再按序拼接分片。
	if playlist.MapURI != "" {
		var start, length int64
		if len(playlist.MapRange) == 2 {
			start, length = playlist.MapRange[0], playlist.MapRange[1]
		}
		data, err := getBytes(ctx, client, playlist.MapURI, opt.Headers, start, length)
		if err != nil {
			return fmt.Errorf("取初始化段失败: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "init"), data, 0o644); err != nil {
			return err
		}
	}

	total := int64(len(playlist.Segments))
	var done atomic.Int64
	concurrency := opt.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	if concurrency > len(playlist.Segments) {
		concurrency = len(playlist.Segments)
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)
	var wg sync.WaitGroup
	var firstErr atomic.Value

	recordErr := func(err error) {
		// 只保留第一个错误，后续并发失败都是它的连锁反应。
		if firstErr.CompareAndSwap(nil, err) {
			cancel()
		}
	}

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if err := fetchSegment(workCtx, client, playlist, idx, key, dir, opt.Headers); err != nil {
					recordErr(err)
					continue
				}
				n := done.Add(1)
				if opt.Progress != nil {
					opt.Progress(n, total)
				}
			}
		}()
	}

dispatch:
	for idx := range playlist.Segments {
		select {
		case <-workCtx.Done():
			break dispatch
		case jobs <- idx:
		}
	}
	close(jobs)
	wg.Wait()

	if err := workCtx.Err(); err != nil && ctx.Err() == nil {
		if e, ok := firstErr.Load().(error); ok {
			return e
		}
		return fmt.Errorf("分片下载中断: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if done.Load() != total {
		if e, ok := firstErr.Load().(error); ok {
			return fmt.Errorf("有分片下载失败（完成 %d/%d）: %w", done.Load(), total, e)
		}
		return fmt.Errorf("有分片下载失败（完成 %d/%d）", done.Load(), total)
	}

	return concat(dir, playlist, opt.OutPath)
}

// fetchSegment 下载并（必要时）解密单个分片，落到 dir 下的定长文件名。
func fetchSegment(
	ctx context.Context,
	client *http.Client,
	playlist *MediaPlaylist,
	idx int,
	key []byte,
	dir string,
	headers http.Header,
) error {
	seg := playlist.Segments[idx]
	var start, length int64
	if seg.HasRange {
		start, length = seg.RangeStart, seg.RangeLen
	}
	data, err := getBytes(ctx, client, seg.URI, headers, start, length)
	if err != nil {
		return fmt.Errorf("分片 %d: %w", idx, err)
	}
	if playlist.Key.Method == "AES-128" {
		if data, err = decryptAES128(data, key, playlist.Key.IV, seg.Seq); err != nil {
			return fmt.Errorf("分片 %d 解密失败: %w", idx, err)
		}
	}
	return os.WriteFile(filepath.Join(dir, segmentName(idx)), data, 0o644)
}

func segmentName(idx int) string { return fmt.Sprintf("seg-%08d", idx) }

// concat 按顺序拼接到最终文件：先写初始化段，再逐个分片。
func concat(dir string, playlist *MediaPlaylist, outPath string) error {
	tmp := outPath + ".hls-tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("创建临时输出失败: %w", err)
	}
	defer func() { _ = os.Remove(tmp) }()

	appendFile := func(path string) error {
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(f, src)
		return err
	}

	if playlist.MapURI != "" {
		if err := appendFile(filepath.Join(dir, "init")); err != nil {
			_ = f.Close()
			return fmt.Errorf("拼接初始化段失败: %w", err)
		}
	}
	for idx := range playlist.Segments {
		if err := appendFile(filepath.Join(dir, segmentName(idx))); err != nil {
			_ = f.Close()
			return fmt.Errorf("拼接分片 %d 失败: %w", idx, err)
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, outPath)
}

// getBytes 取一段字节。length>0 时发 Range 请求。
func getBytes(
	ctx context.Context,
	client *http.Client,
	rawURL string,
	headers http.Header,
	start, length int64,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if length > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+length-1))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if length > 0 {
		if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
	} else if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// decryptAES128 按 HLS 规范做 CBC 解密并去 PKCS7 填充。
// iv 为 nil 时用分片序号补零成 16 字节作为 IV。
func decryptAES128(data, key, iv []byte, seq int) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("密文长度不是块大小的整数倍（%d）", len(data))
	}
	useIV := iv
	if len(useIV) == 0 {
		useIV = make([]byte, 16)
		for i := 0; i < 8; i++ {
			useIV[15-i] = byte(seq >> (8 * i))
		}
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, useIV).CryptBlocks(out, data)

	// 去 PKCS7 填充；填充非法时保留原文（某些源并不规范，宁可多留字节）。
	if n := int(out[len(out)-1]); n > 0 && n <= aes.BlockSize && n <= len(out) {
		valid := true
		for i := len(out) - n; i < len(out); i++ {
			if int(out[i]) != n {
				valid = false
				break
			}
		}
		if valid {
			out = out[:len(out)-n]
		}
	}
	return out, nil
}
