// Package attachment provides business logic for file attachments.
//
// Phase 3.8 ships StoreFromBytes + ListByTarget + Get + Delete. The
// repository handles the DB row; this service owns the file layout on
// disk (data/uploads/YYYY/MM/{uuid}-{sanitized_filename}).
//
// Allowed mime types come from cfg.Uploads.AllowedMimes (config-driven)
// and the max size from cfg.Uploads.MaxSizeMB.
package attachment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/attachment"
)

// Sentinel errors.
var (
	ErrNotFound     = errors.New("attachment service: not found")
	ErrInvalidInput = errors.New("attachment service: invalid input")
	ErrTooLarge     = errors.New("attachment service: file too large")
	ErrMimeRejected = errors.New("attachment service: mime type not allowed")
)

// Repository is the small surface the service needs from storage.
type Repository interface {
	Create(ctx context.Context, a *attachment.Attachment) error
	GetByID(ctx context.Context, id string) (*attachment.Attachment, error)
	ListByTarget(ctx context.Context, targetType attachment.TargetType, targetID string) ([]*attachment.Attachment, error)
	ListByProject(ctx context.Context, projectID string) ([]*attachment.ProjectAttachment, error)
	FindBySHA256(ctx context.Context, sha string) (*attachment.Attachment, error)
	Delete(ctx context.Context, id string) error
}

// Config holds the upload directory + size/mime limits.
type Config struct {
	UploadDir    string   // absolute path to data/uploads
	MaxSizeBytes int64    // hard cap (default 50 MiB)
	AllowedMimes []string // e.g. ["image/*", "application/pdf", "text/*"]
}

// Service is the dependency holder.
type Service struct {
	Repo   Repository
	Config Config
	Hub    ws.Hub
}

// New returns a Service. The service lazily creates UploadDir on the
// first store if it does not exist (cmd/orenda only resolves the path).
func New(repo Repository, cfg Config, hub ws.Hub) *Service {
	return &Service{Repo: repo, Config: cfg, Hub: hub}
}

// StoreResult is what StoreFromBytes returns.
type StoreResult struct {
	Attachment *attachment.Attachment
	Duplicate  bool // true when the file was already present (sha256 match)
}

// StoreFromBytes persists a file with the given metadata. The body is
// streamed to disk and the SHA-256 is computed in the same pass.
//
// If an attachment with the same sha256 already exists for any target,
// the existing row is returned with Duplicate=true and no new file is
// written (Phase 3.8 dedup semantics).
func (s *Service) StoreFromBytes(
	ctx context.Context,
	targetType attachment.TargetType,
	targetID, filename, mime string,
	uploaderType attachment.UploaderType,
	uploaderID string,
	body io.Reader,
) (*StoreResult, error) {
	if targetID == "" || filename == "" || uploaderID == "" {
		return nil, ErrInvalidInput
	}
	if !s.mimeAllowed(mime) {
		return nil, ErrMimeRejected
	}
	if s.Config.MaxSizeBytes <= 0 {
		s.Config.MaxSizeBytes = 50 * 1024 * 1024 // 50 MiB default
	}

	// Make sure the upload dir exists: nothing else on the production
	// path creates it, so the service is self-sufficient here.
	if err := os.MkdirAll(s.Config.UploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("attachment store: mkdir upload dir: %w", err)
	}

	// Stream to a temp file while hashing + counting.
	tmp, err := os.CreateTemp(s.Config.UploadDir, ".upload-*.part")
	if err != nil {
		return nil, fmt.Errorf("attachment store: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	hasher := newHash()
	limited := io.LimitReader(body, s.Config.MaxSizeBytes+1)
	n, err := io.Copy(io.MultiWriter(tmp, hasher), limited)
	if err != nil {
		return nil, fmt.Errorf("attachment store: write: %w", err)
	}
	if n > s.Config.MaxSizeBytes {
		return nil, ErrTooLarge
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("attachment store: close tmp: %w", err)
	}
	sha := hasher.Sum(nil)

	// Dedup check.
	if existing, err := s.Repo.FindBySHA256(ctx, toHex(sha)); err == nil && existing != nil {
		return &StoreResult{Attachment: existing, Duplicate: true}, nil
	}

	// Final path: data/uploads/YYYY/MM/{id}-{sanitized}.
	now := time.Now()
	relDir := filepath.Join(fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", int(now.Month())))
	absDir := filepath.Join(s.Config.UploadDir, relDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("attachment store: mkdir: %w", err)
	}
	a := &attachment.Attachment{
		ID:             newUUIDLite(),
		TargetType:     targetType,
		TargetID:       targetID,
		Filename:       filename,
		Mime:           mime,
		Size:           n,
		SHA256:         toHex(sha),
		UploadedByType: uploaderType,
		UploadedByID:   uploaderID,
	}
	finalRel := filepath.Join(relDir, a.ID+"-"+sanitize(filename))
	finalAbs := filepath.Join(s.Config.UploadDir, finalRel)
	if err := os.Rename(tmpPath, finalAbs); err != nil {
		return nil, fmt.Errorf("attachment store: rename: %w", err)
	}
	a.Path = finalAbs
	if err := a.Validate(); err != nil {
		// Best-effort: roll back the rename so a retry doesn't accumulate.
		_ = os.Remove(finalAbs)
		return nil, err
	}

	if err := s.Repo.Create(ctx, a); err != nil {
		_ = os.Remove(finalAbs)
		return nil, fmt.Errorf("attachment store: persist: %w", err)
	}

	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "attachments",
			Body: map[string]any{
				"type":       "attachment.added",
				"attachment": a,
			},
		})
	}
	return &StoreResult{Attachment: a, Duplicate: false}, nil
}

