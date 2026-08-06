package mount

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/amaan/infinity-storage/internal/awsutil"
	"github.com/amaan/infinity-storage/internal/cacheclient"
	"github.com/amaan/infinity-storage/internal/catalog"
	"github.com/amaan/infinity-storage/internal/ingest"
	"github.com/amaan/infinity-storage/internal/liverelay"
	"github.com/amaan/infinity-storage/internal/proxypool"
	"github.com/amaan/infinity-storage/internal/s3origin"
)

// CatalogLoader re-lists Infinity Storage entries. Nil means static catalog.
type CatalogLoader func(ctx context.Context) ([]catalog.Entry, error)

// Prepared is a ready catalog + byte pipeline for a volume backend.
type Prepared struct {
	Entries []catalog.Entry
	Pool    *proxypool.Pool
	Load    CatalogLoader
	Cleanup func()
	Summary string
	Ingest  *ingest.Manager

	// Single-file (--uds / --listen-tcp style) mode.
	SingleClient *cacheclient.Client
	SingleName   string
	SingleSize   uint64
}

func prepareConfig(cfg Config) (proxyBin string, err error) {
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
		return "", fmt.Errorf("mount: provide exactly one of Bucket, Dir, or UDS")
	}
	relayFields := 0
	if cfg.LiveRelayURL != "" {
		relayFields++
	}
	if cfg.LibraryID != "" {
		relayFields++
	}
	if cfg.RelayToken != "" {
		relayFields++
	}
	if relayFields != 0 && relayFields != 3 {
		return "", fmt.Errorf("mount: live relay requires LiveRelayURL, LibraryID, and RelayToken")
	}
	if relayFields != 0 && cfg.Bucket == "" {
		return "", fmt.Errorf("mount: live relay requires Bucket mode")
	}
	proxyBin = cfg.ProxyBin
	if proxyBin == "" {
		proxyBin = "stream_proxy"
	}
	return proxyBin, nil
}

