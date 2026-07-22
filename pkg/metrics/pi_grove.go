package metrics

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/grovetools/eval/pkg/record"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// The customType values the grove-pi extension stamps into pi session files.
const (
	GroveConfigCustomType = "grove_config"
	GroveMetricCustomType = "grove_metric"
)

// Warning codes emitted by the lift. All are advisory: a corpus scan must
// survive any number of malformed sessions.
const (
	WarnLegacyConfigShape  = "legacy_config_shape"
	WarnConfigHashMismatch = "config_hash_mismatch"
	WarnBadMetricKey       = "bad_metric_key"
	WarnMalformedEntry     = "malformed_entry"
	WarnModelDisagreement  = "model_disagreement"
	WarnCompactedArm       = "compacted_arm"
)

// PiGroveMeta is everything the grove extension recorded about one arm,
// recovered from that arm's `custom` entries.
//
// This is D2's designated validation point: the TypeScript emitter mirrors
// record.ConfigVector by hand, with no compiler holding it to the Go struct, so
// this is the only place drift can be caught. Every mismatch is a WARNING, never
// an error.
type PiGroveMeta struct {
	// Config is the arm's config vector, or nil if the arm carries no
	// grove_config stamp. nil means NOT STAMPED (D4) — an unattributed arm,
	// which callers must exclude from record emission rather than treat as a
	// default-configured one.
	Config *record.ConfigVector
	// ConfigHash is the hash as STAMPED by the emitter, which is not necessarily
	// the hash of Config — that discrepancy is what the mismatch warning
	// reports. Empty when the emitter stamped none (the legacy shape never did).
	ConfigHash string
	JobID      string
	Schema     string
	TaskID     string
	// Replicate is a POINTER because replicate 0 is a real and common value. A
	// plain int could not distinguish "replicate 0" from "never stamped" (D4).
	Replicate *int
	// Sweep marks an arm produced by a sweep runner.
	Sweep bool
	// Metrics holds lifted grove_metric values, keyed per D8
	// ("<component>.<metric>"). Keys failing the grammar are dropped with a
	// warning rather than coerced.
	//
	// nil means no grove_metric entry was present. An empty non-nil map means an
	// entry was present but contributed no valid keys. Neither is a zero.
	Metrics map[string]float64
	// Turns and ToolCounts are diagnostics carried beside (never inside)
	// ComponentMetrics. The last grove_metric entry on the branch wins.
	Turns      *int
	ToolCounts map[string]int
	// SeedBundlePaths is the seed manifest's bundle list when one was stamped.
	// Nothing writes one today, so this is essentially always nil.
	SeedBundlePaths []string
	// Compacted marks an arm whose path contains a compaction entry: its prefix
	// is no longer byte-identical to its siblings', so it is not comparable with
	// them. Report it; never silently drop it.
	Compacted bool
	// Models is the distinct model set across the arm's assistant messages.
	Models []string
	// Cost folds usage over THIS ARM'S PATH ENTRIES ONLY.
	Cost PiArmCost
}

// Attributed reports whether the arm carries a config stamp. An unattributed
// arm cannot be assigned to a condition and must not be emitted as a record.
func (m PiGroveMeta) Attributed() bool { return m.Config != nil }

// PiArmCost is a per-arm usage fold.
//
// Deliberately NOT the same computation as piTranscriptEntries in pkg/usage,
// which sums EVERY line in the file including abandoned branches. That
// whole-file sum is correct for "what did this session cost me" (aglogs usage)
// and wrong for "what did this arm cost": an abandoned branch's tokens were
// really billed, but were not spent on this arm's work. The two computations
// must stay separate — do not unify them.
type PiArmCost struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedUSD     float64
}

// LiftWarning is one validity complaint about a session.
type LiftWarning struct {
	// EntryID is the custom entry at fault; empty for arm-level warnings.
	EntryID string
	Code    string
	Message string
}

func (w LiftWarning) String() string {
	if w.EntryID == "" {
		return fmt.Sprintf("%s: %s", w.Code, w.Message)
	}
	return fmt.Sprintf("%s (entry %s): %s", w.Code, w.EntryID, w.Message)
}

