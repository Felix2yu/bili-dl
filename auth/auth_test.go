package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Felix2yu/bili-dl/site"
)

func writeAuthFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写 auth.toml 失败: %v", err)
	}
	return path
}

const sampleAuth = `
# yutto 兼容格式
[profiles.default]
sessdata = "secret-default"
bili_jct = 'csrf-token'
buvid3   = "buvid-default"   # 行尾注释

[profiles.vip]
SESSDATA = "secret-vip"

[profiles.x-main]
auth_token = "tok"
ct0        = "csrf"

[other]
sessdata = "should-be-ignored"
`

func TestFileStoreBilibiliProfile(t *testing.T) {
	store := FileStore{Path: writeAuthFile(t, sampleAuth)}

	p, err := store.Get(context.Background(), "bilibili", "default")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if p == nil {
		t.Fatal("应读到 profile")
	}
	// 键名大小写不敏感，且映射成站点实际使用的 Cookie 名。
	if v, _ := p.Cookie("SESSDATA"); v != "secret-default" {
		t.Errorf("SESSDATA = %q", v)
	}
	if v, _ := p.Cookie("bili_jct"); v != "csrf-token" {
		t.Errorf("bili_jct = %q（单引号应被去掉）", v)
	}
	if v, _ := p.Cookie("buvid3"); v != "buvid-default" {
		t.Errorf("buvid3 = %q（行尾注释应被去掉）", v)
	}
}

func TestFileStoreCaseInsensitiveKey(t *testing.T) {
	store := FileStore{Path: writeAuthFile(t, sampleAuth)}
	p, err := store.Get(context.Background(), "bilibili", "vip")
	if err != nil || p == nil {
		t.Fatalf("Get 失败: %v %v", p, err)
	}
	// 文件里写的是大写 SESSDATA，也要能按小写键映射成规范名。
	if v, _ := p.Cookie("SESSDATA"); v != "secret-vip" {
		t.Errorf("SESSDATA = %q", v)
	}
}

func TestFileStoreXProfile(t *testing.T) {
	store := FileStore{Path: writeAuthFile(t, sampleAuth)}
	p, err := store.Get(context.Background(), "x", "x-main")
	if err != nil || p == nil {
		t.Fatalf("Get 失败: %v %v", p, err)
	}
	if v, _ := p.Cookie("auth_token"); v != "tok" {
		t.Errorf("auth_token = %q", v)
	}
	if v, _ := p.Cookie("ct0"); v != "csrf" {
		t.Errorf("ct0 = %q", v)
	}
}

func TestFileStoreMissingProfileIsNil(t *testing.T) {
	store := FileStore{Path: writeAuthFile(t, sampleAuth)}
	p, err := store.Get(context.Background(), "bilibili", "does-not-exist")
	if err != nil {
		t.Fatalf("缺 profile 不应报错: %v", err)
	}
	if p != nil {
		t.Errorf("缺 profile 应返回 nil，实际 %+v", p)
	}
}

func TestFileStoreMissingFileIsNil(t *testing.T) {
	store := FileStore{Path: filepath.Join(t.TempDir(), "nope.toml")}
	p, err := store.Get(context.Background(), "bilibili", "default")
	if err != nil || p != nil {
		t.Errorf("文件不存在应返回 (nil, nil)，实际 (%v, %v)", p, err)
	}
}

func TestFileStoreIgnoresNonProfilesTables(t *testing.T) {
	store := FileStore{Path: writeAuthFile(t, sampleAuth)}
	p, _ := store.Get(context.Background(), "bilibili", "other")
	// [other] 不是 profiles.* 表，必须忽略。
	if p != nil {
		t.Errorf("[other] 不应被当成 profile，实际 %+v", p)
	}
}

func TestFileStoreList(t *testing.T) {
	store := FileStore{Path: writeAuthFile(t, sampleAuth)}
	names, err := store.List(context.Background(), "bilibili")
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	want := []string{"default", "vip", "x-main"}
	if len(names) != len(want) {
		t.Fatalf("List = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("List[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestEnvStore(t *testing.T) {
	t.Setenv("SESSDATA", "from-env")
	t.Setenv("BILI_JCT", "env-csrf")

	p, err := EnvStore{}.Get(context.Background(), "bilibili", "env")
	if err != nil || p == nil {
		t.Fatalf("EnvStore.Get 失败: %v %v", p, err)
	}
	if v, _ := p.Cookie("SESSDATA"); v != "from-env" {
		t.Errorf("SESSDATA = %q", v)
	}

	// 具名 profile 不能被环境变量顶替。
	if p, _ := (EnvStore{}).Get(context.Background(), "bilibili", "vip"); p != nil {
		t.Errorf("非 env profile 应返回 nil，实际 %+v", p)
	}
}

func TestChainPriorityAndEmptySkip(t *testing.T) {
	// 第一个 store 有 profile 但没有 cookie（空表），必须继续找下一个。
	empty := FileStore{Path: writeAuthFile(t, "[profiles.default]\n")}
	env := EnvStore{}
	t.Setenv("SESSDATA", "env-value")

	p, err := Chain{empty, env}.Get(context.Background(), "bilibili", "")
	if err != nil {
		t.Fatalf("Chain.Get 失败: %v", err)
	}
	if p == nil {
		t.Fatal("应回落到 EnvStore")
	}
	if v, _ := p.Cookie("SESSDATA"); v != "env-value" {
		t.Errorf("应取环境变量的值，实际 %q", v)
	}
}

func TestChainListMergesWithoutDuplicates(t *testing.T) {
	a := FileStore{Path: writeAuthFile(t, "[profiles.default]\nsessdata=\"x\"\n")}
	names, err := Chain{a, EnvStore{}}.List(context.Background(), "bilibili")
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(names) != 2 || names[0] != "default" || names[1] != "env" {
		t.Errorf("List = %v, want [default env]", names)
	}
}

func TestContextAuth(t *testing.T) {
	if FromContext(context.Background()) != nil {
		t.Error("空 context 应返回 nil")
	}
	if _, ok := CookieValue(context.Background(), "SESSDATA"); ok {
		t.Error("空 context 不应有 cookie")
	}

	a := &site.Auth{
		Cookies: []site.Cookie{{Name: "SESSDATA", Value: "ctx-value"}},
		Headers: map[string]string{"User-Agent": "ua-test"},
	}
	ctx := WithAuth(context.Background(), a)

	if v, ok := CookieValue(ctx, "sessdata"); !ok || v != "ctx-value" {
		t.Errorf("CookieValue 大小写不敏感失败: %q %v", v, ok)
	}
	if v, ok := HeaderValue(ctx, "user-agent"); !ok || v != "ua-test" {
		t.Errorf("HeaderValue 失败: %q %v", v, ok)
	}

	// 显式传 nil 表示「本次不要登录态」，必须覆盖掉上层的凭据。
	if FromContext(WithAuth(ctx, nil)) != nil {
		t.Error("WithAuth(ctx, nil) 应显式清空")
	}
}
