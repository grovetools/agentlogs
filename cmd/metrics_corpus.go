package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/grovetools/eval/pkg/record"

	"github.com/grovetools/agentlogs/pkg/metrics"
	"github.com/grovetools/agentlogs/pkg/transcript"
)

// armView is one attributed arm: a branch of one session file, plus everything
// the lift recovered about it.
//
// Under fork-per-session a SWEEP arm is a whole file — its own ActivePath —
// because pi's RPC wire protocol exposes `fork` (which creates a new sibling
// session file) and no in-place branch method at all. Multi-arm files still
// occur in the wild (in-place branch/navigateTree, the TUI's /tree), which is
// what --branches is for; the sweep simply never produces them.
type armView struct {
	SessionPath string
	SessionID   string
	LeafID      string
	Meta        metrics.PiGroveMeta
	Warnings    []metrics.LiftWarning
	Process     metrics.Result
}

// collectPiCorpus discovers every pi session file and lifts the requested
// branches out of each.
//
// A malformed session produces warnings and is skipped, never an error: a
// corpus scan must not die on one bad file.
func collectPiCorpus(allBranches bool) ([]armView, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return collectPiCorpusFrom(homeDir, allBranches)
}

// collectPiCorpusFrom is collectPiCorpus with the home directory injected.
//
// The seam exists so the aggregation path is testable against a fixture corpus:
// with os.UserHomeDir() called inline there was no way to reach
// groupByComponent/summarise from a test at all, and the entire --by-config
// path shipped uncovered.
//
// NOTE: this is a homeDir seam, NOT a PI_CODING_AGENT_DIR one. pi does honour
// that env override, but agentlogs deliberately does not read it — see the
// comment on transcript.PiSessionsDir ("grove assumes the default location,
// like it does for ~/.claude and ~/.codex"). Honouring it here would make
// discovery inconsistent with pkg/usage's three sources, which all resolve the
// default location. Changing that is an ecosystem-wide decision, not a
// test-seam one.
func collectPiCorpusFrom(homeDir string, allBranches bool) ([]armView, error) {
	// Same discovery the usage source uses; transcript.PiSessionsGlob is the
	// single definition of pi's on-disk layout.
	matches, err := filepath.Glob(transcript.PiSessionsGlob(homeDir, ""))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)

	var arms []armView
	for _, path := range matches {
		arms = append(arms, liftSessionFile(path, allBranches)...)
	}
	return arms, nil
}

// liftSessionFile lifts one session file's branches.
func liftSessionFile(path string, allBranches bool) []armView {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	tree, err := transcript.ParsePiSessionTree(f)
	if err != nil || tree == nil {
		return nil
	}

	branches := []transcript.PiBranch{tree.ActivePath()}
	if allBranches {
		branches = tree.Branches()
	}

	var out []armView
	for _, branch := range branches {
		meta, warnings := metrics.LiftGroveEntries(tree, branch)
		result := metrics.Compute(tree.Normalize(branch))
		result.Provider = "pi"
		out = append(out, armView{
			SessionPath: path,
			SessionID:   sessionIDFromPiPath(path),
			LeafID:      branch.LeafID,
			Meta:        meta,
			Warnings:    warnings,
			Process:     result,
		})
	}
	return out
}

// sessionIDFromPiPath recovers the session id from pi's
// "<timestamp>_<uuid>.jsonl" filename.
func sessionIDFromPiPath(path string) string {
	base := filepath.Base(path)
	base = base[:len(base)-len(filepath.Ext(base))]
	for i := 0; i < len(base); i++ {
		if base[i] == '_' {
			return base[i+1:]
		}
	}
	return base
}

// --- --by-config ----------------------------------------------------------

