package bilibili

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/Felix2yu/bili-dl/c"
)

// ---------- 番剧 / 剧集（PGC）----------

type pgcEpisode struct {
	EpID      int64       `json:"ep_id"`
	Cid       json.Number `json:"cid"`
	BVID      string      `json:"bvid"`
	AID       int64       `json:"aid"`
	Title     string      `json:"title"`
	LongTitle string      `json:"long_title"`
	Badge     string      `json:"badge"`
}

type pgcSeasonResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		SeasonID    int64        `json:"season_id"`
		Title       string       `json:"title"`
		SeasonTitle string       `json:"season_title"`
		Episodes    []pgcEpisode `json:"episodes"`
		Section     []struct {
			ID      int64        `json:"id"`
			Title   string       `json:"title"`
			Episode []pgcEpisode `json:"episode"`
		} `json:"section"`
	} `json:"data"`
}

var (
	// 匹配 ss123 / /ss123 / season_id=123 / season/123 等形态。
	reSSID = regexp.MustCompile(`(?i)(?:^|[/?&=])(?:ss|season)(?:_id)?[=/]?(\d{1,12})`)
	reEpid = regexp.MustCompile(`(?i)(?:^|[/?&=])(?:ep|ep_id)[=/]?(\d{1,12})`)
)

// ParsePGCSInput 从 ss<id> / ep<id> / 番剧 URL 中识别番剧输入。
// 返回 kind（"ss" 或 "ep"）、id；非番剧输入返回空 kind。
func ParsePGCSInput(raw string) (kind, id string) {
	s := strings.TrimSpace(raw)
	if m := reEpid.FindStringSubmatch(s); len(m) == 2 {
		return "ep", m[1]
	}
	if m := reSSID.FindStringSubmatch(s); len(m) == 2 {
		return "ss", m[1]
	}
	return "", ""
}

// SeasonVideos 拉取番剧/剧集的剧集列表。seasonID 与 epID 二选一。
// B 站 PGC 接口在未登录时可能返回空 data，此时给出明确提示而不是静默零产出。
func SeasonVideos(seasonID, epID string) ([]Video, error) {
	u := "https://api.bilibili.com/pgc/view/web/season"
	if epID != "" {
		u += "?ep_id=" + strings.TrimSpace(epID)
	} else if seasonID != "" {
		u += "?season_id=" + strings.TrimSpace(seasonID)
	} else {
		return nil, fmt.Errorf("番剧需要 --ss <season_id> 或 --ep <ep_id>")
	}
	req, err := newAPIRequest(apiCtx(), u)
	if err != nil {
		return nil, err
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
	var resp pgcSeasonResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("番剧接口返回 code=%d %s（可能需要登录/大会员）", resp.Code, resp.Message)
	}
	if resp.Data.Title == "" && len(resp.Data.Episodes) == 0 && len(resp.Data.Section) == 0 {
		return nil, fmt.Errorf("番剧信息为空：请在 --auth-file 配置 SESSDATA 后重试（番剧通常需要登录）")
	}
	// 封面 owner 用番剧名，保证流影 owner 归属不为空。
	owner := resp.Data.SeasonTitle
	if owner == "" {
		owner = resp.Data.Title
	}
	if owner == "" {
		owner = "番剧"
	}

	// 合并 data.episodes 与 data.section[].episode，按 ep_id 去重。
	seen := make(map[int64]bool)
	var out []Video
	add := func(eps []pgcEpisode) {
		for _, e := range eps {
			if e.EpID == 0 && e.BVID == "" {
				continue
			}
			if seen[e.EpID] {
				continue
			}
			seen[e.EpID] = true
			title := e.LongTitle
			if strings.TrimSpace(title) == "" {
				title = e.Title
			}
			if strings.TrimSpace(title) == "" {
				title = fmt.Sprintf("第%d集", len(out)+1)
			}
			cid := e.Cid.String()
			if e.BVID == "" && cid == "" {
				continue
			}
			out = append(out, Video{
				Title:      title,
				BV:         e.BVID,
				Cid:        cid,
				Owner:      owner,
				OwnerM:     0,
				PGC:        true,
				EpID:       strconv.FormatInt(e.EpID, 10),
				Pages:      []biliVideoPage{{Page: 1, Cid: e.Cid, Part: title}},
				ActivePage: 1,
			})
		}
	}
	add(resp.Data.Episodes)
	for _, sec := range resp.Data.Section {
		add(sec.Episode)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("番剧无可用剧集（可能需要大会员或该地区不可播）")
	}
	return out, nil
}

