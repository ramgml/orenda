// Package mirror writes markdown mirrors of entities into data/mirror/
// so a plain `git push` of the directory preserves content history.
//
// Phase 7 ships:
//
//   - WriteTask / WritePage / WriteComment — Obsidian-compatible frontmatter
//     with id/type/status/tags/updated fields (PLAN#7.2)
//   - DeleteTask / DeletePage — remove the mirror file when the row is gone
//
// The service is best-effort: a failure to write never rolls back the
// source operation, and the next push run will surface the missing file.
package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ramgml/orenda/internal/domain/comment"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/wiki"
)

// Service writes mirror files.
type Service struct {
	// Dir is the mirror root (typically data/mirror).
	Dir string
}

// New returns a mirror Service. Dir must exist (cmd/orenda creates it).
func New(dir string) *Service {
	return &Service{Dir: dir}
}

// Task renders a task with its subtasks and comments (if any are
// provided) as an Obsidian-style markdown file.
func (s *Service) WriteTask(t *task.Task, subtasks []*task.Subtask, comments []*comment.Comment) (string, error) {
	if t == nil {
		return "", fmt.Errorf("mirror: task is nil")
	}
	fm := frontmatter(map[string]any{
		"id":       t.ID,
		"type":     "task",
		"status":   string(t.Status),
		"priority": string(t.Priority),
		"updated":  formatRFC3339(t.UpdatedAt),
	})
	if len(subtasks) > 0 {
		tags := make([]string, 0, len(subtasks))
		for _, st := range subtasks {
			tags = append(tags, st.Title)
		}
		fm = frontmatter(map[string]any{
			"id":       t.ID,
			"type":     "task",
			"status":   string(t.Status),
			"priority": string(t.Priority),
			"subtasks": tags,
			"updated":  formatRFC3339(t.UpdatedAt),
		})
	}

	var b strings.Builder
	b.WriteString(fm)
	b.WriteString("\n# ")
	b.WriteString(t.Title)
	b.WriteString("\n\n")
	if t.Description != "" {
		b.WriteString(t.Description)
		b.WriteString("\n\n")
	}
	if t.ContextMD != "" {
		b.WriteString("## Context\n\n")
		b.WriteString(t.ContextMD)
		b.WriteString("\n\n")
	}
	if t.AgentNotes != "" {
		b.WriteString("## Agent notes\n\n")
		b.WriteString(t.AgentNotes)
		b.WriteString("\n\n")
	}
	if len(subtasks) > 0 {
		b.WriteString("## Subtasks\n\n")
		for _, st := range subtasks {
			mark := "[ ]"
			if st.Done {
				mark = "[x]"
			}
			b.WriteString("- " + mark + " " + st.Title + "\n")
		}
		b.WriteString("\n")
	}
	if len(comments) > 0 {
		b.WriteString("## Comments\n\n")
		for _, c := range comments {
			b.WriteString("- **" + string(c.AuthorType) + ":" + c.AuthorID + "** " +
				"(" + c.CreatedAt.UTC().Format(time.RFC3339) + "):\n")
			b.WriteString("  " + c.BodyMD + "\n")
		}
	}
	return s.writeFile("tasks", t.ID, b.String())
}

// WritePage renders a wiki page.
func (s *Service) WritePage(p *wiki.Page) (string, error) {
	if p == nil {
		return "", fmt.Errorf("mirror: page is nil")
	}
	fm := frontmatter(map[string]any{
		"id":      p.ID,
		"type":    "wiki_page",
		"slug":    p.Slug,
		"updated": formatRFC3339(p.UpdatedAt),
	})
	body := fm + "\n# " + p.Title + "\n\n" + p.ContentMD + "\n"
	return s.writeFile("pages", p.Slug, body)
}

// DeleteTask removes a task mirror file (no error if missing).
func (s *Service) DeleteTask(id string) error {
	err := os.Remove(filepath.Join(s.Dir, "tasks", id+".md"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeletePage removes a page mirror by slug.
func (s *Service) DeletePage(slug string) error {
	err := os.Remove(filepath.Join(s.Dir, "pages", slug+".md"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// writeFile writes body to dir/kind/{name}.md and returns the absolute path.
func (s *Service) writeFile(kind, name, body string) (string, error) {
	dir := filepath.Join(s.Dir, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mirror: mkdir: %w", err)
	}
	abs := filepath.Join(dir, name+".md")
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("mirror: write: %w", err)
	}
	return abs, nil
}

// frontmatter renders a small YAML frontmatter block.
//
// Values are formatted with no YAML library to keep the package's
// dependency surface minimal. Strings are quoted; slices become a
// [a, b, c] list. Numbers and booleans print bare.
func frontmatter(fields map[string]any) string {
	// Stable order so diffs are clean.
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range keys {
		v := fields[k]
		switch x := v.(type) {
		case string:
			b.WriteString(k + ": \"" + strings.ReplaceAll(x, "\"", "\\\"") + "\"\n")
		case []string:
			b.WriteString(k + ": [")
			for i, item := range x {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString("\"" + strings.ReplaceAll(item, "\"", "\\\"") + "\"")
			}
			b.WriteString("]\n")
		case bool:
			b.WriteString(fmt.Sprintf("%s: %t\n", k, x))
		case int:
			b.WriteString(fmt.Sprintf("%s: %d\n", k, x))
		case int64:
			b.WriteString(fmt.Sprintf("%s: %d\n", k, x))
		default:
			b.WriteString(fmt.Sprintf("%s: %v\n", k, x))
		}
	}
	b.WriteString("---\n")
	return b.String()
}

// formatRFC3339 renders a time.Time as RFC3339; zero time becomes "".
func formatRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
