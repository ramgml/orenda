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

// WriteTask renders a task as an Obsidian-style markdown file.
//
// Phase 14 swap: child tasks are first-class tasks and get their own
// mirror files (the caller is responsible for invoking WriteTask for
// each one). This entry point therefore only embeds the task's own
// checklists — local quick-checkbox primitives that live and die with
// the parent and have no representation in git elsewhere.
//
// Phase 13: `tags` is rendered as a YAML list in the frontmatter
// (`tags: [a, b]`) so plain git diffs show label churn alongside
// status/priority changes. Names are used (not ids) because the
// labels are what humans read in git history.
func (s *Service) WriteTask(
	t *task.Task,
	checklists []task.Checklist,
	itemsByList map[string][]task.ChecklistItem,
	comments []*comment.Comment,
	tags []task.Tag,
) (string, error) {
	if t == nil {
		return "", fmt.Errorf("mirror: task is nil")
	}
	fmFields := map[string]any{
		"id":       t.ID,
		"type":     "task",
		"status":   string(t.Status),
		"priority": string(t.Priority),
		"updated":  formatRFC3339(t.UpdatedAt),
	}
	if t.ParentTaskID != "" {
		fmFields["parent_task_id"] = t.ParentTaskID
	}
	if len(tags) > 0 {
		names := make([]string, len(tags))
		for i, tg := range tags {
			names[i] = tg.Name
		}
		fmFields["tags"] = names
	}
	if t.Color != "" {
		fmFields["color"] = t.Color
	}
	fm := frontmatter(fmFields)

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
	if len(checklists) > 0 {
		b.WriteString("## Checklists\n\n")
		for _, cl := range checklists {
			b.WriteString("### " + cl.Title + "\n\n")
			items := itemsByList[cl.ID]
			if len(items) == 0 {
				b.WriteString("_(empty)_\n\n")
				continue
			}
			for _, it := range items {
				mark := "[ ]"
				if it.Done {
					mark = "[x]"
				}
				b.WriteString("- " + mark + " " + it.Title + "\n")
			}
			b.WriteString("\n")
		}
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
