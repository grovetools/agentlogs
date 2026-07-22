package metrics

import (
	"path/filepath"
	"strings"

	"github.com/grovetools/agentlogs/pkg/transcript"
)

// Component metric keys this package owns.
//
// Key-level ownership is how D3's dual contributor resolves: agentlogs owns
// pi-derived keys, eval's probes own probe keys, and a collision between them is
// a D6 error rather than a merge.
const (
	MetricContextOffBundleReads = "context.off_bundle_reads"
)

// ComputeOffBundleReads counts reads of files the seeded bundle did not
// contain.
//
// ⚠ NOT WIRED INTO ANY PRODUCTION PATH IN WAVE ONE. Its only callers are
// ArmComponentMetrics (itself unwired) and tests. The shipping code —
// armPartial, armSummaries, groupByComponent — reads arm.Meta.Metrics directly
// and never invokes this fold. That is EXPECTED while wave one ships zero
// computed metrics; the function and its D4 guards exist so the semantics are
// pinned BEFORE the fold is wired, not certified after.
//
// WHOEVER WIRES THIS must re-run the mutation tests in pi_grove_test.go rather
// than inheriting them as already-passing: a guard that has never gated a
// production caller has never been proven load-bearing on one.
//
// 🛑 IT RETURNS nil WHENEVER THERE IS NO BUNDLE MANIFEST. Flow now seeds a
// per-run manifest for new Pi jobs, while old/non-flow sessions remain nil.
//
// nil means NOT MEASURED (D4). Returning 0 instead would assert "no off-bundle
// reads occurred" — a claim about the world that nothing measured. That precise
// substitution is the defect this phase exists to stop repeating: the shipped
// extension's own off_bundle_reads could never fire (it read `args` off an event
// type that has no `args` field) and therefore reported "none occurred" forever.
// Do not make an unmeasurable metric look measured.
//
// An EMPTY-BUT-NON-NIL bundle is different and is honoured as a real
// measurement: it means the manifest existed and declared no files, so every
// read is genuinely off-bundle.
//
// Paths are normalised cwd-relative before comparison so an absolute read of a
// bundled file still matches.
func ComputeOffBundleReads(tree *transcript.PiSessionTree, branch transcript.PiBranch, bundle []string, cwd string) *float64 {
	if bundle == nil {
		return nil // not measured — no manifest, no denominator
	}

	inBundle := make(map[string]struct{}, len(bundle))
	for _, p := range bundle {
		inBundle[normalizeBundlePath(p, cwd)] = struct{}{}
	}

	count := 0.0
	for _, entry := range tree.Normalize(branch) {
		for _, part := range entry.Parts {
			if part.Type != PartTypeToolCall {
				continue
			}
			call := partToolCall(part)
			if strings.ToLower(call.Name) != "read" {
				continue
			}
			target := firstStringValue(call.Input, []string{"path"})
			if target == "" {
				continue
			}
			if _, ok := inBundle[normalizeBundlePath(target, cwd)]; !ok {
				count++
			}
		}
	}
	return &count
}

// normalizeBundlePath makes a path comparable: cwd-relative and slash-cleaned.
func normalizeBundlePath(p, cwd string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) && cwd != "" {
		if rel, err := filepath.Rel(cwd, p); err == nil {
			p = rel
		}
	}
	return filepath.ToSlash(filepath.Clean(p))
}

// ArmComponentMetrics assembles the ComponentMetrics map for one attributed arm.
//
// ⚠ NOT WIRED INTO ANY PRODUCTION PATH IN WAVE ONE — it has no non-test caller.
// armPartial and armSummaries in cmd/metrics_corpus.go read arm.Meta.Metrics
// directly. Wiring this fold in is the natural next step; when you do, re-run
// the mutations its tests declare (see pi_grove_test.go) instead of treating a
// green suite as evidence they still bite.
//
// An empty map remains valid. knowledge.tool_result_bytes can arrive from the
// emitter; context.off_bundle_reads is measurable for newly seeded sessions but
// this fold is still intentionally unwired until cwd/path attribution is proven.
// skills.load_order_violations has no verified input at all: nothing in the
//
//	ecosystem declares a skill load ordering that a fold could check against.
//	Skill frontmatter carries only name/domain/description. Rather than invent
//	an ordering rule so the metric has something to compute — a fabricated
//	denominator is worse than a missing metric — it is DEFERRED and emits
//	nothing.
//
// Lifted grove_metric values from the transcript ARE included when present: those
// were measured by the emitter, whatever this build can compute post-hoc.
//
// Do not add placeholder zeros to make this map non-empty.
func ArmComponentMetrics(tree *transcript.PiSessionTree, branch transcript.PiBranch, meta PiGroveMeta, cwd string) map[string]float64 {
	out := make(map[string]float64)

	// Metrics the emitter actually recorded.
	for key, value := range meta.Metrics {
		out[key] = value
	}

	// Post-hoc folds. Each contributes ONLY when genuinely measurable.
	if v := ComputeOffBundleReads(tree, branch, meta.SeedBundlePaths, cwd); v != nil {
		out[MetricContextOffBundleReads] = *v
	}

	// skills.load_order_violations: deliberately not computed. See above.

	return out
}
