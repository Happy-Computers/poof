// Package cacheclient talks to the Zig stream_proxy over the SPCH binary protocol (UDS or TCP).
package cacheclient

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

const (
	magic   uint32 = 0x53504348 // "SPCH"
	version uint16 = 1

	opSize     uint16 = 1
	opRead     uint16 = 2
	opPrefetch uint16 = 3
	opMetrics  uint16 = 4
	opInfo     uint16 = 5

	statusOK       uint32 = 0
	statusInvalid  uint32 = 1
	statusRange    uint32 = 2
	statusTooLarge uint32 = 3
	statusIO       uint32 = 4
	statusBusy     uint32 = 5

	reqSize = 24
	hdrSize = 8

	// MaxInflight caps concurrent RPCs per Client.
	// Must stay below Zig MAX_CONNECTIONS (32) — media players issue many
	// parallel reads; one dial-per-RPC without this limit drops clients.
	MaxInflight = 16
)

// Client is a thread-safe SPCH client to stream_proxy (unix or tcp).
type Client struct {
	network string // "unix" or "tcp"
	address string
	sem     chan struct{}
}

// New dials a Unix domain socket path (Linux).
func New(path string) *Client {
	return NewNetwork("unix", path)
}

// NewTCP dials a TCP address like "127.0.0.1:12345" (Windows / portable).
func NewTCP(addr string) *Client {
	return NewNetwork("tcp", addr)
}

// NewNetwork dials network+address (e.g. "unix", path or "tcp", "127.0.0.1:9").
func NewNetwork(network, address string) *Client {
	return &Client{
		network: network,
		address: address,
		sem:     make(chan struct{}, MaxInflight),
	}
}

func (c *Client) dial() (net.Conn, error) {
	conn, err := net.Dial(c.network, c.address)
	if err != nil {
		return nil, fmt.Errorf("dial %s://%s: %w", c.network, c.address, err)
	}
	return conn, nil
}

func (c *Client) Size() (uint64, error) {
	payload, err := c.roundTrip(opSize, 0, 0)
	if err != nil {
		return 0, err
	}
	if len(payload) != 8 {
		return 0, fmt.Errorf("size: want 8 bytes, got %d", len(payload))
	}
	return binary.LittleEndian.Uint64(payload), nil
}

func (c *Client) Info() (string, error) {
	payload, err := c.roundTrip(opInfo, 0, 0)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (c *Client) Prefetch() error {
	_, err := c.roundTrip(opPrefetch, 0, 0)
	return err
}

// ReadAt fills dest with bytes starting at offset. Returns bytes read (may be short at EOF).
func (c *Client) ReadAt(dest []byte, offset uint64) (int, error) {
	if len(dest) == 0 {
		return 0, nil
	}
	if len(dest) > 8<<20 {
		return 0, fmt.Errorf("read: length %d exceeds MAX_RANGE_BYTES", len(dest))
	}
	payload, err := c.roundTrip(opRead, offset, uint32(len(dest)))
	if err != nil {
		return 0, err
	}
	n := copy(dest, payload)
	return n, nil
}

type Metrics struct {
	BytesFromOrigin      uint64
	BytesToClient        uint64
	CacheHits            uint64
	CacheMisses          uint64
	CacheOccupancyBlocks uint64
	PrefetchBytes        uint64
	PrefetchCancelled    uint64
	ObjectSize           uint64
}

func (c *Client) Metrics() (Metrics, error) {
	payload, err := c.roundTrip(opMetrics, 0, 0)
	if err != nil {
		return Metrics{}, err
	}
	if len(payload) != 64 {
		return Metrics{}, fmt.Errorf("metrics: want 64 bytes, got %d", len(payload))
	}
	return Metrics{
		BytesFromOrigin:      binary.LittleEndian.Uint64(payload[0:8]),
		BytesToClient:        binary.LittleEndian.Uint64(payload[8:16]),
		CacheHits:            binary.LittleEndian.Uint64(payload[16:24]),
		CacheMisses:          binary.LittleEndian.Uint64(payload[24:32]),
		CacheOccupancyBlocks: binary.LittleEndian.Uint64(payload[32:40]),
		PrefetchBytes:        binary.LittleEndian.Uint64(payload[40:48]),
		PrefetchCancelled:    binary.LittleEndian.Uint64(payload[48:56]),
		ObjectSize:           binary.LittleEndian.Uint64(payload[56:64]),
	}, nil
}

func (c *Client) roundTrip(op uint16, offset uint64, length uint32) ([]byte, error) {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var req [reqSize]byte
	binary.LittleEndian.PutUint32(req[0:4], magic)
	binary.LittleEndian.PutUint16(req[4:6], version)
	binary.LittleEndian.PutUint16(req[6:8], op)
	binary.LittleEndian.PutUint64(req[8:16], offset)
	binary.LittleEndian.PutUint32(req[16:20], length)

	if _, err := conn.Write(req[:]); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	var hdr [hdrSize]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return nil, fmt.Errorf("read response header: %w", err)
	}
	status := binary.LittleEndian.Uint32(hdr[0:4])
	nbytes := binary.LittleEndian.Uint32(hdr[4:8])
	if status != statusOK {
		return nil, statusError(status)
	}
	if nbytes == 0 {
		return nil, nil
	}
	payload := make([]byte, nbytes)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	return payload, nil
}

func statusError(status uint32) error {
	switch status {
	case statusInvalid:
		return fmt.Errorf("cache: invalid request")
	case statusRange:
		return fmt.Errorf("cache: range not satisfiable")
	case statusTooLarge:
		return fmt.Errorf("cache: range too large")
	case statusIO:
		return fmt.Errorf("cache: origin I/O error")
	case statusBusy:
		return fmt.Errorf("cache: busy")
	default:
		return fmt.Errorf("cache: status %d", status)
	}
}
