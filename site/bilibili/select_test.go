package bilibili

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

func Test_pickCodec(t *testing.T) {
	streams := []dashStream{
		{ID: 80, Codecs: "hev1.1.6", Width: 1280, Height: 720},
		{ID: 80, Codecs: "avc064032", Width: 1280, Height: 720},
	}

	cases := []struct {
		name       string
		streams    []dashStream
		priorities []string
		legacy     string
		wantCodec  string
		wantRestr  bool
	}{
		{"无优先级无 legacy 时不限制编码", streams, nil, "", "", false},
		{"无优先级时沿用旧 codec", streams, nil, "hevc", "hevc", true},
		{"优先级首选存在时直接选中", streams, []string{"hevc", "avc"}, "", "hevc", true},
		{"首选 av1 缺失时回退 hevc", streams, []string{"av1", "hevc", "avc"}, "", "hevc", true},
		{"有优先级时忽略 --vcodec 的单一编码", streams, []string{"av1", "hevc", "avc"}, "av1", "hevc", true},
		{
			"优先级列表全部落空时返回强制空集",
			[]dashStream{{Codecs: "vp09.00.10.08"}},
			[]string{"av1", "hevc", "avc"},
			"",
			"",
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codec, restrict := pickCodec(tc.streams, tc.priorities, tc.legacy)
			if codec != tc.wantCodec || restrict != tc.wantRestr {
				t.Fatalf("pickCodec() = (%q, %v), 期望 (%q, %v)",
					codec, restrict, tc.wantCodec, tc.wantRestr)
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
