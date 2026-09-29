package postgres

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deadPID returns a pid of an already-exited process — the reliable
// "dead" anchor for the stale-detect tests.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	return pid
}

func writeMarker(t *testing.T, dataPath, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dataPath, name), []byte(content), 0o644))
}

func writePostmasterPid(t *testing.T, dataPath string, pid int) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dataPath, "postmaster.pid"),
		[]byte(fmt.Sprintf("%d\n%s\n12345\n5433\n", pid, dataPath)), 0o644))
}

// TestPIDAlive pins the existence probe: live pid true, never-started
// pids false.
func TestPIDAlive(t *testing.T) {
	assert.True(t, PIDAlive(os.Getpid()), "the test process itself is alive")
	assert.False(t, PIDAlive(deadPID(t)), "an exited pid is dead")
	assert.False(t, PIDAlive(0), "pid 0 is not a process")
	assert.False(t, PIDAlive(-5), "negative pids are not processes")
}

// TestReadPostmasterState covers the postmaster.pid classification
// used by the stale-detect: absent, unreadable, dead, live.
func TestReadPostmasterState(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		state, pid := ReadPostmasterState(t.TempDir())
		assert.Equal(t, PostmasterAbsent, state)
		assert.Zero(t, pid)
	})
	t.Run("unreadable", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(t, dir, "postmaster.pid", "not a pid\n")
		state, pid := ReadPostmasterState(dir)
		assert.Equal(t, PostmasterUnreadable, state)
		assert.Zero(t, pid)
	})
	t.Run("dead", func(t *testing.T) {
		dir := t.TempDir()
		writePostmasterPid(t, dir, deadPID(t))
		state, pid := ReadPostmasterState(dir)
		assert.Equal(t, PostmasterDead, state)
		assert.Positive(t, pid)
	})
	t.Run("live", func(t *testing.T) {
		dir := t.TempDir()
		writePostmasterPid(t, dir, os.Getpid())
		state, pid := ReadPostmasterState(dir)
		assert.Equal(t, PostmasterLive, state)
		assert.Equal(t, os.Getpid(), pid)
	})
}

// TestReadPreviewOwner covers the QA-preview marker parsing: the
// required pid, the optional keys, and everything that makes a marker
// invalid (unknown ownership must read as protected, never cleanable).
func TestReadPreviewOwner(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		owner, ms := ReadPreviewOwner(t.TempDir())
		assert.Equal(t, MarkerAbsent, ms)
		assert.Zero(t, owner)
	})
	t.Run("valid full", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(t, dir, PreviewOwnerFile, "pid=4242\nport=5433\nowner=pm-review\npurpose=review-T365\n")
		owner, ms := ReadPreviewOwner(dir)
		assert.Equal(t, MarkerValid, ms)
		assert.Equal(t, PreviewOwner{PID: 4242, Port: 5433, Owner: "pm-review", Purpose: "review-T365"}, owner)
	})
	t.Run("valid minimal", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(t, dir, PreviewOwnerFile, "pid=7\n")
		owner, ms := ReadPreviewOwner(dir)
		assert.Equal(t, MarkerValid, ms)
		assert.Equal(t, 7, owner.PID)
	})
	t.Run("comments and blank lines are skipped", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(t, dir, PreviewOwnerFile, "# dropped by pm\n\npid=9\n")
		_, ms := ReadPreviewOwner(dir)
		assert.Equal(t, MarkerValid, ms)
	})
	t.Run("invalid missing pid", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(t, dir, PreviewOwnerFile, "port=5433\nowner=x\n")
		_, ms := ReadPreviewOwner(dir)
		assert.Equal(t, MarkerInvalid, ms)
	})
	t.Run("invalid pid garbage", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(t, dir, PreviewOwnerFile, "pid=soon\n")
		_, ms := ReadPreviewOwner(dir)
		assert.Equal(t, MarkerInvalid, ms)
	})
	t.Run("invalid pid zero", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(t, dir, PreviewOwnerFile, "pid=0\n")
		_, ms := ReadPreviewOwner(dir)
		assert.Equal(t, MarkerInvalid, ms)
	})
	t.Run("invalid port garbage", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(t, dir, PreviewOwnerFile, "pid=7\nport=fast\n")
		_, ms := ReadPreviewOwner(dir)
		assert.Equal(t, MarkerInvalid, ms)
	})
	t.Run("invalid stray line", func(t *testing.T) {
		dir := t.TempDir()
		writeMarker(t, dir, PreviewOwnerFile, "pid=7\nwhatever\n")
		_, ms := ReadPreviewOwner(dir)
		assert.Equal(t, MarkerInvalid, ms)
	})
}

// dialClusterOK asserts the cluster described by opts accepts queries —
// the "untouched" half of the refused-start tests.
func dialClusterOK(t *testing.T, opts EmbeddedOptions) {
	t.Helper()
	dsn, err := ResolveDSN(opts.Connection())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(ctx))
}

