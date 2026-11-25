package auth

import (
	"context"
	"os"

	"github.com/Felix2yu/liuying-engine/site"
)

// EnvStore 从环境变量读取凭据，供容器部署（bili-history 镜像）使用。
//
// 环境变量名与 bili-history 现有约定一致：SESSDATA / BILI_JCT 等，
// 不引入新变量名，避免部署侧要改两处。
type EnvStore struct{}

// envCookies 是各站点支持的环境变量 → Cookie 名映射。
var envCookies = map[string][]struct{ Env, Cookie string }{
	"bilibili": {
		{"SESSDATA", "SESSDATA"},
		{"BILI_JCT", "bili_jct"},
		{"DEDE_USER_ID", "DedeUserID"},
	},
	"x": {
		{"X_AUTH_TOKEN", "auth_token"},
		{"X_CT0", "ct0"},
	},
	"xiaohongshu": {
		{"XHS_WEB_SESSION", "web_session"},
		{"XHS_A1", "a1"},
	},
}

// Get 只认 profile 名 "env"（或空），避免环境变量意外顶替文件里的具名 profile。
func (EnvStore) Get(_ context.Context, siteName, profile string) (*Profile, error) {
	if profile != "" && profile != "env" {
		return nil, nil
	}
	specs, ok := envCookies[siteName]
	if !ok {
		return nil, nil
	}
	p := &Profile{Site: siteName, Name: "env"}
	for _, spec := range specs {
		if v := os.Getenv(spec.Env); v != "" {
			p.Cookies = append(p.Cookies, site.Cookie{Name: spec.Cookie, Value: v})
		}
	}
	return p, nil
}

// List 环境变量只有 "env" 一个来源。
func (EnvStore) List(_ context.Context, siteName string) ([]string, error) {
	if _, ok := envCookies[siteName]; !ok {
		return nil, nil
	}
	return []string{"env"}, nil
}
