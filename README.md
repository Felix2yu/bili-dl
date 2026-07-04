# bili-dl

## 安装
``` shell
go install github.com/yu1745/bili-dl@latest
```

## 注意

* 要想下载画质高于480P的视频请指定cookie, cookie获取方式为用浏览器登录b站后，按F12打开控制台，点击右上角加号，选择"应用"或"Application",选择存储,选择Cookie,选择www.bilibili.com然后找到名称是SESSDATA那一行，将值复制出来
* 需要环境变量中有ffmpeg，软件使用dash的方式取流，取得的音视频流是分开的，需要调用ffmpeg合并

## 功能

下载b站视频，支持批量下载，支持指定cookie实现高画质视频下载，支持通过UP主mid获取其所有视频。支持选择分辨率和视频编码，支持仅下载音频。

## 参数

``` shell
-bv string
    单或多个bv号, 多个时用逗号分隔, 如: "BVxxxxxx,BVyyyyyyy"
-c string
    cookie,cookie的key是SESSDATA,不设置只能下载清晰度小于等于480P的视频
-resolution string
    分辨率, 可选值: 8k/dolby/4k/1080p/720p/480p/360p
    不设置则自动选择最高
-codec string
    视频编码, 可选值: av1/hevc/avc
    不设置则按优先级av1>hevc>avc
-audio-only
    仅下载音频(最高码率)
-d    合并后是否删除单视频和单音频 (default true)
-j int
    同时下载的任务数, 机械硬盘不应超过5 (default 1)
-m    是否合并视频流和音频流 (default true)
-no-overwrite
    跳过下载过的视频 (default true)
-o string
    下载路径,可填相对或绝对路径 (default ".")
-suffix
    在下载的视频文件名后添加bv号 (default true)
-up string
    UP主mid,设置后会下载该UP主的所有视频
-i    仅查看视频DASH信息, 不下载
```

## 容器格式

根据视频编码自动选择容器格式：

| 编码 | 容器 |
|------|------|
| AVC (H.264) | .mp4 |
| HEVC (H.265) | .mov |
| AV1 | .mkv |

音频文件统一为 .m4a (AAC编码)。

## 使用示例

``` shell
# 下载指定BV号视频(自动选择最高分辨率和最优编码)
bili-dl -bv BV1iyQhB5Eze -c "你的SESSDATA" -o /path/to/save

# 下载多个BV号视频
bili-dl -bv "BV1iyQhB5Eze,BV1BzQhBmEtK" -c "你的SESSDATA" -o /path/to/save -j 3

# 通过UP主mid下载该UP所有视频
bili-dl -up 3546944890734614 -c "你的SESSDATA" -o /path/to/save -j 3

# 指定分辨率和编码
bili-dl -bv BV1iyQhB5Eze -c "你的SESSDATA" -resolution 1080p -codec avc

# 仅下载音频
bili-dl -bv BV1iyQhB5Eze -c "你的SESSDATA" -audio-only

# 仅查看DASH信息(不下载)
bili-dl -bv BV1iyQhB5Eze -c "你的SESSDATA" -i
```
