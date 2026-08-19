package mediawebtransport

import (
	"bytes"
	"encoding/binary"
	"testing"
)

type discardWriter struct{}

func (discardWriter) Write(data []byte) (int, error) {
	return len(data), nil
}

func TestWriteRecord(t *testing.T) {
	payload := []byte{1, 2, 3, 4}
	var output bytes.Buffer
	var header [4]byte
	if err := writeRecord(&output, &header, payload); err != nil {
		t.Fatalf("writeRecord() error = %v", err)
	}

	message := output.Bytes()
	if size := binary.BigEndian.Uint32(message[:4]); size != uint32(len(payload)) {
		t.Fatalf("record size = %d", size)
	}
	if !bytes.Equal(message[4:], payload) {
		t.Fatalf("record payload = %v", message[4:])
	}
}

func TestWriteRecordDoesNotAllocate(t *testing.T) {
	payload := make([]byte, 4096)
	var header [4]byte
	if allocations := testing.AllocsPerRun(100, func() {
		if err := writeRecord(discardWriter{}, &header, payload); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("allocations per record = %f", allocations)
	}
}
