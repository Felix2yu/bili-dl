package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	url2 "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yu1745/bili-dl/C"
)

var client = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		//禁止复用连接，防止同一个连接长时间大流量被限速
		DisableKeepAlives: true,
	},
}

type biliVideoInfoResp struct {
	Data struct {
		Cid   string `json:"cid"`
		Title string `json:"title"`
		Page  struct {
			Count int `json:"count"`
		} `json:"page"`
	} `json:"data"`
}

type biliDashResp struct {
	Data struct {
		Dash struct {
			Video []dashStream `json:"video"`
			Audio []dashStream `json:"audio"`
		} `json:"dash"`
	} `json:"data"`
}

type dashStream struct {
	ID        float64 `json:"id"`
	Codecs    string  `json:"codecs"`
	Width     float64 `json:"width"`
	Height    float64 `json:"height"`
	Bandwidth float64 `json:"bandwidth"`
	BaseURL   string  `json:"base_url"`
}

type biliVlistResp struct {
	Data struct {
		Page struct {
			Count int `json:"count"`
		} `json:"page"`
		List struct {
			VList []struct {
				BV string `json:"bvid"`
			} `json:"vlist"`
		} `json:"list"`
	} `json:"data"`
}

func videoInfo(bv string) ([]byte, error) {
	url := "https://api.bilibili.com/x/web-interface/view"
	parse, _ := url2.Parse(url)
	query := parse.Query()
	query.Add("bvid", bv)
	parse.RawQuery = query.Encode()
	url = parse.String()
	method := "GET"
	req, err := http.NewRequest(method, url, nil)

	if err != nil {
		log.Println(err)
		return nil, err
	}
	req.Header.Add("User-Agent", "Apifox/1.0.0 (https://www.apifox.cn)")

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	return body, nil
}

func ResolveVideo(v *Video) (*Video, error) {
	info, err := videoInfo(v.BV)
	if err != nil {
		return nil, err
	}
	var resp biliVideoInfoResp
	if err := json.Unmarshal(info, &resp); err != nil {
		return nil, err
	}
	v.Cid = resp.Data.Cid
	v.Title = resp.Data.Title
	return v, nil
}

func videoFromUP(mid string, pn int) (rt []byte, err error) {
	url := "https://api.bilibili.com/x/space/wbi/arc/search?order=pubdate&ps=49"
	parse, _ := url2.Parse(url)
	query := parse.Query()
	query.Add("mid", mid)
	query.Add("pn", strconv.Itoa(pn))
	parse.RawQuery = query.Encode()
	url = parse.String()
	url, err = sign(url)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)

	if err != nil {
		log.Println(err)
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36 Edg/123.0.0.0")
	req.Header.Set("Referer", "https://www.bilibili.com/")
	if C.Cookie != "" {
		req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: C.Cookie})
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return body, err
}

type Video struct {
	Title string `json:"title,omitempty"`
	BV    string `json:"bv,omitempty"`
	Cid   string `json:"cid,omitempty"`
}

func AllVideo(mid string) ([]Video, error) {
	bytes, err := videoFromUP(mid, 1)
	if err != nil {
		return nil, err
	}
	var resp biliVlistResp
	if err := json.Unmarshal(bytes, &resp); err != nil {
		return nil, err
	}
	count := resp.Data.Page.Count
	if count == 0 {
		count = 1
	}
	pn := (count + 48) / 49
	if pn < 1 {
		pn = 1
	}

	var videos []Video
	for i := 1; i <= pn; i++ {
		time.Sleep(time.Second)
		bytes, err := videoFromUP(mid, i)
		if err != nil {
			log.Printf("Failed to fetch page %d: %v, continuing...", i, err)
			continue
		}
		var pageResp biliVlistResp
		if err := json.Unmarshal(bytes, &pageResp); err != nil {
			log.Printf("Failed to parse page %d: %v, continuing...", i, err)
			continue
		}
		for _, v := range pageResp.Data.List.VList {
			if v.BV != "" {
				video := Video{BV: v.BV}
				videoJson, _ := json.Marshal(video)
				println(string(videoJson))
				videos = append(videos, video)
			}
		}
	}
	return videos, nil
}

func codec2i(codec string) int {
	if strings.HasPrefix(codec, "avc") {
		return 1
	} else if strings.HasPrefix(codec, "hev") {
		return 2
	} else if strings.HasPrefix(codec, "av01") {
		return 3
	}
	return 0
}

func matchResolution(id float64, width float64, resolution string) bool {
	switch resolution {
	case "8k":
		return id == 127 || width >= 7680
	case "dolby":
		return id == 126
	case "4k":
		return id == 120
	case "1080p":
		return id == 116 || id == 112 || id == 80
	case "720p":
		return id == 64
	case "480p":
		return id == 32
	case "360p":
		return id == 16
	default:
		return true
	}
}

