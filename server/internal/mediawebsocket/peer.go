package mediawebsocket

import (
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/m1k1o/neko/server/internal/media"
	"github.com/m1k1o/neko/server/pkg/types"
)

type peer struct {
	manager    *Manager
	connection *websocket.Conn
	sessionID  string

	samples   chan []byte
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newPeer(manager *Manager, connection *websocket.Conn, sessionID string) *peer {
	return &peer{
		manager:    manager,
		connection: connection,
		sessionID:  sessionID,
		samples:    make(chan []byte, 64),
		done:       make(chan struct{}),
	}
}

func (peer *peer) SessionID() string {
	return peer.sessionID
}

func (peer *peer) WriteSample(track byte, sample types.Sample) {
	message := media.EncodeSample(track, sample)
	select {
	case <-peer.done:
		return
	default:
	}

	select {
	case peer.samples <- message:
	default:
		peer.Close(errors.New("media websocket client is too slow"))
	}
}

func (peer *peer) Close(err error) {
	peer.closeOnce.Do(func() {
		close(peer.done)
		if err != nil {
			peer.manager.logger.Warn().Err(err).Str("session_id", peer.sessionID).Msg("closing media websocket")
		}
		_ = peer.connection.Close()
	})
}

func (peer *peer) Run() {
	peer.wg.Add(1)
	go peer.write()

	for {
		if _, _, err := peer.connection.ReadMessage(); err != nil {
			peer.Close(nil)
			break
		}
	}
	peer.wg.Wait()
}

func (peer *peer) write() {
	defer peer.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-peer.done:
			return
		case sample := <-peer.samples:
			if err := peer.connection.WriteMessage(websocket.BinaryMessage, sample); err != nil {
				peer.Close(err)
				return
			}
		case <-ticker.C:
			if err := peer.connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				peer.Close(err)
				return
			}
		}
	}
}
