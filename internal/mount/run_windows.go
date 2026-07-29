//go:build windows

package mount

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	winfs "github.com/amaan/video-storage-engine/internal/mount/windows"
	"github.com/winfsp/cgofuse/fuse"
)

// Run attaches a Space volume via WinFsp (cgofuse).
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

	var space *winfs.SpaceFS
	switch {
	case prep.SingleClient != nil:
		space = winfs.NewSingle(prep.SingleClient, prep.SingleName, prep.SingleSize)
	default:
		space = winfs.NewMulti(prep.Entries, prep.Pool, winfs.CatalogLoader(prep.Load))
	}
	oldCleanup := cleanup
	cleanup = func() {
		space.Stop()
		if oldCleanup != nil {
			oldCleanup()
		}
	}

	host := fuse.NewFileSystemHost(space)
	if cfg.Debug {
		host.SetCapCaseInsensitive(true)
	}

	opts := []string{"-o", "ro"}
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