// groveConfigData is the nested (current) shape of a grove_config entry's data.
type groveConfigData struct {
	Config      *record.ConfigVector `json:"config"`
	ConfigHash  string               `json:"config_hash"`
	Schema      string               `json:"schema"`
	JobID       string               `json:"job_id"`
	TaskID      string               `json:"task_id"`
	Replicate   *int                 `json:"replicate"`
	Sweep       bool                 `json:"sweep"`
	BundleFiles []string             `json:"bundle_files"`
}

// groveMetricData is a grove_metric entry's data.
type groveMetricData struct {
	Metrics    map[string]float64 `json:"metrics"`
	Turns      *int               `json:"turns"`
	ToolCounts map[string]int     `json:"tool_counts"`
}

// legacyConfigScalars are the keys the LEGACY flat grove_config shape carries
// alongside the component hashes. Everything in a legacy entry's data that is
// not one of these is a component name.
//
// The legacy shape spreads component hashes as TOP-LEVEL keys of data:
//
//	data = { ...renderedConfig, grove_metrics_version, job_id, provider }
//
// so "prompt"/"context"/"memory"/"skills"/"plan" sit beside the scalars rather
// than nested under a "components" object.
var legacyConfigScalars = map[string]bool{
	"grove_metrics_version": true,
	"job_id":                true,
	"provider":              true,
	"schema":                true,
	"config_hash":           true,
	"task_id":               true,
	"replicate":             true,
	"sweep":                 true,
	"bundle_files":          true,
}

// LiftGroveEntries recovers the grove extension's stamps for one branch.
//
// It is total: any malformed input produces warnings and a best-effort result,
// never an error. Callers scanning a corpus depend on that — one bad session
// must not abort the scan.
//
// Config resolution is LAST-WINS along the path. This is load-bearing rather
// than cosmetic: pi's createBranchedSession physically COPIES the seed prefix
// (including the seed's grove_config) into every forked arm file, so an arm file
// contains the seed stamp followed by its own variant stamp. Taking the last one
// is what makes the variant take effect without a side table.
func LiftGroveEntries(tree *transcript.PiSessionTree, branch transcript.PiBranch) (PiGroveMeta, []LiftWarning) {
	var meta PiGroveMeta
	var warnings []LiftWarning

	if tree == nil {
		return meta, warnings
	}

	for _, entry := range tree.CustomEntriesOn(branch) {
		switch entry.CustomType {
		case GroveConfigCustomType:
			warnings = append(warnings, liftConfigEntry(entry, &meta)...)
		case GroveMetricCustomType:
			warnings = append(warnings, liftMetricEntry(entry, &meta)...)
		}
	}

	// Compaction: an arm whose path was compacted no longer shares a
	// byte-identical prefix with its siblings.
	for _, typ := range tree.EntryTypesOn(branch) {
		if typ == "compaction" {
			meta.Compacted = true
			warnings = append(warnings, LiftWarning{
				Code:    WarnCompactedArm,
				Message: "arm path contains a compaction entry; its prefix diverges from its siblings and it is not comparable with them",
			})
			break
		}
	}

	// Model set. The stamp is the DECLARED condition, so a transcript that
	// disagrees is a validity warning, not an override.
	meta.Models = tree.ModelsOn(branch)
	if len(meta.Models) > 1 {
		warnings = append(warnings, LiftWarning{
			Code:    WarnModelDisagreement,
			Message: fmt.Sprintf("arm spans %d models (%s); it is not a single controlled condition", len(meta.Models), strings.Join(meta.Models, ", ")),
		})
	} else if meta.Config != nil && len(meta.Models) == 1 && meta.Config.Model != "" {
		if !modelMatches(meta.Config.Model, meta.Models[0]) {
			warnings = append(warnings, LiftWarning{
				Code:    WarnModelDisagreement,
				Message: fmt.Sprintf("stamped model %q disagrees with transcript model %q; keeping the stamp", meta.Config.Model, meta.Models[0]),
			})
		}
	}

	meta.Cost = foldArmCost(tree, branch)

	return meta, warnings
}

