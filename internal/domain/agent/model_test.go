package agent_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/agent"
)

func TestAgent_Validate_Defaults(t *testing.T) {
	a := &agent.Agent{Name: "qwen-alpha"}
	require.NoError(t, a.Validate())
	assert.Equal(t, agent.TypeCustom, a.Type)
	assert.Equal(t, 3, a.MaxConcurrent)
	assert.Equal(t, agent.StatusOffline, a.Status)
}

func TestAgent_Validate_RejectsEmptyName(t *testing.T) {
	a := &agent.Agent{}
	require.Error(t, a.Validate())
	assert.ErrorIs(t, a.Validate(), agent.ErrInvalidInput)
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
