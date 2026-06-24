#!/usr/bin/env bash
#
# ccusage-diff.sh — acceptance gate for `aglogs usage`.
#
# Builds aglogs, runs `aglogs usage --ccusage-json` and `ccusage claude session
# --json` over the same ~/.claude/projects, and asserts that per-session and
# grand totals match (token classes exactly, cost within a rounding epsilon).
#
# Because ~/.claude/projects is written live, both tools are run back-to-back so
# they observe the same on-disk state.
#
# Usage:
#   scripts/ccusage-diff.sh [--ccusage <path-to-ccusage-binary>] [--epsilon <usd>]
#
# CCUSAGE_BIN env var is honored if --ccusage is not given. If neither is set the
# script looks for `ccusage` on PATH.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CCUSAGE_BIN="${CCUSAGE_BIN:-}"
EPSILON="0.01"

while [[ $# -gt 0 ]]; do
	case "$1" in
	--ccusage)
		CCUSAGE_BIN="$2"
		shift 2
		;;
	--epsilon)
		EPSILON="$2"
		shift 2
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
done

if [[ -z "$CCUSAGE_BIN" ]]; then
	if command -v ccusage >/dev/null 2>&1; then
		CCUSAGE_BIN="$(command -v ccusage)"
	else
		echo "ccusage binary not found: pass --ccusage <path> or set CCUSAGE_BIN" >&2
		exit 2
	fi
fi

PROJECTS_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects"
if [[ ! -d "$PROJECTS_DIR" ]]; then
	# CLAUDE_CONFIG_DIR may already point at projects/ itself.
	PROJECTS_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude/projects}"
fi

echo "Building aglogs..."
AGLOGS_BIN="$(mktemp -t aglogs.XXXXXX)"
AGLOGS_JSON="$(mktemp -t aglogs-usage.XXXXXX)"
CCUSAGE_JSON="$(mktemp -t ccusage-usage.XXXXXX)"
SNAPSHOT_DIR="$(mktemp -d -t aglogs-snapshot.XXXXXX)"
trap 'rm -rf "$AGLOGS_BIN" "$AGLOGS_JSON" "$CCUSAGE_JSON" "$SNAPSHOT_DIR"' EXIT
(cd "$REPO_ROOT" && go build -o "$AGLOGS_BIN" .)

# Freeze the live projects directory so both tools observe identical state
# (~/.claude/projects is written continuously by running agents). Both tools
# honor CLAUDE_CONFIG_DIR pointing at a config dir containing projects/.
echo "Snapshotting $PROJECTS_DIR ..."
cp -R "$PROJECTS_DIR" "$SNAPSHOT_DIR/projects"
export CLAUDE_CONFIG_DIR="$SNAPSHOT_DIR"

echo "Running ccusage..."
"$CCUSAGE_BIN" claude session --json >"$CCUSAGE_JSON" 2>/dev/null
echo "Running aglogs usage..."
"$AGLOGS_BIN" usage --ccusage-json >"$AGLOGS_JSON" 2>/dev/null

echo "Diffing..."
python3 - "$AGLOGS_JSON" "$CCUSAGE_JSON" "$EPSILON" <<'PY'
import json
import sys
from collections import defaultdict

aglogs_path, ccusage_path, epsilon = sys.argv[1], sys.argv[2], float(sys.argv[3])
a = json.load(open(aglogs_path))
c = json.load(open(ccusage_path))

TOKEN_FIELDS = [
    "inputTokens",
    "outputTokens",
    "cacheCreationTokens",
    "cacheReadTokens",
    "totalTokens",
]


def by_key(doc):
    m = defaultdict(lambda: {f: 0 for f in TOKEN_FIELDS} | {"totalCost": 0.0})
    for s in doc["sessions"]:
        k = (s["projectPath"], s["sessionId"])
        for f in TOKEN_FIELDS:
            m[k][f] += s[f]
        m[k]["totalCost"] += s["totalCost"]
    return m


am, cm = by_key(a), by_key(c)
failures = []

only_a = set(am) - set(cm)
only_c = set(cm) - set(am)
for k in sorted(only_a):
    failures.append(f"session only in aglogs: {k}")
for k in sorted(only_c):
    failures.append(f"session only in ccusage: {k}")

for k in sorted(set(am) & set(cm)):
    for f in TOKEN_FIELDS:
        if am[k][f] != cm[k][f]:
            failures.append(f"session {k} {f}: aglogs={am[k][f]} ccusage={cm[k][f]}")
    if abs(am[k]["totalCost"] - cm[k]["totalCost"]) > epsilon:
        failures.append(
            f"session {k} totalCost: aglogs={am[k]['totalCost']:.6f} ccusage={cm[k]['totalCost']:.6f}"
        )

for f in TOKEN_FIELDS:
    if a["totals"][f] != c["totals"][f]:
        failures.append(f"TOTAL {f}: aglogs={a['totals'][f]} ccusage={c['totals'][f]}")
if abs(a["totals"]["totalCost"] - c["totals"]["totalCost"]) > epsilon:
    failures.append(
        f"TOTAL totalCost: aglogs={a['totals']['totalCost']:.6f} ccusage={c['totals']['totalCost']:.6f}"
    )

print(f"aglogs sessions: {len(a['sessions'])}  ccusage sessions: {len(c['sessions'])}")
print(f"grand total tokens: aglogs={a['totals']['totalTokens']} ccusage={c['totals']['totalTokens']}")
print(f"grand total cost:   aglogs=${a['totals']['totalCost']:.4f} ccusage=${c['totals']['totalCost']:.4f}")

if failures:
    print(f"\nFAIL: {len(failures)} mismatch(es):")
    for line in failures[:50]:
        print(f"  {line}")
    if len(failures) > 50:
        print(f"  ... and {len(failures) - 50} more")
    sys.exit(1)

print("\nPASS: aglogs usage matches ccusage (per-session and grand total).")
PY
