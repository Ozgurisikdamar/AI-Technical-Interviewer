package recording

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Store deletes recording objects from an S3-compatible bucket (used by the purge job only; the
// egress service writes with its own credentials).
type S3Store struct {
	client *minio.Client
	bucket string
}

// NewS3Store connects to an S3-compatible endpoint (host[:port], no scheme).
func NewS3Store(endpoint, bucket, accessKey, secretKey string, useSSL bool) (*S3Store, error) {
	if endpoint == "" || bucket == "" {
		return nil, fmt.Errorf("recording storage endpoint and bucket are required")
	}
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: useSSL})
	if err != nil {
		return nil, err
	}
	return &S3Store{client: c, bucket: bucket}, nil
}

// Remove deletes an object; deleting a missing object succeeds (idempotent purge).
func (s *S3Store) Remove(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}
