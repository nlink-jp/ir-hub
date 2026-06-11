// Package gcs is the Google Cloud Storage backend (adapted from
// webhook-relay). The client uses Application Default Credentials.
package gcs

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
)

// Backend writes knowledge documents to a GCS bucket.
type Backend struct {
	client *storage.Client
	bucket string
}

// New creates a GCS backend. It returns the client-construction
// error so the caller can degrade gracefully when credentials are
// unavailable.
func New(ctx context.Context, bucket string) (*Backend, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs: create client: %w", err)
	}
	return &Backend{client: client, bucket: bucket}, nil
}

func (b *Backend) Name() string { return "gcs" }

// Write stores content at the object path. Content-Type is forced
// to application/octet-stream so GCS never infers a type (e.g.
// text/html) that would be risky if the bucket were public.
func (b *Backend) Write(ctx context.Context, path string, content []byte) error {
	w := b.client.Bucket(b.bucket).Object(path).NewWriter(ctx)
	w.ContentType = "application/octet-stream"
	if _, err := w.Write(content); err != nil {
		w.Close()
		return fmt.Errorf("gcs: write %s: %w", path, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("gcs: finalize %s: %w", path, err)
	}
	return nil
}

// Close releases the GCS client.
func (b *Backend) Close() error { return b.client.Close() }
