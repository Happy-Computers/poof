//go:build linux

package mount

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/amaan/video-storage-engine/internal/mount/linux"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Run attaches a Space volume via Linux FUSE.
func Run(cfg Config) error {
	if cfg.MountPoint == "" {
		return fmt.Errorf("mount: MountPoint required")
	}

	prep, err := Prepare(cfg)
	if err != nil {
		return err
	}
	cleanup := prep.Cleanup
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()

	if err := linux.EnsureMountPoint(cfg.MountPoint); err != nil {
		return fmt.Errorf("mount point: %w", err)
	}

	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			AllowOther: false,
			Debug:      cfg.Debug,
			FsName:     "space",
			Name:       "space",
			Options:    []string{"ro", "default_permissions"},
		},
	}
	zero := time.Duration(0)
	opts.AttrTimeout = &zero
	opts.EntryTimeout = &zero
	opts.NegativeTimeout = &zero

	var root fs.InodeEmbedder
	switch {
	case prep.SingleClient != nil:
		root = linux.NewRootSingle(prep.SingleClient, prep.SingleName, prep.SingleSize)
	case prep.Load != nil:
		live := linux.NewRootMultiLive(prep.Entries, prep.Pool, linux.CatalogLoader(prep.Load))
		root = live
		oldCleanup := cleanup
		cleanup = func() {
			live.Stop()
			if oldCleanup != nil {
				oldCleanup()
			}
		}
	default:
		root = linux.NewRootMulti(prep.Entries, prep.Pool)
	}

	server, err := fs.Mount(cfg.MountPoint, root, opts)
	if err != nil {
		return fmt.Errorf("fuse mount: %w", err)
	}

	log.Printf("mounted %s — %s", cfg.MountPoint, prep.Summary)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		log.Printf("unmounting %s", cfg.MountPoint)
		_ = server.Unmount()
	}()

	server.Wait()
	return nil
}
