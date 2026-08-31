// Package search provides FTS5 full-text search across pages, tasks and
// comments.
//
// Phase 5.4 ships a single Search() entry point that returns a slice of
// (type, id, snippet) tuples ranked by FTS5 BM25. Snippets are produced
// via the FTS5 snippet() function with the search terms highlighted.
package search

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ramgml/orenda/internal/api/ws"
)

// Type identifies the kind of hit.
type Type string

const (
	TypePage    Type = "page"
	TypeTask    Type = "task"
	TypeComment Type = "comment"
)

// AllTypes is the ordered list used when no explicit filter is given.
var AllTypes = []Type{TypePage, TypeTask, TypeComment}

// Sentinel errors.
var (
	ErrInvalidInput = errors.New("search: invalid input")
)

// Hit is one result row.
type Hit struct {
	Type    Type    `json:"type"`
	ID      string  `json:"id"`
	Slug    string  `json:"slug,omitempty"` // wiki page slug (empty for task/comment hits)
	Title   string  `json:"title,omitempty"`
	Snippet string  `json:"snippet"` // BM25-ranked extract
	Score   float64 `json:"score"`
}

// Repository is the tiny surface the search service needs.
type Repository interface {
	SearchPages(ctx context.Context, q string, limit int) ([]Hit, error)
	SearchTasks(ctx context.Context, q string, limit int) ([]Hit, error)
	SearchComments(ctx context.Context, q string, limit int) ([]Hit, error)
}

// Service is the dependency holder.
type Service struct {
	Repo Repository
	Hub  ws.Hub
}

// New returns a Search service.
func New(repo Repository, hub ws.Hub) *Service {
	return &Service{Repo: repo, Hub: hub}
}

// Search returns BM25-ranked hits across the requested types.
//
// limit is per type; total may exceed limit when multiple types are
// searched. Empty types → all types.
func (s *Service) Search(ctx context.Context, q string, types []Type, limit int) ([]Hit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, ErrInvalidInput
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if len(types) == 0 {
		types = AllTypes
	}

	var out []Hit
	for _, t := range types {
		var got []Hit
		var err error
		switch t {
		case TypePage:
			got, err = s.Repo.SearchPages(ctx, q, limit)
		case TypeTask:
			got, err = s.Repo.SearchTasks(ctx, q, limit)
		case TypeComment:
			got, err = s.Repo.SearchComments(ctx, q, limit)
		default:
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("search %s: %w", t, err)
		}
		for _, h := range got {
			h.Type = t
			out = append(out, h)
		}
	}

	// Sort by score descending (BM25 returns negative scores, more negative = better).
	// For FTS5, bm25() returns negative values; we negate so larger is better.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Score > out[i].Score {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}
