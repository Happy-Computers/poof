//go:build windows

package mount

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	winfs "github.com/amaan/infinity-storage/internal/mount/windows"
	"github.com/winfsp/cgofuse/fuse"
)

// Run attaches a Infinity Storage volume via WinFsp (cgofuse).
func Run(cfg Config) error {
	if cfg.MountPoint == "" {
		return fmt.Errorf("mount: MountPoint required (e.g. Z:)")
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

	var storage *winfs.InfinityStorageFS
	switch {
	case prep.SingleClient != nil:
		storage = winfs.NewSingle(prep.SingleClient, prep.SingleName, prep.SingleSize)
	case prep.Ingest != nil:
		storage = winfs.NewMultiWritable(prep.Entries, prep.Pool, winfs.CatalogLoader(prep.Load), prep.Ingest)
	default:
		storage = winfs.NewMulti(prep.Entries, prep.Pool, winfs.CatalogLoader(prep.Load))
	}
	oldCleanup := cleanup
	cleanup = func() {
		storage.Stop()
		if oldCleanup != nil {
			oldCleanup()
		}
	}

	host := fuse.NewFileSystemHost(storage)
	if cfg.Debug {
		host.SetCapCaseInsensitive(true)
	}

	opts := []string{}
	if cfg.Debug {
		opts = append(opts, "-o", "debug")
	}

	log.Printf("mounting %s — %s", cfg.MountPoint, prep.Summary)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigc
		log.Printf("unmounting %s", cfg.MountPoint)
		host.Unmount()
	}()

	ok := host.Mount(cfg.MountPoint, opts)
	if !ok {
		return fmt.Errorf("winfsp mount failed at %s (is WinFsp installed?)", cfg.MountPoint)
	}
	return nil
}
