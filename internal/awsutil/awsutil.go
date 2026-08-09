// Package awsutil loads local AWS config the same way infinity-storage-origin does.
package awsutil

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Options for LoadConfig.
type Options struct {
	Region   string
	Profile  string
	Endpoint string // custom S3 endpoint (path-style)
	EnvFile  string // if empty: load .env then .env.local
}

// LoadDotEnv loads KEY=VAL pairs without overwriting existing env.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[len("export "):])
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, val)
	}
	return sc.Err()
}

// LoadDotEnvDefault loads env-file or .env / .env.local.
func LoadDotEnvDefault(envFile string) {
	if envFile != "" {
		_ = LoadDotEnv(envFile)
		return
	}
	_ = LoadDotEnv(".env")
	_ = LoadDotEnv(".env.local")
}

// LoadConfig loads AWS config (IMDS disabled for laptop/dev).
func LoadConfig(ctx context.Context, opts Options) (aws.Config, error) {
	LoadDotEnvDefault(opts.EnvFile)

	var loadOpts []func(*config.LoadOptions) error
	loadOpts = append(loadOpts, config.WithEC2IMDSClientEnableState(imds.ClientDisabled))
	if opts.Region != "" {
		loadOpts = append(loadOpts, config.WithRegion(opts.Region))
	}
	if opts.Profile != "" {
		loadOpts = append(loadOpts, config.WithSharedConfigProfile(opts.Profile))
	}

	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return aws.Config{}, err
	}
	if cfg.Region == "" && opts.Endpoint == "" {
		return aws.Config{}, fmt.Errorf("aws region is empty; pass --region or set AWS_REGION")
	}
	return cfg, nil
}

// NewS3Client builds an S3 client; endpoint enables path-style (MinIO).
func NewS3Client(cfg aws.Config, endpoint string) *s3.Client {
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})
}

// CheckIdentity calls STS GetCallerIdentity to fail fast on bad creds.
func CheckIdentity(ctx context.Context, cfg aws.Config) (string, error) {
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("cannot resolve credentials: %w", err)
	}
	who := aws.ToString(out.Arn)
	if who == "" {
		who = aws.ToString(out.Account)
	}
	return who, nil
}

// DiagnoseCredSources returns a short string of where creds might come from.
func DiagnoseCredSources(profile string) string {
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
	for _, name := range []string{"credentials", "config"} {
		p := filepath.Join(home, ".aws", name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			parts = append(parts, "file:"+p)
		} else {
			parts = append(parts, "missing:"+p)
		}
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}
