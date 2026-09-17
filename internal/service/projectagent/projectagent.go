// Package projectagent provisions the dedicated per-project agent
// (T330: проект → авто-создание выделенного агента).
//
// EnsureProjectAgent is called by createProjectHandler right after the
// project row exists: it registers an agent named
// project-<number>-<slug> (ASCII-transliterated project name, -N
// suffix on name collisions) and writes the single project_agents
// grant row so delegation works out of the box. The plaintext token
// is returned exactly once — the handler surfaces it in the 201 body.
package projectagent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ramgml/orenda/internal/domain/project"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
)

const (
	// MaxNameAttempts bounds the collision retry loop: the base name
	// plus the -2..-20 suffixes. Exhausting it is a hard error — the
	// handler degrades to 201 + X-Agent-Provision-Error.
	MaxNameAttempts = 20

	// MaxSlugLen caps the slug part of the agent name so
	// project-<number>-<slug> stays a sane identifier even for long
	// project names. Cut at a "-" boundary.
	MaxSlugLen = 24
)

// Projects is the slice of project.Repository the provisioner needs:
// writing the project_agents grant row (task 140 grant model).
type Projects interface {
	SetAllowedAgents(ctx context.Context, projectID string, agentIDs []string, addedByUserID string) error
}

// Service provisions a per-project dedicated agent: agent.Register +
// one project_agents grant row. Both steps are separate transactions;
// a grant failure leaves the registered agent behind (harmless: the
// name is deterministic in the project number, so a retry converges
// instead of duplicating).
type Service struct {
	Agents   *agentservice.Service
	Projects Projects
}

// New returns a provisioner wired to the agent service and the project
// repository.
func New(agents *agentservice.Service, projects Projects) *Service {
	return &Service{Agents: agents, Projects: projects}
}

// EnsureProjectAgent registers the dedicated agent for p and grants it
// to the project. The returned Registered carries the plaintext token,
// valid exactly once (stored bcrypt-hashed).
func (s *Service) EnsureProjectAgent(ctx context.Context, p *project.Project, ownerUserID string) (*agentservice.Registered, error) {
	if p == nil {
		return nil, errors.New("projectagent: nil project")
	}
	if s.Agents == nil || s.Projects == nil {
		return nil, errors.New("projectagent: not wired")
	}
	base := fmt.Sprintf("project-%d", p.Number)
	if slug := slugify(p.Name); slug != "" {
		base += "-" + slug
	}
	description := fmt.Sprintf("Auto-created for project #%d %q (T330)", p.Number, p.Name)

	var reg *agentservice.Registered
	var err error
	name := base
	for attempt := 1; attempt <= MaxNameAttempts; attempt++ {
		if attempt > 1 {
			name = fmt.Sprintf("%s-%d", base, attempt)
		}
		// T330 decisions: type ["custom"] (no built-in backend
		// semantics), empty scopes (access is grant-based, task 140),
		// Register's default MaxConcurrent 3.
		reg, err = s.Agents.Register(ctx, name, []string{"custom"}, description, nil)
		if err == nil {
			break
		}
		if !errors.Is(err, agentservice.ErrNameTaken) {
			return nil, fmt.Errorf("projectagent: register %s: %w", name, err)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("projectagent: no free name after %d attempts (base %q): %w", MaxNameAttempts, base, err)
	}
	if err := s.Projects.SetAllowedAgents(ctx, p.ID, []string{reg.Agent.ID}, ownerUserID); err != nil {
		return nil, fmt.Errorf("projectagent: grant %s: %w", reg.Agent.ID, err)
	}
	return reg, nil
}

// translit maps the characters the name slug keeps, mirroring the SPA
// table (web/src/shared/util/slug.ts): common Cyrillic (Russian +
// Ukrainian + Belarusian) plus a few Latin-1 letters. Silent chars
// (ъ, ь) map to "" — dropped without adding a separator.
var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e",
	'ё': "yo", 'ж': "zh", 'з': "z", 'и': "i", 'й': "i", 'к': "k",
	'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r",
	'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "h", 'ц': "ts",
	'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "",
	'э': "e", 'ю': "yu", 'я': "ya",
	// Ukrainian / Belarusian specifics.
	'і': "i", 'ї': "yi", 'є': "ye", 'ґ': "g",
	// Latin-1 extras people type in names.
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ì': "i", 'í': "i",
	'î': "i", 'ï': "i", 'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o",
	'ö': "o", 'ø': "o", 'ù': "u", 'ú': "u", 'û': "u", 'ü': "u",
	'ñ': "n", 'ß': "ss", 'ç': "c",
}

// slugify derives the slug part of the agent name from a project name:
// lowercase, ASCII-transliterated, [a-z0-9-] only, runs of anything
// else collapsed to one "-", cut at MaxSlugLen on a "-" boundary.
// Returns "" when nothing survives (title was all emoji/CJK) — the
// caller falls back to the bare project-<number> name.
func slugify(name string) string {
	var b strings.Builder
	prevDash := true // suppresses leading separators
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			t, ok := translit[r]
			if !ok {
				t = "-" // unknown char: separator
			}
			if t == "" {
				continue // silent letter (ъ/ь): drop, no separator
			}
			if !prevDash && !strings.HasSuffix(t, "-") {
				b.WriteByte('-')
			}
			b.WriteString(strings.Trim(t, "-"))
			prevDash = strings.HasSuffix(t, "-")
		}
	}
	out := b.String()
	out = strings.TrimSuffix(out, "-")
	if len(out) <= MaxSlugLen {
		return out
	}
	out = out[:MaxSlugLen]
	if i := strings.LastIndexByte(out, '-'); i > 0 {
		out = out[:i]
	}
	return strings.TrimSuffix(out, "-")
}
