package cacheclient

import (
	"encoding/binary"
	"testing"
)

func TestRequestLayout(t *testing.T) {
	var req [reqSize]byte
	binary.LittleEndian.PutUint32(req[0:4], magic)
	binary.LittleEndian.PutUint16(req[4:6], version)
	binary.LittleEndian.PutUint16(req[6:8], opRead)
	binary.LittleEndian.PutUint64(req[8:16], 1024)
	binary.LittleEndian.PutUint32(req[16:20], 4096)

	if binary.LittleEndian.Uint32(req[0:4]) != magic {
		t.Fatal("magic")
	}
	if binary.LittleEndian.Uint16(req[6:8]) != opRead {
		t.Fatal("op")
	}
	if binary.LittleEndian.Uint64(req[8:16]) != 1024 {
		t.Fatal("offset")
	}
}

func TestStatusError(t *testing.T) {
	err := statusError(statusRange)
	if err == nil {
		t.Fatal("expected error")
	}
}
