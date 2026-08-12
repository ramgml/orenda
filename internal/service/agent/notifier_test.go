package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/agent"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
)

// fakeNotifier records events the agent service fans out. Mirrors
// the real `*notifier.Service.Notify` contract.
type fakeNotifier struct {
	events []notifierservice.Event
}

func (f *fakeNotifier) Notify(ctx context.Context, e notifierservice.Event) error {
	f.events = append(f.events, e)
	return nil
}

// notifService shim — Service wants a typed `NotifierEmitter` so we
// build the local type the Service expects. fakeNotifier satisfies
// it (Notify signature matches NotifierEmitter).
type notifService = fakeNotifier

// stubRepo is a minimal Repository for the SweepOffline test path.
// We only need ListStaleOnlineAgents and SweepOffline. The
// ListStaleOnlineAgents is the one that needs to return a list.
type stubRepo struct {
	list     func(context.Context, time.Duration) ([]*agent.Agent, error)
	swept    int64
	sweepErr error
}

func (s *stubRepo) ListStaleOnlineAgents(ctx context.Context, ttl time.Duration) ([]*agent.Agent, error) {
	if s.list != nil {
		return s.list(ctx, ttl)
	}
	return nil, nil
}
func (s *stubRepo) SweepOffline(ctx context.Context, ttl time.Duration) (int64, error) {
	return s.swept, s.sweepErr
}

// We also need all the other Repository methods because Service is
// a real struct, not an interface. Stub them out with the minimum
// that compiles.
func (s *stubRepo) Create(_ context.Context, _ *agent.Agent) error {
	return nil
}
func (s *stubRepo) GetByID(_ context.Context, _ string) (*agent.Agent, error) {
	return nil, nil
}
func (s *stubRepo) GetByName(_ context.Context, _ string) (*agent.Agent, error) {
	return nil, nil
}
func (s *stubRepo) GetByTokenID(_ context.Context, _ string) (*agent.Agent, error) {
	return nil, nil
}
func (s *stubRepo) List(_ context.Context) ([]*agent.Agent, error) {
	return nil, nil
}
func (s *stubRepo) ListAll(_ context.Context) ([]*agent.Agent, error) {
	return nil, nil
}
func (s *stubRepo) TouchLastSeen(_ context.Context, _ string) (*agent.Agent, error) {
	return nil, nil
}
func (s *stubRepo) Update(_ context.Context, _ *agent.Agent) error {
	return nil
}
func (s *stubRepo) Delete(_ context.Context, _ string) error {
	return nil
}

func TestSweepOffline_EmitsPerAgent(t *testing.T) {
	stale := []*agent.Agent{
		{ID: "ag-1", Name: "qa-bot", Status: agent.StatusOnline},
		{ID: "ag-2", Name: "build-bot", Status: agent.StatusOnline},
	}
	repo := &stubRepo{
		list: func(_ context.Context, _ time.Duration) ([]*agent.Agent, error) {
			return stale, nil
		},
		swept: 2,
	}
	notif := &fakeNotifier{}

	svc := &Service{Agents: repo, Notifier: notif, SweepTTL: 2 * time.Minute}
	_, err := svc.SweepOffline(context.Background())
	require.NoError(t, err)

	require.Len(t, notif.events, 2, "every stale agent should produce one event")
	for i, e := range notif.events {
		assert.Equal(t, "agent.offline", e.Type, "event %d type", i)
		assert.Equal(t, "agent", e.TargetType)
		assert.Equal(t, stale[i].ID, e.TargetID)
		assert.Contains(t, e.Title, stale[i].Name)
		assert.NotEmpty(t, e.DedupKey)
	}
}

func TestSweepOffline_NoStaleNoEvents(t *testing.T) {
	repo := &stubRepo{
		list:  func(_ context.Context, _ time.Duration) ([]*agent.Agent, error) { return nil, nil },
		swept: 0,
	}
	notif := &fakeNotifier{}

	svc := &Service{Agents: repo, Notifier: notif, SweepTTL: 2 * time.Minute}
	_, err := svc.SweepOffline(context.Background())
	require.NoError(t, err)
	assert.Empty(t, notif.events, "no stale agents → no events")
}

func TestSweepOffline_NilNotifierSafe(t *testing.T) {
	// Notifier: nil — the service must not panic. This is the path
	// single-user installs that don't wire notifications take.
	repo := &stubRepo{
		list: func(_ context.Context, _ time.Duration) ([]*agent.Agent, error) {
			return []*agent.Agent{{ID: "ag-1", Name: "x", Status: agent.StatusOnline}}, nil
		},
		swept: 1,
	}
	svc := &Service{Agents: repo, Notifier: nil, SweepTTL: 2 * time.Minute}
	_, err := svc.SweepOffline(context.Background())
	require.NoError(t, err)
	// No panic = test passes. We don't assert anything else; the
	// contract is "don't crash, don't block".
}

func TestNotifEventFor_Shape(t *testing.T) {
	// notifierEventFor is unexported but lives in the same package,
	// so we can test it directly. Pins the wire shape so a
	// downstream notifier template change doesn't silently break.
	a := &agent.Agent{ID: "ag-1", Name: "qa-bot"}
	e := notifierEventFor(a)
	assert.Equal(t, "agent.offline", e.Type)
	assert.Equal(t, "agent", e.TargetType)
	assert.Equal(t, "ag-1", e.TargetID)
	assert.Equal(t, "qa-bot went offline", e.Title)
	assert.Equal(t, "agent.offline:ag-1", e.DedupKey)
	assert.NotEmpty(t, e.Body)
}

// Compile-time check: errors.Is works on the package sentinel.
var _ = errors.Is(ErrNotFound, ErrNotFound)
