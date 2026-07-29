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

func TestMaxInflightCap(t *testing.T) {
	if MaxInflight >= 32 {
		t.Fatal("MaxInflight must stay below Zig MAX_CONNECTIONS (32)")
	}
	if MaxInflight < 1 {
		t.Fatal("MaxInflight")
	}
	c := New("/tmp/nonexistent-space-cache.sock")
	if cap(c.sem) != MaxInflight {
		t.Fatalf("sem cap %d", cap(c.sem))
	}
	tcp := NewTCP("127.0.0.1:1")
	if tcp.network != "tcp" || tcp.address != "127.0.0.1:1" {
		t.Fatalf("%+v", tcp)
	}
}
