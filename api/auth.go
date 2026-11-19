package api

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 最小 TOML 读取：只需要 [profiles.<name>] 下的 sessdata/SESSDATA。
// 与流影 AuthFile.swift 的读取规则保持一致，避免两端对同一 auth.toml 理解不同。
var (
	reTable = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*(?:#.*)?$`)
	reKV    = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_-]*)\s*=\s*(.+?)\s*(?:#.*)?$`)
)

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

// LoadSessdata 从 --auth-file 指向的 TOML 里读指定 profile 的 SESSDATA。
// path 为空或 profile 未命中时返回空串（退化到未登录，不报错）。
func LoadSessdata(path, profile string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	expanded := expandHome(path)
	f, err := os.Open(expanded)
	if err != nil {
		return "", fmt.Errorf("读取认证文件失败 %s: %w", expanded, err)
	}
	defer f.Close()

	want := "profiles." + profile
	inTable := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if m := reTable.FindStringSubmatch(line); len(m) == 2 {
			inTable = strings.TrimSpace(m[1]) == want
			continue
		}
		if !inTable {
			continue
		}
		kv := reKV.FindStringSubmatch(line)
		if len(kv) != 3 {
			continue
		}
		key := strings.ToLower(kv[1])
		if key != "sessdata" {
			continue
		}
		val := strings.TrimSpace(kv[2])
		val = strings.TrimSpace(unquote(val))
		if val != "" {
			return val, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("扫描认证文件失败: %w", err)
	}
	return "", nil
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
