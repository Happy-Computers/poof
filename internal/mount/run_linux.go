//go:build linux

package mount

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/amaan/video-storage-engine/internal/awsutil"
	"github.com/amaan/video-storage-engine/internal/cacheclient"
	"github.com/amaan/video-storage-engine/internal/mount/linux"
	"github.com/amaan/video-storage-engine/internal/proxypool"
	"github.com/amaan/video-storage-engine/internal/s3origin"
	"github.com/amaan/video-storage-engine/internal/spacecatalog"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Run attaches a Space volume via Linux FUSE.
func Run(cfg Config) error {
	if cfg.MountPoint == "" {
		return fmt.Errorf("mount: MountPoint required")
	}
	modes := 0
	if cfg.Dir != "" {
		modes++
	}
	if cfg.UDS != "" {
		modes++
	}
	if cfg.Bucket != "" {
		modes++
	}
	if modes != 1 {
		return fmt.Errorf("mount: provide exactly one of Bucket, Dir, or UDS")
	}
	proxyBin := cfg.ProxyBin
	if proxyBin == "" {
		proxyBin = "stream_proxy"
	}

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
	var cleanup func()
	var summary string

	switch {
	case cfg.Bucket != "":
		r, c, sum, err := mountS3(cfg, proxyBin)
		if err != nil {
			return err
		}
		root, cleanup, summary = r, c, sum
	case cfg.Dir != "":
		entries, err := spacecatalog.LoadDir(cfg.Dir)
		if err != nil {
			return fmt.Errorf("catalog: %w", err)
		}
		pool, err := proxypool.New(proxypool.Config{ProxyBin: proxyBin})
		if err != nil {
			return fmt.Errorf("proxypool: %w", err)
		}
		cleanup = func() { _ = pool.Close() }
		root = linux.NewRootMulti(entries, pool)
		summary = fmt.Sprintf("%d files from %s (max %d active proxies)", len(entries), cfg.Dir, proxypool.MaxActiveProxies)
	default:
		client := cacheclient.New(cfg.UDS)
		size, err := client.Size()
		if err != nil {
			return fmt.Errorf("cache Size: %w (is stream_proxy running with --uds?)", err)
		}
		name, err := client.Info()
		if err != nil {
			return fmt.Errorf("cache Info: %w", err)
		}
		if name == "" {
			name = "video.mp4"
		}
		name = filepath.Base(name)
		root = linux.NewRootSingle(client, name, size)
		summary = fmt.Sprintf("%s/%s (%d bytes) via %s", cfg.MountPoint, name, size, cfg.UDS)
	}

	server, err := fs.Mount(cfg.MountPoint, root, opts)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return fmt.Errorf("fuse mount: %w", err)
	}

	log.Printf("mounted %s — %s", cfg.MountPoint, summary)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		log.Printf("unmounting %s", cfg.MountPoint)
		_ = server.Unmount()
	}()

	server.Wait()
	if cleanup != nil {
		cleanup()
	}
	return nil
}

func mountS3(cfg Config, proxyBin string) (fs.InodeEmbedder, func(), string, error) {
	ctx := context.Background()
	awsCfg, err := awsutil.LoadConfig(ctx, awsutil.Options{
		Region:   cfg.Region,
		Profile:  cfg.Profile,
		Endpoint: cfg.Endpoint,
		EnvFile:  cfg.EnvFile,
	})
	if err != nil {
		return nil, nil, "", fmt.Errorf("aws config: %w", err)
	}
	who, err := awsutil.CheckIdentity(ctx, awsCfg)
	if err != nil {
		return nil, nil, "", fmt.Errorf("aws credentials: %w", err)
	}
	log.Printf("aws identity: %s", who)

	s3Client := awsutil.NewS3Client(awsCfg, cfg.Endpoint)
	store, err := s3origin.NewStoreFromList(ctx, s3Client, cfg.Bucket, cfg.Prefix)
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

	liveRoot := linux.NewRootMultiLive(entries, pool, load)
	cleanup := func() {
		liveRoot.Stop()
		_ = pool.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}

	summary := fmt.Sprintf("%d objects from s3://%s/%s via %s (live catalog, max %d proxies)",
		len(entries), cfg.Bucket, cfg.Prefix, originBase, proxypool.MaxActiveProxies)
	return liveRoot, cleanup, summary, nil
}
