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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/amaan/video-storage-engine/internal/s3origin"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithy "github.com/aws/smithy-go"
)

func main() {
	bucket := flag.String("bucket", "", "S3 bucket name")
	key := flag.String("key", "", "S3 object key")
	listen := flag.String("listen", "127.0.0.1:9090", "listen address")
	endpoint := flag.String("endpoint", "", "custom S3 endpoint (enables path-style, e.g. MinIO)")
	region := flag.String("region", "", "AWS region (same as AWS_REGION / aws configure)")
	profile := flag.String("profile", "", "AWS shared config profile (same as AWS_PROFILE / aws --profile)")
	envFile := flag.String("env-file", "", "optional dotenv file with AWS_* keys (default: .env then .env.local)")
	flag.Parse()

	if *bucket == "" || *key == "" {
		fmt.Fprintf(os.Stderr, "usage: space-origin --bucket BUCKET --key KEY [--region REGION] [--profile PROFILE] [--env-file PATH] [--listen ADDR] [--endpoint URL]\n")
		os.Exit(2)
	}

	// Same places humans put keys for local AWS CLI / dotenv workflows.
	if *envFile != "" {
		loadDotEnvFiles(*envFile)
	} else {
		loadDotEnvFiles(".env", ".env.local")
	}

	ctx := context.Background()

	var loadOpts []func(*config.LoadOptions) error
	// Laptop/dev: do not hang on EC2 IMDS when ~/.aws or env creds are missing.
	loadOpts = append(loadOpts, config.WithEC2IMDSClientEnableState(imds.ClientDisabled))
	if *region != "" {
		loadOpts = append(loadOpts, config.WithRegion(*region))
	}
	if *profile != "" {
		loadOpts = append(loadOpts, config.WithSharedConfigProfile(*profile))
	}

	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		log.Fatalf("aws config: %v\n%s", err, credHelp())
	}
	if cfg.Region == "" && *endpoint == "" {
		log.Fatalf("aws region is empty; pass --region or set AWS_REGION / aws configure\n%s", credHelp())
	}

	log.Printf("aws credential sources: %s", diagnoseCredSources(*profile))

	if err := logCallerIdentity(ctx, cfg, *profile); err != nil {
		log.Fatalf("aws credentials: %v\n%s", err, credHelp())
	}

	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if *endpoint != "" {
			o.BaseEndpoint = aws.String(*endpoint)
			o.UsePathStyle = true
		}
	})

	origin, err := s3origin.NewOrigin(ctx, s3Client, *bucket, *key)
	if err != nil {
		log.Fatalf("origin: %v\n%s", clarifyS3Err(err), credHelp())
	}

	handler := s3origin.NewHandler(origin)
	server := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("space-origin listening on http://%s/object (s3://%s/%s, %d bytes, region=%s)",
		*listen, *bucket, *key, origin.ObjectSize(), cfg.Region)

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

func diagnoseCredSources(profile string) string {
	var parts []string
	if v, ok := os.LookupEnv("AWS_ACCESS_KEY_ID"); ok && v != "" {
		parts = append(parts, "env:AWS_ACCESS_KEY_ID")
	}
	if v, ok := os.LookupEnv("AWS_PROFILE"); ok && v != "" {
		parts = append(parts, "env:AWS_PROFILE="+v)
	}
	if profile != "" {
		parts = append(parts, "flag:--profile="+profile)
	}
	home, _ := os.UserHomeDir()
	credPath := filepath.Join(home, ".aws", "credentials")
	cfgPath := filepath.Join(home, ".aws", "config")
	if st, err := os.Stat(credPath); err == nil && !st.IsDir() {
		parts = append(parts, "file:"+credPath)
	} else {
		parts = append(parts, "missing:"+credPath)
	}
	if st, err := os.Stat(cfgPath); err == nil && !st.IsDir() {
		parts = append(parts, "file:"+cfgPath)
	} else {
		parts = append(parts, "missing:"+cfgPath)
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}

func logCallerIdentity(ctx context.Context, cfg aws.Config, profile string) error {
	client := sts.NewFromConfig(cfg)
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("cannot resolve credentials (same chain as AWS CLI): %w", err)
	}
	who := aws.ToString(out.Arn)
	if who == "" {
		who = aws.ToString(out.Account)
	}
	if profile != "" {
		log.Printf("aws identity via profile %q: %s", profile, who)
	} else {
		log.Printf("aws identity: %s", who)
	}
	return nil
}

func clarifyS3Err(err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%w (api=%s code=%s)", err, apiErr.ErrorCode(), apiErr.ErrorMessage())
	}
	if strings.Contains(err.Error(), "IMDS") || strings.Contains(err.Error(), "ec2imds") {
		return fmt.Errorf("%w (no local AWS credentials; IMDS is disabled on purpose)", err)
	}
	return err
}

func credHelp() string {
	return strings.TrimSpace(`
No usable AWS credentials on this machine yet.

This host had no ~/.aws/credentials and no AWS_* env vars — Go cannot guess keys.
AWS CLI is installed at ~/.local/bin/aws. Do ONE of:

  A) Interactive (same as normal AWS CLI):
       aws configure
       # paste Access Key ID, Secret, region (e.g. us-east-1)
       aws sts get-caller-identity
       aws s3 ls s3://BUCKET/KEY

  B) Dotenv in the repo (not committed):
       cp .env.example .env
       # edit .env with your keys + AWS_REGION
       go run ./cmd/space-origin --bucket ... --key ...

  C) Export in this shell:
       export AWS_ACCESS_KEY_ID=...
       export AWS_SECRET_ACCESS_KEY=...
       export AWS_REGION=us-east-1
`)
}
