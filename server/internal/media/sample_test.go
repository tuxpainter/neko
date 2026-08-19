package media

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/m1k1o/neko/server/pkg/types"
)

type sampleRecorder struct {
	samples [][]byte
}

func (recorder *sampleRecorder) WriteSample(track byte, sample types.Sample) {
	recorder.samples = append(recorder.samples, EncodeSample(track, sample))
}

func TestEncodeSample(t *testing.T) {
	sample := types.Sample{
		Data:      []byte{1, 2, 3},
		PTS:       1234567 * time.Nanosecond,
		Duration:  -1,
		DeltaUnit: false,
	}

	message := EncodeSample(VideoTrack, sample)
	if len(message) != SampleHeaderSize+len(sample.Data) {
		t.Fatalf("message length = %d", len(message))
	}
	if message[0] != protocolVersion || message[1] != VideoTrack || message[2] != 1 {
		t.Fatalf("header = %v", message[:4])
	}
	if got := int64(binary.BigEndian.Uint64(message[4:12])); got != 1234 {
		t.Fatalf("timestamp = %d", got)
	}
	if got := int64(binary.BigEndian.Uint64(message[12:20])); got != -1 {
		t.Fatalf("duration = %d", got)
	}
	for index, value := range sample.Data {
		if message[SampleHeaderSize+index] != value {
			t.Fatalf("payload = %v", message[SampleHeaderSize:])
		}
	}
}

func TestTrackConsumerNormalizesTimestamps(t *testing.T) {
	recorder := &sampleRecorder{}
	consumer := newTrackConsumer(recorder, AudioTrack)
	consumer.WriteSample(types.Sample{PTS: 5 * time.Second, Duration: 20 * time.Millisecond})
	consumer.WriteSample(types.Sample{PTS: 5020 * time.Millisecond, Duration: 20 * time.Millisecond})

	if got := int64(binary.BigEndian.Uint64(recorder.samples[0][4:12])); got != 0 {
		t.Fatalf("first timestamp = %d", got)
	}
	if got := int64(binary.BigEndian.Uint64(recorder.samples[1][4:12])); got != 20000 {
		t.Fatalf("second timestamp = %d", got)
	}
}
