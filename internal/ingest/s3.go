package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3Store struct {
	client *s3.Client
	bucket string
	prefix string
}

func NewS3Store(client *s3.Client, bucket, prefix string) (*S3Store, error) {
	if client == nil {
		return nil, fmt.Errorf("ingest: S3 client required")
	}
	if bucket == "" {
		return nil, fmt.Errorf("ingest: S3 bucket required")
	}
	return &S3Store{client: client, bucket: bucket, prefix: prefix}, nil
}

func (s *S3Store) Begin(ctx context.Context, name string) (Upload, error) {
	key := s.prefix + name
	created, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:            aws.String(s.bucket),
		Key:               aws.String(key),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
	})
	if err != nil {
		return nil, err
	}
	if aws.ToString(created.UploadId) == "" {
		return nil, fmt.Errorf("ingest: S3 did not return an upload ID")
	}
	return &s3Upload{
		client:   s.client,
		bucket:   s.bucket,
		key:      key,
		uploadID: aws.ToString(created.UploadId),
	}, nil
}

type s3Upload struct {
	client   *s3.Client
	bucket   string
	key      string
	uploadID string
}

func (u *s3Upload) PutPart(
	ctx context.Context,
	number int32,
	content []byte,
	checksum [sha256.Size]byte,
) (Part, error) {
	checksumText := base64.StdEncoding.EncodeToString(checksum[:])
	length := int64(len(content))
	out, err := u.client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:         aws.String(u.bucket),
		Key:            aws.String(u.key),
		UploadId:       aws.String(u.uploadID),
		PartNumber:     aws.Int32(number),
		Body:           bytes.NewReader(content),
		ContentLength:  aws.Int64(length),
		ChecksumSHA256: aws.String(checksumText),
	})
	if err != nil {
		return Part{}, err
	}
	if aws.ToString(out.ETag) == "" {
		return Part{}, fmt.Errorf("ingest: S3 did not return an ETag for part %d", number)
	}
	return Part{Number: number, ETag: aws.ToString(out.ETag)}, nil
}

func (u *s3Upload) Complete(
	ctx context.Context,
	parts []Part,
	size uint64,
	checksum [sha256.Size]byte,
) error {
	completed := make([]types.CompletedPart, 0, len(parts))
	for _, part := range parts {
		completed = append(completed, types.CompletedPart{
			ETag:       aws.String(part.ETag),
			PartNumber: aws.Int32(part.Number),
		})
	}
	checksumText := base64.StdEncoding.EncodeToString(checksum[:])
	_, err := u.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:         aws.String(u.bucket),
		Key:            aws.String(u.key),
		UploadId:       aws.String(u.uploadID),
		ChecksumSHA256: aws.String(checksumText),
		IfNoneMatch:    aws.String("*"),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completed,
		},
	})
	if err != nil {
		return err
	}
	head, err := u.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:       aws.String(u.bucket),
		Key:          aws.String(u.key),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		return fmt.Errorf("head completed object: %w", err)
	}
	if head.ContentLength == nil || *head.ContentLength < 0 || uint64(*head.ContentLength) != size {
		return fmt.Errorf("completed object size does not match accepted bytes")
	}
	if aws.ToString(head.ChecksumSHA256) != checksumText {
		return fmt.Errorf("completed object checksum does not match accepted bytes")
	}
	return nil
}

func (u *s3Upload) Abort(ctx context.Context) error {
	_, err := u.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(u.bucket),
		Key:      aws.String(u.key),
		UploadId: aws.String(u.uploadID),
	})
	return err
}
