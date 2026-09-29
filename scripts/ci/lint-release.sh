#!/usr/bin/env bash
# Release-gate lint (Task 378) — the command behind the `lint` job in
# .github/workflows/ci.yml. Run it locally for an exact simulation of the
# CI step: `scripts/ci/lint-release.sh`.
#
# Gate semantics: fail only on lint issues that are BOTH new since the
# previous release AND not part of the standing dev lint debt.
#
#   baseline — previous release tag: `git describe --tags --abbrev=0 HEAD^`,
#              resolved to its merge-base with HEAD ("what's new since the
#              last release"). Before Task 378 the gate diffed against
#              origin/main, so a minor release's delta was the whole epic
#              plus whatever dev debt got churned — guaranteed red
#              (v0.25.0, run 36521959026). When no tag is reachable from
#              HEAD^ (fresh repo, untagged history) the script falls back
#              to the old origin/main baseline with a loud warning.
#
#   debt     — golangci-lint issues already present on origin/dev are the
#              Phase 30.16 standing inventory (`make lint`); they never
#              block a release (closing that debt is opportunistic, not
#              release-blocking). Issues dev does NOT carry yet still fail
#              the job — the gate keeps teeth for regressions introduced by
#              release-only commits (version bump, changelog, main-side
#              hotfixes). Corollary: once dev has merged the release, the
#              tag-push run of this gate is vacuous by construction — the
#              authoritative run is the release PR into main, before dev
#              carries the branch.
#
# Requires: git, jq, go, golangci-lint v2 (PATH or $(go env GOPATH)/bin).
# Exit codes: 0 — green; 1 — blocking issues; 2 — harness error.
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

tmp="$(mktemp -d)"
dev_tree=""
cleanup() {
	if [[ -n "$dev_tree" ]]; then
		git worktree remove --force "$dev_tree" >/dev/null 2>&1 || true
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT

# --- tool resolution -------------------------------------------------------
if command -v golangci-lint >/dev/null 2>&1; then
	GCL="$(command -v golangci-lint)"
elif [[ -x "$(go env GOPATH)/bin/golangci-lint" ]]; then
	GCL="$(go env GOPATH)/bin/golangci-lint"
else
	echo "ERROR: golangci-lint not found (PATH or \$(go env GOPATH)/bin)." >&2
	echo "  install: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest" >&2
	exit 2
fi
"$GCL" --version 2>/dev/null | grep -q "version 2" || {
	echo "ERROR: golangci-lint v2 required (config schema is v2), got: $("$GCL" --version 2>/dev/null || echo '?')" >&2
	exit 2
}

# The baseline needs release tags and both branch heads. fetch-depth: 0 in
# CI already provides them; this fetch is a cheap no-op there and covers
# plain local checkouts.
git fetch --no-tags origin main dev >/dev/null 2>&1 ||
	echo "WARN: 'git fetch origin main dev' failed — proceeding with locally known refs" >&2

# --- baseline: previous release tag (fallback origin/main) -----------------
prev_tag=""
if prev_tag="$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null)" &&
	base="$(git merge-base "$prev_tag" HEAD)"; then
	base_kind="previous release tag ${prev_tag}"
else
	base="$(git rev-parse origin/main)"
	base_kind="origin/main fallback (no release tag reachable from HEAD^)"
	echo "WARN: no release tag reachable from HEAD^ — using the pre-T378 origin/main baseline" >&2
fi
echo "release-lint: baseline ${base} (${base_kind})"

# --- delta run: issues on lines changed since the baseline ------------------
"$GCL" run --new-from-rev="$base" --output.json.path "$tmp/delta.json" ./... >"$tmp/delta.out" 2>&1 || true
jq -e 'has("Issues")' "$tmp/delta.json" >/dev/null 2>&1 || {
	echo "ERROR: delta lint produced no JSON report; raw output:" >&2
	sed -n '1,40p' "$tmp/delta.out" >&2
	exit 2
}

# --- standing dev inventory (Phase 30.16): full lint of origin/dev ----------
dev_rev="$(git rev-parse origin/dev)"
dev_tree="$tmp/dev-tree"
git worktree add --detach "$dev_tree" "$dev_rev" >/dev/null
(cd "$dev_tree" && "$GCL" run --output.json.path "$tmp/dev.json" ./...) >"$tmp/dev.out" 2>&1 || true
jq -e 'has("Issues")' "$tmp/dev.json" >/dev/null 2>&1 || {
	echo "ERROR: origin/dev inventory lint produced no JSON report; raw output:" >&2
	sed -n '1,40p' "$tmp/dev.out" >&2
	exit 2
}

# --- gate: delta minus inventory (keyed by linter + file + message) ---------
# Line numbers are deliberately NOT part of the key: debt on a churned line
# is still the same accepted debt.
jq -r '.Issues[]? | [.FromLinter, .Pos.Filename, .Text] | @tsv' "$tmp/delta.json" | sort -u >"$tmp/delta.keys"
jq -r '.Issues[]? | [.FromLinter, .Pos.Filename, .Text] | @tsv' "$tmp/dev.json" | sort -u >"$tmp/dev.keys"
comm -23 "$tmp/delta.keys" "$tmp/dev.keys" >"$tmp/blocking.keys"

delta_total="$(wc -l <"$tmp/delta.keys")"
blocking_count="$(wc -l <"$tmp/blocking.keys")"
carried="$((delta_total - blocking_count))"

if [[ "$blocking_count" -gt 0 ]]; then
	echo "release-lint: FAIL — ${blocking_count} new issue(s) not covered by the dev debt inventory:" >&2
	while IFS=$'\t' read -r linter file text; do
		jq -r --arg l "$linter" --arg f "$file" --arg t "$text" \
			'.Issues[]? | select(.FromLinter==$l and .Pos.Filename==$f and .Text==$t) | "\(.Pos.Filename):\(.Pos.Line): \(.Text) (\(.FromLinter))"' \
			"$tmp/delta.json"
	done <"$tmp/blocking.keys" >&2
	echo "release-lint: baseline ${base} (${base_kind}); carried by dev inventory: ${carried}; blocking: $(wc -l <"$tmp/blocking.keys")" >&2
	exit 1
fi

echo "release-lint: OK — ${delta_total} issue(s) new since the baseline, all carried by the Phase 30.16 standing dev inventory (origin/dev ${dev_rev}); blocking: 0"
