// Package fetch 是站点无关的传输层：HLS 清单解析与分片下载。
//
// 之所以单独成包：m3u8 的处理与「哪个站点」无关，X、以及将来的其他站点
// 都共用这一套；把它放进某个站点实现里会逼着别的站点复制一遍。
package fetch

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Variant 是 master playlist 里的一条可选码流。
type Variant struct {
	URI       string
	Bandwidth int
	Width     int
	Height    int
	Codecs    string
}

// Segment 是 media playlist 里的一个分片。
type Segment struct {
	// URI 已解析成绝对地址。
	URI      string
	Duration float64
	Seq      int
	// HasRange 为 true 时只取 RangeStart 起 RangeLen 字节（EXT-X-BYTERANGE）。
	HasRange   bool
	RangeStart int64
	RangeLen   int64
}

// Key 是 AES-128 解密参数。Method 为 "NONE" 表示不加密。
type Key struct {
	Method string
	// URI 已解析成绝对地址；Method 为 NONE 时为空。
	URI string
	// IV 为 16 字节初始向量；缺失时按 HLS 规范用分片序号补零填充。
	IV []byte
}

// MediaPlaylist 是解析后的 media playlist。
type MediaPlaylist struct {
	Segments []Segment
	// MapURI 是 EXT-X-MAP 指向的初始化段（fMP4 必需），已解析成绝对地址。
	MapURI   string
	MapRange []int64 // [start, len]，空表示整段
	Key      Key
	// TargetDuration 是声明的最长分片时长，用于超时估算。
	TargetDuration float64
}

// IsMaster 判断清单是否还有下一层（master playlist）。
func IsMaster(body []byte) bool {
	return bytes.Contains(body, []byte("#EXT-X-STREAM-INF"))
}

// ParseMasterPlaylist 解析 master playlist，返回全部候选码流。
// 属性行与 URI 分两行书写（HLS 规范），空行与注释忽略。
func ParseMasterPlaylist(body []byte, base *url.URL) ([]Variant, error) {
	var out []Variant
	var pending *Variant

	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			v := parseStreamInf(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
			pending = &v
		case strings.HasPrefix(line, "#"):
			continue
		default:
			if pending == nil {
				continue
			}
			abs, err := resolveURI(base, line)
			if err != nil {
				return nil, err
			}
			pending.URI = abs
			out = append(out, *pending)
			pending = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 master playlist 失败: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("master playlist 里没有可用码流")
	}
	return out, nil
}

// parseStreamInf 解析 "BANDWIDTH=…,RESOLUTION=1920x1080,CODECS=\"…\""。
func parseStreamInf(attrs string) Variant {
	var v Variant
	for _, part := range splitAttrs(attrs) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch key {
		case "BANDWIDTH", "AVERAGE-BANDWIDTH":
			if n, err := strconv.Atoi(value); err == nil && n > v.Bandwidth {
				v.Bandwidth = n
			}
		case "RESOLUTION":
			w, h, ok := strings.Cut(value, "x")
			if !ok {
				continue
			}
			v.Width, _ = strconv.Atoi(strings.TrimSpace(w))
			v.Height, _ = strconv.Atoi(strings.TrimSpace(h))
		case "CODECS":
			v.Codecs = value
		}
	}
	return v
}

