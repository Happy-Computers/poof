package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/amaan/infinity-storage/internal/awsutil"
	"github.com/amaan/infinity-storage/internal/s3origin"
	smithy "github.com/aws/smithy-go"
)

func main() {
	bucket := flag.String("bucket", "", "S3 bucket name")
	key := flag.String("key", "", "single S3 object key (mutually exclusive with multi-list)")
	prefix := flag.String("prefix", "", "list flat objects under prefix (Delimiter=/); empty = bucket root")
	listAll := flag.Bool("list", false, "multi-object mode: list flat keys (use with --bucket; optional --prefix)")
	listen := flag.String("listen", "127.0.0.1:9090", "listen address")
	endpoint := flag.String("endpoint", "", "custom S3 endpoint (enables path-style, e.g. MinIO)")
	region := flag.String("region", "", "AWS region")
	profile := flag.String("profile", "", "AWS shared config profile")
	envFile := flag.String("env-file", "", "optional dotenv file (default: .env then .env.local)")
	flag.Parse()

	if *bucket == "" {
		usage()
		os.Exit(2)
	}
	if *key != "" && *listAll {
		fmt.Fprintf(os.Stderr, "infinity-storage-origin: --key and --list are mutually exclusive\n")
		usage()
		os.Exit(2)
	}
	if *key == "" && !*listAll {
		// Default: require --key for backward compat, or --list for multi.
		fmt.Fprintf(os.Stderr, "infinity-storage-origin: provide --key KEY or --list\n")
		usage()
		os.Exit(2)
	}

	ctx := context.Background()
	cfg, err := awsutil.LoadConfig(ctx, awsutil.Options{
		Region:   *region,
		Profile:  *profile,
		Endpoint: *endpoint,
		EnvFile:  *envFile,
	})
	if err != nil {
		log.Fatalf("aws config: %v\n%s", err, credHelp())
	}

	log.Printf("aws credential sources: %s", awsutil.DiagnoseCredSources(*profile))
	who, err := awsutil.CheckIdentity(ctx, cfg)
	if err != nil {
		log.Fatalf("aws credentials: %v\n%s", err, credHelp())
	}
	if *profile != "" {
		log.Printf("aws identity via profile %q: %s", *profile, who)
	} else {
		log.Printf("aws identity: %s", who)
	}

	s3Client := awsutil.NewS3Client(cfg, *endpoint)

	var store *s3origin.Store
	if *listAll {
		store, err = s3origin.NewStoreFromList(ctx, s3Client, *bucket, *prefix)
	} else {
		store, err = s3origin.NewStoreFromKey(ctx, s3Client, *bucket, *key)
	}
	if err != nil {
		log.Fatalf("origin: %v\n%s", clarifyS3Err(err), credHelp())
	}

	handler := s3origin.NewHandler(store)
	server := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if m, ok := store.Single(); ok {
		log.Printf("infinity-storage-origin listening on http://%s/object (s3://%s/%s, %d bytes, region=%s)",
			*listen, *bucket, m.Key, m.Size, cfg.Region)
	} else {
		log.Printf("infinity-storage-origin listening on http://%s/object/<name> (%d objects in s3://%s/%s, region=%s)",
			*listen, store.Len(), *bucket, *prefix, cfg.Region)
	}

	go func() {
		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
		<-sigc
		log.Printf("shutting down")
		_ = server.Close()
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  infinity-storage-origin --bucket B --key KEY [--listen ADDR] ...\n")
	fmt.Fprintf(os.Stderr, "  infinity-storage-origin --bucket B --list [--prefix P] [--listen ADDR] ...\n")
}

func clarifyS3Err(err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%w (api=%s code=%s)", err, apiErr.ErrorCode(), apiErr.ErrorMessage())
	}
	return err
}

func credHelp() string {
	return `
No usable AWS credentials.

  aws configure
  # or: cp .env.example .env and fill AWS_* keys
`
}
