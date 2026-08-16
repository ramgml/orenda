package main

import (
	"os"
	"strings"
	"testing"
)

// TestSkillDoc_HasAgentFrontmatter guards the skill source against a
// regression observed 2026-08-16: docs/skills/orenda/SKILL.md shipped
// without YAML frontmatter, and agent-skill discovery (opencode, claude,
// agents skills registries) silently skipped it — discovery keys on the
// `name` + `description` frontmatter pair. Every `orenda skill install`
// re-deployed an invisible skill.
func TestSkillDoc_HasAgentFrontmatter(t *testing.T) {
	// Test CWD is the package dir (cmd/orenda).
	raw, err := os.ReadFile("../../docs/skills/orenda/SKILL.md")
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	content := string(raw)
	if !strings.HasPrefix(content, "---\n") {
		t.Fatal("SKILL.md must start with a YAML frontmatter block (---)")
	}
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		t.Fatal("SKILL.md frontmatter block is not closed")
	}
	fm := content[4 : 4+end]
	if !strings.Contains(fm, "name: orenda") {
		t.Error("frontmatter missing `name: orenda`")
	}
	if !strings.Contains(fm, "description: ") {
		t.Error("frontmatter missing `description:` (used as the discovery trigger)")
	}
}
