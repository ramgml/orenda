// Package wiki provides business logic for the wiki: Save (parse [[slug]]
// links + update wiki_links), Backlinks, and Tree.
package wiki

import (
	"context"
	"errors"
	"regexp"
	"sort"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/wiki"
)

// Sentinel errors.
var (
	ErrNotFound     = errors.New("wiki service: not found")
	ErrInvalidInput = errors.New("wiki service: invalid input")
	ErrSlugTaken    = errors.New("wiki service: slug already in use")
)

// Repository mirrors the wiki.Repository interface for convenience.
type Repository = wiki.Repository

// PageMirror is the seam for the markdown mirror. The concrete impl is
// internal/mirror.Service.
type PageMirror interface {
	WritePage(p *wiki.Page) (string, error)
	DeletePage(slug string) error
}

// Service is the dependency holder.
type Service struct {
	Repo   Repository
	Hub    ws.Hub
	Mirror PageMirror
}

// New returns a Wiki service.
func New(repo Repository, hub ws.Hub) *Service {
	return &Service{Repo: repo, Hub: hub}
}

// slugLinkRE matches [[slug]] tokens; slug may contain letters, digits,
// dashes and underscores.
var slugLinkRE = regexp.MustCompile(`\[\[([A-Za-z0-9_-]+)\]\]`)

// Save creates or updates a page. On update the service re-extracts
// [[slug]] tokens from content_md and updates wiki_links in a single
// transaction.
//
// The pipeline:
//
//  1. If page.ID is set → UPDATE, else → INSERT.
//  2. Parse [[slug]] from content_md.
//  3. For each slug, resolve to a page id (auto-create if missing).
//  4. Replace outgoing links in wiki_links.
func (s *Service) Save(ctx context.Context, p *wiki.Page) (*wiki.Page, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	var got *wiki.Page
	var err error
	if p.ID == "" {
		got, err = s.Repo.Create(ctx, p)
	} else {
		if err := s.Repo.Update(ctx, p); err != nil {
			return nil, err
		}
		got, err = s.Repo.GetByID(ctx, p.ID)
	}
	if err != nil {
		// Translate domain errors to service-level sentinels.
		if errors.Is(err, wiki.ErrSlugTaken) {
			return nil, ErrSlugTaken
		}
		if errors.Is(err, wiki.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Extract slug references and update wiki_links.
	slugs := extractSlugs(p.ContentMD)
	toIDs, err := s.resolveSlugs(ctx, slugs)
	if err != nil {
		return nil, err
	}
	if err := s.Repo.SetLinks(ctx, got.ID, toIDs); err != nil {
		return nil, err
	}

	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "wiki",
			Body: map[string]any{
				"type": "wiki.saved",
				"page": got,
			},
		})
	}
	if s.Mirror != nil {
		_, _ = s.Mirror.WritePage(got)
	}
	return got, nil
}

// GetBySlug fetches by slug (URL-friendly).
func (s *Service) GetBySlug(ctx context.Context, slug string) (*wiki.Page, error) {
	return s.Repo.GetBySlug(ctx, slug)
}

// Backlinks returns every page that links to the given id.
func (s *Service) Backlinks(ctx context.Context, pageID string) ([]*wiki.Page, error) {
	return s.Repo.Backlinks(ctx, pageID)
}

// Tree returns the full hierarchical tree, with top-level pages first and
// children nested. The order matches position ASC per level.
func (s *Service) Tree(ctx context.Context) ([]*wiki.TreeNode, error) {
	all, err := s.Repo.List(ctx)
	if err != nil {
		return nil, err
	}
	byParent := make(map[string][]*wiki.Page)
	for _, p := range all {
		byParent[p.ParentID] = append(byParent[p.ParentID], p)
	}
	// Sort each level by position.
	for k := range byParent {
		sort.Slice(byParent[k], func(i, j int) bool {
			return byParent[k][i].Position < byParent[k][j].Position
		})
	}
	return buildTree(byParent, ""), nil
}

func buildTree(byParent map[string][]*wiki.Page, parentID string) []*wiki.TreeNode {
	children := byParent[parentID]
	out := make([]*wiki.TreeNode, 0, len(children))
	for _, p := range children {
		out = append(out, &wiki.TreeNode{
			Page:     p,
			Children: buildTree(byParent, p.ID),
		})
	}
	return out
}

// extractSlugs returns every [[slug]] token in body.
func extractSlugs(body string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, m := range slugLinkRE.FindAllStringSubmatch(body, -1) {
		if _, ok := seen[m[1]]; ok {
			continue
		}
		seen[m[1]] = struct{}{}
		out = append(out, m[1])
	}
	return out
}

// resolveSlugs converts each slug to a page id. Missing pages are
// auto-created with the slug as title (and empty content).
func (s *Service) resolveSlugs(ctx context.Context, slugs []string) ([]string, error) {
	out := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		p, err := s.Repo.GetBySlug(ctx, slug)
		if errors.Is(err, wiki.ErrNotFound) {
			// Auto-create a stub page.
			newPage := &wiki.Page{
				Slug:      slug,
				Title:     slug,
				ContentMD: "",
			}
			created, err := s.Repo.Create(ctx, newPage)
			if err != nil {
				if errors.Is(err, wiki.ErrSlugTaken) {
					// Raced create; re-read.
					p2, err := s.Repo.GetBySlug(ctx, slug)
					if err != nil {
						return nil, err
					}
					out = append(out, p2.ID)
					continue
				}
				return nil, err
			}
			out = append(out, created.ID)
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p.ID)
	}
	return out, nil
}
