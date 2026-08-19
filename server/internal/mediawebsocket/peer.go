package mediawebsocket

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/m1k1o/neko/server/pkg/types"
)

const (
	protocolVersion  byte = 1
	videoTrack       byte = 1
	audioTrack       byte = 2
	sampleHeaderSize      = 20
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

type mediaPeer interface {
	WriteSample(byte, types.Sample)
	SessionID() string
	Close(error)
	run()
}

type sampleWriter interface {
	WriteSample(byte, types.Sample)
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
	message := encodeSample(track, sample)
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

type trackConsumer struct {
	peer    sampleWriter
	track   byte
	base    time.Duration
	baseSet bool
}

func newTrackConsumer(peer sampleWriter, track byte) *trackConsumer {
	return &trackConsumer{peer: peer, track: track}
}

func (consumer *trackConsumer) WriteSample(sample types.Sample) {
	timestamp := sample.PTS
	if timestamp < 0 {
		timestamp = sample.DTS
	}
	if timestamp >= 0 {
		if !consumer.baseSet {
			consumer.base = timestamp
			consumer.baseSet = true
		}
		sample.PTS = timestamp - consumer.base
	}
	consumer.peer.WriteSample(consumer.track, sample)
}

func encodeSample(track byte, sample types.Sample) []byte {
	message := make([]byte, sampleHeaderSize+len(sample.Data))
	message[0] = protocolVersion
	message[1] = track
	if !sample.DeltaUnit {
		message[2] = 1
	}
	binary.BigEndian.PutUint64(message[4:12], uint64(durationMicroseconds(sample.PTS)))
	binary.BigEndian.PutUint64(message[12:20], uint64(durationMicroseconds(sample.Duration)))
	copy(message[sampleHeaderSize:], sample.Data)
	return message
}

func durationMicroseconds(duration time.Duration) int64 {
	if duration < 0 {
		return -1
	}
	return duration.Microseconds()
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

func (peer *peer) run() {
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
