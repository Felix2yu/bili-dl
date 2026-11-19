package c

import (
	"testing"
)

func TestParseRate(t *testing.T) {
	cases := map[string]int64{
		"2m":      2 * 1024 * 1024,
		"500k":    500 * 1024,
		"1.5M":    int64(1.5 * 1024 * 1024),
		"1024":    1024,
		"1g":      1024 * 1024 * 1024,
		"2MB":     2 * 1024 * 1024,
		"":        0,
		"invalid": 0,
		"0":       0,
		"-1m":     0,
	}
	for in, want := range cases {
		if got := ParseRate(in); got != want {
			t.Errorf("ParseRate(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCollectionAndPGCFlags(t *testing.T) {
	Parse([]string{"--ss", "23615", "--collection", "--progress-json", "--rate", "500k"})
	if SeasonID != "23615" || !Collection || !ProgressJSON || RateBytes != 500*1024 {
		t.Errorf("ss=%q collection=%v json=%v rate=%d", SeasonID, Collection, ProgressJSON, RateBytes)
	}
	Parse([]string{"--ep", "999", "--no-progress-json", "--no-collection"})
	if EpID != "999" || ProgressJSON || Collection {
		t.Errorf("ep=%q json=%v collection=%v", EpID, ProgressJSON, Collection)
	}
}

func TestDefaultsExtrasOn(t *testing.T) {
	Defaults()
	if !DownloadDanmaku || !DownloadSubtitle || !SaveCover {
		t.Error("弹幕/字幕/封面默认应开启（对齐 yutto）")
	}
}
