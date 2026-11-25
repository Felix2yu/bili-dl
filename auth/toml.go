package auth

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Felix2yu/liuying-engine/site"
)

// FileStore 从 yutto 兼容的 auth.toml 读取凭据。
//
// 文件形态（沿用 yutto 的约定，只是把 profile 表扩展成通用键值）：
//
//	[profiles.default]
//	sessdata = "xxx"
//	bili_jct = "yyy"
//
//	[profiles.x-main]
//	auth_token = "..."
//	ct0 = "..."
//
// 键名大小写不敏感；未知键按 Cookie 原名透传。
type FileStore struct {
	Path string
}

// canonicalCookies 把 auth.toml 里的小写键映射成站点实际使用的 Cookie 名。
// 未列出的键按原样透传（首字母保持用户写法）。
var canonicalCookies = map[string]map[string]string{
	"bilibili": {
		"sessdata":   "SESSDATA",
		"bili_jct":   "bili_jct",
		"buvid3":     "buvid3",
		"buvid4":     "buvid4",
		"dedeuserid": "DedeUserID",
	},
	"x": {
		"auth_token": "auth_token",
		"ct0":        "ct0",
	},
	"xiaohongshu": {
		"web_session": "web_session",
		"a1":          "a1",
		"webid":       "webId",
	},
	"douyin": {
		"ms_token": "msToken",
		"ttwid":    "ttwid",
		"odin_tt":  "odin_tt",
	},
}

var (
	reTable = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*(?:#.*)?$`)
	reKV    = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*=\s*(.+?)\s*(?:#.*)?$`)
)

// Get 读取 [profiles.<name>] 表。文件不存在或表缺失时返回 (nil, nil)。
func (s FileStore) Get(_ context.Context, siteName, profile string) (*Profile, error) {
	if strings.TrimSpace(s.Path) == "" {
		return nil, nil
	}
	tables, err := s.read()
	if err != nil {
		return nil, err
	}
	if profile == "" {
		profile = "default"
	}
	values, ok := tables[profile]
	if !ok || len(values) == 0 {
		return nil, nil
	}

	p := &Profile{Site: siteName, Name: profile}
	canon := canonicalCookies[siteName]
	// 排序保证 Cookie 顺序稳定（便于测试与排障）。
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		name := k
		if mapped, ok := canon[strings.ToLower(k)]; ok {
			name = mapped
		}
		p.Cookies = append(p.Cookies, site.Cookie{Name: name, Value: values[k]})
	}
	return p, nil
}

// List 返回文件里出现的全部 profile 名（与站点无关）。
func (s FileStore) List(_ context.Context, _ string) ([]string, error) {
	if strings.TrimSpace(s.Path) == "" {
		return nil, nil
	}
	tables, err := s.read()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// read 解析整份文件，返回 profile 名 → 键值。
func (s FileStore) read() (map[string]map[string]string, error) {
	path := expandHome(s.Path)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取认证文件失败 %s: %w", path, err)
	}
	defer f.Close()

	out := map[string]map[string]string{}
	current := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if m := reTable.FindStringSubmatch(line); len(m) == 2 {
			name := strings.TrimSpace(m[1])
			if strings.HasPrefix(name, "profiles.") {
				current = strings.TrimPrefix(name, "profiles.")
				if _, ok := out[current]; !ok {
					out[current] = map[string]string{}
				}
			} else {
				current = ""
			}
			continue
		}
		if current == "" {
			continue
		}
		kv := reKV.FindStringSubmatch(line)
		if len(kv) != 3 {
			continue
		}
		value := strings.TrimSpace(unquote(strings.TrimSpace(kv[2])))
		if value == "" {
			continue
		}
		out[current][kv[1]] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("扫描认证文件失败: %w", err)
	}
	return out, nil
}

// unquote 去掉成对的单/双引号（Go regexp 不支持反向引用，故手写）。
func unquote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func expandHome(path string) string {
	if path == "~" {
		h, _ := os.UserHomeDir()
		return h
	}
	if strings.HasPrefix(path, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, path[2:])
	}
	return path
}
