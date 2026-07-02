# models-dev-pricing.json — provenance

Embedded pricing snapshot for `pkg/usage` (`DefaultPricing`). Rates are USD
per **million** tokens (divided down to per-token at load time by
`loadModelsDevJSON`); `cache_write` defaults to `input * 1.25` and
`cache_read` to `input * 0.1` when absent. Only `cost.{input,output,
cache_read,cache_write}` are read — `limit` is informational.

## Provenance

- **Anthropic entries** (226 keys): the ccusage embedded models.dev fallback
  table this file was originally ported from (see `pricing.go`).
- **Non-Anthropic entries** (added 2026-07-02 for provider-neutral usage
  accounting, P5 of `codex-pi-opencode-support`): hand-curated from published
  provider list prices as of the curator's knowledge date (2026-01), covering
  the codex CLI models (gpt-5 family incl. codex variants, gpt-4.1/4o
  families, o3/o4 series) and common opencode backends (Google Gemini, xAI
  Grok, DeepSeek, Moonshot Kimi K2, Zhipu GLM, MiniMax, Alibaba Qwen), each
  as both a bare key and a `provider/model` key (the shape opencode's
  `providerID/modelID` produces).
  - `gemini-3.1-pro-preview` is **assumed** to price like `gemini-3-pro`
    (no published figure was available); replace via regeneration when
    models.dev carries it.
  - Models newer than the curation date (e.g. hypothetical `gpt-5.2`) are
    absent and will surface as `MissingPricing` rather than a wrong $0 —
    regenerate to pick them up.

Note that pi sessions never consult this table: pi records a native
per-message dollar cost, which takes precedence over any computed cost
(`EntryCost`). opencode messages also carry a native cost; the table is only
their fallback.

## Regeneration

Run `scripts/update-models-dev-pricing.sh` (requires network access to
models.dev and `jq`). It fetches the upstream `https://models.dev/api.json`,
extracts the providers listed in the script, emits both bare and
`provider/model` keys, and **merges over** this file (upstream wins on key
collisions, existing-only keys are kept — the Anthropic port and any manual
curation survive a refresh). Update the date above when you regenerate.
