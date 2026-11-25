package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Felix2yu/bili-dl/fetch"
	"github.com/Felix2yu/bili-dl/site"
)

// Download 是引擎的统一入口：解析 → 选流 → 传输 → 收尾。
//
// 目前支持的传输形态：HLS 点播（m3u8）与直链文件（Range 续传）。
// 音视频分离需要合并的站点（哔哩哔哩）仍走各自的既有实现，
// 因为那套逻辑已经包含 DASH 选流、弹幕字幕与容器策略，强行搬进来只会更脆。
func (e *Engine) Download(ctx context.Context, req Request, sink EventSink) (*Result, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, NewError(CodeUnsupportedURL, StageResolve, "链接为空")
	}
	if req.OutputDir == "" {
		return nil, NewError(CodeUnsupportedURL, StageResolve, "缺少输出目录")
	}

	media, err := e.Resolve(ctx, req.URL, site.ResolveOptions{Auth: req.Auth, Client: e.client})
	if err != nil {
		return nil, err
	}

	result := &Result{MediaID: media[0].ID}
	var failures []string
	total := 0

	for _, m := range media {
		parts := m.Parts
		if len(parts) == 0 {
			parts = []site.Part{{Index: 1, Title: m.Title}}
		}
		for _, part := range parts {
			total++
			outputs, err := e.downloadPart(ctx, m, part, req, sink)
			if err != nil {
				result.Outputs = append(result.Outputs, outputs...)
				failures = append(failures, fmt.Sprintf("%s: %v", partLabel(m, part), err))
				sink.Emit(Event{
					Event:     "error",
					MediaID:   m.ID,
					Kind:      "video",
					Message:   err.Error(),
					Timestamp: time.Now().UnixMilli(),
				})
				continue
			}
			result.Outputs = append(result.Outputs, outputs...)
		}
	}

	switch {
	case len(failures) == 0:
		result.State = StateSucceeded
	case len(result.Outputs) == 0:
		result.State = StateFailed
	default:
		result.State = StatePartial
	}
	result.Warnings = failures
	if result.State == StateFailed {
		return result, NewError(CodeNetwork, StageTransfer, "%s", failures[0])
	}
	return result, nil
}

// downloadPart 下载单个条目，返回落盘产物。
func (e *Engine) downloadPart(
	ctx context.Context,
	m *site.Media,
	part site.Part,
	req Request,
	sink EventSink,
) ([]site.Output, error) {
	sel := req.Selection
	sel.PartIndex = part.Index

	set, err := e.Streams(ctx, m, sel)
	if err != nil {
		return nil, err
	}
	stream, err := pickStream(set, sel)
	if err != nil {
		return nil, err
	}

	outPath := e.outPath(m, part, req, stream)
	sink.Emit(Event{
		Event:     "start",
		MediaID:   m.ID,
		Page:      part.Index,
		Kind:      "video",
		Message:   filepath.Base(outPath),
		Timestamp: time.Now().UnixMilli(),
	})

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return nil, WrapError(CodeNetwork, StageFinalize, err, "创建输出目录失败")
	}

	if stream.HLS {
		err = fetch.DownloadHLS(ctx, stream.URL, fetch.HLSOptions{
			OutPath:     outPath,
			Headers:     stream.Headers,
			Client:      e.client,
			Concurrency: req.Concurrency,
			TempDir:     req.WorkDir,
			Progress: func(done, total int64) {
				sink.Emit(Event{
					Event:     "progress",
					MediaID:   m.ID,
					Page:      part.Index,
					Kind:      "video",
					Current:   done,
					Total:     total,
					Percent:   percent(done, total),
					Timestamp: time.Now().UnixMilli(),
				})
			},
		})
	} else {
		err = e.downloadDirect(ctx, stream, outPath, m.ID, part.Index, sink)
	}
	if err != nil {
		return nil, WrapError(CodeNetwork, StageTransfer, err, "下载失败")
	}

	st, statErr := os.Stat(outPath)
	if statErr != nil {
		return nil, WrapError(CodeNetwork, StageFinalize, statErr, "产物未落盘")
	}
	out := site.Output{
		Path:      outPath,
		Kind:      "video",
		Container: strings.TrimPrefix(filepath.Ext(outPath), "."),
		Bytes:     st.Size(),
	}
	sink.Emit(Event{
		Event:     "done",
		MediaID:   m.ID,
		Page:      part.Index,
		Kind:      "video",
		Message:   filepath.Base(outPath),
		Outputs:   []site.Output{out},
		Timestamp: time.Now().UnixMilli(),
	})
	return []site.Output{out}, nil
}

// downloadDirect 下载直链文件：先写临时文件，完成后原子改名。
func (e *Engine) downloadDirect(
	ctx context.Context,
	stream site.Stream,
	outPath string,
	mediaID string,
	page int,
	sink EventSink,
) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stream.URL, nil)
	if err != nil {
		return err
	}
	for k, vs := range stream.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmp := outPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()

	total := resp.ContentLength
	var written int64
	buf := make([]byte, 256*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				_ = f.Close()
				return werr
			}
			written += int64(n)
			sink.Emit(Event{
				Event:     "progress",
				MediaID:   mediaID,
				Page:      page,
				Kind:      "video",
				Current:   written,
				Total:     total,
				Percent:   percent(written, total),
				Timestamp: time.Now().UnixMilli(),
			})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			return readErr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, outPath)
}

// pickStream 按偏好挑一条码流：默认取视频列表第一条（适配器已按质量排好），
// AudioOnly 时取音频列表。
func pickStream(set *site.StreamSet, sel site.Selection) (site.Stream, error) {
	if sel.AudioOnly {
		if len(set.Audios) > 0 {
			return set.Audios[0], nil
		}
		return site.Stream{}, NewError(CodeNotFound, StageSelect, "没有音频流")
	}
	if len(set.Videos) > 0 {
		return set.Videos[0], nil
	}
	if len(set.Audios) > 0 {
		return set.Audios[0], nil
	}
	return site.Stream{}, NewError(CodeNotFound, StageSelect, "没有可用码流")
}

// outPath 渲染产物路径：默认 {author}/{title}，多条目追加序号避免互相覆盖。
func (e *Engine) outPath(m *site.Media, part site.Part, req Request, stream site.Stream) string {
	ext := stream.Container
	if ext == "" {
		ext = "mp4"
	}
	author := sanitize(m.Author.Name)
	if author == "" {
		author = sanitize(m.Site)
	}
	title := sanitize(m.Title)
	if title == "" {
		title = sanitize(m.ID)
	}

	base := title
	if len(m.Parts) > 1 {
		base = fmt.Sprintf("%s - %02d", title, part.Index)
		if t := sanitize(part.Title); t != "" && t != title {
			base = fmt.Sprintf("%s - %02d - %s", title, part.Index, t)
		}
	}
	return filepath.Join(req.OutputDir, author, base+"."+ext)
}

var illegal = strings.NewReplacer(
	"/", " ", "\\", " ", ":", " ", "*", " ", "?", " ",
	"\"", " ", "<", " ", ">", " ", "|", " ",
)

// sanitize 清洗路径段：非法字符换成空格、折叠空白、去掉首尾的点与空格。
func sanitize(s string) string {
	s = illegal.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, ". ")
	if len([]rune(s)) > 120 {
		s = string([]rune(s)[:120])
	}
	return s
}

func percent(cur, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(cur) / float64(total) * 100
}

func partLabel(m *site.Media, part site.Part) string {
	if part.Title != "" {
		return part.Title
	}
	return fmt.Sprintf("%s #%d", m.Title, part.Index)
}
