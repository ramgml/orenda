#!/usr/bin/env bash
# plan-unblocked.sh — watchdog check for the docs/PLAN.md task registry.
#
# Computes the set of claimable tasks and fires (exit 0, prints NEW ids to
# stdout) when that set grows — either because a claimed `[~]` predecessor
# was merged (its "close X.Y" / "Merge phase-X-Y-*" commit reached the
# watched ref) or because new free `[ ]` tasks were added to the plan.
#
# Claimable definition (matches the claim protocol in PLAN.md):
#   - the task line is `- [ ] **X.Y** ...`
#   - no `[~]` task with the same phase number X and a smaller Y remains open
#     (a `[~]` counts as closed once the watched git ref mentions "close X.Y"
#     or merges its `phase-X-Y-*` branch)
#
# Watched ref: PLAN_WATCH_REF (default `dev` — local branch, NOT origin/dev:
# claim/close commits are merged locally and intentionally not pushed, so the
# remote ref lags behind and would report false blocks).
#
# Watched phases (executable backlog) are detected automatically:
#   - any phase with `docs(plan): claim N.` / `docs(plan): close N.` commits
#     in the watched ref's history — the registry claim protocol (introduced
#     with the Phase 30 dispatch contract) is the only reliable marker of an
#     executable phase; plain `Merge phase-N-*` commits and `[x]` markers exist
#     for every shipped phase ever, so they do NOT qualify, or
#   - the highest-numbered phase in the plan (a brand-new, all-`[ ]` phase —
#     phases are numbered monotonically, so the newest registry is always max).
# Everything else (historical `[ ]` markers in shipped phases) is excluded:
# per PLAN.md those are historical records, not open tasks.
# PLAN_WATCH_PHASES (space-separated, e.g. "30 31") overrides auto-detection.
#
# The plan is read from the watched ref (`git show $PLAN_WATCH_REF:docs/PLAN.md`),
# not from the worktree on disk — the plugin may run from any checkout, and the
# ref is the same source of truth the close-trail grep uses. PLAN_FILE overrides
# this for tests.
#
# State: .opencode/watchdog/state/plan-claimable.txt (gitignored).
#
# Exit codes: 0 = new claimable tasks appeared (ids on stdout);
#             1 = nothing new; 2 = operational error.

set -euo pipefail

REPO_ROOT="${REPO_ROOT:-$(git rev-parse --show-toplevel)}"
WATCH_REF="${PLAN_WATCH_REF:-dev}"
WATCH_PHASES="${PLAN_WATCH_PHASES:-}"   # optional override; empty = auto-detect
PLAN_FILE="${PLAN_FILE:-}"              # test override; empty = read from $WATCH_REF
STATE_DIR="${WATCHDOG_STATE_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../state}"
STATE_FILE="$STATE_DIR/plan-claimable.txt"
mkdir -p "$STATE_DIR"
touch "$STATE_FILE"

cd "$REPO_ROOT"
git fetch origin -q 2>/dev/null || true

if [ -z "$PLAN_FILE" ]; then
  PLAN_FILE="$STATE_DIR/.plan-snapshot.md"
  git show "$WATCH_REF:docs/PLAN.md" > "$PLAN_FILE" || {
    echo "plan-unblocked: cannot read docs/PLAN.md from ref $WATCH_REF" >&2
    exit 2
  }
fi

current=$(WATCH_PHASES="$WATCH_PHASES" WATCH_REF="$WATCH_REF" perl - "$PLAN_FILE" "$REPO_ROOT" <<'PERL'
use strict;
use warnings;

my ( $plan, $repo ) = @ARGV;
open my $fh, '<', $plan or exit 2;
my @entries;    # [id, mark, major, minor]
my $max_phase = -1;
while (<$fh>) {
    next unless /^- \[([ x~])\] \*\*(\d+)\.(\d+)\*\*/;
    my ( $mark, $maj, $min ) = ( $1, $2, $3 );
    push @entries, [ "$maj.$min", $mark, $maj, $min ];
    $max_phase = $maj if $maj > $max_phase;
}
close $fh;

my $ref = $ENV{WATCH_REF};
my $log = `git -C "$repo" log --oneline "$ref" 2>/dev/null`;

my %watched;
if ( $ENV{WATCH_PHASES} =~ /\S/ ) {
    %watched = map { $_ => 1 } split /\s+/, $ENV{WATCH_PHASES};
}
else {
    # Phases with registry claim-protocol commits ("docs(plan): claim N." /
    # "docs(plan): close N.") — the claim protocol is the marker of an
    # executable registry phase. Plain merges/[x] markers exist for every
    # shipped phase ever and must not qualify.
    $watched{$1} = 1 while $log =~ /docs\(plan\): (?:claim|close) (\d+)\./g;
    # The newest phase (brand-new registries start all-`[ ]` with no trail).
    $watched{$max_phase} = 1 if $max_phase >= 0;
}

@entries = grep { $watched{ $_->[2] } } @entries;

for my $e (@entries) {
    my ( $id, $mark, $maj, $min ) = @$e;
    next unless $mark eq ' ';

    my $blocked = 0;
    for my $o (@entries) {
        my ( $oid, $omark, $omaj, $omin ) = @$o;
        next unless $omark eq '~';
        next unless $omaj == $maj && $omin < $min;
        # A `[~]` predecessor counts as closed when the watched ref shows
        # either an explicit "close X.Y" docs commit or the merge of its
        # phase-X-Y-* branch (claim protocol: close commit flips [~]→[x],
        # but the git trail is the source of truth when markers lag).
        ( my $slug = $oid ) =~ s/\./-/g;
        $blocked = 1
          if $log !~ /close \Q$oid\E/ && $log !~ /Merge \Qphase-$slug\E/;
        last if $blocked;
    }
    print "$id\n" unless $blocked;
}
PERL
)

new=$(comm -23 <(printf '%s\n' "$current" | sort -u) <(sort -u "$STATE_FILE") || true)

printf '%s\n' "$current" | sort -u > "$STATE_FILE"

if [ -n "$new" ]; then
  printf '%s\n' "$new"
  exit 0
fi
exit 1
