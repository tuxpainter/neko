package mediawebtransport

import (
	"net/http"

	webtransport "github.com/quic-go/webtransport-go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/m1k1o/neko/server/internal/media"
)

type Manager struct {
	logger zerolog.Logger
	media  *media.Manager
	server *webtransport.Server
}

func New(manager *media.Manager, server *webtransport.Server) *Manager {
	return &Manager{
		logger: log.With().Str("module", "media-webtransport").Logger(),
		media:  manager,
		server: server,
	}
}

func (manager *Manager) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		logger := manager.logger.With().Str("remote", request.RemoteAddr).Logger()
		logger.Debug().Msg("media webtransport request")
		ticket, session, err := manager.media.ConsumeTicket(request.URL.Query().Get("ticket"), media.TransportWebTransport)
		if err != nil {
			logger.Warn().Err(err).Msg("rejecting media webtransport ticket")
			http.Error(w, "invalid or expired media ticket", http.StatusUnauthorized)
			return
		}
		connection, err := manager.server.Upgrade(w, request)
		if err != nil {
			logger.Warn().Err(err).Msg("unable to upgrade media webtransport")
			return
		}
		logger.Info().Str("session_id", session.ID()).Msg("media webtransport connected")
		manager.media.Serve(ticket, newPeer(manager, connection, session.ID()))
	}
}
