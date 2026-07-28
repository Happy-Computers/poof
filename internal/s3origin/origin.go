// Package s3origin serves a single S3 object over HTTP with range-only GETs.
package s3origin

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Origin fetches byte ranges from one S3 object.
type Origin struct {
	client     *s3.Client
	bucket     string
	key        string
	objectSize uint64
	sem        chan struct{}
}

// NewOrigin heads the object and returns a ready origin.
func NewOrigin(ctx context.Context, client *s3.Client, bucket, key string) (*Origin, error) {
	o := &Origin{
		client: client,
		bucket: bucket,
		key:    key,
		sem:    make(chan struct{}, MaxConcurrentS3Fetches),
	}
	if err := o.acquire(ctx); err != nil {
		return nil, err
	}
	head, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	o.release()
	if err != nil {
		return nil, fmt.Errorf("head object s3://%s/%s: %w", bucket, key, err)
	}
	if head.ContentLength == nil || *head.ContentLength <= 0 {
		return nil, fmt.Errorf("head object s3://%s/%s: empty or unknown size", bucket, key)
	}
	o.objectSize = uint64(*head.ContentLength)
	return o, nil
}

func (o *Origin) ObjectSize() uint64 {
	return o.objectSize
}

func (o *Origin) acquire(ctx context.Context) error {
	select {
	case o.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *Origin) release() {
	<-o.sem
}

// Head fetches object metadata from S3.
func (o *Origin) Head(ctx context.Context) (int64, error) {
	if err := o.acquire(ctx); err != nil {
		return 0, err
	}
	defer o.release()

	out, err := o.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(o.bucket),
		Key:    aws.String(o.key),
	})
	if err != nil {
		return 0, err
	}
	if out.ContentLength == nil {
		return 0, fmt.Errorf("head object: missing content-length")
	}
	return *out.ContentLength, nil
}

// GetRange fetches an inclusive byte range from S3.
func (o *Origin) GetRange(ctx context.Context, br ByteRange) (io.ReadCloser, int64, error) {
	if err := o.acquire(ctx); err != nil {
		return nil, 0, err
	}

	rangeHeader := fmt.Sprintf("bytes=%d-%d", br.Start, br.EndInclusive)
	out, err := o.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(o.bucket),
		Key:    aws.String(o.key),
		Range:  aws.String(rangeHeader),
	})
	if err != nil {
		o.release()
		return nil, 0, err
	}

	length := int64(br.Length())
	if out.ContentLength != nil && *out.ContentLength > 0 {
		length = *out.ContentLength
	}
	return &releaseOnClose{ReadCloser: out.Body, release: o.release}, length, nil
}

type releaseOnClose struct {
	io.ReadCloser
	release func()
}

func (r *releaseOnClose) Close() error {
	err := r.ReadCloser.Close()
	r.release()
	return err
}
