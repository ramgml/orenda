#!/usr/bin/env bash
# plan-unblocked.sh — watchdog check for the docs/PLAN.md task registry.
#
# Computes the set of claimable tasks and fires (exit 0, prints NEW ids to
# stdout) when that set grows — either because a claimed `[~]` predecessor
# was merged (its "close X.Y" commit reached origin/dev) or because new
# free `[ ]` tasks were added to the plan.
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
# Scope: only phases listed in PLAN_WATCH_PHASES (space-separated, e.g.
# "30 31") are considered executable backlog — per PLAN.md the `[ ]` markers
# in phases ≤ 28.x are historical records, not open tasks.
#
# The plan is read from the watched ref (`git show $PLAN_WATCH_REF:docs/PLAN.md`),
# not from the worktree on disk — the plugin may run from any checkout, and the
# ref is the same source of truth the close-trail grep uses. PLAN_FILE overrides
# this for tests.
#
# Exit codes: 0 = new claimable tasks appeared (ids on stdout);
#             1 = nothing new; 2 = operational error.

set -euo pipefail

WATCH_PHASES="${PLAN_WATCH_PHASES:-}"
[ -n "$WATCH_PHASES" ] || {
  echo "plan-unblocked: PLAN_WATCH_PHASES is not set (space-separated phase numbers, e.g. '30 31')" >&2
  exit 2
}

REPO_ROOT="${REPO_ROOT:-$(git rev-parse --show-toplevel)}"
WATCH_REF="${PLAN_WATCH_REF:-dev}"
PLAN_FILE="${PLAN_FILE:-}"   # test override; empty = read from $WATCH_REF
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

my %watched = map { $_ => 1 } split /\s+/, $ENV{WATCH_PHASES};
my ($plan, $repo) = @ARGV;
open my $fh, '<', $plan or exit 2;
my @entries;    # [id, mark]
while (<$fh>) {
    push @entries, [ $2, $1 ] if /^- \[([ x~])\] \*\*(\d+\.\d+)\*\*/;
}
close $fh;

@entries = grep { $watched{ ( split /\./, $_->[0] )[0] } } @entries;

my $ref = $ENV{WATCH_REF};
my $log = `git -C "$repo" log --oneline "$ref" 2>/dev/null`;

for my $e (@entries) {
    my ( $id, $mark ) = @$e;
    next unless $mark eq ' ';
    my ( $maj, $min ) = split /\./, $id;

    my $blocked = 0;
    for my $o (@entries) {
        my ( $oid, $omark ) = @$o;
        next unless $omark eq '~';
        my ( $omaj, $omin ) = split /\./, $oid;
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
