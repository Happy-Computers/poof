package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu         sync.Mutex
	begins     int
	parts      [][]byte
	checksums  [][sha256.Size]byte
	partCalls  map[int32]int
	failFirst  bool
	completed  bool
	completeAt uint64
	checksum   [sha256.Size]byte
}

func (s *memoryStore) Begin(ctx context.Context, name string) (Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.begins++
	return s, nil
}

func (s *memoryStore) PutPart(
	ctx context.Context,
	number int32,
	content []byte,
	checksum [sha256.Size]byte,
) (Part, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.partCalls == nil {
		s.partCalls = make(map[int32]int)
	}
	s.partCalls[number]++
	if s.failFirst && s.partCalls[number] == 1 {
		return Part{}, errors.New("transient")
	}
	copied := append([]byte(nil), content...)
	if sha256.Sum256(copied) != checksum {
		return Part{}, errors.New("part checksum mismatch")
	}
	s.parts = append(s.parts, copied)
	s.checksums = append(s.checksums, checksum)
	return Part{Number: number, ETag: "part", Checksum: checksum}, nil
}

func (s *memoryStore) Complete(
	ctx context.Context,
	parts []Part,
	size uint64,
	checksum [sha256.Size]byte,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(parts) != len(s.parts) || len(parts) != len(s.checksums) {
		return errors.New("part count mismatch")
	}
	content := make([]byte, 0, size)
	for index, part := range s.parts {
		if parts[index].Checksum != s.checksums[index] {
			return errors.New("completed part checksum mismatch")
		}
		content = append(content, part...)
	}
	if uint64(len(content)) != size {
		return errors.New("size mismatch")
	}
	if sha256.Sum256(content) != checksum {
		return errors.New("object checksum mismatch")
	}
	s.completed = true
	s.completeAt = size
	s.checksum = checksum
	return nil
}

func (s *memoryStore) Abort(ctx context.Context) error { return nil }

func newTestManager(t *testing.T, store Store) *Manager {
	t.Helper()
	manager, err := NewManager(Config{
		SpoolDir:       t.TempDir(),
		Store:          store,
		MaxSpoolBytes:  64 * 1024 * 1024,
		MaxFileBytes:   64 * 1024 * 1024,
		RangeWait:      10 * time.Millisecond,
		UploadAttempts: PartUploadAttempts,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestSequentialIngestUploadsPartsAndBecomesDurable(t *testing.T) {
	store := &memoryStore{failFirst: true}
	manager := newTestManager(t, store)
	file, err := manager.Reserve("clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	content := make([]byte, PartBytes*2+17)
	for i := range content {
		content[i] = byte(i)
	}
	if n, err := file.WriteAt(content[:PartBytes], 0); err != nil || n != PartBytes {
		t.Fatalf("first write: n=%d err=%v", n, err)
	}
	if n, err := file.WriteAt(content[PartBytes:], PartBytes); err != nil || n != PartBytes+17 {
		t.Fatalf("second write: n=%d err=%v", n, err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := file.WaitDurable(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := file.Snapshot()
	if snapshot.State != StateDurable || snapshot.Size != uint64(len(content)) || snapshot.Uploaded != uint64(len(content)) {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.completed || len(store.parts) != 3 {
		t.Fatalf("completed=%v parts=%d", store.completed, len(store.parts))
	}
	if store.partCalls[1] != 2 {
		t.Fatalf("first part calls: %d", store.partCalls[1])
	}
}

func TestLiveRangeWaitsForAcceptedBytes(t *testing.T) {
	manager := newTestManager(t, &memoryStore{})
	file, err := manager.Reserve("clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("abcd"), 0); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		buf := make([]byte, 2)
		n, err := file.ReadAt(buf, 4)
		if err == nil && (n != 2 || string(buf) != "ef") {
			err = errors.New("unexpected range content")
		}
		result <- err
	}()
	time.Sleep(time.Millisecond)
	if _, err := file.WriteAt([]byte("ef"), 4); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if _, err := file.ReadAt(make([]byte, 1), 7); !errors.Is(err, ErrRangeUnavailable) {
		t.Fatalf("unwritten range error: %v", err)
	}
}

func TestManagerReleasesClosedWriteHandle(t *testing.T) {
	manager := newTestManager(t, &memoryStore{})
	first, err := manager.Reserve("first.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reserve("second.mp4"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reserve("third.mp4"); !errors.Is(err, ErrActiveWriteLimit) {
		t.Fatalf("active-write limit: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reserve("third.mp4"); err != nil {
		t.Fatalf("released write handle: %v", err)
	}
}

func TestIngestRejectsNonSequentialConflictAndSpoolOverflow(t *testing.T) {
	manager := newTestManager(t, &memoryStore{})
	file, err := manager.Reserve("clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reserve("clip.mp4"); !errors.Is(err, ErrNameExists) {
		t.Fatalf("duplicate reservation: %v", err)
	}
	if _, err := file.WriteAt([]byte("abc"), 1); !errors.Is(err, ErrNonSequential) {
		t.Fatalf("non-sequential write: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for file.Snapshot().State != StateAborted {
		if time.Now().After(deadline) {
			t.Fatalf("state after non-sequential write: %s", file.Snapshot().State)
		}
		time.Sleep(time.Millisecond)
	}
	spoolManager := newTestManager(t, &memoryStore{})
	spoolManager.maxSpoolBytes = 2
	spoolFile, err := spoolManager.Reserve("spool.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spoolFile.WriteAt([]byte("abc"), 0); !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("spool limit: %v", err)
	}
}

func TestCloseMakesUnwrittenRangesUnavailable(t *testing.T) {
	manager := newTestManager(t, &memoryStore{})
	file, err := manager.Reserve("clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("abc"), 0); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.ReadAt(make([]byte, 1), 3); !errors.Is(err, ErrRangeUnavailable) {
		t.Fatalf("unavailable range: %v", err)
	}
}