// modelMatches compares a provider-qualified stamped model
// ("anthropic/claude-sonnet-4-5") with a transcript's bare model id
// ("claude-sonnet-4-5"). Equality on the suffix is enough; this only decides
// whether to warn.
func modelMatches(stamped, observed string) bool {
	if stamped == observed {
		return true
	}
	if idx := strings.LastIndex(stamped, "/"); idx >= 0 {
		return stamped[idx+1:] == observed
	}
	return false
}

// liftConfigEntry parses one grove_config entry into meta, accepting BOTH the
// nested and the legacy flat shapes.
//
// D2 wants the on-disk shape to correspond to record.ConfigVector so agentlogs
// can validate the TS mirror against the typed Go struct — and D11's
// ConfigVector.Hash() cannot be recomputed from a flat spread at all, because it
// hashes the marshalled struct. But the flat form is already sitting in real
// corpora, and refusing to read it would discard data. So: accept both,
// discriminate on the presence of a "components" key, and WARN on the legacy
// one rather than coercing it silently.
func liftConfigEntry(entry transcript.PiCustomEntry, meta *PiGroveMeta) []LiftWarning {
	if len(entry.Data) == 0 {
		return []LiftWarning{{
			EntryID: entry.ID,
			Code:    WarnMalformedEntry,
			Message: "grove_config entry has no data",
		}}
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(entry.Data, &probe); err != nil {
		return []LiftWarning{{
			EntryID: entry.ID,
			Code:    WarnMalformedEntry,
			Message: fmt.Sprintf("grove_config data is not a JSON object: %v", err),
		}}
	}

	if _, nested := probe["config"]; nested {
		return liftNestedConfig(entry, meta)
	}
	return liftLegacyConfig(entry, probe, meta)
}

func liftNestedConfig(entry transcript.PiCustomEntry, meta *PiGroveMeta) []LiftWarning {
	var data groveConfigData
	// Unknown fields are IGNORED rather than rejected (D7 additive-read): a
	// newer emitter may stamp fields this reader predates.
	if err := json.Unmarshal(entry.Data, &data); err != nil {
		return []LiftWarning{{
			EntryID: entry.ID,
			Code:    WarnMalformedEntry,
			Message: fmt.Sprintf("grove_config data did not parse: %v", err),
		}}
	}
	if data.Config == nil {
		return []LiftWarning{{
			EntryID: entry.ID,
			Code:    WarnMalformedEntry,
			Message: "grove_config carries a \"config\" key that is not a config vector",
		}}
	}

	meta.Config = data.Config
	meta.ConfigHash = data.ConfigHash
	meta.Schema = data.Schema
	meta.JobID = data.JobID
	meta.TaskID = data.TaskID
	meta.Replicate = data.Replicate
	meta.Sweep = data.Sweep
	// nil-vs-EMPTY is load-bearing here and must survive the lift (D4).
	//
	// A stamped "bundle_files": [] is a REAL measurement — a manifest that
	// declared no files, i.e. a cold-start arm where every read is genuinely
	// off-bundle. Only an ABSENT key means not-measured. Testing len()>0 instead
	// of != nil would collapse the declared-empty manifest into nil and discard
	// that measurement; ComputeOffBundleReads keys its whole not-measured
	// contract off exactly this distinction.
	if data.BundleFiles != nil {
		meta.SeedBundlePaths = data.BundleFiles
	}

	// The emitter computes the hash independently, in TypeScript. Recomputing it
	// here is the ONLY check on that mirror.
	if data.ConfigHash != "" {
		if computed := data.Config.Hash(); computed != data.ConfigHash {
			return []LiftWarning{{
				EntryID: entry.ID,
				Code:    WarnConfigHashMismatch,
				Message: fmt.Sprintf("stamped config_hash %s does not match the recomputed hash %s; the TS emitter has drifted from record.ConfigVector", data.ConfigHash, computed),
			}}
		}
	}
	return nil
}

// liftLegacyConfig reconstructs a vector from the flat spread that the shipped
// v0.1.0 emitter writes.
//
// The reconstruction is necessarily lossy: the flat shape carries no model, no
// fixture_commit and no config_hash, so the recovered vector's Hash() is NOT
// comparable with a nested-shape arm's. Say so in the warning rather than
// letting a caller compare them.
func liftLegacyConfig(entry transcript.PiCustomEntry, probe map[string]json.RawMessage, meta *PiGroveMeta) []LiftWarning {
	components := make(map[string]string)
	for key, raw := range probe {
		if legacyConfigScalars[key] {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || s == "" {
			continue
		}
		components[key] = s
	}

	vector := &record.ConfigVector{Provider: "pi"}
	if len(components) > 0 {
		vector.Components = components
	}
	// The legacy shape's "provider" scalar is the agent provider, which is the
	// same axis ConfigVector.Provider models.
	if raw, ok := probe["provider"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			vector.Provider = s
		}
	}
	if raw, ok := probe["job_id"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			meta.JobID = s
		}
	}

	meta.Config = vector
	// Deliberately NOT setting ConfigHash: the legacy shape stamps none, and
	// synthesising one would assert a cross-check that never happened.
	meta.ConfigHash = ""

	return []LiftWarning{{
		EntryID: entry.ID,
		Code:    WarnLegacyConfigShape,
		Message: "grove_config uses the legacy flat shape (component hashes spread at top level); it carries no model, fixture_commit or config_hash, so the config hash could not be cross-checked and this arm's hash is not comparable with a nested-shape arm's",
	}}
}

