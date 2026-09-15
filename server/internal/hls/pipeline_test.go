package hls

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/m1k1o/neko/server/pkg/gst"
)

func TestQualities(t *testing.T) {
	cases := map[string]quality{
		"480p": {854, 480, 1500}, "720p": {1280, 720, 3000}, "1080p": {1920, 1080, 6000},
	}
	for name, want := range cases {
		if got := qualities[name]; got != want {
			t.Errorf("qualities[%q] = %+v, want %+v", name, got, want)
		}
	}
}

func TestQuoteLaunchString(t *testing.T) {
	got := quoteLaunchString(`/tmp/a b\\c"d`)
	if got != `"/tmp/a b\\\\c\"d"` {
		t.Fatalf("quoted path = %q", got)
	}
}

func TestPipelineSourceConfiguration(t *testing.T) {
	for _, element := range []string{"x264enc", "voaacenc", "hlssink2"} {
		if err := gst.CheckElement(element); err != nil {
			t.Skipf("native GStreamer element %s unavailable: %v", element, err)
		}
	}
	q := qualities["480p"]
	p, err := newPipeline("videotestsrc is-live=true", "audiotestsrc is-live=true", t.TempDir(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Destroy()
	src := p.Src()
	for _, want := range []string{"bitrate=1500", "bitrate=128000", "profile=constrained-baseline", "h264parse config-interval=-1"} {
		if !strings.Contains(src, want) {
			t.Errorf("pipeline source missing %q: %s", want, src)
		}
	}
	if strings.Contains(src, "bitrate=1500000") {
		t.Error("x264enc bitrate incorrectly converted to bits per second")
	}
}
func TestNewPipelineNativeSources(t *testing.T) {
	for _, element := range []string{"videotestsrc", "audiotestsrc", "x264enc", "voaacenc", "hlssink2"} {
		if err := gst.CheckElement(element); err != nil {
			t.Skipf("native GStreamer element %s unavailable: %v", element, err)
		}
	}
	dir := t.TempDir()
	started := time.Now()
	p, err := newPipeline("videotestsrc is-live=true pattern=black", "audiotestsrc is-live=true wave=sine", dir, qualities["480p"])
	if err != nil {
		t.Fatal(err)
	}
	defer p.Destroy()

	playlist := filepath.Join(dir, "index.m3u8")
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(playlist); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("playlist did not appear within 8s (elapsed %s)", time.Since(started).Round(time.Millisecond))
		}
		time.Sleep(100 * time.Millisecond)
	}
	data, err := os.ReadFile(playlist)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "#EXTM3U") || !strings.Contains(text, ".ts") {
		t.Fatalf("invalid HLS playlist: %q", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "segment00000.ts")); err != nil {
		t.Fatalf("playlist has no first segment: %v", err)
	}
	if _, err := exec.LookPath("ffprobe"); err == nil {
		cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height,r_frame_rate", "-of", "default=noprint_wrappers=1", filepath.Join(dir, "segment00000.ts"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ffprobe rejected TS: %v\n%s", err, out)
		}
		video := string(out)
		for _, want := range []string{"codec_name=h264", "width=854", "height=480", "r_frame_rate=30/1"} {
			if !strings.Contains(video, want) {
				t.Errorf("ffprobe video output missing %q: %s", want, video)
			}
		}
		cmd = exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_name,channels,sample_rate", "-of", "default=noprint_wrappers=1", filepath.Join(dir, "segment00000.ts"))
		out, err = cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ffprobe found no valid AAC stream: %v\n%s", err, out)
		}
		audio := string(out)
		for _, want := range []string{"codec_name=aac", "channels=2", "sample_rate=48000"} {
			if !strings.Contains(audio, want) {
				t.Errorf("ffprobe audio output missing %q: %s", want, audio)
			}
		}
	}
	t.Logf("playlist latency: %s", time.Since(started).Round(time.Millisecond))
}