// ---------- 合集（ugc_season）----------

type ugcSeasonEpisode struct {
	BVID  string      `json:"bvid"`
	Cid   json.Number `json:"cid"`
	Title string      `json:"title"`
	Part  string      `json:"part"`
	Page  int         `json:"page"`
}

// UnmarshalJSON 兼容 B站 ugc_season.episodes[].page 的两种形态：
// 旧接口返回整数分 P 序号（如 1），新接口改为对象
// （如 {"page":1,"part":"...","cid":...}）。两种情况都归一化为 Page 整数。
func (e *ugcSeasonEpisode) UnmarshalJSON(b []byte) error {
	type plain ugcSeasonEpisode
	var aux struct {
		plain
		Page json.RawMessage `json:"page"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*e = ugcSeasonEpisode(aux.plain)
	raw := bytes.TrimSpace(aux.Page)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if raw[0] == '{' {
		var obj struct {
			Page int `json:"page"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		e.Page = obj.Page
		return nil
	}
	return json.Unmarshal(raw, &e.Page)
}

type ugcSeason struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Sections []struct {
		ID       int64              `json:"id"`
		Title    string             `json:"title"`
		Episodes []ugcSeasonEpisode `json:"episodes"`
	} `json:"sections"`
}

// ExpandPages 把投稿按分 P 展开成下载目标。batch 为 true 时取全部分 P，
// 否则只取 startPage；startPage 越界时回退 P1 并记录告警。
// 页码选择规则与 CLI 的 --batch/-p 完全一致，供嵌入方复用。
func ExpandPages(v *Video, startPage int, batch bool) []Video {
	if v == nil {
		return nil
	}
	if startPage < 1 {
		startPage = 1
	}
	if !batch || len(v.Pages) <= 1 {
		if startPage > len(v.Pages) {
			log.Printf("分 P 页码 %d 超出范围（共 %d P），改为 P1", startPage, len(v.Pages))
			startPage = 1
		}
		one, err := VideoForPage(v, startPage)
		if err != nil {
			log.Printf("选择分 P 失败: %v", err)
			return nil
		}
		return []Video{one}
	}
	var out []Video
	for p := 1; p <= len(v.Pages); p++ {
		one, err := VideoForPage(v, p)
		if err != nil {
			log.Printf("选择分 P 失败: %v", err)
			continue
		}
		out = append(out, one)
	}
	return out
}

// ExpandUGCSeason 把视频所属合集展开成多个下载目标。
// 主视频的合集信息由 VideoFromBV 填充到 v.UGCSeason。
func ExpandUGCSeason(base *Video) []Video {
	us := base.UGCSeason
	if us == nil || len(us.Sections) == 0 {
		return nil
	}
	var out []Video
	seen := make(map[string]bool)
	for _, sec := range us.Sections {
		for _, e := range sec.Episodes {
			if e.BVID == "" || seen[e.BVID] {
				continue
			}
			seen[e.BVID] = true
			title := e.Title
			if strings.TrimSpace(title) == "" {
				title = e.Part
			}
			if strings.TrimSpace(title) == "" {
				title = base.Title
			}
			out = append(out, Video{
				Title:  title,
				BV:     e.BVID,
				Cid:    e.Cid.String(),
				Owner:  base.Owner,
				OwnerM: base.OwnerM,
				Pic:    base.Pic,
			})
		}
	}
	return out
}

// BuildSeasonTargets 供 main 调用：把 --ss/--ep 转成剧集目标列表。
// 未指定番剧参数时返回 nil。
func BuildSeasonTargets() ([]Video, error) {
	if strings.TrimSpace(c.SeasonID) != "" || strings.TrimSpace(c.EpID) != "" {
		return SeasonVideos(c.SeasonID, c.EpID)
	}
	return nil, nil
}
