package mediawebtransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

	webtransport "github.com/quic-go/webtransport-go"

	"github.com/m1k1o/neko/server/internal/media"
	"github.com/m1k1o/neko/server/pkg/types"
)

type peer struct {
	manager   *Manager
	session   *webtransport.Session
	sessionID string

	samples   chan *media.EncodedSample
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newPeer(manager *Manager, session *webtransport.Session, sessionID string) *peer {
	return &peer{
		manager:   manager,
		session:   session,
		sessionID: sessionID,
		samples:   make(chan *media.EncodedSample, 64),
		done:      make(chan struct{}),
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
		peer.Close(errors.New("media webtransport client is too slow"))
	}
}

func (peer *peer) Close(err error) {
	peer.closeOnce.Do(func() {
		close(peer.done)
		if err != nil {
			peer.manager.logger.Warn().Err(err).Str("session_id", peer.sessionID).Msg("closing media webtransport")
		}
		_ = peer.session.CloseWithError(0, "")
	})
}

func (peer *peer) Run() {
	peer.wg.Add(1)
	go peer.write()

	select {
	case <-peer.done:
	case <-peer.session.Context().Done():
		peer.Close(nil)
	}
	peer.wg.Wait()
}

func (peer *peer) write() {
	defer peer.wg.Done()
	defer peer.releaseSamples()
	for {
		select {
		case <-peer.done:
			return
		case sample := <-peer.samples:
			if err := peer.writeSample(sample); err != nil {
				peer.Close(err)
				return
			}
		}
	}
}

func (peer *peer) writeSample(sample *media.EncodedSample) error {
	defer sample.Release()
	ctx, cancel := context.WithTimeout(peer.session.Context(), 5*time.Second)
	stream, err := peer.session.OpenUniStreamSync(ctx)
	cancel()
	if err != nil {
		return err
	}
	_ = stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.Copy(stream, bytes.NewReader(sample.Data)); err != nil {
		stream.CancelWrite(0)
		return err
	}
	return stream.Close()
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
