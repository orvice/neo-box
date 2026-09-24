// Package blobstore stores opaque objects (snapshot content) by key. The S3
// implementation is used in production; the filesystem implementation is a
// local-development fallback.
package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ErrNotFound is returned by Get when the key does not exist.
var ErrNotFound = errors.New("blob not found")

// Store is a minimal object store.
type Store interface {
	// Put uploads size bytes from r under key, replacing any existing object.
	Put(ctx context.Context, key string, r io.ReadSeeker, size int64, contentType string) error
	// Get opens the object for reading. Callers must close the reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the object. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
}

// S3 stores objects in one bucket under an optional key prefix.
type S3 struct {
	client *awss3.Client
	bucket string
	prefix string
}

// NewS3 wraps a configured S3 client.
func NewS3(client *awss3.Client, bucket, prefix string) *S3 {
	return &S3{client: client, bucket: bucket, prefix: normalizePrefix(prefix)}
}

func (s *S3) Put(ctx context.Context, key string, r io.ReadSeeker, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(s.prefix + key),
		Body:          r,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.prefix + key),
	})
	if err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	return out.Body, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.prefix + key),
	})
	if err != nil {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	return nil
}

// FS stores objects as files under a root directory.
type FS struct {
	root string
}

// NewFS creates the root directory if needed.
func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create blob dir %s: %w", root, err)
	}
	return &FS{root: root}, nil
}

func (f *FS) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	if clean == "/" {
		return "", fmt.Errorf("invalid blob key %q", key)
	}
	return filepath.Join(f.root, clean), nil
}

func (f *FS) Put(_ context.Context, key string, r io.ReadSeeker, _ int64, _ string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (f *FS) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return file, err
}

func (f *FS) Delete(_ context.Context, key string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func normalizePrefix(prefix string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return ""
	}
	return prefix + "/"
}
