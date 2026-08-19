package media

import (
	"encoding/binary"
	"time"

	"github.com/m1k1o/neko/server/pkg/types"
)

const (
	protocolVersion  byte = 1
	VideoTrack       byte = 1
	AudioTrack       byte = 2
	SampleHeaderSize      = 20
)

type sampleWriter interface {
	WriteSample(byte, types.Sample)
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

func EncodeSample(track byte, sample types.Sample) []byte {
	message := make([]byte, SampleHeaderSize+len(sample.Data))
	message[0] = protocolVersion
	message[1] = track
	if !sample.DeltaUnit {
		message[2] = 1
	}
	binary.BigEndian.PutUint64(message[4:12], uint64(durationMicroseconds(sample.PTS)))
	binary.BigEndian.PutUint64(message[12:20], uint64(durationMicroseconds(sample.Duration)))
	copy(message[SampleHeaderSize:], sample.Data)
	return message
}

func durationMicroseconds(duration time.Duration) int64 {
	if duration < 0 {
		return -1
	}
	return duration.Microseconds()
}
