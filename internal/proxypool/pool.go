// Package proxypool runs a bounded set of single-object stream_proxy children.
package proxypool

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/amaan/infinity-storage/internal/cacheclient"
	"github.com/amaan/infinity-storage/internal/catalog"
)

// Hard limits. Change only by deliberate redesign.
const (
	MaxActiveProxies = 4
	readyAttempts    = 50
	readyInterval    = 20 * time.Millisecond
)

// ErrBusy means MaxActiveProxies are in use and none are idle to evict.
var ErrBusy = errors.New("proxypool: no idle proxy slot")

// Config for a Pool.
type Config struct {
	ProxyBin  string // path to stream_proxy binary
	SockDir   string // directory for UDS sockets (Linux); unused on Windows TCP
	MaxActive int    // default MaxActiveProxies; must be ≤ MaxActiveProxies
	// UseTCP forces SPCH over TCP 127.0.0.1 (default: true on Windows, false elsewhere).
	// Set explicitly in tests.
	UseTCP *bool
}

// Pool manages at most MaxActive stream_proxy processes (one object each).
type Pool struct {
	mu        sync.Mutex
	bin       string
	sockDir   string
	useTCP    bool
	maxActive int
	slots     map[string]*slot // key = Entry.SlotKey()
	seq       int
	closed    bool
}

type slot struct {
	key      string
	name     string
	endpoint string // UDS path or host:port
	cmd      *exec.Cmd
	waitDone <-chan error
	client   *cacheclient.Client
	refs     int
	lastUsed time.Time
}

// New creates a Pool. Caller must Close.
func New(cfg Config) (*Pool, error) {
	if cfg.ProxyBin == "" {
		return nil, fmt.Errorf("proxypool: ProxyBin required")
	}
	bin, err := exec.LookPath(cfg.ProxyBin)
	if err != nil {
		return nil, fmt.Errorf("proxypool: proxy bin %q: %w", cfg.ProxyBin, err)
	}
	useTCP := runtime.GOOS == "windows"
	if cfg.UseTCP != nil {
		useTCP = *cfg.UseTCP
	}

	sockDir := cfg.SockDir
	if !useTCP {
		if sockDir == "" {
			sockDir, err = os.MkdirTemp("", "infinity-storage-proxies-")
			if err != nil {
				return nil, fmt.Errorf("proxypool: sock dir: %w", err)
			}
		} else if err := os.MkdirAll(sockDir, 0700); err != nil {
			return nil, fmt.Errorf("proxypool: sock dir: %w", err)
		}
	}

	max := cfg.MaxActive
	if max <= 0 {
		max = MaxActiveProxies
	}
	if max > MaxActiveProxies {
		return nil, fmt.Errorf("proxypool: MaxActive %d > hard cap %d", max, MaxActiveProxies)
	}
	return &Pool{
		bin:       bin,
		sockDir:   sockDir,
		useTCP:    useTCP,
		maxActive: max,
		slots:     make(map[string]*slot),
	}, nil
}

// Acquire returns a client for entry. Call release when the open is done.
func (p *Pool) Acquire(entry catalog.Entry) (*cacheclient.Client, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, nil, fmt.Errorf("proxypool: closed")
	}

	key := entry.SlotKey()
	if key == "" {
		return nil, nil, fmt.Errorf("proxypool: entry %q has no AbsPath or OriginURL", entry.Name)
	}

	if s, ok := p.slots[key]; ok {
		s.refs++
		s.lastUsed = time.Now()
		return s.client, p.releaser(key), nil
	}

	if len(p.slots) >= p.maxActive {
		if !p.evictIdleLocked() {
			return nil, nil, ErrBusy
		}
	}

	s, err := p.spawnLocked(entry, key)
	if err != nil {
		return nil, nil, err
	}
	s.refs = 1
	s.lastUsed = time.Now()
	p.slots[key] = s
	return s.client, p.releaser(key), nil
}

func (p *Pool) releaser(key string) func() {
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		s, ok := p.slots[key]
		if !ok {
			return
		}
		if s.refs > 0 {
			s.refs--
		}
		s.lastUsed = time.Now()
	}
}

// ActiveCount returns how many stream_proxy processes are alive.
func (p *Pool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.slots)
}

// Close kills all children and removes the sock directory.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	var firstErr error
	for key, s := range p.slots {
		if err := p.killLocked(s); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(p.slots, key)
	}
	if p.sockDir != "" {
		if err := os.RemoveAll(p.sockDir); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (p *Pool) evictIdleLocked() bool {
	var victim *slot
	var victimKey string
	for k, s := range p.slots {
		if s.refs != 0 {
			continue
		}
		if victim == nil || s.lastUsed.Before(victim.lastUsed) {
			victim = s
			victimKey = k
		}
	}
	if victim == nil {
		return false
	}
	_ = p.killLocked(victim)
	delete(p.slots, victimKey)
	return true
}

func (p *Pool) spawnLocked(entry catalog.Entry, key string) (*slot, error) {
	p.seq++

	var endpoint string
	var args []string
	var client *cacheclient.Client

	if p.useTCP {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("proxypool: reserve tcp port: %w", err)
		}
		endpoint = ln.Addr().String()
		_ = ln.Close()
		args = []string{"--name", entry.Name, "--listen-tcp", endpoint, "--no-http"}
		client = cacheclient.NewTCP(endpoint)
	} else {
		endpoint = filepath.Join(p.sockDir, fmt.Sprintf("%d.sock", p.seq))
		_ = os.Remove(endpoint)
		args = []string{"--name", entry.Name, "--uds", endpoint, "--no-http"}
		client = cacheclient.New(endpoint)
	}

	if entry.OriginURL != "" {
		args = append([]string{"--origin-url", entry.OriginURL}, args...)
	} else {
		args = append([]string{"--file", entry.AbsPath}, args...)
	}

	cmd := exec.Command(p.bin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("proxypool: start %s: %w", p.bin, err)
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var lastErr error
	for i := 0; i < readyAttempts; i++ {
		select {
		case err := <-waitDone:
			if !p.useTCP {
				_ = os.Remove(endpoint)
			}
			if err == nil {
				err = fmt.Errorf("exit 0")
			}
			return nil, fmt.Errorf("proxypool: proxy exited early for %s: %w", entry.Name, err)
		default:
		}
		_, err := client.Size()
		if err == nil {
			return &slot{
				key:      key,
				name:     entry.Name,
				endpoint: endpoint,
				cmd:      cmd,
				waitDone: waitDone,
				client:   client,
			}, nil
		}
		lastErr = err
		time.Sleep(readyInterval)
	}
	_ = cmd.Process.Kill()
	<-waitDone
	if !p.useTCP {
		_ = os.Remove(endpoint)
	}
	return nil, fmt.Errorf("proxypool: not ready for %s: %w", entry.Name, lastErr)
}

func (p *Pool) killLocked(s *slot) error {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.waitDone != nil {
		select {
		case <-s.waitDone:
		case <-time.After(2 * time.Second):
		}
	}
	if !p.useTCP && s.endpoint != "" {
		_ = os.Remove(s.endpoint)
	}
	return nil
}
