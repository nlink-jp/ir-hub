// Package s3 is the AWS S3 storage backend. It uses the default
// AWS credential chain (env, shared config, IAM role).
package s3

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Backend writes knowledge documents to an S3 bucket under an
// optional key prefix.
type Backend struct {
	client *s3.Client
	bucket string
	prefix string
}

// New creates an S3 backend. It returns the config-loading error so
// the caller can degrade gracefully when credentials are
// unavailable. prefix is prepended to every key (empty allowed).
func New(ctx context.Context, bucket, prefix string) (*Backend, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("s3: load aws config: %w", err)
	}
	// Normalize prefix to have no leading and exactly one trailing
	// slash when non-empty.
	prefix = strings.TrimLeft(prefix, "/")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &Backend{client: s3.NewFromConfig(cfg), bucket: bucket, prefix: prefix}, nil
}

func (b *Backend) Name() string { return "s3" }

// Write puts content at prefix+path. Content-Type is forced to
// application/octet-stream so the object is never served as an
// executable type if the bucket is public.
func (b *Backend) Write(ctx context.Context, path string, content []byte) error {
	key := b.prefix + path
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return fmt.Errorf("s3: put %s: %w", key, err)
	}
	return nil
}