func matchCodec(codecs string, codec string) bool {
	switch codec {
	case "av1":
		return strings.HasPrefix(codecs, "av01")
	case "hevc":
		return strings.HasPrefix(codecs, "hev")
	case "avc":
		return strings.HasPrefix(codecs, "avc")
	default:
		return true
	}
}

type Stream struct {
	V       string
	A       string
	Video
	VCodec  string
	VWidth  float64
	VHeight float64
}

func GetStream(v Video) (*Stream, error) {
	url := "https://api.bilibili.com/x/player/wbi/playurl?fnver=0&fnval=3216&fourk=1&qn=127"
	parse, _ := url2.Parse(url)
	query := parse.Query()
	query.Add("bvid", v.BV)
	query.Add("cid", v.Cid)
	parse.RawQuery = query.Encode()
	url = parse.String()
	var err error
	url, err = sign(url)
	if err != nil {
		return nil, err
	}
	method := "GET"

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36 Edg/123.0.0.0")
	req.Header.Set("Referer", "https://www.bilibili.com/")

	req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: C.Cookie})

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var dashResp biliDashResp
	if err := json.Unmarshal(body, &dashResp); err != nil {
		return nil, err
	}

	var videoURL, vCodec string
	var vWidth, vHeight float64

	if !C.AudioOnly {
		videoStreams := dashResp.Data.Dash.Video
		if len(videoStreams) == 0 {
			return nil, fmt.Errorf("no video streams available")
		}

		// Filter by resolution and codec
		var filtered []dashStream
		for _, s := range videoStreams {
			if !matchResolution(s.ID, s.Width, C.Resolution) {
				continue
			}
			if !matchCodec(s.Codecs, C.Codec) {
				continue
			}
			filtered = append(filtered, s)
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("没有找到匹配的视频流 (分辨率: %s, 编码: %s)", C.Resolution, C.Codec)
		}

		// Select best: codec priority (av1>hevc>avc), then width desc
		bestIdx := 0
		for i := 1; i < len(filtered); i++ {
			ci, cj := codec2i(filtered[i].Codecs), codec2i(filtered[bestIdx].Codecs)
			if ci > cj || (ci == cj && filtered[i].Width > filtered[bestIdx].Width) {
				bestIdx = i
			}
		}
		videoURL = filtered[bestIdx].BaseURL
		vCodec = filtered[bestIdx].Codecs
		vWidth = filtered[bestIdx].Width
		vHeight = filtered[bestIdx].Height
	}

	audioStreams := dashResp.Data.Dash.Audio
	if len(audioStreams) == 0 {
		return nil, fmt.Errorf("no audio streams available")
	}
	// Find best audio stream without full sort
	bestAudioIdx := 0
	for i := 1; i < len(audioStreams); i++ {
		if audioStreams[i].Bandwidth > audioStreams[bestAudioIdx].Bandwidth {
			bestAudioIdx = i
		}
	}
	stream := &Stream{V: videoURL, A: audioStreams[bestAudioIdx].BaseURL, Video: v, VCodec: vCodec, VWidth: vWidth, VHeight: vHeight}
	return stream, nil
}

func PrintDASHInfo(v Video) error {
	url := "https://api.bilibili.com/x/player/wbi/playurl?fnver=0&fnval=3216&fourk=1&qn=127"
	parse, _ := url2.Parse(url)
	query := parse.Query()
	query.Add("bvid", v.BV)
	query.Add("cid", v.Cid)
	parse.RawQuery = query.Encode()
	url = parse.String()
	var err error
	url, err = sign(url)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36 Edg/123.0.0.0")
	req.Header.Set("Referer", "https://www.bilibili.com/")
	req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: C.Cookie})

	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	var dashResp biliDashResp
	if err := json.Unmarshal(body, &dashResp); err != nil {
		return fmt.Errorf("解析DASH信息失败: %w", err)
	}

	fmt.Printf("===== 视频DASH信息 =====\n")
	fmt.Printf("标题: %s\n", v.Title)
	fmt.Printf("BV号: %s\n", v.BV)
	fmt.Printf("CID: %s\n", v.Cid)
	fmt.Println()

	videoStreams := dashResp.Data.Dash.Video
	fmt.Printf("视频流 (%d 个):\n", len(videoStreams))
	for i, vs := range videoStreams {
		fmt.Printf("  [%d] 画质ID: %.0f | 分辨率: %.0fx%.0f | 编码: %s | 码率: %.0f bps\n",
			i+1, vs.ID, vs.Width, vs.Height, vs.Codecs, vs.Bandwidth)
		fmt.Printf("      URL: %s\n", vs.BaseURL)
	}
	fmt.Println()

	audioStreams := dashResp.Data.Dash.Audio
	fmt.Printf("音频流 (%d 个):\n", len(audioStreams))
	for i, as := range audioStreams {
		fmt.Printf("  [%d] 音质ID: %.0f | 编码: %s | 码率: %.0f bps\n",
			i+1, as.ID, as.Codecs, as.Bandwidth)
		fmt.Printf("      URL: %s\n", as.BaseURL)
	}
	fmt.Println("========================")
	return nil
}

