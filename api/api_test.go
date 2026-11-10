package api

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/Felix2yu/bili-dl/c"
)

var testVideo = Video{BV: "BV1uP4y1S7ps", Cid: "873198432", Title: "会有人不喜欢玛奇玛？硬撑罢了！"}

// requireIntegration 用于跳过会真实下载大文件的集成测试。
// 云端/共享 IP 访问 B 站 CDN 时容易被风控（HTTP 412/567），
// CI 中无意义地重试只会造成误报，需要时用 BILI_INTEGRATION=1 显式启用。
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("BILI_INTEGRATION") != "1" {
		t.Skip("跳过真实下载测试；设置 BILI_INTEGRATION=1 启用")
	}
}

func TestVideoInfo(t *testing.T) {
	bytes, err := videoInfo("BV1uP4y1S7ps")
	if err != nil {
		t.Fatal(err)
	}
	var resp biliVideoInfoResp
	if err := json.Unmarshal(bytes, &resp); err != nil {
		t.Fatal(err)
	}
	t.Log(resp.Data.Cid.String())
}

func TestAllVideo(t *testing.T) {
	/*videos*/ _, err := AllVideo("2223018")
	if err != nil {
		t.Log(err)
	}
	//fmt.Printf("%+v\n", videos)
}

func TestStream(t *testing.T) {
	stream, err := GetStream(testVideo)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%+v\n", *stream)
}

func TestDl(t *testing.T) {
	requireIntegration(t)

	stream, err := GetStream(testVideo)
	if err != nil {
		t.Fatal(err)
	}

	// 下载到临时目录，避免测试产物污染工作区
	oldOut := c.O
	c.O = t.TempDir()
	t.Cleanup(func() { c.O = oldOut })

	if err := Dl(stream); err != nil {
		t.Fatal(err)
	}
}

func Test_fileNameFix(t *testing.T) {
	type args struct {
		name string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"test", args{"a|a"}, "a a"},
		{"test", args{"a:a"}, "a a"},
		{"test", args{"a*a"}, "a a"},
		{"test", args{"a?a"}, "a a"},
		{"test", args{"a<a"}, "a a"},
		{"test", args{"a>a"}, "a a"},
		{"test", args{"a\"a"}, "a a"},
		{"test", args{"a\\a"}, "a a"},
		{"test", args{"a/a"}, "a a"},
		{"test", args{"a|a:a*a?a<a>a\"a\\a/a"}, "a a a a a a a a a a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fileNameFix(tt.args.name); got != tt.want {
				t.Errorf("fileNameFix() = %v, want %v", got, tt.want)
			}
		})
	}
}
