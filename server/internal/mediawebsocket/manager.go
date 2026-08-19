package mediawebsocket

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/m1k1o/neko/server/pkg/auth"
	"github.com/m1k1o/neko/server/pkg/types"
	"github.com/m1k1o/neko/server/pkg/utils"
)

const ticketLifetime = 30 * time.Second

type ticket struct {
	sessionID string
	videoID   string
	expires   time.Time
}

type Manager struct {
	logger   zerolog.Logger
	sessions types.SessionManager
	media    types.EncodedMediaSource

	mu      sync.Mutex
	tickets map[string]ticket
	peers   map[*peer]struct{}
	closed  bool
}

func New(sessions types.SessionManager, media types.EncodedMediaSource) *Manager {
	manager := &Manager{
		logger:   log.With().Str("module", "media-websocket").Logger(),
		sessions: sessions,
		media:    media,
		tickets:  map[string]ticket{},
		peers:    map[*peer]struct{}{},
	}

	sessions.OnDeleted(func(session types.Session) {
		manager.closeSession(session.ID(), errors.New("session deleted"))
	})
	sessions.OnProfileChanged(func(session types.Session, profile, _ types.MemberProfile) {
		if !profile.CanConnect || !profile.CanWatch {
			manager.closeSession(session.ID(), errors.New("media permission revoked"))
		}
	})
	sessions.OnSettingsChanged(func(_ types.Session, new, _ types.Settings) {
		if !new.PrivateMode {
			return
		}
		for _, session := range sessions.List() {
			if session.PrivateModeEnabled() {
				manager.closeSession(session.ID(), errors.New("private mode enabled"))
			}
		}
	})

	return manager
}

func (manager *Manager) Route(router types.Router) {
	router.With(auth.CanWatchOnly).Post("/ticket", manager.createTicket)
}

type ticketRequest struct {
	VideoID string `json:"video_id"`
}

type ticketResponse struct {
	Ticket   string      `json:"ticket"`
	Protocol string      `json:"protocol"`
	Video    codecConfig `json:"video"`
	Audio    codecConfig `json:"audio"`
}

type codecConfig struct {
	Codec            string `json:"codec"`
	SampleRate       uint32 `json:"sample_rate,omitempty"`
	NumberOfChannels uint16 `json:"number_of_channels,omitempty"`
}

func (manager *Manager) createTicket(w http.ResponseWriter, request *http.Request) error {
	session, ok := auth.GetSession(request)
	if !ok {
		return utils.HttpUnauthorized()
	}
	if !session.Profile().CanConnect {
		return utils.HttpForbidden("session cannot connect")
	}
	if session.PrivateModeEnabled() {
		return utils.HttpForbidden("private mode is enabled")
	}

	payload := ticketRequest{}
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		return utils.HttpBadRequest().WithInternalErr(err)
	}
	_, _, videoConfig, audioConfig, err := manager.resolve(payload.VideoID)
	if err != nil {
		return utils.HttpUnprocessableEntity(err.Error())
	}

	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(random)

	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return utils.HttpError(http.StatusServiceUnavailable, "media websocket is shutting down")
	}
	now := time.Now()
	for token, ticket := range manager.tickets {
		if now.After(ticket.expires) {
			delete(manager.tickets, token)
		}
	}
	manager.tickets[token] = ticket{sessionID: session.ID(), videoID: payload.VideoID, expires: now.Add(ticketLifetime)}
	manager.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(ticketResponse{
		Ticket: token, Protocol: "webcodecs-v1", Video: videoConfig, Audio: audioConfig,
	})
}

