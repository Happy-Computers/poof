package s3origin

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/amaan/infinity-storage/internal/catalog"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ListFlat lists non-nested objects under prefix using Delimiter="/".
// Nested "folders" (CommonPrefixes) are ignored — flat Infinity Storage only.
func ListFlat(ctx context.Context, client *s3.Client, bucket, prefix string) ([]ObjectMeta, error) {
	if bucket == "" {
		return nil, fmt.Errorf("bucket required")
	}
	if client == nil {
		return nil, fmt.Errorf("s3 client required")
	}

	var (
		out       []ObjectMeta
		seen      = make(map[string]struct{})
		token     *string
		pageGuard = 64 // hard cap on list pages
	)

	for page := 0; page < pageGuard; page++ {
		resp, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("list s3://%s/%s: %w", bucket, prefix, err)
		}

		for _, obj := range resp.Contents {
			key := aws.ToString(obj.Key)
			if key == "" || strings.HasSuffix(key, "/") {
				continue // directory placeholder
			}
			// With Delimiter=/, Contents should be leaf keys under prefix; still reject nested.
			rel := key
			if prefix != "" {
				if !strings.HasPrefix(key, prefix) {
					continue
				}
				rel = strings.TrimPrefix(key, prefix)
			}
			if strings.Contains(rel, "/") {
				continue
			}
			name := path.Base(key)
			if name == "." || name == "/" || name == "" {
				continue
			}
			if len(name) > catalog.MaxNameBytes {
				return nil, fmt.Errorf("name too long (%d > %d): %q", len(name), catalog.MaxNameBytes, name)
			}
			if obj.Size == nil || *obj.Size <= 0 {
				return nil, fmt.Errorf("empty object not allowed: %q", name)
			}
			if _, ok := seen[name]; ok {
				return nil, fmt.Errorf("duplicate basename: %q", name)
			}
			if len(out) >= catalog.MaxFiles {
				return nil, fmt.Errorf("too many files (>%d) in s3://%s/%s", catalog.MaxFiles, bucket, prefix)
			}
			seen[name] = struct{}{}
			out = append(out, ObjectMeta{
				Name: name,
				Key:  key,
				Size: uint64(*obj.Size),
			})
		}

		if !aws.ToBool(resp.IsTruncated) {
			return out, nil
		}
		token = resp.NextContinuationToken
		if token == nil {
			return out, nil
		}
	}
	return nil, fmt.Errorf("list s3://%s/%s: too many pages (>%d)", bucket, prefix, pageGuard)
}
