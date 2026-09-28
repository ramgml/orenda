package pgtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramgml/orenda/internal/storage/postgres"
)

// Stale-cluster sweep for the pgtest temp prefixes (T370).
//
// A test binary killed between Start and Stop (harness cancel,
// SIGKILL) leaves its embedded cluster behind: the postmaster keeps
// running (or, on the graceful path, dies with the group and leaves an
// unwiped PGDATA), the temp data dir and the runtime scratch dir
// (~100 MB of extracted binaries) stay in os.TempDir forever. Every
// pgtest start sweeps its OWN prefix and reclaims what is provably
// dead:
//
//   - a valid, live PREVIEW_OWNER marker protects the dir — the
//     QA-preview convention (docs/context/DOGFOOD.md) applies inside
//     pgtest's temp prefix too; an invalid marker protects as well
//     (unknown ownership beats convenient cleanup);
//   - a live postmaster.pid protects the dir — it belongs to a
//     concurrently running binary, or it is an orphaned postmaster
//     nobody may auto-kill; the leak snapshot surfaces it and a
//     human/agent stops it deliberately;
//   - evidence of death (dead postmaster.pid, or a dead PGTEST_OWNER
//     owner pid) reclaims the dir and the runtime dir paired in the
//     marker;
//   - no evidence at all (no pid files, no markers) skips the dir —
//     it may be a concurrent binary's in-flight initdb.
//
// The sweep never crosses its prefix: anything outside
// orenda-pgtest-data-* / orenda-pg-runtime-* is invisible to it.

// pgtestOwnerFile marks a pgtest-provisioned cluster's PGDATA. Written
// right after the cluster starts (an empty pre-init PGDATA would make
// initdb refuse the directory). `pid` is the test binary's pid — the
// liveness anchor for the sweep; `runtime_path` pairs the scratch dir
// so a reclaimed cluster takes its binaries with it.
const pgtestOwnerFile = "PGTEST_OWNER"

const (
	dataDirPrefix  = "orenda-pgtest-data-"
	runtimePrefix  = "orenda-pg-runtime-"
	orphanStopHint = "stop it deliberately (verify: ps -p <pid>; kill <pid>), then the next start reclaims the dir"
)

// writeTestOwnerMarker drops the ownership marker into a freshly
// started cluster's PGDATA. Best-effort: the sweep falls back to the
// postmaster.pid evidence when the marker is missing.
func writeTestOwnerMarker(dataPath string, port int, runtimePath string) {
	marker := fmt.Sprintf("pid=%d\nport=%d\nowner=pgtest:%s\npurpose=go-test-accelerator\nruntime_path=%s\n",
		os.Getpid(), port, packageName(), runtimePath)
	if err := os.WriteFile(filepath.Join(dataPath, pgtestOwnerFile), []byte(marker), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: write %s: %v\n", pgtestOwnerFile, err)
	}
}

// sweepStaleTestClusters reclaims provably dead pgtest temp clusters.
// Called once per binary, before provisioning its own cluster; every
// decision is logged so the sweep is auditable in -v output.
func sweepStaleTestClusters(t testing.TB) {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), dataDirPrefix) {
			continue
		}
		dir := filepath.Join(os.TempDir(), e.Name())
		pairedRuntime, reason, ok := classifyStaleCluster(dir)
		if !ok {
			if reason != "" {
				t.Logf("pgtest: sweep keeps %s (%s)", dir, reason)
			}
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("pgtest: sweep wanted %s (%s) but remove failed: %v", dir, reason, err)
			continue
		}
		t.Logf("pgtest: swept stale cluster %s (%s)", dir, reason)
		if pairedRuntime != "" {
			if err := os.RemoveAll(pairedRuntime); err != nil {
				t.Logf("pgtest: sweep: paired runtime %s remove failed: %v", pairedRuntime, err)
			} else {
				t.Logf("pgtest: swept paired runtime %s", pairedRuntime)
			}
		}
	}
}

// classifyStaleCluster decides whether one orenda-pgtest-data-* dir is
// a dead run's leftover. ok=true means "reclaim"; ok=false with a
// non-empty reason means "protected — the reason says why" (logged so
// protected dirs are visible in the sweep output); ok=false with an
// empty reason means "no verdict — skip silently" (in-flight or
// no-evidence dirs).
func classifyStaleCluster(dir string) (pairedRuntime, reason string, ok bool) {
	// 1. PREVIEW_OWNER: the QA-preview convention outranks everything.
	owner, ms := postgres.ReadPreviewOwner(dir)
	switch ms {
	case postgres.MarkerValid:
		if postgres.PIDAlive(owner.PID) {
			return "", fmt.Sprintf("live PREVIEW_OWNER pid=%d (owner=%s purpose=%s) — protected",
				owner.PID, owner.Owner, owner.Purpose), false
		}
		// Dead preview: fall through to the pid-file evidence; the
		// dead marker alone justifies reclamation.
		return runtimeOfDeadCluster(dir), fmt.Sprintf("dead PREVIEW_OWNER pid=%d", owner.PID), true
	case postgres.MarkerInvalid:
		return "", "unparseable PREVIEW_OWNER — unknown ownership, protected", false
	}

	// 2. postmaster.pid: a live postmaster is never touched.
	state, pid := postgres.ReadPostmasterState(dir)
	if state == postgres.PostmasterLive {
		return "", fmt.Sprintf("live postmaster pid=%d — %s", pid, orphanStopHint), false
	}

	// 3. PGTEST_OWNER: a live owner pid means a concurrent binary is
	// starting or running this cluster.
	runtime, ownerPID, haveOwner := readTestOwnerMarker(dir)
	if haveOwner && postgres.PIDAlive(ownerPID) {
		return "", "", false
	}

	// 4. Death evidence only: a dead postmaster or a dead owner.
	if state == postgres.PostmasterDead {
		return runtime, fmt.Sprintf("dead postmaster pid=%d", pid), true
	}
	if haveOwner {
		return runtime, fmt.Sprintf("dead owner pid=%d", ownerPID), true
	}
	// No pid file, no markers: an in-flight start of an older binary
	// or an unidentifiable leftover — skip, disk loss beats corrupting
	// a concurrent start.
	return "", "", false
}

// readTestOwnerMarker parses the pgtest ownership marker: the owner
// pid and the paired runtime dir. runtime is non-empty only when it
// points inside os.TempDir with the pgtest runtime prefix.
func readTestOwnerMarker(dir string) (runtime string, ownerPID int, haveOwner bool) {
	raw, err := os.ReadFile(filepath.Join(dir, pgtestOwnerFile))
	if err != nil {
		return "", 0, false
	}
	tmp := os.TempDir()
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "pid":
			if n, err := fmt.Sscanf(value, "%d", &ownerPID); err != nil || n != 1 {
				return "", 0, false
			}
			haveOwner = true
		case "runtime_path":
			runtime = value
		}
	}
	if runtime != "" &&
		(!filepath.IsAbs(runtime) ||
			filepath.Dir(runtime) != tmp ||
			!strings.HasPrefix(filepath.Base(runtime), runtimePrefix)) {
		runtime = ""
	}
	return runtime, ownerPID, haveOwner
}

// runtimeOfDeadCluster is readTestOwnerMarker's runtime half for the
// dead-PREVIEW_OWNER path (the pgtest marker may coexist).
func runtimeOfDeadCluster(dir string) string {
	runtime, _, _ := readTestOwnerMarker(dir)
	return runtime
}
