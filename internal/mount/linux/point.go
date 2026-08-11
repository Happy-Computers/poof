//go:build linux

package linux

import (
	"fmt"
	"log"
	"os"
	"os/exec"
)

// EnsureMountPoint creates the mount dir, or clears a stale FUSE mount left behind.
func EnsureMountPoint(path string) error {
	fi, err := os.Lstat(path)
	if err == nil {
		if fi.IsDir() {
			return nil
		}
		return fmt.Errorf("%s exists and is not a directory", path)
	}
	if os.IsNotExist(err) {
		return os.MkdirAll(path, 0755)
	}
	log.Printf("stale mount at %s (%v); trying fusermount3 -u", path, err)
	unmount := exec.Command("fusermount3", "-u", path)
	unmount.Stdout = os.Stderr
	unmount.Stderr = os.Stderr
	_ = unmount.Run()
	lazy := exec.Command("fusermount3", "-uz", path)
	_ = lazy.Run()

	fi, err = os.Lstat(path)
	if err == nil {
		if fi.IsDir() {
			return nil
		}
		return fmt.Errorf("%s exists and is not a directory after unmount", path)
	}
	if os.IsNotExist(err) {
		return os.MkdirAll(path, 0755)
	}
	return fmt.Errorf("%s still broken (%v); run: fusermount3 -uz %s", path, err, path)
}
