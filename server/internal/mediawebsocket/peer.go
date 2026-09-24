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

	samples   chan *media.EncodedSample
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newPeer(manager *Manager, connection *websocket.Conn, sessionID string) *peer {
	return &peer{
		manager:    manager,
		connection: connection,
		sessionID:  sessionID,
		samples:    make(chan *media.EncodedSample, 64),
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
		message.Release()
		return
	default:
	}

	select {
	case peer.samples <- message:
	default:
		message.Release()
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
	defer peer.releaseSamples()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-peer.done:
			return
		case sample := <-peer.samples:
			err := peer.connection.WriteMessage(websocket.BinaryMessage, sample.Data)
			sample.Release()
			if err != nil {
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

func (peer *peer) releaseSamples() {
	for {
		select {
		case sample := <-peer.samples:
			sample.Release()
		default:
			return
		}
	}
}
