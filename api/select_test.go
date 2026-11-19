package api

import "testing"

func Test_selectVideoStream(t *testing.T) {
	streams := []dashStream{
		{ID: 80, Codecs: "avc064032", Width: 1280, Height: 720},
		{ID: 80, Codecs: "hev1.1.6", Width: 1280, Height: 720},
		{ID: 116, Codecs: "av01.0.05M.08", Width: 1280, Height: 720},
		{ID: 32, Codecs: "avc064032", Width: 640, Height: 480},
	}

	cases := []struct {
		name   string
		wanted int
		want   int
	}{
		{"未指定画质时按编码优先级", 0, 2},
		{"指定画质 id 时同 id 内按编码优先级", 80, 1},
		{"画质 id 无命中时退回全局择优", 999, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectVideoStream(streams, tc.wanted); got != tc.want {
				t.Fatalf("selectVideoStream(wanted=%d) = %d, 期望 %d (%+v)",
					tc.wanted, got, tc.want, streams[got])
			}
		})
	}
}

func Test_videoOptions(t *testing.T) {
	got := videoOptions([]dashStream{
		{ID: 116, Codecs: "av01.0.05M.08", Width: 1920, Height: 1080, Bandwidth: 3000000},
		{ID: 32, Codecs: "avc064032", Width: 640, Height: 480, Bandwidth: 320000},
	})
	if len(got) != 2 {
		t.Fatalf("候选数 = %d, 期望 2", len(got))
	}
	if got[0].Ext != "mkv" || got[1].Ext != "mp4" {
		t.Fatalf("容器按编码决策失效: %+v", got)
	}
	if got[0].ID != 116 || got[0].Height != 1080 || got[0].Bandwidth != 3000000 {
		t.Fatalf("字段映射错误: %+v", got[0])
	}
}

func Test_audioOptions(t *testing.T) {
	var dash dashInfo
	dash.Audio = []dashStream{{ID: 30280, Codecs: "mp4a.40.2", Bandwidth: 192000}}
	dash.Dolby.Audio = []dashStream{{ID: 30250, Codecs: "eac-3", Bandwidth: 640000}}
	dash.Flac.Audio = []dashStream{{ID: 30251, Codecs: "fLaC", Bandwidth: 900000}}

	got := audioOptions(dash)
	if len(got) != 3 {
		t.Fatalf("候选数 = %d, 期望普通+dolby+flac 共 3 条", len(got))
	}
	if got[0].Ext != "m4a" || got[2].Ext != "flac" {
		t.Fatalf("无损轨应保留 flac 容器: %+v", got)
	}
}
