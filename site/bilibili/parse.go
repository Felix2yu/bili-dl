package bilibili

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	reBV  = regexp.MustCompile(`BV[0-9A-Za-z]{10}`)
	reAV  = regexp.MustCompile(`(?i)\bav(\d{1,15})\b`)
	reURL = regexp.MustCompile(`https?://[^\s，。、）】"'<>]+`)
	reP   = regexp.MustCompile(`(?i)(?:\?|&)p=(\d{1,3})(?:&|$)`)
)

// b23 短链跳转用的客户端：不复用主 client（主 client 会跟随重定向，这里只需要最终 URL）。
var shortClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error { return nil },
}

// ParseInput 把用户输入（BV 号 / 完整 URL / b23.tv 短链 / av 号 / 分享口令）解析成
// BV 号与起始分 P 页码。不合法的短链会跟随一次 302 后再解析。
//
// 页码默认 1；`?p=N` 由 URL 显式给出。
func ParseInput(input string) (bv string, page int, err error) {
	page = 1
	s := strings.TrimSpace(input)
	if s == "" {
		return "", 0, fmt.Errorf("空链接")
	}

	// 分享口令里先抠出 URL；没有 URL 时直接在裸文本里找 BV/av。
	if m := reURL.FindString(s); m != "" {
		s = strings.TrimRight(m, ".,;:!?，。；：！？)）]】")
	}

	if u, perr := urlpkg.Parse(s); perr == nil && u.Scheme != "" {
		if p := pageFromURL(u); p > 0 {
			page = p
		}
		// b23.tv 短链：跟随跳转拿到最终 URL。
		if strings.HasSuffix(u.Host, "b23.tv") {
			final, ferr := resolveShortLink(s)
			if ferr != nil {
				return "", 0, ferr
			}
			s = final
			if fu, e := urlpkg.Parse(final); e == nil {
				if p := pageFromURL(fu); p > 0 {
					page = p
				}
			}
		}
	}

	if bv = reBV.FindString(s); bv != "" {
		return bv, page, nil
	}

	// av 号：需要换 BV。取 URL query 里的 aid，或裸文本里的 av\d+。
	if aid := avID(s); aid != "" {
		bv, err = avToBV(aid)
		if err != nil {
			return "", 0, err
		}
		return bv, page, nil
	}

	return "", 0, fmt.Errorf("无法从输入解析出 BV 号或 av 号: %q", truncate(input, 60))
}

func pageFromURL(u *urlpkg.URL) int {
	if v := u.Query().Get("p"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return n
		}
	}
	// /video/BVxxx/p2 形态
	seg := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range seg {
		if strings.HasPrefix(part, "p") && i > 0 {
			if n, err := strconv.Atoi(part[1:]); err == nil && n >= 1 {
				return n
			}
		}
	}
	return 0
}

func avID(s string) string {
	if u, err := urlpkg.Parse(s); err == nil && u.Scheme != "" {
		if v := u.Query().Get("aid"); v != "" {
			return v
		}
	}
	if m := reAV.FindStringSubmatch(s); len(m) == 2 {
		return m[1]
	}
	return ""
}

func resolveShortLink(raw string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := shortClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	final := resp.Request.URL.String()
	if final == "" {
		final = raw
	}
	return final, nil
}

type avViewResp struct {
	Code int `json:"code"`
	Data struct {
		BVID string `json:"bvid"`
	} `json:"data"`
}

func avToBV(aid string) (string, error) {
	u := "https://api.bilibili.com/x/web-interface/view?aid=" + urlpkg.QueryEscape(aid)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Apifox/1.0.0 (https://www.apifox.cn)")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var view avViewResp
	if err := json.Unmarshal(body, &view); err != nil {
		return "", err
	}
	if view.Code != 0 || view.Data.BVID == "" {
		return "", fmt.Errorf("av%s 换 BV 失败: code=%d", aid, view.Code)
	}
	return view.Data.BVID, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
