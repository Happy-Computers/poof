package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/amaan/infinity-storage/internal/catalog"
)

const (
	MaxActiveWrites       = 8
	MaxConcurrentUploads  = 2
	PartBytes             = 16 * 1024 * 1024
	BuffersPerWrite       = 2
	MaxBufferBytes        = 64 * 1024 * 1024
	MaxSpoolBytes         = 64 * 1024 * 1024 * 1024
	MaxPartsPerObject     = 4096
	MaxFileBytes          = 64 * 1024 * 1024 * 1024
	MaxPeerRangeBytes     = 8 * 1024 * 1024
	MaxPeerRangeReads     = 8
	UnwrittenRangeWait    = 30 * time.Second
	SourceHandoffGrace    = 30 * time.Second
	PartUploadAttempts    = 3
	MaxCatalogFiles       = 256
	MaxBasenameBytes      = 255
)

var (
	ErrActiveWriteLimit = errors.New("ingest: active write limit reached")
	ErrCatalogFull      = errors.New("ingest: catalog file limit reached")
	ErrNameExists       = errors.New("ingest: pathname already reserved")
	ErrNonSequential    = errors.New("ingest: non-sequential write")
	ErrFileTooLarge     = errors.New("ingest: file size limit exceeded")
	ErrSpoolFull        = errors.New("ingest: spool limit exceeded")
	ErrRangeTooLarge    = errors.New("ingest: range size limit exceeded")
	ErrRangeUnavailable = errors.New("ingest: requested range is unavailable")
	ErrClosed           = errors.New("ingest: write handle is closed")
)

type State string

const (
	StateReserved    State = "reserved"
	StateStreaming   State = "streaming"
	StateSealing     State = "sealing"
	StateDurable     State = "durable"
	StateInterrupted State = "interrupted"
	StateAborting    State = "aborting"
	StateAborted     State = "aborted"
)

type Part struct {
	Number   int32
	ETag     string
	Checksum [sha256.Size]byte
}

type Upload interface {
	PutPart(ctx context.Context, number int32, content []byte, checksum [sha256.Size]byte) (Part, error)
	Complete(ctx context.Context, parts []Part, size uint64, checksum [sha256.Size]byte) error
	Abort(ctx context.Context) error
}

type Store interface {
	Begin(ctx context.Context, name string) (Upload, error)
}

type Config struct {
	SpoolDir string
	Store    Store

	MaxActiveWrites int
	MaxSpoolBytes   uint64
	MaxFileBytes    uint64
	RangeWait       time.Duration
	UploadAttempts  int
	Publish         func(Snapshot)
	Logf            func(string, ...any)
}

type Snapshot struct {
	Name        string
	State       State
	Size        uint64
	Uploaded    uint64
	UploadError error
}

type Manager struct {
	mu sync.Mutex

	spoolDir        string
	store           Store
	maxActiveWrites int
	maxSpoolBytes   uint64
	maxFileBytes    uint64
	rangeWait       time.Duration
	uploadAttempts  int
	publish         func(Snapshot)
	logf            func(string, ...any)

	spoolBytes   uint64
	activeWrites int
	files        map[string]*File
	uploadTokens chan struct{}
}

type File struct {
	manager       *Manager
	name          string
	path          string
	spool         *os.File
	spoolReleased bool

	mu        sync.Mutex
	changed   *sync.Cond
	state     State
	accepted  uint64
	uploaded  uint64
	hash      hashState
	closed    bool
	active    bool
	started   bool
	writers   int
	upload    Upload
	uploadErr error
	pendingSpoolRelease uint64
}

type hashState struct {
	hash io.Writer
	sum  func() [sha256.Size]byte
}