// splitAttrs 按逗号切分属性列表，但不切引号内的逗号（CODECS="avc1,mp4a"）。
func splitAttrs(s string) []string {
	var out []string
	var current strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			current.WriteRune(r)
		case r == ',' && !inQuote:
			out = append(out, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

// ParseMediaPlaylist 解析 media playlist。
func ParseMediaPlaylist(body []byte, base *url.URL) (*MediaPlaylist, error) {
	pl := &MediaPlaylist{Key: Key{Method: "NONE"}}
	seq := 0

	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var pendingDur float64
	var pendingRange string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			if n, err := strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:")); err == nil {
				seq = n
			}
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			if f, err := strconv.ParseFloat(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"), 64); err == nil {
				pl.TargetDuration = f
			}
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			attrs := parseAttrMap(strings.TrimPrefix(line, "#EXT-X-MAP:"))
			if uri := attrs["URI"]; uri != "" {
				abs, err := resolveURI(base, uri)
				if err != nil {
					return nil, err
				}
				pl.MapURI = abs
				if br := attrs["BYTERANGE"]; br != "" {
					if start, length, ok := parseByteRange(br, 0); ok {
						pl.MapRange = []int64{start, length}
					}
				}
			}
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			attrs := parseAttrMap(strings.TrimPrefix(line, "#EXT-X-KEY:"))
			key, err := parseKey(attrs, base)
			if err != nil {
				return nil, err
			}
			pl.Key = key
		case strings.HasPrefix(line, "#EXTINF:"):
			value := strings.TrimPrefix(line, "#EXTINF:")
			if comma := strings.IndexByte(value, ','); comma >= 0 {
				value = value[:comma]
			}
			pendingDur, _ = strconv.ParseFloat(strings.TrimSpace(value), 64)
		case strings.HasPrefix(line, "#EXT-X-BYTERANGE:"):
			pendingRange = strings.TrimPrefix(line, "#EXT-X-BYTERANGE:")
		case strings.HasPrefix(line, "#"):
			continue
		default:
			abs, err := resolveURI(base, line)
			if err != nil {
				return nil, err
			}
			seg := Segment{URI: abs, Duration: pendingDur, Seq: seq}
			if pendingRange != "" {
				// BYTERANGE 的偏移量省略时表示「紧接上一个分片」。
				var prevEnd int64
				if n := len(pl.Segments); n > 0 && pl.Segments[n-1].HasRange {
					prevEnd = pl.Segments[n-1].RangeStart + pl.Segments[n-1].RangeLen
				}
				if start, length, ok := parseByteRange(pendingRange, prevEnd); ok {
					seg.HasRange = true
					seg.RangeStart = start
					seg.RangeLen = length
				}
			}
			pl.Segments = append(pl.Segments, seg)
			seq++
			pendingDur = 0
			pendingRange = ""
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 media playlist 失败: %w", err)
	}
	if len(pl.Segments) == 0 {
		return nil, fmt.Errorf("media playlist 里没有分片")
	}
	return pl, nil
}

// parseAttrMap 解析 "KEY=VALUE,KEY2="VALUE2""，值里的引号会被去掉。
func parseAttrMap(s string) map[string]string {
	out := map[string]string{}
	for _, part := range splitAttrs(s) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.ToUpper(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return out
}

// parseByteRange 解析 "LENGTH[@START]"。
func parseByteRange(s string, defaultStart int64) (start, length int64, ok bool) {
	s = strings.TrimSpace(s)
	lenPart, startPart, hasStart := strings.Cut(s, "@")
	length, err := strconv.ParseInt(strings.TrimSpace(lenPart), 10, 64)
	if err != nil || length <= 0 {
		return 0, 0, false
	}
	start = defaultStart
	if hasStart {
		if n, err := strconv.ParseInt(strings.TrimSpace(startPart), 10, 64); err == nil {
			start = n
		}
	}
	return start, length, true
}

// parseKey 解析 EXT-X-KEY 属性。只支持 METHOD=NONE 与 AES-128。
func parseKey(attrs map[string]string, base *url.URL) (Key, error) {
	method := strings.ToUpper(attrs["METHOD"])
	switch method {
	case "", "NONE":
		return Key{Method: "NONE"}, nil
	case "AES-128":
		uri := attrs["URI"]
		if uri == "" {
			return Key{}, fmt.Errorf("AES-128 缺少 URI")
		}
		abs, err := resolveURI(base, uri)
		if err != nil {
			return Key{}, err
		}
		k := Key{Method: "AES-128", URI: abs}
		if iv := attrs["IV"]; iv != "" {
			raw, err := parseIV(iv)
			if err != nil {
				return Key{}, err
			}
			k.IV = raw
		}
		return k, nil
	default:
		// SAMPLE-AES 等需要密钥系统，明确拒绝而不是静默下出坏文件。
		return Key{}, fmt.Errorf("不支持的加密方式: %s", method)
	}
}

// parseIV 解析 "0x…" 形式的 16 字节 IV。
func parseIV(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(s)%2 == 1 {
		s = "0" + s
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("IV 解析失败: %w", err)
	}
	if len(raw) > 16 {
		return nil, fmt.Errorf("IV 超过 16 字节")
	}
	// 左补零到 16 字节。
	iv := make([]byte, 16)
	copy(iv[16-len(raw):], raw)
	return iv, nil
}

// resolveURI 把清单里的相对地址解析成绝对地址。
func resolveURI(base *url.URL, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("空 URI")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("URI 解析失败 %q: %w", raw, err)
	}
	if base == nil {
		return u.String(), nil
	}
	return base.ResolveReference(u).String(), nil
}

// BestVariant 选码率最高的变体；同码率时取分辨率更高的。
func BestVariant(variants []Variant) (Variant, bool) {
	if len(variants) == 0 {
		return Variant{}, false
	}
	best := variants[0]
	for _, v := range variants[1:] {
		if v.Bandwidth > best.Bandwidth ||
			(v.Bandwidth == best.Bandwidth && v.Height > best.Height) {
			best = v
		}
	}
	return best, true
}
