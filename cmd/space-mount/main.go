package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/amaan/video-storage-engine/internal/cacheclient"
	"github.com/amaan/video-storage-engine/internal/spacefs"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func main() {
	mountPoint := flag.String("mount", "", "mount point directory")
	udsPath := flag.String("uds", "/tmp/space-cache.sock", "path to stream_proxy UDS")
	debug := flag.Bool("debug", false, "FUSE debug logs")
	flag.Parse()

	if *mountPoint == "" {
		fmt.Fprintf(os.Stderr, "usage: space-mount --mount DIR [--uds PATH]\n")
		os.Exit(2)
	}

	client := cacheclient.New(*udsPath)
	size, err := client.Size()
	if err != nil {
		log.Fatalf("cache Size: %v (is stream_proxy running with --uds?)", err)
	}
	name, err := client.Info()
	if err != nil {
		log.Fatalf("cache Info: %v", err)
	}
	if name == "" {
		name = "video.mp4"
	}
	name = filepath.Base(name)

	if err := os.MkdirAll(*mountPoint, 0755); err != nil {
		log.Fatalf("mkdir mount: %v", err)
	}

	root := spacefs.NewRoot(client, name, size)
	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			AllowOther: false,
			Debug:      *debug,
			FsName:     "space",
			Name:       "space",
			Options:    []string{"ro", "default_permissions"},
		},
	}
	opts.AttrTimeout = nil
	opts.EntryTimeout = nil

	server, err := fs.Mount(*mountPoint, root, opts)
	if err != nil {
		log.Fatalf("mount: %v", err)
	}

	log.Printf("mounted %s/%s (%d bytes) via %s", *mountPoint, name, size, *udsPath)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		log.Printf("unmounting %s", *mountPoint)
		_ = server.Unmount()
	}()

	server.Wait()
}