func prepareBucket(cfg Config, proxyBin string) (*Prepared, error) {
	ctx := context.Background()
	awsCfg, err := awsutil.LoadConfig(ctx, awsutil.Options{
		Region:   cfg.Region,
		Profile:  cfg.Profile,
		Endpoint: cfg.Endpoint,
		EnvFile:  cfg.EnvFile,
	})
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	who, err := awsutil.CheckIdentity(ctx, awsCfg)
	if err != nil {
		return nil, fmt.Errorf("aws credentials: %w", err)
	}
	log.Printf("aws identity: %s", who)

	s3Client := awsutil.NewS3Client(awsCfg, cfg.Endpoint)
	store, err := s3origin.NewStoreFromList(ctx, s3Client, cfg.Bucket, cfg.Prefix)
	if err != nil {
		return nil, fmt.Errorf("s3 list: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen origin: %w", err)
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
	entries := make([]catalog.Entry, 0, len(metas))
	for _, m := range metas {
		entries = append(entries, catalog.Entry{
			Name: m.Name,
			Size: m.Size,
		})
	}
	entries = catalog.WithOriginBase(entries, originBase)
	remoteEntries := append([]catalog.Entry(nil), entries...)
	lastS3Refresh := time.Now()
	var catalogMu sync.Mutex

	uploadPrefix := strings.Trim(cfg.Prefix, "/")
	if uploadPrefix != "" {
		uploadPrefix += "/"
	}
	uploadStore, err := ingest.NewS3Store(s3Client, cfg.Bucket, uploadPrefix)
	if err != nil {
		_ = httpServer.Close()
		_ = ln.Close()
		return nil, err
	}
	var relay *liverelay.Client
	if cfg.LiveRelayURL != "" {
		relay, err = liverelay.NewClient(liverelay.Config{
			URL:     cfg.LiveRelayURL,
			Library: cfg.LibraryID,
			Token:   cfg.RelayToken,
		})
		if err != nil {
			_ = httpServer.Close()
			_ = ln.Close()
			return nil, err
		}
	}
	spoolDir := cfg.SpoolDir
	if spoolDir == "" {
		spoolDir = filepath.Join(os.TempDir(), "infinity-storage-spool")
	}
	manager, err := ingest.NewManager(ingest.Config{
		SpoolDir: spoolDir,
		Store:    uploadStore,
		Publish: func(snapshot ingest.Snapshot) {
			if relay == nil {
				return
			}
			publishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := relay.Publish(publishCtx, snapshot); err != nil {
				log.Printf("live relay publish %s: %v", snapshot.Name, err)
			}
		},
	})
	if err != nil {
		_ = httpServer.Close()
		_ = ln.Close()
		return nil, err
	}
	if relay != nil {
		liveEntries, err := relay.Load(ctx)
		if err != nil {
			_ = httpServer.Close()
			_ = ln.Close()
			return nil, fmt.Errorf("live relay catalog: %w", err)
		}
		entries = mergeEntries(liveEntries, entries)
	}

	pool, err := proxypool.New(proxypool.Config{ProxyBin: proxyBin})
	if err != nil {
		_ = httpServer.Close()
		_ = ln.Close()
		return nil, fmt.Errorf("proxypool: %w", err)
	}

	load := func(ctx context.Context) ([]catalog.Entry, error) {
		var liveEntries []catalog.Entry
		if relay != nil {
			loaded, err := relay.Load(ctx)
			if err != nil {
				return nil, fmt.Errorf("live relay catalog: %w", err)
			}
			liveEntries = loaded
		}
		catalogMu.Lock()
		if time.Since(lastS3Refresh) >= time.Second {
			if err := store.Refresh(ctx); err != nil {
				catalogMu.Unlock()
				return nil, err
			}
			metas := store.Objects()
			refreshed := make([]catalog.Entry, 0, len(metas))
			for _, meta := range metas {
				refreshed = append(refreshed, catalog.Entry{Name: meta.Name, Size: meta.Size})
			}
			remoteEntries = catalog.WithOriginBase(refreshed, originBase)
			lastS3Refresh = time.Now()
		}
		remote := append([]catalog.Entry(nil), remoteEntries...)
		catalogMu.Unlock()
		return mergeEntries(liveEntries, remote), nil
	}

	liveCtx, stopLive := context.WithCancel(context.Background())
	if relay != nil {
		relay.Start(liveCtx, manager)
	}
	cleanup := func() {
		stopLive()
		_ = pool.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}

	summary := fmt.Sprintf("%d objects from s3://%s/%s via %s (live catalog, max %d proxies)",
		len(entries), cfg.Bucket, cfg.Prefix, originBase, proxypool.MaxActiveProxies)
	return &Prepared{
		Entries: entries,
		Pool:    pool,
		Load:    load,
		Cleanup: cleanup,
		Summary: summary,
		Ingest:  manager,
	}, nil
}

func mergeEntries(first, second []catalog.Entry) []catalog.Entry {
	entries := make(map[string]catalog.Entry, len(first)+len(second))
	for _, entry := range first {
		entries[entry.Name] = entry
	}
	for _, entry := range second {
		entries[entry.Name] = entry
	}
	out := make([]catalog.Entry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	return out
}

func prepareDir(cfg Config, proxyBin string) (*Prepared, error) {
	entries, err := catalog.LoadDir(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	pool, err := proxypool.New(proxypool.Config{ProxyBin: proxyBin})
	if err != nil {
		return nil, fmt.Errorf("proxypool: %w", err)
	}
	return &Prepared{
		Entries: entries,
		Pool:    pool,
		Cleanup: func() { _ = pool.Close() },
		Summary: fmt.Sprintf("%d files from %s (max %d active proxies)", len(entries), cfg.Dir, proxypool.MaxActiveProxies),
	}, nil
}

func prepareUDS(cfg Config) (*Prepared, error) {
	client := dialCache(cfg.UDS)
	size, err := client.Size()
	if err != nil {
		return nil, fmt.Errorf("cache Size: %w (is stream_proxy running with --uds/--listen-tcp?)", err)
	}
	name, err := client.Info()
	if err != nil {
		return nil, fmt.Errorf("cache Info: %w", err)
	}
	if name == "" {
		name = "video.mp4"
	}
	name = filepath.Base(name)
	return &Prepared{
		SingleClient: client,
		SingleName:   name,
		SingleSize:   size,
		Cleanup:      func() {},
		Summary:      fmt.Sprintf("%s/%s (%d bytes) via %s", cfg.MountPoint, name, size, cfg.UDS),
	}, nil
}

func dialCache(addr string) *cacheclient.Client {
	// host:port → TCP SPCH (Windows / portable); otherwise Unix socket path.
	if looksLikeTCPAddr(addr) {
		return cacheclient.NewTCP(addr)
	}
	return cacheclient.New(addr)
}

func looksLikeTCPAddr(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return false
	}
	return true
}

// Prepare builds catalog + origin/proxy pipeline for any OS volume backend.
func Prepare(cfg Config) (*Prepared, error) {
	proxyBin, err := prepareConfig(cfg)
	if err != nil {
		return nil, err
	}
	switch {
	case cfg.Bucket != "":
		return prepareBucket(cfg, proxyBin)
	case cfg.Dir != "":
		return prepareDir(cfg, proxyBin)
	default:
		return prepareUDS(cfg)
	}
}
