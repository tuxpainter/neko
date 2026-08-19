package mediawebsocket

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/m1k1o/neko/server/pkg/types"
)

func TestEncodeSample(t *testing.T) {
	sample := types.Sample{
		Data:      []byte{1, 2, 3},
		PTS:       1234567 * time.Nanosecond,
		Duration:  -1,
		DeltaUnit: false,
	}

	message := encodeSample(videoTrack, sample)
	if len(message) != sampleHeaderSize+len(sample.Data) {
		t.Fatalf("message length = %d", len(message))
	}
	if message[0] != protocolVersion || message[1] != videoTrack || message[2] != 1 {
		t.Fatalf("header = %v", message[:4])
	}
	if got := int64(binary.BigEndian.Uint64(message[4:12])); got != 1234 {
		t.Fatalf("timestamp = %d", got)
	}
	if got := int64(binary.BigEndian.Uint64(message[12:20])); got != -1 {
		t.Fatalf("duration = %d", got)
	}
	for index, value := range sample.Data {
		if message[sampleHeaderSize+index] != value {
			t.Fatalf("payload = %v", message[sampleHeaderSize:])
		}
	}
}

func TestTrackConsumerNormalizesTimestamps(t *testing.T) {
	peer := &peer{samples: make(chan []byte, 2), done: make(chan struct{})}
	consumer := newTrackConsumer(peer, audioTrack)
	consumer.WriteSample(types.Sample{PTS: 5 * time.Second, Duration: 20 * time.Millisecond})
	consumer.WriteSample(types.Sample{PTS: 5020 * time.Millisecond, Duration: 20 * time.Millisecond})

	first := <-peer.samples
	second := <-peer.samples
	if got := int64(binary.BigEndian.Uint64(first[4:12])); got != 0 {
		t.Fatalf("first timestamp = %d", got)
	}
	if got := int64(binary.BigEndian.Uint64(second[4:12])); got != 20000 {
		t.Fatalf("second timestamp = %d", got)
	}
}