func NewManager(cfg Config) (*Manager, error) {
	if cfg.SpoolDir == "" {
		return nil, fmt.Errorf("ingest: SpoolDir required")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("ingest: Store required")
	}
	if err := os.MkdirAll(cfg.SpoolDir, 0700); err != nil {
		return nil, fmt.Errorf("ingest: create spool directory: %w", err)
	}
	maxActiveWrites := cfg.MaxActiveWrites
	if maxActiveWrites == 0 {
		maxActiveWrites = MaxActiveWrites
	}
	if maxActiveWrites < 1 || maxActiveWrites > MaxActiveWrites {
		return nil, fmt.Errorf("ingest: MaxActiveWrites must be 1..%d", MaxActiveWrites)
	}
	maxSpoolBytes := cfg.MaxSpoolBytes
	if maxSpoolBytes == 0 {
		maxSpoolBytes = MaxSpoolBytes
	}
	if maxSpoolBytes > MaxSpoolBytes {
		return nil, fmt.Errorf("ingest: MaxSpoolBytes exceeds %d", MaxSpoolBytes)
	}
	maxFileBytes := cfg.MaxFileBytes
	if maxFileBytes == 0 {
		maxFileBytes = MaxFileBytes
	}
	if maxFileBytes > MaxFileBytes {
		return nil, fmt.Errorf("ingest: MaxFileBytes exceeds %d", MaxFileBytes)
	}
	rangeWait := cfg.RangeWait
	if rangeWait == 0 {
		rangeWait = UnwrittenRangeWait
	}
	if rangeWait < 0 {
		return nil, fmt.Errorf("ingest: RangeWait must be non-negative")
	}
	uploadAttempts := cfg.UploadAttempts
	if uploadAttempts == 0 {
		uploadAttempts = PartUploadAttempts
	}
	if uploadAttempts < 1 || uploadAttempts > PartUploadAttempts {
		return nil, fmt.Errorf("ingest: UploadAttempts must be 1..%d", PartUploadAttempts)
	}
	uploadSlots := MaxConcurrentUploads
	if uploadSlots > maxActiveWrites {
		uploadSlots = maxActiveWrites
	}
	return &Manager{
		spoolDir:        cfg.SpoolDir,
		store:           cfg.Store,
		maxActiveWrites: maxActiveWrites,
		maxSpoolBytes:   maxSpoolBytes,
		maxFileBytes:    maxFileBytes,
		rangeWait:       rangeWait,
		uploadAttempts:  uploadAttempts,
		publish:         cfg.Publish,
		logf:            cfg.Logf,
		files:           make(map[string]*File),
		uploadTokens:    make(chan struct{}, uploadSlots),
	}, nil
}

func (m *Manager) Reserve(name string) (*File, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, exists := m.files[name]; exists {
		existing.mu.Lock()
		canReplace := existing.accepted == 0 && existing.closed &&
			(existing.state == StateInterrupted || existing.state == StateAborted)
		path := existing.path
		existing.mu.Unlock()
		if !canReplace {
			return nil, ErrNameExists
		}
		delete(m.files, name)
		_ = os.Remove(path)
	}
	if len(m.files) >= MaxCatalogFiles {
		return nil, ErrCatalogFull
	}
	if m.activeWrites >= m.maxActiveWrites {
		return nil, ErrActiveWriteLimit
	}
	path := filepath.Join(m.spoolDir, name)
	_ = os.Remove(path)
	spool, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrNameExists
		}
		return nil, fmt.Errorf("ingest: create spool: %w", err)
	}
	h := sha256.New()
	file := &File{
		manager: m,
		name:    name,
		path:    path,
		spool:   spool,
		state:   StateReserved,
		active:  true,
		writers: 1,
		hash: hashState{
			hash: h,
			sum: func() [sha256.Size]byte {
				var sum [sha256.Size]byte
				copy(sum[:], h.Sum(nil))
				return sum
			},
		},
	}
	file.changed = sync.NewCond(&file.mu)
	m.files[name] = file
	m.activeWrites++
	return file, nil
}

func (m *Manager) forgetLocked(file *File) {
	if current, ok := m.files[file.name]; ok && current == file {
		delete(m.files, file.name)
	}
}

func (m *Manager) Forget(file *File) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.forgetLocked(file)
}

func (m *Manager) Lookup(name string) (*File, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	file, ok := m.files[name]
	return file, ok
}

func (m *Manager) Entries() []catalog.Entry {
	m.mu.Lock()
	files := make([]*File, 0, len(m.files))
	for _, file := range m.files {
		files = append(files, file)
	}
	m.mu.Unlock()
	entries := make([]catalog.Entry, 0, len(files))
	for _, file := range files {
		switch file.Snapshot().State {
		case StateDurable, StateAborted, StateAborting, StateInterrupted:
			continue
		}
		entries = append(entries, catalog.Entry{
			Name:   file.name,
			Source: file,
			Size:   file.Size(),
		})
	}
	return entries
}

func (m *Manager) log(format string, values ...any) {
	if m.logf != nil {
		m.logf(format, values...)
	}
}

