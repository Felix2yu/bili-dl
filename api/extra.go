package api

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Felix2yu/bili-dl/c"
)

// DownloadExtras 下载弹幕、字幕、封面。三者都是 best-effort：
// 任一失败只记录告警，不影响主视频已下载的结果，也不增加失败计数。
// 产物与视频共用 relBase（含 owner 子目录与分 P 标签），扩展名区分：
//
//	<title>.danmaku.ass
//	<title>.<lan>.srt
//	<title>.cover.jpg
//
// 这样与 yutto 的命名保持一致，流影的 FileSnapshot 差集仍能正确发现。
func DownloadExtras(stream *Stream) {
	if stream == nil || stream.relBase == "" {
		return
	}
	if c.DownloadDanmaku {
		if err := downloadDanmaku(stream); err != nil {
			log.Printf("弹幕下载失败 [%s]: %v", stream.BV, err)
		}
	}
	if c.DownloadSubtitle {
		if err := downloadSubtitles(stream); err != nil {
			log.Printf("字幕下载失败 [%s]: %v", stream.BV, err)
		}
	}
	if c.SaveCover && stream.Pic != "" {
		dst := filepath.Join(c.O, stream.relBase+".cover.jpg")
		meta := downloadMeta{BV: stream.BV, Page: stream.ActivePage, Kind: "cover"}
		if err := downloadFileWith(stream.Pic, dst, meta); err != nil {
			log.Printf("封面下载失败 [%s]: %v", stream.BV, err)
		}
	}
}

// ---------- 弹幕 ----------

type dmXML struct {
	Items []struct {
		P    string `xml:"p,attr"`
		Text string `xml:",chardata"`
	} `xml:"d"`
}

func downloadDanmaku(stream *Stream) error {
	u := "https://api.bilibili.com/x/v1/dm/list.so?oid=" + url.QueryEscape(stream.Cid)
	req, err := newAPIRequest(u)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	// B 站 list.so 常返回 deflate/gzip 编码的 XML，Go Transport 不自动解压 deflate。
	// 优先按 Content-Encoding 判断，magic number 兜底。
	enc := strings.ToLower(strings.TrimSpace(res.Header.Get("Content-Encoding")))
	switch {
	case enc == "gzip" || (len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b):
		zr, zerr := gzip.NewReader(bytes.NewReader(body))
		if zerr != nil {
			return fmt.Errorf("弹幕 gzip 解压失败: %w", zerr)
		}
		plain, rerr := io.ReadAll(zr)
		zr.Close()
		if rerr != nil {
			return rerr
		}
		body = plain
	case enc == "deflate":
		fr := flate.NewReader(bytes.NewReader(body))
		plain, rerr := io.ReadAll(fr)
		fr.Close()
		if rerr != nil {
			return rerr
		}
		body = plain
	}
	ass := danmakuXMLToASS(body, stream.Title)
	if strings.TrimSpace(ass) == "" {
		return fmt.Errorf("无弹幕数据")
	}
	dst := filepath.Join(c.O, stream.relBase+".danmaku.ass")
	if err := ensureDir(dst); err != nil {
		return err
	}
	if err := os.WriteFile(dst, []byte(ass), 0o666); err != nil {
		return err
	}
	emitProgress(ProgressEvent{Event: "done", BV: stream.BV, Page: stream.ActivePage, Kind: "danmaku", Current: int64(len(body)), Message: filepath.Base(dst)})
	return nil
}

