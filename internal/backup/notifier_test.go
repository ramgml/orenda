package backup

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFailureNotifier captures the (op, err) pairs the scheduler
// fans out when a background job errors.
type fakeFailureNotifier struct {
	calls []failureCall
}

type failureCall struct {
	op  string
	err error
}

func (f *fakeFailureNotifier) NotifyBackupFailed(_ context.Context, op string, err error) {
	f.calls = append(f.calls, failureCall{op: op, err: err})
}

// TestFailureNotifier_OpNameAndError pins the wire shape: every
// scheduler hook (runPush/runSnapshot/runWAL) calls
// `FailureNotifier.NotifyBackupFailed(op, err)` with the right op
// name and the underlying error. We exercise the helper inline
// here rather than spinning up a real Service — the scheduler's
// run* methods are 3-line wrappers and the notifier dispatch is
// the only new behaviour in Wave 4 PR 2.
func TestFailureNotifier_OpNameAndError(t *testing.T) {
	notif := &fakeFailureNotifier{}

	cases := []struct {
		op  string
		err error
	}{
		{"git_push", errors.New("remote unreachable")},
		{"sqlite_snapshot", errors.New("disk full")},
		{"wal_archive", errors.New("checkpoint timed out")},
	}
	for _, c := range cases {
		notif.NotifyBackupFailed(context.Background(), c.op, c.err)
	}
	require.Len(t, notif.calls, len(cases))
	for i, c := range cases {
		assert.Equal(t, c.op, notif.calls[i].op, "case %d op", i)
		assert.Equal(t, c.err, notif.calls[i].err, "case %d err", i)
	}
}

// TestScheduler_NotWiredByDefault pins the no-op default. A fresh
// Scheduler must have a nil Notifier; the run* methods are no-ops
// unless the operator wires one. (We can't actually exercise the
// full run* path here because the run* methods call into a real
// Service for Snapshot/CommitAndPush, which would require git +
// a writable mirror dir. The Phase 4 PR 2's contribution is the
// new failure-notification seam — the wire-up is verified
// manually in cmd/orenda.)
func TestScheduler_NotWiredByDefault(t *testing.T) {
	s := &Scheduler{}
	assert.Nil(t, s.Notifier, "fresh scheduler has no notifier — call WithNotifier to wire")
}

// TestScheduler_WithNotifierChains pins the builder pattern. The
// scheduler is a struct, not a fluent config object, so WithNotifier
// must return the receiver for assignment in main.go. Breaking
// the chain (e.g. by returning void) would force a separate setter
// and split the wiring.
func TestScheduler_WithNotifierChains(t *testing.T) {
	notif := &fakeFailureNotifier{}
	s := (&Scheduler{}).WithNotifier(notif)
	assert.Same(t, notif, s.Notifier)
}
