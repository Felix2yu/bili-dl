package bilibili

import (
	"context"
	"strings"

	"github.com/Felix2yu/bili-dl/auth"
)

// LoadSessdata 从 yutto 兼容的 auth.toml 读取指定 profile 的 SESSDATA。
//
// 解析逻辑已收敛到 auth.FileStore（同一份文件格式只允许一个实现），
// 这里保留原签名供 CLI 与 bili-history 使用。
func LoadSessdata(path, profile string) (string, error) {
	p, err := auth.FileStore{Path: path}.Get(context.Background(), "bilibili", profile)
	if err != nil || p == nil {
		return "", err
	}
	sess, _ := p.Cookie("SESSDATA")
	return strings.TrimSpace(sess), nil
}
