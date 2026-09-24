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

	videoSamples chan *sampleRecord
	audioSamples chan *sampleRecord
	done         chan struct{}
	closeOnce    sync.Once
	wg           sync.WaitGroup
}

const sampleQueueSize = 32

var sampleRecordPool sync.Pool

type sampleRecord struct {
	header  media.SampleHeader
	payload []byte
}

func newSampleRecord(track byte, sample types.Sample) *sampleRecord {
	record, _ := sampleRecordPool.Get().(*sampleRecord)
	if record == nil {
		record = &sampleRecord{}
	}
	media.EncodeSampleHeader(&record.header, track, sample)
	record.payload = sample.Data
	return record
}

func (record *sampleRecord) release() {
	record.payload = nil
	sampleRecordPool.Put(record)
}

func newPeer(manager *Manager, session *webtransport.Session, sessionID string) *peer {
	return &peer{
		manager:      manager,
		session:      session,
		sessionID:    sessionID,
		videoSamples: make(chan *sampleRecord, sampleQueueSize),
		audioSamples: make(chan *sampleRecord, sampleQueueSize),
		done:         make(chan struct{}),
	}
}

func (peer *peer) SessionID() string {
	return peer.sessionID
}

func (peer *peer) WriteSample(track byte, sample types.Sample) {
	var samples chan *sampleRecord
	switch track {
	case media.VideoTrack:
		samples = peer.videoSamples
	case media.AudioTrack:
		samples = peer.audioSamples
	default:
		peer.Close(errors.New("unknown media track"))
		return
	}
	record := newSampleRecord(track, sample)
	select {
	case <-peer.done:
		record.release()
		return
	default:
	}

	select {
	case samples <- record:
	default:
		record.release()
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

func (peer *peer) write(samples chan *sampleRecord) {
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
		case record := <-samples:
			_ = stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := writeRecord(stream, &recordHeader, record); err != nil {
				record.release()
				peer.Close(err)
				return
			}
			record.release()
		}
	}
}

func writeRecord(writer io.Writer, recordHeader *[4]byte, record *sampleRecord) error {
	size := len(record.header) + len(record.payload)
	if size > math.MaxUint32 {
		return errors.New("media sample exceeds framing limit")
	}
	binary.BigEndian.PutUint32(recordHeader[:], uint32(size))
	if written, err := writer.Write(recordHeader[:]); err != nil {
		return err
	} else if written != len(recordHeader) {
		return io.ErrShortWrite
	}
	if written, err := writer.Write(record.header[:]); err != nil {
		return err
	} else if written != len(record.header) {
		return io.ErrShortWrite
	}
	if written, err := writer.Write(record.payload); err != nil {
		return err
	} else if written != len(record.payload) {
		return io.ErrShortWrite
	}
	return nil
}

func (peer *peer) releaseSamples(samples chan *sampleRecord) {
	for {
		select {
		case record := <-samples:
			record.release()
		default:
			return
		}
	}
}