// configGroup aggregates the arms sharing one value of the grouped component.
type configGroup struct {
	// ComponentHash is the grouped component's content hash.
	//
	// omitempty is load-bearing, not cosmetic. Without it a group keyed on a
	// measured-empty hash and a group keyed on a not-measured one serialise
	// IDENTICALLY as `"component_hash": ""`, which is the D4 substitution this
	// phase exists to stop. groupByComponent no longer produces the
	// not-measured case at all (arms with no entry for the component are
	// excluded — see ArmsNoComponent), so this is defence in depth: it makes
	// the two states impossible to conflate on the wire even if a future path
	// reintroduces the merge.
	ComponentHash string `json:"component_hash,omitempty"`
	N             int    `json:"n"`
	// ConfigHashes lists the distinct full config hashes in this group: arms
	// can agree on one component and differ elsewhere, and collapsing that
	// distinction would silently compare unlike conditions.
	ConfigHashes []string `json:"config_hashes"`
	SessionIDs   []string `json:"session_ids"`

	// Pointers with omitempty so a metric NO arm reported is ABSENT rather than
	// a zeroed summary (D4). A serialised {"n":0,"mean":0,"min":0,"max":0}
	// states a mean of 0 for something nothing measured, disambiguated only by
	// a reader who notices the adjacent n:0 — exactly the "not measured reads
	// as zero" substitution this phase exists to stop.
	//
	// omitempty on a POINTER omits only when the pointer is nil; it never
	// inspects the pointee. That is what makes this correct here and what makes
	// it wrong on a struct value (where omitempty is a no-op — a zeroed struct
	// serialises in full).
	ToolCalls *MetricSummary `json:"tool_calls,omitempty"`
	Turns     *MetricSummary `json:"turns,omitempty"`

	// ComponentMetrics summarises each D8 key present on the arms in this
	// group. A key absent from an arm is NOT counted as zero — n records how
	// many arms actually reported it.
	ComponentMetrics map[string]MetricSummary `json:"component_metrics,omitempty"`

	CompactedArms int `json:"compacted_arms"`
}

