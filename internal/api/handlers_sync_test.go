package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// failingSyncOps implements SyncOpsStore and always returns an error
// from Record. Used to drive the counter / log path of syncOpsRecord.
type failingSyncOps struct{}

func (failingSyncOps) Seen(_ context.Context, _ string) (bool, string, error) {
	return false, "", nil
}

func (failingSyncOps) Record(_ context.Context, _, _ string) error {
	return errors.New("simulated record failure")
}

// okSyncOps implements SyncOpsStore and never returns an error.
type okSyncOps struct{}

func (okSyncOps) Seen(_ context.Context, _ string) (bool, string, error) {
	return false, "", nil
}

func (okSyncOps) Record(_ context.Context, _, _ string) error {
	return nil
}

// syncOpsRecordFailureSnapshot captures the counter before a test so
// we can assert delta (not absolute value — other tests in the same
// process may have bumped it).
func syncOpsRecordFailureSnapshot() uint64 {
	return liveStats.syncOpsRecordFailures.Load()
}

// TestSyncOpsRecordFailsAndCounts pins the contract that a failing
// Record bumps the counter and emits a Warn log. Regression guard
// for Phase 30.2 — the previous behaviour was `_ = syncOpsRecord(...)`
// which silently swallowed the error.
func TestSyncOpsRecordFailsAndCounts(t *testing.T) {
	core, observed := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	before := syncOpsRecordFailureSnapshot()

	deps := &Dependencies{
		Logger:  logger,
		SyncOps: failingSyncOps{},
	}
	err := syncOpsRecord(context.Background(), deps, "client-abc", "server-xyz")

	assert.Error(t, err, "syncOpsRecord must return the underlying error")
	after := syncOpsRecordFailureSnapshot()
	assert.Equal(t, before+1, after, "counter must bump on Record failure")

	// One Warn must have been emitted with the client_id and server_id.
	entries := observed.FilterMessage("sync_ops record failed; client may replay this op").All()
	assert.Len(t, entries, 1, "exactly one Warn per failed Record")
	if len(entries) == 1 {
		fields := entries[0].ContextMap()
		assert.Equal(t, "client-abc", fields["client_id"])
		assert.Equal(t, "server-xyz", fields["server_id"])
	}
}

// TestSyncOpsRecordSuccess_NoCounterOrLog pins the inverse: a
// successful Record does NOT bump the counter and does NOT emit a log.
func TestSyncOpsRecordSuccess_NoCounterOrLog(t *testing.T) {
	core, observed := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	before := syncOpsRecordFailureSnapshot()

	deps := &Dependencies{
		Logger:  logger,
		SyncOps: okSyncOps{},
	}
	err := syncOpsRecord(context.Background(), deps, "client-ok", "server-ok")

	assert.NoError(t, err)
	after := syncOpsRecordFailureSnapshot()
	assert.Equal(t, before, after, "counter must NOT bump on Record success")
	entries := observed.FilterMessage("sync_ops record failed; client may replay this op").All()
	assert.Empty(t, entries, "no log on success")
}

// TestSyncOpsRecordNilStore_NoOp pins the nil-guard: when SyncOps is
// not wired (deps.SyncOps == nil) the call is a no-op with no error
// and no counter movement.
func TestSyncOpsRecordNilStore_NoOp(t *testing.T) {
	before := syncOpsRecordFailureSnapshot()

	deps := &Dependencies{
		Logger:  zap.NewNop(),
		SyncOps: nil,
	}
	err := syncOpsRecord(context.Background(), deps, "client-x", "server-x")

	assert.NoError(t, err)
	after := syncOpsRecordFailureSnapshot()
	assert.Equal(t, before, after)
}

// TestStatsExposesSyncOpsRecordFailuresField pins the wire shape so
// the operator can scrape the counter from /api/v1/stats.
func TestStatsExposesSyncOpsRecordFailuresField(t *testing.T) {
	// Snapshot before; bump by one; assert delta is in the response.
	before := liveStats.syncOpsRecordFailures.Load()
	liveStats.syncOpsRecordFailures.Add(1)
	defer liveStats.syncOpsRecordFailures.Store(before) // restore for other tests

	resp := statsResponse{
		SyncOpsRecordFailures: liveStats.syncOpsRecordFailures.Load(),
	}
	assert.Equal(t, before+1, resp.SyncOpsRecordFailures,
		"statsResponse.SyncOpsRecordFailures must read the counter")

	// JSON tag parity — dashboards / monitoring read the field by name.
	body, _ := json.Marshal(resp)
	assert.Contains(t, string(body), `"sync_ops_record_failures"`)
}
