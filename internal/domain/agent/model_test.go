package agent_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/agent"
)

// Phase 28.19: Validate no longer defaults the Type label set to
// "custom". An agent may exist with an empty Type — that is a valid
// state and serialises as "[]" in JSON. The defaults for MaxConcurrent
// and Status are unchanged.
func TestAgent_Validate_AppliesDefaultsExceptLabels(t *testing.T) {
	a := &agent.Agent{Name: "qwen-alpha"}
	require.NoError(t, a.Validate())
	assert.Equal(t, []string{}, a.Type, "empty Type must stay empty, not fall back to a default label")
	assert.Equal(t, 3, a.MaxConcurrent)
	assert.Equal(t, agent.StatusOffline, a.Status)
}

func TestAgent_Validate_RejectsEmptyName(t *testing.T) {
	a := &agent.Agent{}
	require.Error(t, a.Validate())
	assert.ErrorIs(t, a.Validate(), agent.ErrInvalidInput)
}

func TestAgent_NormalizeLabels(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil stays empty", nil, []string{}},
		{"empty stays empty", []string{}, []string{}},
		{"trim + lowercase", []string{" Qwen ", "claude"}, []string{"claude", "qwen"}},
		{"dedupe exact", []string{"qwen", "qwen"}, []string{"qwen"}},
		{"dedupe after lower", []string{"Qwen", "qwen"}, []string{"qwen"}},
		{"drop empties", []string{"", "  ", "installer"}, []string{"installer"}},
		{"full sort + dedupe + trim", []string{"z", "A", "a", " b ", "B"}, []string{"a", "b", "z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := agent.NormalizeLabels(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Phase 28.19: Validate normalises in place — the slice header is
// replaced so callers that hold onto the original []string see the
// pre-normalised value, while the Agent carries the canonical set.
func TestAgent_Validate_NormalisesTypeInPlace(t *testing.T) {
	in := []string{"  Qwen ", "claude", "Qwen"}
	a := &agent.Agent{Name: "x", Type: in}
	require.NoError(t, a.Validate())
	assert.Equal(t, []string{"claude", "qwen"}, a.Type)
	// Original slice is untouched (we never mutate caller memory).
	assert.Equal(t, []string{"  Qwen ", "claude", "Qwen"}, in)
}

func TestAgent_IsAlive(t *testing.T) {
	a := &agent.Agent{}
	now := time.Now()

	// No last_seen → not alive.
	assert.False(t, a.IsAlive(time.Minute))

	// Last seen 10s ago, TTL 1m → alive.
	a.LastSeenAt = ptrTime(now.Add(-10 * time.Second))
	assert.True(t, a.IsAlive(time.Minute))

	// Last seen 5m ago, TTL 1m → not alive.
	a.LastSeenAt = ptrTime(now.Add(-5 * time.Minute))
	assert.False(t, a.IsAlive(time.Minute))
}

func ptrTime(t time.Time) *time.Time { return &t }
