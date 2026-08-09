//go:build windows

package windows

import (
	"errors"
	"testing"

	"github.com/winfsp/cgofuse/fuse"
)

type failingSource struct{}

func (failingSource) ReadAt(dest []byte, offset uint64) (int, error) {
	return 0, errors.New("read failed")
}

func TestReadErrors(t *testing.T) {
	filesystem := &InfinityStorageFS{
		handles: map[uint64]*openHandle{
			1: {source: failingSource{}, size: 1},
		},
	}
	buffer := make([]byte, 1)

	if status := filesystem.Read("/file", buffer, -1, 1); status != -fuse.EINVAL {
		t.Fatalf("negative offset status: %d", status)
	}
	if status := filesystem.Read("/file", buffer, 0, 2); status != -fuse.EBADF {
		t.Fatalf("invalid handle status: %d", status)
	}
	if status := filesystem.Read("/file", buffer, 0, 1); status != -fuse.EIO {
		t.Fatalf("source error status: %d", status)
	}
}