func (m *Manager) notify(file *File) {
	if m.publish != nil {
		go func() {
			m.publish(file.Snapshot())
		}()
	}
}

func (m *Manager) reserveSpoolBytes(count uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if count > m.maxSpoolBytes-m.spoolBytes {
		return ErrSpoolFull
	}
	m.spoolBytes += count
	return nil
}

func (m *Manager) releaseWriteHandle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeWrites == 0 {
		panic("ingest: active write accounting underflow")
	}
	m.activeWrites--
}

func (m *Manager) releaseSpoolBytes(count uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if count > m.spoolBytes {
		panic("ingest: spool accounting underflow")
	}
	m.spoolBytes -= count
}

func (f *File) WriteAt(content []byte, offset uint64) (int, error) {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return 0, ErrClosed
	}
	if offset != f.accepted {
		releaseHandle := f.abortLocked()
		f.mu.Unlock()
		if releaseHandle {
			f.manager.releaseWriteHandle()
		}
		return 0, ErrNonSequential
	}
	if len(content) == 0 {
		f.mu.Unlock()
		return 0, nil
	}
	if uint64(len(content)) > f.manager.maxFileBytes-f.accepted {
		f.mu.Unlock()
		return 0, ErrFileTooLarge
	}
	if err := f.manager.reserveSpoolBytes(uint64(len(content))); err != nil {
		f.mu.Unlock()
		return 0, err
	}
	n, err := f.spool.WriteAt(content, int64(offset))
	if n > 0 {
		if _, hashErr := f.hash.hash.Write(content[:n]); hashErr != nil {
			f.manager.releaseSpoolBytes(uint64(len(content) - n))
			f.mu.Unlock()
			return n, fmt.Errorf("ingest: hash accepted bytes: %w", hashErr)
		}
		f.accepted += uint64(n)
		if f.state == StateReserved {
			f.state = StateStreaming
			f.started = true
			f.manager.log("ingest state name=%s state=%s", f.name, f.state)
			go f.runUpload()
		}
		f.changed.Broadcast()
		f.manager.log("ingest write name=%s offset=%d bytes=%d accepted=%d", f.name, offset, n, f.accepted)
		f.manager.notify(f)
	}
	if n < len(content) {
		f.manager.releaseSpoolBytes(uint64(len(content) - n))
	}
	f.mu.Unlock()
	if err != nil {
		return n, fmt.Errorf("ingest: write spool: %w", err)
	}
	if n != len(content) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

func (f *File) releaseSpoolLocked() {
	if f.spoolReleased {
		return
	}
	if f.spool != nil {
		if err := f.spool.Close(); err != nil {
			f.manager.log("ingest close spool error name=%s error=%v", f.name, err)
		}
		f.spool = nil
	}
	if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		f.manager.log("ingest remove spool error name=%s error=%v", f.name, err)
		return
	}
	f.pendingSpoolRelease = f.accepted
	f.spoolReleased = true
}

func (f *File) abortLocked() (releaseHandle bool) {
	f.closed = true
	f.writers = 0
	if f.active {
		f.active = false
		releaseHandle = true
	}
	f.state = StateAborting
	upload := f.upload
	f.changed.Broadcast()
	go func() {
		if upload != nil {
			_ = upload.Abort(context.Background())
		}
		f.mu.Lock()
		var spoolRelease uint64
		if f.state == StateAborting {
			f.state = StateAborted
			f.releaseSpoolLocked()
			spoolRelease = f.pendingSpoolRelease
			f.pendingSpoolRelease = 0
			f.changed.Broadcast()
			f.manager.notify(f)
		}
		f.mu.Unlock()
		if spoolRelease > 0 {
			f.manager.releaseSpoolBytes(spoolRelease)
		}
		f.manager.Forget(f)
	}()
	return releaseHandle
}

func (f *File) AddWriter() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	f.writers++
	return nil
}

func (f *File) Flush() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	if err := f.spool.Sync(); err != nil {
		return fmt.Errorf("ingest: sync spool: %w", err)
	}
	return nil
}

