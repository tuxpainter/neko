package hls

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/m1k1o/neko/server/internal/api"
	"github.com/m1k1o/neko/server/internal/config"
	serverhttp "github.com/m1k1o/neko/server/internal/http"
	"github.com/m1k1o/neko/server/internal/media"
	"github.com/m1k1o/neko/server/internal/mediawebsocket"
	"github.com/m1k1o/neko/server/internal/session"
	"github.com/m1k1o/neko/server/internal/websocket"
	"github.com/m1k1o/neko/server/pkg/types"
)

func nativeManager(t *testing.T) (*Manager, *session.SessionManagerCtx) {
	t.Helper()
	sessions := session.New(&config.Session{APIToken: "test-admin"})
	manager := New(sessions, &config.Capture{}, "/neko")
	manager.videoSource = "videotestsrc is-live=true pattern=ball"
	manager.audioSource = "audiotestsrc is-live=true wave=sine"
	t.Cleanup(manager.Shutdown)
	return manager, sessions
}

func createOutput(t *testing.T, manager *Manager, quality string) Status {
	t.Helper()
	response := httptest.NewRecorder()
	err := manager.create(response, httptest.NewRequest("POST", "/", strings.NewReader(`{"quality":"`+quality+`"}`)))
	if err != nil {
		t.Fatal(err)
	}
	var status Status
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.IsActive || !status.Ready || status.PlaybackPath == "" {
		t.Fatalf("stream not ready: %+v", status)
	}
	return status
}

// Exercise the actual API, session authentication, router/path prefix, HTTP
// serving and native H264/AAC encoder. No WebRTC connection is created.
func TestHTTPOutputLifecycle(t *testing.T) {
	manager, sessions := nativeManager(t)
	_, guestToken, err := sessions.Create("guest", types.MemberProfile{CanLogin: true})
	if err != nil {
		t.Fatal(err)
	}
	apiManager := api.New(sessions, nil, nil, nil)
	apiManager.AddRouter("/hls", manager.Route)
	var router types.Router
	serverhttp.New(websocket.New(sessions, nil, nil, nil), mediawebsocket.New(media.New(sessions, nil)), apiManager, &config.Server{PathPrefix: "/neko"}, func(r types.Router) {
		manager.RoutePlayback(r)
		router = r
	})
	server := httptest.NewServer(router)
	defer server.Close()

	request := func(method, target, token, body string, want int) ([]byte, http.Header) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+target, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s: status %d, want %d: %s", method, strings.ReplaceAll(target, manager.key, "[key]"), response.StatusCode, want, data)
		}
		return data, response.Header
	}
	endpoint := "/neko/api/hls/"
	for _, method := range []string{"GET", "POST", "DELETE"} {
		request(method, endpoint, "", `{ "quality":"480p" }`, 401)
		request(method, endpoint, guestToken, `{ "quality":"480p" }`, 403)
	}
	request("POST", endpoint, "test-admin", `{"quality":"4k"}`, 400)
	request("POST", endpoint, "test-admin", `{`, 400)
	request("GET", "/neko/hls/not-a-key/index.m3u8", "", "", 401)

	create := func(quality string) Status {
		t.Helper()
		data, header := request("POST", endpoint, "test-admin", `{"quality":"`+quality+`"}`, 200)
		var status Status
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatal(err)
		}
		if !status.IsActive || !status.Ready || status.Quality != quality || !strings.Contains(header.Get("Cache-Control"), "no-store") {
			t.Fatal("invalid HLS create status or cache headers")
		}
		return status
	}
	first := create("480p")
	originalDir := manager.output.dir
	playlist, header := request("GET", first.PlaybackPath, "", "", 200)
	if !bytes.HasPrefix(playlist, []byte("#EXTM3U")) || header.Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Fatal("invalid playlist response")
	}
	var segment string
	for _, line := range strings.Split(string(playlist), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			if !segmentName.MatchString(line) {
				t.Fatalf("playlist URI is not a relative segment: %q", line)
			}
			segment = path.Join(path.Dir(first.PlaybackPath), line)
		}
	}
	if segment == "" {
		t.Fatal("playlist has no segment")
	}
	data, header := request("GET", segment, "", "", 200)
	if len(data) < 188 || data[0] != 0x47 || header.Get("Content-Type") != "video/mp2t" || !strings.Contains(header.Get("Cache-Control"), "no-store") {
		t.Fatal("invalid TS segment or cache headers")
	}
	data, _ = request("HEAD", segment, "", "", 200)
	if len(data) != 0 {
		t.Fatal("HEAD returned content")
	}
	request("GET", path.Join(path.Dir(segment), "secret.txt"), "", "", 404)
	request("GET", path.Dir(segment)+"/%2e%2e%2findex.m3u8", "", "", 404)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(originalDir, "segment99999.ts")); err != nil {
		t.Fatal(err)
	}
	request("GET", path.Join(path.Dir(segment), "segment99999.ts"), "", "", 404)

	rotated := create("480p")
	if rotated.PlaybackPath == first.PlaybackPath || manager.output.dir != originalDir {
		t.Fatal("rotation must replace key but reuse healthy encoder")
	}
	request("GET", first.PlaybackPath, "", "", 401)
	request("GET", segment, "", "", 401)
	request("GET", rotated.PlaybackPath, "", "", 200)
	// A viewer key is not an administrator API credential.
	request("DELETE", endpoint, manager.key, "", 401)
	upgraded := create("720p")
	if manager.output.dir == originalDir {
		t.Fatal("quality change did not replace encoder")
	}
	if _, err := os.Stat(originalDir); !os.IsNotExist(err) {
		t.Fatal("old output not removed")
	}
	request("GET", rotated.PlaybackPath, "", "", 401)

	// A failed replacement preserves the working output and its authorization.
	manager.videoSource = "nonexistent_hls_test_source"
	request("POST", endpoint, "test-admin", `{"quality":"1080p"}`, 500)
	request("GET", upgraded.PlaybackPath, "", "", 200)
	manager.videoSource = "videotestsrc is-live=true"
	lastDir := manager.output.dir
	request("DELETE", endpoint, "test-admin", "", 200)
	request("DELETE", endpoint, "test-admin", "", 200)
	request("GET", upgraded.PlaybackPath, "", "", 401)
	if _, err := os.Stat(lastDir); !os.IsNotExist(err) {
		t.Fatal("revoked output files remain")
	}
	recreated := create("480p")
	if recreated.PlaybackPath == first.PlaybackPath {
		t.Fatal("recreated a revoked key")
	}
	request("GET", first.PlaybackPath, "", "", 401)
	request("GET", recreated.PlaybackPath, "", "", 200)
	manager.Shutdown()
	request("GET", recreated.PlaybackPath, "", "", 401)
	request("POST", endpoint, "test-admin", `{"quality":"480p"}`, 503)
}

