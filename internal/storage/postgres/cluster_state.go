package postgres

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Cluster-state inspection for embedded PGDATA directories (T370).
//
// An embedded cluster is owned by a postmaster that OUTLIVES its
// parent: a serve/CLI/test process killed between StartEmbedded and
// Stop (SIGKILL, harness cancel) leaves the postmaster running and the
// PGDATA's postmaster.pid behind. The next start on the same PGDATA
// used to fail with an opaque library lock error — or, worse, an agent
// "cleaning up" killed a foreign live cluster (the T365 preview
// incident). These helpers classify the state so callers can act
// deliberately:
//
//   - PostmasterDead  → stale lock; postgres clears it on startup, so
//     StartEmbedded warns through its log sink and proceeds;
//   - PostmasterLive  → someone owns the cluster; StartEmbedded fails
//     loudly naming the pid and, when present, the PREVIEW_OWNER
//     marker identity — killing an orphan stays a human/agent
//     decision, never an automatic one;
//   - the PREVIEW_OWNER marker (QA-preview convention,
//     docs/context/DOGFOOD.md) identifies the owning instance and is
//     honoured by every cleaner.

// PreviewOwnerFile is the marker file a QA preview (or any long-lived
// embedded cluster on a shared host) drops into its PGDATA. Format:
// KEY=VALUE lines; `pid` (the owning serve process, not the
// postmaster) is required and is what cleaners liveness-check; `port`,
// `owner` and `purpose` are informational.
const PreviewOwnerFile = "PREVIEW_OWNER"

// MarkerState is the outcome of reading a PREVIEW_OWNER marker.
type MarkerState int

const (
	// MarkerAbsent — no marker file in the PGDATA.
	MarkerAbsent MarkerState = iota
	// MarkerValid — present and parsed (PID ≥ 1; port, when present,
	// numeric).
	MarkerValid
	// MarkerInvalid — present but unparseable: ownership is unknown,
	// so cleaners must treat the PGDATA as protected.
	MarkerInvalid
)

// PreviewOwner is the parsed PREVIEW_OWNER marker content.
type PreviewOwner struct {
	PID     int
	Port    int
	Owner   string
	Purpose string
}

// ReadPreviewOwner parses the PGDATA's PREVIEW_OWNER marker. An
// invalid marker returns MarkerInvalid and must never be cleaned up by
// automated tooling — unknown ownership beats convenient cleanup.
func ReadPreviewOwner(dataPath string) (PreviewOwner, MarkerState) {
	var owner PreviewOwner
	raw, err := os.ReadFile(filepath.Join(dataPath, PreviewOwnerFile))
	if err != nil {
		return owner, MarkerAbsent
	}
	havePID := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return owner, MarkerInvalid
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "pid":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return owner, MarkerInvalid
			}
			owner.PID, havePID = n, true
		case "port":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return owner, MarkerInvalid
			}
			owner.Port = n
		case "owner":
			owner.Owner = value
		case "purpose":
			owner.Purpose = value
		}
	}
	if !havePID {
		return owner, MarkerInvalid
	}
	return owner, MarkerValid
}

// PostmasterState classifies the PGDATA's postmaster.pid.
type PostmasterState int

const (
	// PostmasterAbsent — no pid file: fresh or fully-cleaned PGDATA.
	PostmasterAbsent PostmasterState = iota
	// PostmasterUnreadable — pid file exists but has no parseable pid.
	PostmasterUnreadable
	// PostmasterDead — pid file names a process that is gone: a
	// previous run crashed after the postmaster died with it. Postgres
	// clears the stale lock itself on the next start.
	PostmasterDead
	// PostmasterLive — pid file names a running process: the cluster
	// is in use (managed run, live preview or orphaned postmaster).
	PostmasterLive
)

// ReadPostmasterState reads the PGDATA's postmaster.pid (first line is
// the postmaster pid) and reports whether that process is alive.
func ReadPostmasterState(dataPath string) (state PostmasterState, pid int) {
	raw, err := os.ReadFile(filepath.Join(dataPath, "postmaster.pid"))
	if err != nil {
		return PostmasterAbsent, 0
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	parsed, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || parsed < 1 {
		return PostmasterUnreadable, 0
	}
	pid = parsed
	if PIDAlive(pid) {
		return PostmasterLive, pid
	}
	return PostmasterDead, pid
}

// PIDAlive reports whether a process with this pid exists. A pid we
// cannot signal for permission reasons (EPERM — foreign user) counts
// as alive: cleanup decisions must err on the side of not touching.
func PIDAlive(pid int) bool {
	if pid < 1 {
		return false
	}
	err := syscall.Kill(pid, 0) // signal 0 = existence probe, no signal sent
	return err == nil || err == syscall.EPERM
}

// checkExistingCluster inspects an existing PGDATA before the start
// attempt and turns the incident-class failure modes (T370) into
// actionable outcomes:
//
//   - a stale postmaster.pid (dead pid — the parent process was killed
//     and the postmaster died with or after it) no longer surfaces as
//     an opaque lock error: postgres clears the stale lock itself, so
//     the start proceeds after a warning through the log sink;
//   - a live postmaster fails the start loudly, naming the owning pid
//     and the PREVIEW_OWNER identity when present. Nothing is killed
//     automatically: whether a live cluster is a preview to protect or
//     an orphan to stop is an operator/agent decision (T365 incident).
func checkExistingCluster(opts EmbeddedOptions) error {
	state, pid := ReadPostmasterState(opts.DataPath)
	switch state {
	case PostmasterLive:
		marker := ""
		if owner, ms := ReadPreviewOwner(opts.DataPath); ms == MarkerValid {
			marker = fmt.Sprintf("; PREVIEW_OWNER: owner=%s purpose=%s", owner.Owner, owner.Purpose)
		} else if ms == MarkerInvalid {
			marker = "; PREVIEW_OWNER marker present but unparseable"
		}
		return fmt.Errorf(
			"postgres: embedded cluster at %s is already in use: live postmaster pid %d%s — "+
				"stop it deliberately (verify first: ps -p %d; then kill %d for a graceful "+
				"shutdown) or point the config elsewhere; QA previews must carry a "+
				"PREVIEW_OWNER marker in PGDATA (docs/context/DOGFOOD.md, QA-gate section)",
			opts.DataPath, pid, marker, pid, pid)
	case PostmasterDead:
		// Best-effort warning: the log sink may be a discard writer in
		// tests; the stale lock itself is handled by postgres.
		_, _ = fmt.Fprintf(opts.Logs,
			"postgres: stale postmaster.pid in %s (pid %d is not running) — a previous "+
				"run was killed; continuing, postgres clears the stale lock on startup\n",
			opts.DataPath, pid)
		return nil
	default:
		// Absent: fresh or clean. Unreadable: let postgres surface its
		// own precise error about the corrupt lock file.
		return nil
	}
}