func (f *File) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	if f.writers > 1 {
		f.writers--
		f.mu.Unlock()
		return nil
	}
	f.writers = 0
	f.closed = true
	releaseHandle := false
	if f.active {
		f.active = false
		releaseHandle = true
	}
	if f.accepted == 0 {
		f.state = StateAborted
		f.releaseSpoolLocked()
		spoolRelease := f.pendingSpoolRelease
		f.pendingSpoolRelease = 0
		f.changed.Broadcast()
		f.mu.Unlock()
		if spoolRelease > 0 {
			f.manager.releaseSpoolBytes(spoolRelease)
		}
		if releaseHandle {
			f.manager.releaseWriteHandle()
		}
		f.manager.Forget(f)
		f.manager.notify(f)
		return nil
	}
	if err := f.spool.Sync(); err != nil {
		f.changed.Broadcast()
		f.mu.Unlock()
		if releaseHandle {
			f.manager.releaseWriteHandle()
		}
		return fmt.Errorf("ingest: sync spool: %w", err)
	}
	if f.state == StateReserved {
		f.state = StateInterrupted
	} else if f.state == StateStreaming {
		f.state = StateSealing
	}
	f.manager.log("ingest state name=%s state=%s accepted=%d", f.name, f.state, f.accepted)
	f.changed.Broadcast()
	f.mu.Unlock()
	if releaseHandle {
		f.manager.releaseWriteHandle()
	}
	f.manager.notify(f)
	return nil
}

func (f *File) ReadAt(dest []byte, offset uint64) (int, error) {
	if len(dest) == 0 {
		return 0, nil
	}
	if uint64(len(dest)) > MaxPeerRangeBytes {
		return 0, ErrRangeTooLarge
	}
	if offset > ^uint64(0)-uint64(len(dest)) {
		return 0, ErrRangeUnavailable
	}
	endExclusive := offset + uint64(len(dest))
	f.mu.Lock()
	timedOut := false
	timer := time.AfterFunc(f.manager.rangeWait, func() {
		f.mu.Lock()
		timedOut = true
		f.changed.Broadcast()
		f.mu.Unlock()
	})
	defer timer.Stop()
	for endExclusive > f.accepted {
		if f.unavailableLocked() {
			f.mu.Unlock()
			return 0, ErrRangeUnavailable
		}
		if f.manager.rangeWait == 0 {
			f.mu.Unlock()
			return 0, ErrRangeUnavailable
		}
		f.changed.Wait()
		if endExclusive > f.accepted && timedOut {
			f.mu.Unlock()
			return 0, ErrRangeUnavailable
		}
	}
	spool := f.spool
	f.mu.Unlock()
	if spool == nil {
		return 0, ErrRangeUnavailable
	}
	n, err := spool.ReadAt(dest, int64(offset))
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("ingest: read spool: %w", err)
	}
	if n != len(dest) {
		return n, ErrRangeUnavailable
	}
	return n, nil
}

func (f *File) unavailableLocked() bool {
	switch f.state {
	case StateInterrupted, StateAborting, StateAborted:
		return true
	case StateSealing, StateDurable:
		return true
	default:
		return false
	}
}

func (f *File) Size() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accepted
}

func (f *File) Snapshot() Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Snapshot{
		Name:        f.name,
		State:       f.state,
		Size:        f.accepted,
		Uploaded:    f.uploaded,
		UploadError: f.uploadErr,
	}
}