// MetricSummary is a mean/spread over the arms that actually reported a value.
//
// N is the number of CONTRIBUTING arms, which is not necessarily the group's
// size. A metric reported by 2 of 7 arms has N=2, and reading its mean as a
// property of all 7 would be wrong — that is why N travels with the number.
type MetricSummary struct {
	N    int     `json:"n"`
	Mean float64 `json:"mean"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
}

// summarise returns nil when NO arm reported the value: not-measured is absent,
// never a zeroed summary. A real measured zero still yields a summary with
// N>0 and Mean 0, which is a different and honest statement.
func summarise(values []float64) *MetricSummary {
	if len(values) == 0 {
		return nil
	}
	s := MetricSummary{N: len(values), Min: values[0], Max: values[0]}
	total := 0.0
	for _, v := range values {
		total += v
		if v < s.Min {
			s.Min = v
		}
		if v > s.Max {
			s.Max = v
		}
	}
	s.Mean = total / float64(len(values))
	return &s
}

// byConfigReport is the --by-config JSON document (D19: JSON on stdout).
type byConfigReport struct {
	Component string `json:"component"`
	// Arms counted / skipped, so a reader can see what the numbers rest on
	// rather than inferring it from the group sizes.
	ArmsTotal      int `json:"arms_total"`
	ArmsAttributed int `json:"arms_attributed"`
	// ArmsNoComponent counts attributed arms EXCLUDED from grouping because
	// their config vector has no entry for the requested component. Such an arm
	// is not varying on this axis at all, so it has no condition to compare and
	// cannot be bucketed — see groupByComponent.
	//
	// It is a SUBSET of ArmsAttributed, so the group sizes sum to
	// ArmsAttributed - ArmsNoComponent, not to ArmsAttributed. Attribution and
	// having-this-component are separate facts and both stay visible.
	ArmsNoComponent  int                    `json:"arms_no_component"`
	ArmsUnattributed int                    `json:"arms_unattributed"`
	Groups           map[string]configGroup `json:"groups"`
	Warnings         []string               `json:"warnings,omitempty"`
}

// groupByComponent buckets attributed arms by their value of one component.
func groupByComponent(arms []armView, component string) byConfigReport {
	report := byConfigReport{
		Component: component,
		ArmsTotal: len(arms),
		Groups:    make(map[string]configGroup),
	}

	buckets := make(map[string][]armView)
	for _, arm := range arms {
		for _, w := range arm.Warnings {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("%s: %s", arm.SessionID, w.String()))
		}
		// An arm with no config stamp cannot be assigned to a condition. It is
		// counted and reported, never bucketed under a default.
		if !arm.Meta.Attributed() {
			report.ArmsUnattributed++
			continue
		}
		report.ArmsAttributed++

		// An arm whose vector has NO entry for the requested component is not
		// varying on this axis, and it is excluded from grouping entirely
		// rather than bucketed under "".
		//
		// The comma-ok is the whole point. A plain map index returns "" for
		// both "stamped empty" and "never stamped", which collapsed the two
		// into a single "" group carrying a full statistical summary for a
		// condition that was never varied on the requested axis — a mean over
		// unlike things, reported as if measured. An arm that stamps an
		// explicit "" STAYS: that is a measured empty and a real condition
		// (D4 cuts both ways — a measured zero must never be discarded as
		// not-measured).
		//
		// Excluding loses an arm loudly (ArmsNoComponent is in the output);
		// merging corrupted the comparison silently. Same shape as the E1
		// ruling: skip the arm, make the skip visible.
		hash, present := arm.Meta.Config.Components[component]
		if !present {
			report.ArmsNoComponent++
			continue
		}
		buckets[hash] = append(buckets[hash], arm)
	}

	for hash, members := range buckets {
		group := configGroup{
			ComponentHash: hash,
			N:             len(members),
		}
		seenConfig := make(map[string]bool)
		var toolCalls, turns []float64
		metricValues := make(map[string][]float64)

		for _, arm := range members {
			group.SessionIDs = append(group.SessionIDs, arm.SessionID)
			if h := arm.Meta.Config.Hash(); !seenConfig[h] {
				seenConfig[h] = true
				group.ConfigHashes = append(group.ConfigHashes, h)
			}
			if arm.Meta.Compacted {
				group.CompactedArms++
			}
			if arm.Process.ToolCalls != nil {
				toolCalls = append(toolCalls, float64(*arm.Process.ToolCalls))
			}
			if arm.Process.Turns != nil {
				turns = append(turns, float64(*arm.Process.Turns))
			}
			for key, value := range arm.Meta.Metrics {
				metricValues[key] = append(metricValues[key], value)
			}
		}

		sort.Strings(group.SessionIDs)
		sort.Strings(group.ConfigHashes)
		group.ToolCalls = summarise(toolCalls)
		group.Turns = summarise(turns)
		if len(metricValues) > 0 {
			group.ComponentMetrics = make(map[string]MetricSummary, len(metricValues))
			for key, values := range metricValues {
				// A key only reaches metricValues because some arm reported it,
				// so summarise is non-nil here by construction.
				if s := summarise(values); s != nil {
					group.ComponentMetrics[key] = *s
				}
			}
		}
		report.Groups[hash] = group
	}

	sort.Strings(report.Warnings)
	return report
}

// --- --emit-partials -------------------------------------------------------

// writeArmPartials writes one partial RunRecord per attributed arm.
//
// C1 makes agentlogs the SOLE per-arm ComponentMetrics writer, so a partial
// carries the envelope (Schema, Key, Config) and ComponentMetrics and NOTHING
// else. Never Cost — the fork runner owns that axis — never Process as an axis,
// never Outcome (grading is eval's, and eval never executes).
//
// ComponentMetrics may contain knowledge.tool_result_bytes. A partial with no
// metric keys still carries an envelope and joins; never add placeholder zeros.
func writeArmPartials(dir string, arms []armView) (int, []string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, nil, fmt.Errorf("creating partials dir: %w", err)
	}

	written := 0
	var skipped []string
	for _, arm := range arms {
		if !arm.Meta.Attributed() {
			// Unattributed arms are excluded from record emission and counted,
			// not emitted under a synthesised config.
			skipped = append(skipped, fmt.Sprintf("%s (unattributed: no grove_config stamp)", arm.SessionID))
			continue
		}

		// An attributed arm with no replicate stamp has no join key, and one
		// cannot be synthesised. record.RunKey.Replicate is a non-nilable int,
		// so defaulting to 0 would make an UNSTAMPED arm collide byte-for-byte
		// with an arm genuinely stamped `replicate: 0` under the same config —
		// the joiner would silently merge two distinct experimental conditions
		// into one. That is P1's blocker, which is the defect this framework
		// exists to prevent.
		//
		// Skipping loses one arm LOUDLY; emitting a wrong key corrupts the
		// matrix SILENTLY. Loud beats silent.
		if arm.Meta.Replicate == nil {
			skipped = append(skipped, fmt.Sprintf(
				"%s (attributed but no replicate stamp: a join key cannot be synthesised — "+
					"defaulting to 0 would collide with a real replicate 0)", arm.SessionID))
			continue
		}

		partial := armPartial(arm)
		data, err := json.MarshalIndent(partial, "", "  ")
		if err != nil {
			return written, skipped, fmt.Errorf("marshalling partial for %s: %w", arm.SessionID, err)
		}
		name := fmt.Sprintf("%s-%s.json", arm.SessionID, arm.LeafID)
		if err := os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o644); err != nil {
			return written, skipped, fmt.Errorf("writing partial for %s: %w", arm.SessionID, err)
		}
		written++
	}
	return written, skipped, nil
}

// armPartial builds the envelope+ComponentMetrics partial for one arm.
func armPartial(arm armView) record.RunRecord {
	schema := arm.Meta.Schema
	if schema == "" {
		schema = record.SchemaVersion
	}

	// Caller contract: writeArmPartials has already skipped arms with no
	// replicate stamp. Dereferencing here is deliberate — there is no safe
	// default (see the skip in writeArmPartials), so a nil must never reach
	// this far rather than being papered over with a 0.
	replicate := *arm.Meta.Replicate

	partial := record.RunRecord{
		Schema: schema,
		Key: record.RunKey{
			TaskID: arm.Meta.TaskID,
			// Recomputed from the vector rather than copied from the stamp: the
			// stamp is the emitter's claim and may have drifted (the lift warns
			// when it has). The joiner keys on the truth of the vector.
			ConfigHash: arm.Meta.Config.Hash(),
			Replicate:  replicate,
		},
		Config: *arm.Meta.Config,
	}

	// Deliberately absent: Cost (the fork runner's axis), Process (a
	// diagnostic here, not an axis this writer owns), Outcome (eval's).
	if len(arm.Meta.Metrics) > 0 {
		partial.ComponentMetrics = arm.Meta.Metrics
	}
	return partial
}

// --- mode runners ----------------------------------------------------------

// runCorpusMetrics scans every pi session file.
func runCorpusMetrics(byConfig string, branches bool, emitPartials string, jsonOutput bool) error {
	if byConfig != "" {
		if err := validateComponentArg(byConfig); err != nil {
			return err
		}
	}

	arms, err := collectPiCorpus(branches)
	if err != nil {
		return fmt.Errorf("scanning the pi session corpus: %w", err)
	}

	if emitPartials != "" {
		written, skipped, err := writeArmPartials(emitPartials, arms)
		if err != nil {
			return err
		}
		reportPartials(emitPartials, written, skipped, len(arms))
	}

	if byConfig == "" {
		return nil
	}

	report := groupByComponent(arms, byConfig)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling report: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// runSessionBranchMetrics handles --branches / --emit-partials against a single
// session file.
func runSessionBranchMetrics(spec string, branches bool, emitPartials string, jsonOutput bool) error {
	info, err := resolveMetricsSession(spec)
	if err != nil {
		return err
	}
	if info.Provider != "pi" {
		return fmt.Errorf("--branches and --emit-partials read pi session trees; %q resolved as provider %q",
			spec, info.Provider)
	}

	arms := liftSessionFile(info.LogFilePath, branches)

	if emitPartials != "" {
		written, skipped, err := writeArmPartials(emitPartials, arms)
		if err != nil {
			return err
		}
		reportPartials(emitPartials, written, skipped, len(arms))
		if !jsonOutput {
			return nil
		}
	}

	data, err := json.MarshalIndent(armSummaries(arms), "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling arms: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// armSummary is the per-arm view printed by --branches.
type armSummary struct {
	SessionID  string   `json:"session_id"`
	LeafID     string   `json:"leaf_id"`
	Attributed bool     `json:"attributed"`
	ConfigHash string   `json:"config_hash,omitempty"`
	TaskID     string   `json:"task_id,omitempty"`
	Replicate  *int     `json:"replicate,omitempty"`
	Sweep      bool     `json:"sweep,omitempty"`
	Compacted  bool     `json:"compacted,omitempty"`
	Models     []string `json:"models,omitempty"`

	// Cost is the arm's OWN path fold — deliberately not the whole-file sum
	// that `aglogs usage` reports, which includes abandoned branches.
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	EstimatedUSD     float64 `json:"estimated_usd"`

	ComponentMetrics map[string]float64 `json:"component_metrics,omitempty"`
	Turns            *int               `json:"turns,omitempty"`
	ToolCounts       map[string]int     `json:"tool_counts,omitempty"`
	Warnings         []string           `json:"warnings,omitempty"`
}

func armSummaries(arms []armView) []armSummary {
	out := make([]armSummary, 0, len(arms))
	for _, arm := range arms {
		s := armSummary{
			SessionID:        arm.SessionID,
			LeafID:           arm.LeafID,
			Attributed:       arm.Meta.Attributed(),
			TaskID:           arm.Meta.TaskID,
			Replicate:        arm.Meta.Replicate,
			Sweep:            arm.Meta.Sweep,
			Compacted:        arm.Meta.Compacted,
			Models:           arm.Meta.Models,
			InputTokens:      arm.Meta.Cost.InputTokens,
			OutputTokens:     arm.Meta.Cost.OutputTokens,
			CacheReadTokens:  arm.Meta.Cost.CacheReadTokens,
			CacheWriteTokens: arm.Meta.Cost.CacheWriteTokens,
			EstimatedUSD:     arm.Meta.Cost.EstimatedUSD,
			ComponentMetrics: arm.Meta.Metrics,
			Turns:            arm.Meta.Turns,
			ToolCounts:       arm.Meta.ToolCounts,
		}
		if arm.Meta.Attributed() {
			s.ConfigHash = arm.Meta.Config.Hash()
		}
		for _, w := range arm.Warnings {
			s.Warnings = append(s.Warnings, w.String())
		}
		out = append(out, s)
	}
	return out
}

// reportPartials prints what was written and — importantly — what was NOT.
// Silent truncation reads as "covered everything" when it did not.
func reportPartials(dir string, written int, skipped []string, total int) {
	fmt.Fprintf(os.Stderr, "wrote %d/%d arm partials to %s\n", written, total, dir)
	for _, s := range skipped {
		fmt.Fprintf(os.Stderr, "  skipped %s\n", s)
	}
}

// validateComponentArg checks --by-config against the D10 vocabulary.
//
// An unknown component is rejected with the vocabulary spelled out rather than
// silently producing one empty group, which would read as "measured, found
// nothing".
func validateComponentArg(component string) error {
	if metrics.IsKnownComponent(component) {
		return nil
	}
	return fmt.Errorf("unknown config component %q; expected one of %v",
		component, metrics.ComponentVocabulary)
}
