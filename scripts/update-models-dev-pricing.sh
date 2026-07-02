#!/usr/bin/env bash
# Regenerates pkg/usage/models-dev-pricing.json from models.dev.
#
# Fetches https://models.dev/api.json (shape: {provider: {models: {id:
# {cost, limit}}}}), extracts the providers in $PROVIDERS, and merges the
# result OVER the existing snapshot: upstream wins on key collisions,
# keys that only exist locally (the original ccusage Anthropic port, manual
# curation) are preserved. Each model is emitted under two keys — the bare
# model id and "provider/id" (the shape opencode's providerID/modelID
# produces) — matching how PricingMap.Find resolves names.
#
# See pkg/usage/models-dev-pricing.README.md for provenance; update its date
# after running this.
#
# Usage: scripts/update-models-dev-pricing.sh   (from the repo root)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SNAPSHOT="$REPO_ROOT/pkg/usage/models-dev-pricing.json"

# Providers grove's coding agents can bill against. Extend as needed.
PROVIDERS='["anthropic","openai","google","xai","deepseek","moonshotai","zai","minimax","alibaba"]'

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

curl -fsSL https://models.dev/api.json |
	jq --argjson providers "$PROVIDERS" '
		# provider -> {models: {id: {cost, limit}}} to a flat key map with
		# both bare and provider-prefixed keys; entries without cost are
		# dropped (the loader skips them anyway).
		[ to_entries[]
		  | select(.key as $p | $providers | index($p))
		  | .key as $provider
		  | (.value.models // {}) | to_entries[]
		  | select(.value.cost != null)
		  | {id: .key, provider: $provider,
		     rec: {cost: .value.cost, limit: (.value.limit // {})}}
		]
		| map({(.id): .rec, ("\(.provider)/\(.id)"): .rec})
		| add // {}
	' > "$TMP"

# Merge upstream over the existing snapshot (upstream wins; local-only keys
# survive), sort keys, and write back with tab indentation.
jq --tab -S -s '.[0] * .[1]' "$SNAPSHOT" "$TMP" > "$SNAPSHOT.new"
mv "$SNAPSHOT.new" "$SNAPSHOT"

echo "Updated $SNAPSHOT ($(jq 'length' "$SNAPSHOT") keys)."
echo "Remember to bump the provenance date in pkg/usage/models-dev-pricing.README.md."