// mimeAllowed reports whether mime is in the allowed list. The list
// supports simple "*/*" wildcards per type prefix (image/*, text/*, …).
func (s *Service) mimeAllowed(mime string) bool {
	if len(s.Config.AllowedMimes) == 0 {
		return true // default: anything goes
	}
	mime = strings.ToLower(mime)
	for _, raw := range s.Config.AllowedMimes {
		pattern := strings.ToLower(strings.TrimSpace(raw))
		if pattern == "" {
			continue
		}
		if pattern == mime {
			return true
		}
		if strings.HasSuffix(pattern, "/*") {
			prefix := strings.TrimSuffix(pattern, "/*")
			if strings.HasPrefix(mime, prefix+"/") {
				return true
			}
		}
	}
	return false
}

var sanitizedRE = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// sanitize replaces anything that isn't [a-zA-Z0-9._-] with '_'. Empty
// input falls back to "file".
func sanitize(name string) string {
	out := sanitizedRE.ReplaceAllString(name, "_")
	if out == "" || out == "_" {
		return "file"
	}
	return out
}

// Get returns the attachment row by id (no file I/O).
func (s *Service) Get(ctx context.Context, id string) (*attachment.Attachment, error) {
	return s.Repo.GetByID(ctx, id)
}

// Open returns the attachment row and an open file handle ready to
// be streamed to a client. The caller is responsible for closing the
// returned file. Returns ErrNotFound if the row is missing or the
// underlying file has been removed from disk.
func (s *Service) Open(ctx context.Context, id string) (*attachment.Attachment, *os.File, error) {
	a, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(a.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("attachment.Open: %w", err)
	}
	return a, f, nil
}

// ListByTarget returns attachments for a target.
func (s *Service) ListByTarget(ctx context.Context, t attachment.TargetType, targetID string) ([]*attachment.Attachment, error) {
	return s.Repo.ListByTarget(ctx, t, targetID)
}

// ListByProject returns every attachment that belongs to a project —
// directly attached rows plus every row attached to a task of the
// project — newest first. Task attachments are annotated with the
// task's title.
func (s *Service) ListByProject(ctx context.Context, projectID string) ([]*attachment.ProjectAttachment, error) {
	return s.Repo.ListByProject(ctx, projectID)
}

// Delete removes the attachment row + file from disk.
func (s *Service) Delete(ctx context.Context, id string) error {
	a, err := s.Repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, attachment.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := s.Repo.Delete(ctx, id); err != nil {
		return err
	}
	if a.Path != "" {
		_ = os.Remove(a.Path)
	}
	return nil
}
