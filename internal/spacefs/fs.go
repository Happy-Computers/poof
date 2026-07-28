// Package spacefs is a read-only FUSE filesystem backed by the Zig block cache.
package spacefs

import (
	"context"
	"syscall"

	"github.com/amaan/video-storage-engine/internal/cacheclient"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Root is the mount root containing a single read-only file.
type Root struct {
	fs.Inode
	client   *cacheclient.Client
	fileName string
	size     uint64
}

func NewRoot(client *cacheclient.Client, fileName string, size uint64) *Root {
	return &Root{client: client, fileName: fileName, size: size}
}

func (r *Root) OnAdd(ctx context.Context) {
	ch := r.NewPersistentInode(ctx, &File{
		client: r.client,
		size:   r.size,
	}, fs.StableAttr{Mode: syscall.S_IFREG, Ino: 2})
	r.AddChild(r.fileName, ch, false)
}

func (r *Root) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0555
	return 0
}

var _ = (fs.NodeOnAdder)((*Root)(nil))
var _ = (fs.NodeGetattrer)((*Root)(nil))

// File serves ranged reads from the Zig cache.
type File struct {
	fs.Inode
	client *cacheclient.Client
	size   uint64
}

func (f *File) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0444
	out.Size = f.size
	return 0
}

func (f *File) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// Zig owns the block cache — skip kernel page cache / readahead.
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (f *File) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if off < 0 {
		return nil, syscall.EINVAL
	}
	if uint64(off) >= f.size {
		return fuse.ReadResultData(nil), 0
	}
	remaining := f.size - uint64(off)
	want := uint64(len(dest))
	if want > remaining {
		want = remaining
	}
	buf := dest[:want]
	n, err := f.client.ReadAt(buf, uint64(off))
	if err != nil {
		return nil, syscall.EIO
	}
	return fuse.ReadResultData(buf[:n]), 0
}

var _ = (fs.NodeGetattrer)((*File)(nil))
var _ = (fs.NodeOpener)((*File)(nil))
var _ = (fs.NodeReader)((*File)(nil))