// liftMetricEntry parses one grove_metric entry, dropping keys that fail the D8
// grammar.
func liftMetricEntry(entry transcript.PiCustomEntry, meta *PiGroveMeta) []LiftWarning {
	if len(entry.Data) == 0 {
		return []LiftWarning{{
			EntryID: entry.ID,
			Code:    WarnMalformedEntry,
			Message: "grove_metric entry has no data",
		}}
	}

	var data groveMetricData
	if err := json.Unmarshal(entry.Data, &data); err != nil {
		return []LiftWarning{{
			EntryID: entry.ID,
			Code:    WarnMalformedEntry,
			Message: fmt.Sprintf("grove_metric data did not parse: %v", err),
		}}
	}

	// A grove_metric entry that parsed establishes that metrics WERE reported,
	// so the map becomes non-nil even if every key is then rejected. Empty is
	// "reported nothing valid"; nil stays "never reported".
	if meta.Metrics == nil {
		meta.Metrics = make(map[string]float64)
	}

	if data.Turns != nil {
		meta.Turns = data.Turns
	}
	if data.ToolCounts != nil {
		meta.ToolCounts = data.ToolCounts
	}

	var warnings []LiftWarning
	for _, key := range sortedMetricKeys(data.Metrics) {
		if err := ValidateComponentMetricKey(key); err != nil {
			warnings = append(warnings, LiftWarning{
				EntryID: entry.ID,
				Code:    WarnBadMetricKey,
				Message: fmt.Sprintf("dropping metric key %q: %v", key, err),
			})
			continue
		}
		meta.Metrics[key] = data.Metrics[key]
	}
	return warnings
}

// sortedMetricKeys gives deterministic warning order.
func sortedMetricKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// foldArmCost sums usage over the branch's own entries.
//
// It reads the normalized entries rather than raw usage so it shares the
// normalizer's view of which messages carry tokens.
func foldArmCost(tree *transcript.PiSessionTree, branch transcript.PiBranch) PiArmCost {
	var cost PiArmCost
	for _, entry := range tree.Normalize(branch) {
		if entry.Tokens == nil {
			continue
		}
		cost.InputTokens += int64(entry.Tokens.Input)
		cost.OutputTokens += int64(entry.Tokens.Output)
		cost.CacheReadTokens += int64(entry.Tokens.CacheRead)
		cost.CacheWriteTokens += int64(entry.Tokens.CacheWrite)
		cost.EstimatedUSD += entry.Tokens.Cost
	}
	return cost
}
