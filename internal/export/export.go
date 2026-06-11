// Package export writes knowledge documents to a storage backend so
// teams outside IR can consume them. Each tactic is written as a
// JSON + Markdown pair named `<tactic-id>-<slug>.{json,md}` —
// deterministic and idempotent, so re-export overwrites in place.
// The enclosing directory is the backend's concern (the local
// backend's root, or the S3 key prefix), so paths aren't doubled.
package export

import (
	"context"
	"fmt"
	"log"

	"github.com/nlink-jp/ir-hub/internal/knowledge"
	"github.com/nlink-jp/ir-hub/internal/storage"
	"github.com/nlink-jp/ir-hub/internal/store"
)

// Service exports knowledge documents to a backend.
type Service struct {
	store   *store.Store
	backend storage.Backend
	logf    func(format string, v ...any)
}

// Option configures a Service.
type Option func(*Service)

// WithLogger overrides the log function.
func WithLogger(logf func(format string, v ...any)) Option {
	return func(s *Service) { s.logf = logf }
}

// New creates an export Service.
func New(st *store.Store, backend storage.Backend, opts ...Option) *Service {
	s := &Service{store: st, backend: backend, logf: log.Printf}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Backend returns the configured backend name (for logging/UX).
func (s *Service) Backend() string { return s.backend.Name() }

// ExportAll writes every knowledge document. Best-effort: a
// per-document failure is logged and counted as a miss, but the
// rest continue; the first error is returned alongside the success
// count.
func (s *Service) ExportAll(ctx context.Context) (int, error) {
	docs, err := s.store.ListAllKnowledge()
	if err != nil {
		return 0, err
	}
	return s.writeDocs(ctx, docs)
}

// ExportCase writes one case's knowledge documents (called at
// postmortem finalization).
func (s *Service) ExportCase(ctx context.Context, caseID int64) (int, error) {
	all, err := s.store.ListAllKnowledge()
	if err != nil {
		return 0, err
	}
	var docs []store.KnowledgeDoc
	for _, d := range all {
		if d.CaseID == caseID {
			docs = append(docs, d)
		}
	}
	return s.writeDocs(ctx, docs)
}

func (s *Service) writeDocs(ctx context.Context, docs []store.KnowledgeDoc) (int, error) {
	var firstErr error
	written := 0
	for _, d := range docs {
		base := d.TacticID
		if slug := knowledge.Slug(d.Title); slug != "" {
			base += "-" + slug
		}
		jsonErr := s.backend.Write(ctx, base+".json", []byte(d.DocJSON))
		mdErr := s.backend.Write(ctx, base+".md", []byte(d.DocMD))
		if jsonErr != nil || mdErr != nil {
			err := jsonErr
			if err == nil {
				err = mdErr
			}
			s.logf("export: %s: %v", d.TacticID, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("export %s: %w", d.TacticID, err)
			}
			continue
		}
		written++
	}
	return written, firstErr
}
