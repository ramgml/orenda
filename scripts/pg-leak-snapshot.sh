#!/usr/bin/env bash
# pg-leak-snapshot.sh — point-in-time snapshot of Orenda-owned embedded
# postgres state, for leak audits (T370).
#
# Run it before/after `make test` (or an interrupted run) and diff the
# outputs: a leak shows up as a new [dir] line with state=dead-pid /
# state=no-pidfile, a new [process] line (live orphan), or a held port.
# Entries carrying a valid, live PREVIEW_OWNER marker are reported as
# state=preview and are NOT leaks — cleaners must leave them alone
# (docs/context/DOGFOOD.md, "QA-гейт перед submit").
#
# Deterministic stdout (sorted, no timestamps) so two runs diff cleanly;
# the wall-clock header goes to stderr.
#
# Scope: postmasters whose cmdline references Orenda paths
# (`orenda-pgtest-data-*` / `orenda-pg-runtime-*` temp dirs or a
# `<worktree>/data/postgres` PGDATA). Foreign postgres instances
# (dogfood on 5432, system clusters) are never matched.
set -euo pipefail

TMPD="${TMPDIR:-/tmp}"

echo "snapshot started" >&2

pid_alive() { [[ -n "${1:-}" ]] && kill -0 "$1" 2>/dev/null; }

# [process] <pid> <cmdline> — every live postmaster started from an
# Orenda path (pgtest temp dirs or a worktree data/postgres PGDATA).
while IFS= read -r line; do
  [[ -n "$line" ]] || continue
  pid="${line%% *}"
  echo "[process] $line"
done < <(pgrep -a -f 'orenda-pgtest-data|orenda-pg-runtime|/data/postgres' | sort -n)

# [port] <pid> <port> — listening socket per matched postmaster (ss).
while IFS= read -r pid; do
  [[ -n "$pid" ]] || continue
  port=$(ss -ltnpH 2>/dev/null | grep -o "pid=${pid}," | head -1 >/dev/null && \
    ss -ltnpH 2>/dev/null | grep "pid=${pid}," | awk '{print $4}' | awk -F: '{print $NF}' | sort -u | tr '\n' ',' | sed 's/,$//')
  [[ -n "$port" ]] && echo "[port] $pid $port"
done < <(pgrep -f 'orenda-pgtest-data|orenda-pg-runtime|/data/postgres' | sort -n)

# [dir] <path> <state> <detail> — leftover pgtest temp dirs.
#   state=live-pid   postmaster.pid names a live process (managed run or orphan)
#   state=dead-pid   postmaster.pid names a dead process — swept on next start
#   state=no-pidfile no pid file — either mid-start or crashed before start
#   state=preview    PREVIEW_OWNER marker present with a live pid (never touch)
#   state=runtime    runtime scratch dir (binaries; no pid file by design)
for d in "$TMPD"/orenda-pgtest-data-*; do
  [[ -e "$d" ]] || continue
  pf="$d/postmaster.pid"
  marker="$d/PREVIEW_OWNER"
  if [[ -f "$marker" ]]; then
    mpid=$(sed -n 's/^pid=//p' "$marker" | head -1)
    if pid_alive "$mpid"; then
      echo "[dir] $d preview pid=$mpid"
      continue
    fi
  fi
  if [[ -f "$pf" ]]; then
    ppid=$(sed -n '1p' "$pf" | tr -dc '0-9')
    if pid_alive "$ppid"; then echo "[dir] $d live-pid pid=$ppid"
    else echo "[dir] $d dead-pid pid=$ppid"; fi
  else
    echo "[dir] $d no-pidfile"
  fi
done | sort
for d in "$TMPD"/orenda-pg-runtime-*; do
  [[ -e "$d" ]] || continue
  echo "[dir] $d runtime"
done | sort

# [preview] <path> <pid> — PREVIEW_OWNER markers under known PGDATA roots.
for base in /work/projects/orenda /work/projects/orenda/.worktrees/*; do
  m="$base/data/postgres/PREVIEW_OWNER"
  [[ -f "$m" ]] || continue
  mpid=$(sed -n 's/^pid=//p' "$m" | head -1)
  if pid_alive "$mpid"; then echo "[preview] $m pid=$mpid LIVE"
  else echo "[preview] $m pid=${mpid:-?} DEAD"; fi
done | sort

echo "snapshot done" >&2