func Dl(stream *Stream) error {
	stream.Title = fileNameFix(stream.Title)
	if stream.V != "" {
		err := DV(stream)
		if err != nil {
			return err
		}
	}
	err := DA(stream)
	if err != nil {
		return err
	}
	log.Println(stream.Title, "下载完成")
	return nil
}

func DV(stream *Stream) error {
	req, err := http.NewRequest("GET", stream.V, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Referer", "https://www.bilibili.com")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	var file *os.File
	if C.AddBVSuffix {
		file, err = os.OpenFile(filepath.Join(C.O, stream.Title+"_"+stream.BV+".mp4"), os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.ModePerm)
		if err != nil {
			return err
		}
	} else {
		file, err = os.OpenFile(filepath.Join(C.O, stream.Title+".mp4"), os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.ModePerm)
		if err != nil {
			return err
		}
	}
	defer file.Close()
	_ = file.Truncate(0)
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}
	return nil
}

func DA(stream *Stream) error {
	req, err := http.NewRequest("GET", stream.A, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Referer", "https://www.bilibili.com")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	var file *os.File
	if C.AddBVSuffix {
		file, err = os.OpenFile(filepath.Join(C.O, stream.Title+"_"+stream.BV+".m4a"), os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.ModePerm)
		if err != nil {
			return err
		}
	} else {
		file, err = os.OpenFile(filepath.Join(C.O, stream.Title+".m4a"), os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.ModePerm)
		if err != nil {
			return err
		}
	}
	defer file.Close()
	_ = file.Truncate(0)
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}
	return nil
}

func VideoFromBV(bv string) (*Video, error) {
	info, err := videoInfo(bv)
	if err != nil {
		return nil, err
	}
	var resp biliVideoInfoResp
	if err := json.Unmarshal(info, &resp); err != nil {
		return nil, err
	}
	video := Video{BV: bv, Cid: resp.Data.Cid, Title: resp.Data.Title}
	log.Printf("%+v\n", video)
	return &video, nil
}

func Merge(stream *Stream) error {
	if stream.V == "" {
		// Audio-only mode: rename audio file to final name
		var audio, output string
		if C.AddBVSuffix {
			audio = filepath.Join(C.O, stream.Title+"_"+stream.BV+".m4a")
			output = filepath.Join(C.O, stream.Title+"_"+stream.BV+"-merged.m4a")
		} else {
			audio = filepath.Join(C.O, stream.Title+".m4a")
			output = filepath.Join(C.O, stream.Title+"-merged.m4a")
		}
		if err := os.Rename(audio, output); err != nil {
			return err
		}
		log.Println(stream.Title, "音频处理完成")
		return nil
	}

	var video, audio, output string
	if C.AddBVSuffix {
		video = filepath.Join(C.O, stream.Title+"_"+stream.BV+".mp4")
		audio = filepath.Join(C.O, stream.Title+"_"+stream.BV+".m4a")
		output = filepath.Join(C.O, stream.Title+"_"+stream.BV+"-merged.mp4")
	} else {
		video = filepath.Join(C.O, stream.Title+".mp4")
		audio = filepath.Join(C.O, stream.Title+".m4a")
		output = filepath.Join(C.O, stream.Title+"-merged.mp4")
	}
	cmd := exec.Command("ffmpeg", "-y", "-i", video, "-i", audio, "-c", "copy", output)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg merge failed: %w", err)
	}
	if C.Delete {
		if err := os.Remove(video); err != nil {
			return err
		}
		if err := os.Remove(audio); err != nil {
			return err
		}
		if C.AddBVSuffix {
			if err := os.Rename(output, filepath.Join(C.O, stream.Title+"_"+stream.BV+".mp4")); err != nil {
				return err
			}
		} else {
			if err := os.Rename(output, filepath.Join(C.O, stream.Title+".mp4")); err != nil {
				return err
			}
		}
	}
	log.Println(stream.Title, "合并完成")
	return nil
}

var reg = regexp.MustCompile(`[/\\:*?"<>|]`)

// 去掉文件名中的非法字符
func fileNameFix(name string) string {
	return reg.ReplaceAllString(name, " ")
}