// gated require: the real-postmaster tests reuse the lifecycle smoke's
// gate — they need the (cached) zonky binaries.
func requireEmbeddedGate(t *testing.T) {
	t.Helper()
	if os.Getenv("ORENDA_TEST_EMBEDDED_PG") != "1" {
		t.Skip("integration smoke: set ORENDA_TEST_EMBEDDED_PG=1 to run against a real postmaster")
	}
}

// TestStartEmbedded_LiveClusterRefused: a second start on a live
// cluster's PGDATA fails loudly naming the pid, and the running
// cluster is untouched (still dials, then stops cleanly).
func TestStartEmbedded_LiveClusterRefused(t *testing.T) {
	requireEmbeddedGate(t)

	opts, err := BuildEmbeddedOptions("orenda_t370_live", freeTestPort(t), "", "")
	require.NoError(t, err)
	opts.DataPath = filepath.Join(t.TempDir(), "postgres")
	scratch, err := ScratchRuntimePath()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })
	opts.RuntimePath = scratch
	opts.Logs = testLogWriter{t: t}

	c := startCluster(t, opts)
	state, pid := ReadPostmasterState(opts.DataPath)
	require.Equal(t, PostmasterLive, state)

	_, err = StartEmbedded(opts)
	require.Error(t, err, "a live cluster must refuse the second start")
	assert.Contains(t, err.Error(), "already in use")
	assert.Contains(t, err.Error(), strconv.Itoa(pid), "the error must name the owning pid")
	assert.NotContains(t, err.Error(), "PREVIEW_OWNER:", "no marker, no marker section")

	// The first cluster must be unharmed.
	dialClusterOK(t, opts)
	require.NoError(t, c.Stop())
	requireNoPostmaster(t, opts.DataPath)
}

// TestStartEmbedded_LivePreviewProtected: a valid PREVIEW_OWNER marker
// with a live pid is reported in the refusal — the cleaner never
// guesses (T365 preview incident).
func TestStartEmbedded_LivePreviewProtected(t *testing.T) {
	requireEmbeddedGate(t)

	opts, err := BuildEmbeddedOptions("orenda_t370_preview", freeTestPort(t), "", "")
	require.NoError(t, err)
	opts.DataPath = filepath.Join(t.TempDir(), "postgres")
	scratch, err := ScratchRuntimePath()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })
	opts.RuntimePath = scratch
	opts.Logs = testLogWriter{t: t}

	c := startCluster(t, opts)
	writeMarker(t, opts.DataPath, PreviewOwnerFile,
		fmt.Sprintf("pid=%d\nport=%d\nowner=pm-review\npurpose=review-T365\n", os.Getpid(), opts.Port))

	_, err = StartEmbedded(opts)
	require.Error(t, err, "a marked live preview must refuse the start")
	assert.Contains(t, err.Error(), "already in use")
	assert.Contains(t, err.Error(), "PREVIEW_OWNER")
	assert.Contains(t, err.Error(), "owner=pm-review")

	dialClusterOK(t, opts)
	require.NoError(t, c.Stop())
	requireNoPostmaster(t, opts.DataPath)
}

// TestStartEmbedded_StalePidRecovers: the incident's restart failure —
// the parent was SIGKILLed, the pid file names a dead process — now
// warns through the log sink and starts cleanly instead of dying on
// the lock. This is the "мёртвый — убран/предупреждён" acceptance.
func TestStartEmbedded_StalePidRecovers(t *testing.T) {
	requireEmbeddedGate(t)

	port := freeTestPort(t)
	opts, err := BuildEmbeddedOptions("orenda_t370_stale", port, "", "")
	require.NoError(t, err)
	opts.DataPath = filepath.Join(t.TempDir(), "postgres")
	scratch, err := ScratchRuntimePath()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })
	opts.RuntimePath = scratch
	opts.Logs = testLogWriter{t: t}

	startCluster(t, opts)
	state, pid := ReadPostmasterState(opts.DataPath)
	require.Equal(t, PostmasterLive, state)

	// Simulate the incident: the serve process is SIGKILLed; the
	// postmaster here is killed the same way — instantly, leaving the
	// pid file behind.
	require.NoError(t, syscall.Kill(pid, syscall.SIGKILL))
	deadline := time.Now().Add(30 * time.Second)
	for {
		state, _ = ReadPostmasterState(opts.DataPath)
		if state == PostmasterDead {
			break
		}
		require.Less(t, time.Now(), deadline, "postmaster did not die after SIGKILL")
		time.Sleep(100 * time.Millisecond)
	}

	var logs bytes.Buffer
	opts.Logs = &logs
	c2, err := StartEmbedded(opts)
	require.NoError(t, err, "a stale pid file must not wedge the next start")
	assert.Contains(t, logs.String(), "stale postmaster.pid", "the crash must be warned about, not silent")
	assert.Contains(t, logs.String(), strconv.Itoa(pid))

	dialClusterOK(t, opts)
	require.NoError(t, c2.Stop())
	requireNoPostmaster(t, opts.DataPath)
}