// danmakuXMLToASS 把 B 站 XML 弹幕转成 ASS 字幕。
// p 字段格式：时间,模式,字号,颜色,时间戳,池,用户ID,弹幕ID,...
// 提取规则（提取纯函数便于单测，不依赖网络）。
func danmakuXMLToASS(xmlBody []byte, title string) string {
	var d dmXML
	if err := xml.Unmarshal(xmlBody, &d); err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Script Info]\nScriptType: v4.00+\nPlayResX: 1920\nPlayResY: 1080\nWrapStyle: 2\n\n")
	b.WriteString("[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	b.WriteString("Style: Default,Microsoft YaHei,25,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1\n\n")
	b.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")

	for _, item := range d.Items {
		fields := strings.Split(item.P, ",")
		if len(fields) < 4 {
			continue
		}
		start, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
		if err != nil || start < 0 {
			continue
		}
		color, err := strconv.ParseInt(strings.TrimSpace(fields[3]), 10, 64)
		if err != nil || color < 0 {
			color = 0xFFFFFF
		}
		// B 站颜色是十进制 RGB，ASS 是 &HAABBGGRR。
		r, g, bl := color&0xff, (color>>8)&0xff, (color>>16)&0xff
		colour := fmt.Sprintf("&H00%02X%02X%02X", bl, g, r)
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		// ASS 需要转义逗号（ Dialog 字段由逗号分隔）。
		text = strings.ReplaceAll(text, ",", "，")
		end := start + 5
		align := 2 // 默认滚动
		switch strings.TrimSpace(fields[1]) {
		case "4":
			align = 5 // 底部
		case "5":
			align = 8 // 顶部
		}
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,{\\an%d\\c%s}%s\n",
			assTime(start), assTime(end), align, colour, text)
	}
	return b.String()
}

// assTime 把秒（浮点）转成 ASS 时间格式 H:MM:SS.cc。
func assTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	cs := int64(sec*100 + 0.5)
	h := cs / 360000
	cs -= h * 360000
	m := cs / 6000
	cs -= m * 6000
	s := cs / 100
	cs -= s * 100
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}

// ---------- 字幕 ----------

type playerV2Resp struct {
	Code int `json:"code"`
	Data struct {
		Subtitle struct {
			Subtitles []struct {
				Lan         string `json:"lan"`
				LanDoc      string `json:"lan_doc"`
				SubtitleURL string `json:"subtitle_url"`
			} `json:"subtitles"`
		} `json:"subtitle"`
	} `json:"data"`
}

type subtitleBody struct {
	Body []struct {
		From    float64 `json:"from"`
		To      float64 `json:"to"`
		Content string  `json:"content"`
	} `json:"body"`
}

func downloadSubtitles(stream *Stream) error {
	u := "https://api.bilibili.com/x/player/v2?bvid=" + url.QueryEscape(stream.BV) + "&cid=" + url.QueryEscape(stream.Cid)
	req, err := newAPIRequest(u)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	var info playerV2Resp
	if err := json.Unmarshal(body, &info); err != nil {
		return err
	}
	if info.Code != 0 || len(info.Data.Subtitle.Subtitles) == 0 {
		return nil // 无字幕，正常
	}
	for _, sub := range info.Data.Subtitle.Subtitles {
		raw := sub.SubtitleURL
		if strings.HasPrefix(raw, "//") {
			raw = "https:" + raw
		}
		if raw == "" {
			continue
		}
		sreq, err := newAPIRequest(raw)
		if err != nil {
			continue
		}
		sres, err := client.Do(sreq)
		if err != nil {
			continue
		}
		sbody, err := io.ReadAll(sres.Body)
		sres.Body.Close()
		if err != nil {
			continue
		}
		srt := subtitleJSONToSRT(sbody)
		if strings.TrimSpace(srt) == "" {
			continue
		}
		lan := sanitizeSegment(sub.Lan)
		dst := filepath.Join(c.O, stream.relBase+"."+lan+".srt")
		if err := ensureDir(dst); err != nil {
			continue
		}
		if err := os.WriteFile(dst, []byte(srt), 0o666); err != nil {
			continue
		}
		emitProgress(ProgressEvent{Event: "done", BV: stream.BV, Page: stream.ActivePage, Kind: "subtitle", Current: int64(len(srt)), Message: filepath.Base(dst)})
		log.Printf("字幕已保存 [%s]: %s", stream.BV, sub.LanDoc)
	}
	return nil
}

// subtitleJSONToSRT 把 B 站字幕 JSON（body[]）转成 SRT。
func subtitleJSONToSRT(body []byte) string {
	var data subtitleBody
	if err := json.Unmarshal(body, &data); err != nil || len(data.Body) == 0 {
		return ""
	}
	var b strings.Builder
	for i, item := range data.Body {
		text := strings.ReplaceAll(strings.TrimSpace(item.Content), "\n", " ")
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(item.From), srtTime(item.To), text)
	}
	return b.String()
}

func srtTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int64(sec*1000 + 0.5)
	h := ms / 3600000
	ms -= h * 3600000
	m := ms / 60000
	ms -= m * 60000
	s := ms / 1000
	ms -= s * 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
