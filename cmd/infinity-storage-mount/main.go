package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/amaan/infinity-storage/internal/mount"
)

func main() {
	mountPoint := flag.String("mount", "", "mount point (Linux dir, or Windows drive letter e.g. Z:)")
	udsPath := flag.String("uds", "", "stream_proxy SPCH endpoint: Unix socket path, or host:port for TCP")
	originDir := flag.String("dir", "", "local origin directory (multi-file harness)")
	bucket := flag.String("bucket", "", "S3 bucket (multi-file Infinity Storage)")
	prefix := flag.String("prefix", "", "S3 key prefix for flat listing (Delimiter=/)")
	endpoint := flag.String("endpoint", "", "custom S3 endpoint (path-style)")
	region := flag.String("region", "", "AWS region")
	profile := flag.String("profile", "", "AWS shared config profile")
	envFile := flag.String("env-file", "", "optional dotenv file (default: .env then .env.local)")
	proxyBin := flag.String("proxy-bin", "stream_proxy", "path to stream_proxy binary (multi-file)")
	debug := flag.Bool("debug", false, "volume debug logs")
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
		fmt.Fprintf(os.Stderr, "infinity-storage-mount: provide exactly one of --bucket, --dir, or --uds\n")
		usage()
		os.Exit(2)
	}

	err := mount.Run(mount.Config{
		MountPoint: *mountPoint,
		Debug:      *debug,
		Bucket:     *bucket,
		Prefix:     *prefix,
		Region:     *region,
		Profile:    *profile,
		Endpoint:   *endpoint,
		EnvFile:    *envFile,
		Dir:        *originDir,
		UDS:        *udsPath,
		ProxyBin:   *proxyBin,
	})
	if err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  infinity-storage-mount --mount DIR|Z: --bucket BUCKET [--prefix P] [--proxy-bin PATH]\n")
	fmt.Fprintf(os.Stderr, "  infinity-storage-mount --mount DIR|Z: --dir ORIGIN_DIR [--proxy-bin PATH]\n")
	fmt.Fprintf(os.Stderr, "  infinity-storage-mount --mount DIR|Z: --uds SOCK|HOST:PORT\n")
}
