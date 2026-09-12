package mediawebtransport

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/m1k1o/neko/server/internal/media"
	"github.com/m1k1o/neko/server/pkg/types"
)

type discardWriter struct{}

func (discardWriter) Write(data []byte) (int, error) {
	return len(data), nil
}

func TestWriteRecord(t *testing.T) {
	payload := []byte{1, 2, 3, 4}
	record := newSampleRecord(media.VideoTrack, types.Sample{Data: payload})
	defer record.release()
	var output bytes.Buffer
	var header [4]byte
	if err := writeRecord(&output, &header, record); err != nil {
		t.Fatalf("writeRecord() error = %v", err)
	}

	message := output.Bytes()
	if size := binary.BigEndian.Uint32(message[:4]); size != uint32(media.SampleHeaderSize+len(payload)) {
		t.Fatalf("record size = %d", size)
	}
	if !bytes.Equal(message[4:4+media.SampleHeaderSize], record.header[:]) {
		t.Fatalf("sample header = %v", message[4:4+media.SampleHeaderSize])
	}
	if !bytes.Equal(message[4+media.SampleHeaderSize:], payload) {
		t.Fatalf("record payload = %v", message[4+media.SampleHeaderSize:])
	}
}

func TestWriteRecordDoesNotAllocate(t *testing.T) {
	payload := make([]byte, 4096)
	record := newSampleRecord(media.VideoTrack, types.Sample{Data: payload})
	defer record.release()
	var header [4]byte
	if allocations := testing.AllocsPerRun(100, func() {
		if err := writeRecord(discardWriter{}, &header, record); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("allocations per record = %f", allocations)
	}
}

func TestSampleRecordReusesMetadataAndPayload(t *testing.T) {
	payload := []byte{1, 2, 3}
	record := newSampleRecord(media.VideoTrack, types.Sample{Data: payload})
	if &record.payload[0] != &payload[0] {
		t.Fatal("sample payload was copied")
	}
	record.release()

	allocations := testing.AllocsPerRun(100, func() {
		record := newSampleRecord(media.VideoTrack, types.Sample{Data: payload})
		record.release()
	})
	if allocations != 0 {
		t.Fatalf("allocations per sample record = %f", allocations)
	}
}
