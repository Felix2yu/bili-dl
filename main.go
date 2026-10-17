package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/Felix2yu/bili-dl/api"
	"github.com/Felix2yu/bili-dl/c"
	"github.com/Felix2yu/bili-dl/util"
)

func init() {
	log.SetFlags(0)
	flag.StringVar(&c.Cookie, "c", "", "cookie,cookie的key是SESSDATA,不设置只能下载清晰度小于等于480P的视频")
	flag.StringVar(&c.UP, "up", "", "up主mid,设置后会下载该up主的所有视频")
	flag.StringVar(&c.O, "o", ".", "下载路径,可填相对或绝对路径,建议在windows下使用相对路径避免正反斜杠问题")
	flag.IntVar(&c.J, "j", 1, "同时下载的任务数\n机械硬盘不应超过5")
	flag.StringVar(&c.BVs, "bv", "", fmt.Sprintf("单或多个bv号, 多个时用逗号分隔, 如: \"BVxxxxxx,BVyyyyyyy\"\n可以通过在浏览器控制台输入以下代码来获取整页的BV\n%s", c.GetAllBV))
	flag.BoolVar(&c.Merge, "m", true, "是否合并视频流和音频流, 不合并将得到单独的视频(不含音频)和单独的音频(不含视频)文件, 不利于正常播放")
	flag.BoolVar(&c.Delete, "d", true, "合并后是否删除单视频和单音频")
	// flag.BoolVar(&c.Debug, "debug", false, "是否打印调试信息")
	flag.BoolVar(&c.AddBVSuffix, "suffix", true, "在下载的视频文件名后添加bv号\n用来解决视频重名问题\n关闭后跳过已下载功能将失效")
	flag.BoolVar(&c.DisableOverwrite, "no-overwrite", true, "跳过下载过的视频\n注意: 需要先前下载时没有指定suffix为false")
	flag.BoolVar(&c.InfoOnly, "i", false, "仅查看视频DASH信息, 不下载")
	flag.StringVar(&c.Resolution, "resolution", "", "分辨率, 可选值: 8k/dolby/4k/1080p/720p/480p/360p, 不设置则自动选择最高")
	flag.StringVar(&c.Codec, "codec", "", "视频编码, 可选值: av1/hevc/avc, 不设置则按优先级av1>hevc>avc")
	flag.BoolVar(&c.AudioOnly, "audio-only", false, "仅下载音频")
	flag.Parse()
	c.WD, _ = os.Getwd()
	if runtime.GOOS == "windows" {
		pattern := `^[a-zA-Z]:\\(?:[^\\/:*?"<>|\r\n]+\\)*[^\\/:*?"<>|\r\n]*$`
		if matched, _ := regexp.MatchString(pattern, c.O); !matched {
			c.O = filepath.Join(c.WD, c.O)
			err := os.MkdirAll(c.O, os.ModePerm)
			if err != nil {
				panic(err)
			}
		}
	} else {
		if !strings.HasPrefix(c.O, "/") {
			c.O = filepath.Join(c.WD, c.O)
			err := os.MkdirAll(c.O, os.ModePerm)
			if err != nil {
				panic(err)
			}
		}
	}
	log.Println("下载路径: ", c.O)
	cmd := exec.Command("ffmpeg", "-version")
	//cmd.Stdout = os.Stdout
	err := cmd.Run()
	if err != nil {
		log.Println(err)
		log.Println("ffmpeg未找到，将不会合并音频和视频")
	} else {
		c.FFMPEG = true
	}
	reg := regexp.MustCompile(`.*_BV[a-zA-Z0-9]+(-merged)?\.(mp4|mkv|mov)`)
	bvReg := regexp.MustCompile(`BV[a-zA-Z0-9]+`)
	bvs := make(map[string]struct{})
	for _, v := range strings.Split(c.BVs, ",") {
		if v != "" {
			bvs[v] = struct{}{}
		}
	}
	exists := make(map[string]struct{})
	if c.DisableOverwrite && c.O != "" {
		_ = os.MkdirAll(c.O, 0755)
		err = filepath.WalkDir(c.O, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d != nil && !d.IsDir() && reg.MatchString(d.Name()) {
				if bv := bvReg.FindString(d.Name()); bv != "" {
					exists[bv] = struct{}{}
				}
			}
			return nil
		})
		if err != nil {
			log.Fatalln(err)
		}
		for k := range bvs {
			if _, ok := exists[k]; ok {
				log.Printf("%s已存在, 将不会下载\n", k)
				delete(bvs, k)
			}
		}
		keys := make([]string, 0, len(bvs))
		for k := range bvs {
			keys = append(keys, k)
		}
		c.BVs = strings.Join(keys, ",")
	}
}

func main() {
	if c.BVs != "" {
		split := strings.Split(c.BVs, ",")
		limit := util.NewGoLimit(c.J)
		//wg := &sync.WaitGroup{}
		for _, v := range split {
			limit.Add()
			//wg.Add(1)
			v := v
			go func() {
				defer limit.Done()
				//defer wg.Done()
				video, err := api.VideoFromBV(v)
				if err != nil {
					log.Println(err)
					return
				}
				_, err = api.ResolveVideo(video)
				if err != nil {
					log.Println(err)
					return
				}
				if c.InfoOnly {
					err = api.PrintDASHInfo(*video)
					if err != nil {
						log.Println(err)
					}
					return
				}
				stream, err := api.GetStream(*video)
				if err != nil {
					log.Println(err)
					return
				}
				err = api.Dl(stream)
				if err != nil {
					log.Println(err)
					return
				}
				if c.FFMPEG {
					err := api.Merge(stream)
					if err != nil {
						log.Println(err)
					}
				}
			}()
		}
		limit.Wait()
	}
	if c.UP != "" {
		videos, err := api.AllVideo(c.UP)
		if err != nil {
			log.Fatalln(err)
		}
		limit := util.NewGoLimit(c.J)
		for _, v := range videos {
			limit.Add()
			go func(v api.Video) {
				defer limit.Done()
				_, err := api.ResolveVideo(&v)
				if err != nil {
					log.Println(err)
					return
				}
				if c.InfoOnly {
					err = api.PrintDASHInfo(v)
					if err != nil {
						log.Println(err)
					}
					return
				}
				for i := 0; i < 3; i++ {
					stream, err := api.GetStream(v)
					if err != nil {
						log.Println(err)
						continue
					}
					err = api.Dl(stream)
					if err != nil {
						log.Println(err)
						continue
					}
					if c.FFMPEG {
						if c.Merge {
							err := api.Merge(stream)
							if err != nil {
								log.Println(err)
							}
						}
					}
					break
				}
			}(v)
		}
		limit.Wait()
	}
	if c.InfoOnly {
		log.Println("DASH信息查看完成")
	} else {
		log.Println("下载完成")
	}
}