func (manager *Manager) resolve(videoID string) (types.EncodedStream, types.EncodedStream, codecConfig, codecConfig, error) {
	video, ok := manager.media.Video().GetStream(types.StreamSelector{ID: videoID})
	if !ok {
		return nil, nil, codecConfig{}, codecConfig{}, errors.New("video stream not found")
	}

	videoConfig := codecConfig{}
	switch video.Codec().Name {
	case "vp8":
		videoConfig.Codec = "vp8"
	case "h264":
		videoConfig.Codec = "avc1.42E01F"
	default:
		return nil, nil, codecConfig{}, codecConfig{}, errors.New("video codec is not supported by WebCodecs transport")
	}

	audio := manager.media.Audio()
	if audio.Codec().Name != "opus" {
		return nil, nil, codecConfig{}, codecConfig{}, errors.New("audio codec is not supported by WebCodecs transport")
	}
	audioConfig := codecConfig{
		Codec:            "opus",
		SampleRate:       audio.Codec().Capability.ClockRate,
		NumberOfChannels: audio.Codec().Capability.Channels,
	}
	return video, audio, videoConfig, audioConfig, nil
}

func (manager *Manager) Upgrade(checkOrigin types.CheckOrigin) types.RouterHandler {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 64 * 1024,
		CheckOrigin:     checkOrigin,
	}

	return func(w http.ResponseWriter, request *http.Request) error {
		ticket, session, err := manager.consumeTicket(request.URL.Query().Get("ticket"))
		if err != nil {
			return utils.HttpUnauthorized().WithInternalErr(err)
		}

		connection, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			return err
		}
		peer := newPeer(manager, connection, session.ID())
		if !manager.addPeer(peer) {
			return nil
		}
		defer manager.removePeer(peer)

		video, audio, _, _, err := manager.resolve(ticket.videoID)
		if err != nil {
			peer.Close(err)
			return nil
		}
		audioSubscription, err := audio.Subscribe(newTrackConsumer(peer, audioTrack))
		if err != nil {
			peer.Close(err)
			return nil
		}
		defer audioSubscription.Close()
		videoSubscription, err := video.Subscribe(newTrackConsumer(peer, videoTrack))
		if err != nil {
			peer.Close(err)
			return nil
		}
		defer videoSubscription.Close()

		peer.run()
		return nil
	}
}

func (manager *Manager) consumeTicket(token string) (ticket, types.Session, error) {
	manager.mu.Lock()
	ticket, ok := manager.tickets[token]
	delete(manager.tickets, token)
	closed := manager.closed
	manager.mu.Unlock()

	if closed {
		return ticket, nil, errors.New("media websocket is shutting down")
	}
	if !ok || token == "" || time.Now().After(ticket.expires) {
		return ticket, nil, errors.New("invalid or expired media ticket")
	}
	session, ok := manager.sessions.Get(ticket.sessionID)
	if !ok || !session.Profile().CanConnect || !session.Profile().CanWatch || session.PrivateModeEnabled() {
		return ticket, nil, errors.New("media session is no longer authorized")
	}
	return ticket, session, nil
}

func (manager *Manager) addPeer(peer *peer) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		peer.Close(errors.New("media websocket is shutting down"))
		return false
	}
	manager.peers[peer] = struct{}{}
	return true
}

func (manager *Manager) removePeer(peer *peer) {
	peer.Close(nil)
	manager.mu.Lock()
	delete(manager.peers, peer)
	manager.mu.Unlock()
}

func (manager *Manager) closeSession(sessionID string, err error) {
	manager.mu.Lock()
	peers := make([]*peer, 0)
	for peer := range manager.peers {
		if peer.sessionID == sessionID {
			peers = append(peers, peer)
		}
	}
	manager.mu.Unlock()
	for _, peer := range peers {
		peer.Close(err)
	}
}

func (manager *Manager) Shutdown() error {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	peers := make([]*peer, 0, len(manager.peers))
	for peer := range manager.peers {
		peers = append(peers, peer)
	}
	manager.tickets = map[string]ticket{}
	manager.mu.Unlock()

	for _, peer := range peers {
		peer.Close(errors.New("media websocket is shutting down"))
	}
	return nil
}