func TestPrivateModeAndResizeRevoke(t *testing.T) {
	manager, sessions := nativeManager(t)
	admin, _, err := sessions.Create("admin", types.MemberProfile{IsAdmin: true, CanLogin: true})
	if err != nil {
		t.Fatal(err)
	}
	createOutput(t, manager, "480p")
	sessions.UpdateSettingsFunc(admin, func(s *types.Settings) bool { s.PrivateMode = true; return true })
	if manager.key != "" || manager.output != nil {
		t.Fatal("private mode did not revoke output")
	}
	err = manager.create(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(`{"quality":"480p"}`)))
	if err == nil {
		t.Fatal("created an output during private mode")
	}
	sessions.UpdateSettingsFunc(admin, func(s *types.Settings) bool { s.PrivateMode = false; return true })
	if manager.output != nil {
		t.Fatal("leaving private mode restarted sharing")
	}
	createOutput(t, manager, "480p")
	manager.BeforeScreenSizeChange()
	if manager.key != "" || manager.output != nil {
		t.Fatal("resize did not revoke output")
	}
	err = manager.create(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(`{"quality":"480p"}`)))
	if err == nil {
		t.Fatal("created output between resize hooks")
	}
	manager.AfterScreenSizeChange()
	if manager.resizing || manager.output != nil {
		t.Fatal("invalid state after resize")
	}
}

func TestConcurrentLifecycle(t *testing.T) {
	manager, _ := nativeManager(t)
	createOutput(t, manager, "480p")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 10 {
				if err := manager.status(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Go(manager.Shutdown)
	wg.Go(func() { manager.revoke(httptest.NewRecorder(), httptest.NewRequest("DELETE", "/", nil)) })
	wg.Wait()
	if manager.output != nil || manager.key != "" {
		t.Fatal("concurrent shutdown retained output")
	}
}
