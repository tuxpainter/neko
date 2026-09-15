package hls

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/m1k1o/neko/server/internal/config"
	"github.com/m1k1o/neko/server/pkg/auth"
	"github.com/m1k1o/neko/server/pkg/gst"
	"github.com/m1k1o/neko/server/pkg/types"
	"github.com/m1k1o/neko/server/pkg/utils"
)

var segmentName = regexp.MustCompile(`^segment[0-9]+\.ts$`)

// Manager owns one shared, room-wide output. Playback keys grant read access
// only and are intentionally independent of individual viewers' neko sessions.
type Manager struct {
	sessions                             types.SessionManager
	videoSource, audioSource, pathPrefix string

	mu                    sync.Mutex // serializes lifecycle changes and authorization/file opening
	output                *output
	key, quality, message string
	resizing, closed      bool
}

type output struct {
	pipeline gst.Pipeline
	dir      string
}

type Status struct {
	IsActive     bool   `json:"is_active"`
	Quality      string `json:"quality"`
	PlaybackPath string `json:"playback_path,omitempty"`
	Ready        bool   `json:"ready"`
	Message      string `json:"message,omitempty"`
}

func New(sessions types.SessionManager, capture *config.Capture, pathPrefix string) *Manager {
	manager := &Manager{
		sessions:    sessions,
		videoSource: fmt.Sprintf("ximagesrc display-name=%q show-pointer=%v use-damage=false", capture.Display, capture.VideoShowPointer),
		audioSource: fmt.Sprintf("pulsesrc device=%q", capture.AudioDevice),
		pathPrefix:  pathPrefix,
		quality:     "720p",
	}
	sessions.OnSettingsChanged(func(_ types.Session, new, _ types.Settings) {
		if new.PrivateMode {
			manager.mu.Lock()
			defer manager.mu.Unlock()
			manager.stopLocked("Stream revoked because private mode was enabled.")
		}
	})
	return manager
}

func (manager *Manager) Route(router types.Router) {
	router = router.With(auth.AdminsOnly)
	router.Get("/", manager.status)
	router.Post("/", manager.create)
	router.Delete("/", manager.revoke)
}

func (manager *Manager) RoutePlayback(router types.Router) {
	router.Get("/hls/{key}/{file}", manager.playback)
	router.Head("/hls/{key}/{file}", manager.playback)
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (manager *Manager) statusLocked() Status {
	status := Status{Quality: manager.quality, Message: manager.message}
	if manager.output != nil && !manager.sessions.Settings().PrivateMode {
		status.IsActive = true
		status.Ready = manager.output.ready()
		status.PlaybackPath = path.Join(manager.pathPrefix, "/hls", manager.key, "index.m3u8")
	}
	return status
}

func (manager *Manager) status(w http.ResponseWriter, _ *http.Request) error {
	noStore(w)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return utils.HttpSuccess(w, manager.statusLocked())
}

func (manager *Manager) create(w http.ResponseWriter, r *http.Request) error {
	noStore(w)
	var payload struct {
		Quality string `json:"quality"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := utils.HttpJsonRequest(w, r, &payload); err != nil {
		return err
	}
	quality, ok := qualities[payload.Quality]
	if !ok {
		return utils.HttpBadRequest("quality must be 480p, 720p, or 1080p")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed || manager.resizing {
		return utils.HttpError(http.StatusServiceUnavailable, "HLS output is unavailable during shutdown or screen resize")
	}
	if manager.sessions.Settings().PrivateMode {
		return utils.HttpForbidden("disable private mode before creating an HLS stream")
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return err
	}

	// Rotating a healthy stream's key does not restart its encoder. A new
	// quality is prepared first so a startup failure preserves the old output.
	if manager.output == nil || manager.quality != payload.Quality || !manager.output.ready() {
		dir, err := os.MkdirTemp("", "neko-hls-")
		if err != nil {
			return err
		}
		pipeline, err := newPipeline(manager.videoSource, manager.audioSource, dir, quality)
		if err != nil {
			os.RemoveAll(dir)
			return utils.HttpInternalServerError("unable to start HLS encoder").WithInternalErr(err)
		}
		candidate := &output{pipeline: pipeline, dir: dir}
		if err := candidate.waitReady(r.Context()); err != nil {
			candidate.close()
			return utils.HttpError(http.StatusServiceUnavailable, "HLS encoder did not produce video; check capture devices and GStreamer plugins").WithInternalErr(err)
		}
		// Settings may change while the native encoder is starting.
		if manager.sessions.Settings().PrivateMode {
			candidate.close()
			return utils.HttpForbidden("private mode was enabled while starting the stream")
		}
		manager.stopLocked("")
		manager.output = candidate
	}
	manager.key = base64.RawURLEncoding.EncodeToString(secret[:])
	manager.quality = payload.Quality
	manager.message = ""
	return utils.HttpSuccess(w, manager.statusLocked())
}

func (manager *Manager) revoke(w http.ResponseWriter, _ *http.Request) error {
	noStore(w)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.stopLocked("")
	return utils.HttpSuccess(w, manager.statusLocked())
}

func (manager *Manager) stopLocked(message string) {
	manager.key = ""
	manager.message = message
	if manager.output != nil {
		manager.output.close()
		manager.output = nil
	}
}

// Stop before capture dimensions change. A new URL must be created explicitly,
// avoiding reused HLS sequence numbers and accidentally resuming a revoked link.
func (manager *Manager) BeforeScreenSizeChange() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.resizing = true
	manager.stopLocked("Stream revoked because the desktop resolution changed. Create a new stream to resume.")
}

func (manager *Manager) AfterScreenSizeChange() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.resizing = false
}

func (manager *Manager) Shutdown() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.closed = true
	manager.stopLocked("")
}

func (manager *Manager) playback(w http.ResponseWriter, r *http.Request) error {
	noStore(w)
	key, name := chi.URLParam(r, "key"), chi.URLParam(r, "file")
	manager.mu.Lock()
	if manager.output == nil || manager.sessions.Settings().PrivateMode || subtle.ConstantTimeCompare([]byte(key), []byte(manager.key)) != 1 {
		manager.mu.Unlock()
		return utils.HttpUnauthorized("invalid or revoked stream key")
	}
	if name != "index.m3u8" && !segmentName.MatchString(name) {
		manager.mu.Unlock()
		return utils.HttpNotFound()
	}
	// Open under the authorization lock, but never hold it while a slow client
	// downloads. Revocation blocks new requests, not already-authorized transfers.
	root, err := os.OpenRoot(manager.output.dir)
	if err != nil {
		manager.mu.Unlock()
		return utils.HttpNotFound()
	}
	file, err := root.Open(name)
	root.Close()
	manager.mu.Unlock()
	if err != nil {
		return utils.HttpNotFound()
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return utils.HttpNotFound()
	}
	if name == "index.m3u8" {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else {
		w.Header().Set("Content-Type", "video/mp2t")
	}
	http.ServeContent(w, r, name, info.ModTime(), file)
	return nil
}

func (output *output) ready() bool {
	info, err := os.Stat(filepath.Join(output.dir, "index.m3u8"))
	return err == nil && info.Size() > 0 && time.Since(info.ModTime()) < 10*time.Second
}

func (output *output) waitReady(ctx context.Context) error {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if output.ready() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("HLS startup timed out")
		case <-tick.C:
		}
	}
}

func (output *output) close() {
	output.pipeline.Destroy()
	if err := os.RemoveAll(output.dir); err != nil {
		log.Error().Err(err).Msg("unable to remove HLS temporary output")
	}
}