func (f *File) WaitDurable(ctx context.Context) error {
	for {
		snapshot := f.Snapshot()
		if snapshot.State == StateDurable {
			return nil
		}
		if snapshot.UploadError != nil {
			return snapshot.UploadError
		}
		if snapshot.State == StateInterrupted || snapshot.State == StateAborted {
			return ErrRangeUnavailable
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func (f *File) Abort(ctx context.Context) error {
	f.mu.Lock()
	if f.state == StateDurable || f.state == StateAborted {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.writers = 0
	releaseHandle := false
	if f.active {
		f.active = false
		releaseHandle = true
	}
	f.state = StateAborting
	upload := f.upload
	f.changed.Broadcast()
	f.mu.Unlock()
	if releaseHandle {
		f.manager.releaseWriteHandle()
	}
	if upload != nil {
		if err := upload.Abort(ctx); err != nil {
			return fmt.Errorf("ingest: abort multipart upload: %w", err)
		}
	}
	f.mu.Lock()
	f.state = StateAborted
	f.releaseSpoolLocked()
	spoolRelease := f.pendingSpoolRelease
	f.pendingSpoolRelease = 0
	f.changed.Broadcast()
	f.manager.notify(f)
	f.mu.Unlock()
	if spoolRelease > 0 {
		f.manager.releaseSpoolBytes(spoolRelease)
	}
	f.manager.Forget(f)
	return nil
}

func (f *File) runUpload() {
	f.manager.log("ingest upload queued name=%s", f.name)
	f.manager.uploadTokens <- struct{}{}
	f.manager.log("ingest upload started name=%s", f.name)
	defer func() { <-f.manager.uploadTokens }()
	f.mu.Lock()
	aborted := f.state == StateAborting || f.state == StateAborted
	f.mu.Unlock()
	if aborted {
		return
	}
	upload, err := f.manager.store.Begin(context.Background(), f.name)
	if err != nil {
		f.setUploadError(fmt.Errorf("ingest: begin multipart upload: %w", err))
		return
	}
	f.mu.Lock()
	f.upload = upload
	f.mu.Unlock()

	buffer := make([]byte, PartBytes)
	parts := make([]Part, 0, MaxPartsPerObject)
	var offset uint64
	for number := int32(1); ; number++ {
		count, done, err := f.waitPart(offset)
		if err != nil {
			_ = upload.Abort(context.Background())
			f.setUploadError(err)
			return
		}
		if done {
			break
		}
		if len(parts) >= MaxPartsPerObject {
			_ = upload.Abort(context.Background())
			f.setUploadError(fmt.Errorf("ingest: multipart part limit exceeded"))
			return
		}
		content := buffer[:count]
		if _, err := f.spool.ReadAt(content, int64(offset)); err != nil {
			_ = upload.Abort(context.Background())
			f.setUploadError(fmt.Errorf("ingest: read spool for upload: %w", err))
			return
		}
		checksum := sha256.Sum256(content)
		part, err := f.putPart(upload, number, content, checksum)
		if err != nil {
			_ = upload.Abort(context.Background())
			f.setUploadError(err)
			return
		}
		parts = append(parts, part)
		offset += uint64(count)
		f.manager.log("ingest upload part name=%s number=%d bytes=%d uploaded=%d", f.name, number, count, offset)
		f.mu.Lock()
		f.uploaded = offset
		f.changed.Broadcast()
		f.mu.Unlock()
	}
	f.mu.Lock()
	size := f.accepted
	checksum := f.hash.sum()
	f.mu.Unlock()
	if size == 0 {
		_ = upload.Abort(context.Background())
		f.setUploadError(fmt.Errorf("ingest: empty files are unsupported"))
		return
	}
	if err := upload.Complete(context.Background(), parts, size, checksum); err != nil {
		f.setUploadError(fmt.Errorf("ingest: complete multipart upload: %w", err))
		return
	}
	f.mu.Lock()
	f.state = StateDurable
	f.releaseSpoolLocked()
	spoolRelease := f.pendingSpoolRelease
	f.pendingSpoolRelease = 0
	f.manager.log("ingest state name=%s state=%s size=%d", f.name, f.state, size)
	f.changed.Broadcast()
	f.manager.notify(f)
	f.mu.Unlock()
	if spoolRelease > 0 {
		f.manager.releaseSpoolBytes(spoolRelease)
	}
}

func (f *File) waitPart(offset uint64) (count int, done bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for {
		if f.state == StateAborting || f.state == StateAborted || f.state == StateInterrupted {
			return 0, false, ErrRangeUnavailable
		}
		available := f.accepted - offset
		if available >= PartBytes {
			return PartBytes, false, nil
		}
		if f.closed {
			if available == 0 {
				return 0, true, nil
			}
			return int(available), false, nil
		}
		f.changed.Wait()
	}
}

func (f *File) putPart(upload Upload, number int32, content []byte, checksum [sha256.Size]byte) (Part, error) {
	var lastErr error
	for attempt := 0; attempt < f.manager.uploadAttempts; attempt++ {
		part, err := upload.PutPart(context.Background(), number, content, checksum)
		if err == nil {
			return part, nil
		}
		lastErr = err
	}
	return Part{}, fmt.Errorf("ingest: upload part %d after %d attempts: %w", number, f.manager.uploadAttempts, lastErr)
}

func (f *File) setUploadError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploadErr = err
	f.manager.log("ingest upload error name=%s error=%v", f.name, err)
	f.changed.Broadcast()
	f.manager.notify(f)
}

func validateName(name string) error {
	if name == "" || len(name) > MaxBasenameBytes || filepath.Base(name) != name || name == "." {
		return fmt.Errorf("ingest: invalid basename %q", name)
	}
	return nil
}
