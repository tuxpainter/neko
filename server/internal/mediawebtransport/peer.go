package mediawebtransport

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
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

	videoSamples chan *media.EncodedSample
	audioSamples chan *media.EncodedSample
	done         chan struct{}
	closeOnce    sync.Once
	wg           sync.WaitGroup
}

const sampleQueueSize = 32

func newPeer(manager *Manager, session *webtransport.Session, sessionID string) *peer {
	return &peer{
		manager:      manager,
		session:      session,
		sessionID:    sessionID,
		videoSamples: make(chan *media.EncodedSample, sampleQueueSize),
		audioSamples: make(chan *media.EncodedSample, sampleQueueSize),
		done:         make(chan struct{}),
	}
}

func (peer *peer) SessionID() string {
	return peer.sessionID
}

func (peer *peer) WriteSample(track byte, sample types.Sample) {
	message := media.EncodeSample(track, sample)
	var samples chan *media.EncodedSample
	switch track {
	case media.VideoTrack:
		samples = peer.videoSamples
	case media.AudioTrack:
		samples = peer.audioSamples
	default:
		message.Release()
		peer.Close(errors.New("unknown media track"))
		return
	}
	select {
	case <-peer.done:
		message.Release()
		return
	default:
	}

	select {
	case samples <- message:
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
	peer.wg.Add(2)
	go peer.write(peer.videoSamples)
	go peer.write(peer.audioSamples)

	select {
	case <-peer.done:
	case <-peer.session.Context().Done():
		peer.Close(nil)
	}
	peer.wg.Wait()
}

func (peer *peer) write(samples chan *media.EncodedSample) {
	defer peer.wg.Done()
	defer peer.releaseSamples(samples)

	ctx, cancel := context.WithTimeout(peer.session.Context(), 5*time.Second)
	stream, err := peer.session.OpenUniStreamSync(ctx)
	cancel()
	if err != nil {
		peer.Close(err)
		return
	}
	defer stream.Close()
	var recordHeader [4]byte

	for {
		select {
		case <-peer.done:
			return
		case sample := <-samples:
			_ = stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := writeRecord(stream, &recordHeader, sample.Data); err != nil {
				sample.Release()
				peer.Close(err)
				return
			}
			sample.Release()
		}
	}
}

func writeRecord(writer io.Writer, header *[4]byte, sample []byte) error {
	if len(sample) > math.MaxUint32 {
		return errors.New("media sample exceeds framing limit")
	}
	binary.BigEndian.PutUint32(header[:], uint32(len(sample)))
	if written, err := writer.Write(header[:]); err != nil {
		return err
	} else if written != len(header) {
		return io.ErrShortWrite
	}
	if written, err := writer.Write(sample); err != nil {
		return err
	} else if written != len(sample) {
		return io.ErrShortWrite
	}
	return nil
}

func (peer *peer) releaseSamples(samples chan *media.EncodedSample) {
	for {
		select {
		case sample := <-samples:
			sample.Release()
		default:
			return
		}
	}
}
