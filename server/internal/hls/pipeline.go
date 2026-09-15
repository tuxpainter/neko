package hls

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/m1k1o/neko/server/pkg/gst"
)

type quality struct {
	width, height, bitrate int
}

var qualities = map[string]quality{
	"480p":  {width: 854, height: 480, bitrate: 1500},
	"720p":  {width: 1280, height: 720, bitrate: 3000},
	"1080p": {width: 1920, height: 1080, bitrate: 6000},
}

// quoteLaunchString quotes a filesystem value for gst_parse_launch's string
// properties. It deliberately does not use shell quoting: gst's parser is not
// a shell and accepts backslash escapes inside double quoted values.
func quoteLaunchString(value string) string {
	return strconv.Quote(value)
}

func newPipeline(videoSource, audioSource, dir string, q quality) (gst.Pipeline, error) {
	if q.width <= 0 || q.height <= 0 || q.bitrate <= 0 {
		return nil, fmt.Errorf("invalid HLS quality %dx%d at %dkbps", q.width, q.height, q.bitrate)
	}

	for _, element := range []string{
		"videoconvert", "videoscale", "videorate", "x264enc", "h264parse", "audioconvert",
		"audioresample", "voaacenc", "aacparse", "queue", "hlssink2",
	} {
		if err := gst.CheckElement(element); err != nil {
			return nil, fmt.Errorf("HLS pipeline unavailable: %w", err)
		}
	}
	if strings.TrimSpace(videoSource) == "" || strings.TrimSpace(audioSource) == "" {
		return nil, fmt.Errorf("video and audio sources are required")
	}

	videoLocation := quoteLaunchString(filepath.Join(dir, "segment%05d.ts"))
	playlistLocation := quoteLaunchString(filepath.Join(dir, "index.m3u8"))
	pipeline := fmt.Sprintf(
		"%s ! videoconvert ! videoscale add-borders=true ! videorate ! video/x-raw,format=I420,width=%d,height=%d,framerate=30/1,pixel-aspect-ratio=1/1 ! x264enc bitrate=%d speed-preset=veryfast tune=zerolatency key-int-max=60 bframes=0 threads=4 byte-stream=true ! video/x-h264,profile=constrained-baseline ! h264parse config-interval=-1 ! queue ! hls.video "+
			"%s ! audioconvert ! audioresample ! audio/x-raw,format=S16LE,channels=2,rate=48000 ! voaacenc bitrate=128000 ! aacparse ! queue ! hls.audio "+
			"hlssink2 name=hls target-duration=2 playlist-length=4 max-files=6 location=%s playlist-location=%s",
		videoSource, q.width, q.height, q.bitrate, audioSource, videoLocation, playlistLocation,
	)

	p, err := gst.CreatePipeline(pipeline)
	if err != nil {
		return nil, fmt.Errorf("create HLS pipeline: %w", err)
	}
	p.Play()
	return p, nil
}
