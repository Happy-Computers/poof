package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/amaan/video-storage-engine/internal/awsutil"
	"github.com/amaan/video-storage-engine/internal/cacheclient"
	"github.com/amaan/video-storage-engine/internal/proxypool"
	"github.com/amaan/video-storage-engine/internal/s3origin"
	"github.com/amaan/video-storage-engine/internal/spacecatalog"
	"github.com/amaan/video-storage-engine/internal/spacefs"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func main() {
	mountPoint := flag.String("mount", "", "mount point directory")
	udsPath := flag.String("uds", "", "path to stream_proxy UDS (single-file mode)")
	originDir := flag.String("dir", "", "local origin directory (multi-file harness)")
	bucket := flag.String("bucket", "", "S3 bucket (multi-file cloud Space)")
	prefix := flag.String("prefix", "", "S3 key prefix for flat listing (Delimiter=/)")
	endpoint := flag.String("endpoint", "", "custom S3 endpoint (path-style)")
	region := flag.String("region", "", "AWS region")
	profile := flag.String("profile", "", "AWS shared config profile")
	envFile := flag.String("env-file", "", "optional dotenv file (default: .env then .env.local)")
	proxyBin := flag.String("proxy-bin", "stream_proxy", "path to stream_proxy binary (multi-file)")
	debug := flag.Bool("debug", false, "FUSE debug logs")
	flag.Parse()

	if *mountPoint == "" {
		usage()
		os.Exit(2)
	}

	modes := 0
	if *originDir != "" {
		modes++
	}
	if *udsPath != "" {
		modes++
	}
	if *bucket != "" {
		modes++
	}
	if modes != 1 {
		fmt.Fprintf(os.Stderr, "space-mount: provide exactly one of --bucket, --dir, or --uds\n")
		usage()
		os.Exit(2)
	}

	if err := ensureMountPoint(*mountPoint); err != nil {
		log.Fatalf("mount point: %v", err)
	}

	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			AllowOther: false,
			Debug:      *debug,
			FsName:     "space",
			Name:       "space",
			Options:    []string{"ro", "default_permissions"},
		},
	}
	// No kernel name cache so S3 catalog refreshes show up on ls / Explorer.
	zero := time.Duration(0)
	opts.AttrTimeout = &zero
	opts.EntryTimeout = &zero
	opts.NegativeTimeout = &zero

	var root fs.InodeEmbedder
	var cleanup func()
	var summary string

	switch {
	case *bucket != "":
		r, c, sum, err := mountS3(*bucket, *prefix, *region, *profile, *endpoint, *envFile, *proxyBin)
		if err != nil {
			log.Fatal(err)
		}
		root, cleanup, summary = r, c, sum
	case *originDir != "":
		entries, err := spacecatalog.LoadDir(*originDir)
		if err != nil {
			log.Fatalf("catalog: %v", err)
		}
		pool, err := proxypool.New(proxypool.Config{ProxyBin: *proxyBin})
		if err != nil {
			log.Fatalf("proxypool: %v", err)
		}
		cleanup = func() { _ = pool.Close() }
		root = spacefs.NewRootMulti(entries, pool)
		summary = fmt.Sprintf("%d files from %s (max %d active proxies)", len(entries), *originDir, proxypool.MaxActiveProxies)
	default:
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
		root = spacefs.NewRootSingle(client, name, size)
		summary = fmt.Sprintf("%s/%s (%d bytes) via %s", *mountPoint, name, size, *udsPath)
	}

	server, err := fs.Mount(*mountPoint, root, opts)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		log.Fatalf("mount: %v", err)
	}

	log.Printf("mounted %s — %s", *mountPoint, summary)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		log.Printf("unmounting %s", *mountPoint)
		_ = server.Unmount()
	}()

	server.Wait()
	if cleanup != nil {
		cleanup()
	}
}

func mountS3(bucket, prefix, region, profile, endpoint, envFile, proxyBin string) (fs.InodeEmbedder, func(), string, error) {
	ctx := context.Background()
	cfg, err := awsutil.LoadConfig(ctx, awsutil.Options{
		Region:   region,
		Profile:  profile,
		Endpoint: endpoint,
		EnvFile:  envFile,
	})
	if err != nil {
		return nil, nil, "", fmt.Errorf("aws config: %w", err)
	}
	who, err := awsutil.CheckIdentity(ctx, cfg)
	if err != nil {
		return nil, nil, "", fmt.Errorf("aws credentials: %w", err)
	}
	log.Printf("aws identity: %s", who)

	s3Client := awsutil.NewS3Client(cfg, endpoint)
	store, err := s3origin.NewStoreFromList(ctx, s3Client, bucket, prefix)
	if err != nil {
		return nil, nil, "", fmt.Errorf("s3 list: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, "", fmt.Errorf("listen origin: %w", err)
	}
	httpServer := &http.Server{
		Handler:           s3origin.NewHandler(store),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("s3 origin serve: %v", err)
		}
	}()

	originBase := "http://" + ln.Addr().String()
	metas := store.Objects()
	entries := make([]spacecatalog.Entry, 0, len(metas))
	for _, m := range metas {
		entries = append(entries, spacecatalog.Entry{
			Name: m.Name,
			Size: m.Size,
		})
	}
	entries = spacecatalog.WithOriginBase(entries, originBase)

	pool, err := proxypool.New(proxypool.Config{ProxyBin: proxyBin})
	if err != nil {
		_ = httpServer.Close()
		_ = ln.Close()
		return nil, nil, "", fmt.Errorf("proxypool: %w", err)
	}

	load := func(ctx context.Context) ([]spacecatalog.Entry, error) {
		if err := store.Refresh(ctx); err != nil {
			return nil, err
		}
		metas := store.Objects()
		ents := make([]spacecatalog.Entry, 0, len(metas))
		for _, m := range metas {
			ents = append(ents, spacecatalog.Entry{Name: m.Name, Size: m.Size})
		}
		return spacecatalog.WithOriginBase(ents, originBase), nil
	}

	liveRoot := spacefs.NewRootMultiLive(entries, pool, load)
	cleanup := func() {
		liveRoot.Stop()
		_ = pool.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}

	summary := fmt.Sprintf("%d objects from s3://%s/%s via %s (live catalog, max %d proxies)",
		len(entries), bucket, prefix, originBase, proxypool.MaxActiveProxies)
	return liveRoot, cleanup, summary, nil
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  space-mount --mount DIR --bucket BUCKET [--prefix P] [--proxy-bin PATH]\n")
	fmt.Fprintf(os.Stderr, "  space-mount --mount DIR --dir ORIGIN_DIR [--proxy-bin PATH]\n")
	fmt.Fprintf(os.Stderr, "  space-mount --mount DIR --uds SOCK\n")
}

// ensureMountPoint creates the mount dir, or clears a stale FUSE mount left behind.
func ensureMountPoint(path string) error {
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
	// Common after a killed space-mount: "transport endpoint is not connected".
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
