package transcript

import (
	"bufio"
	"encoding/json"
	"io"
)

// PiSessionTree is a parsed pi session file: the entry tree, plus the pointers
// needed to reconstruct any branch through it.
//
// pi session files are append-only TREES. Every entry carries id/parentId, and
// editing or retrying inside a session moves the leaf pointer back to an earlier
// entry so the next append starts a new branch. A file therefore holds one tree
// with N leaves, of which only one is "active".
//
// Two different questions can be asked of such a file and they have different
// right answers:
//
//   - "What conversation happened?" -> ActivePath. This is what renders, and
//     what NormalizePiFile has always returned.
//   - "What branches exist?" -> Branches. Needed to attribute per-arm work in a
//     file that genuinely forked.
//
// Raw entries stay unexported: only IDs and UnifiedEntry cross the package
// boundary, so the pi file format is not re-exported as public surface.
type PiSessionTree struct {
	byID     map[string]*piFileEntry
	children map[string][]string
	// roots holds entries with no parent, in file order. An entry whose
	// parentId names an id absent from the file is an ORPHAN and is treated as
	// a root: that matches the existing walk, where byID[*cur.ParentID]
	// returning nil simply terminates the walk rather than erroring.
	roots []string
	// lastID is the final surviving line in FILE order. pi's own
	// SessionManager._buildIndex assigns leafId the same way, so this preserves
	// the active-path semantics exactly.
	lastID string
	// order preserves file order, which insertion-orders children.
	order []*piFileEntry
}

// PiBranch is one root-to-leaf path through the tree.
type PiBranch struct {
	// LeafID is the terminal entry of this branch.
	LeafID string
	// PathIDs runs root -> leaf.
	PathIDs []string
	// ForkPointID is the deepest entry on this path with more than one child —
	// the point where this branch diverged from its siblings. Empty when the
	// tree never forks along this path.
	ForkPointID string
}

// ParsePiSessionTree reads a pi session JSONL stream into a tree.
//
// The scan loop replicates NormalizePiFile's tolerances EXACTLY, because
// NormalizePiFile is implemented on top of this function and its rendered
// output must not move. There are four predicates and all four are load-bearing:
//
//  1. empty line -> skip
//  2. unmarshal error -> skip (live files can end mid-write, torn)
//  3. type == "session" (the header) OR empty id -> skip. The header carries a
//     UUIDv7 id and no parentId; admitting it would make it a spurious root.
//  4. no surviving entries -> (nil, nil), NOT an error. An empty or
//     header-only file is a legitimate empty transcript.
//
// The buffer sizing matters too: a 4MB max token guards long lines, and
// shrinking it would silently drop exactly the large sessions worth measuring.
func ParsePiSessionTree(r io.Reader) (*PiSessionTree, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	t := &PiSessionTree{
		byID:     make(map[string]*piFileEntry),
		children: make(map[string][]string),
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw piFileEntry
		if err := json.Unmarshal(line, &raw); err != nil {
			continue // tolerate torn/partial lines (live files)
		}
		if raw.Type == "session" || raw.ID == "" {
			continue
		}
		entry := raw
		t.order = append(t.order, &entry)
		t.byID[entry.ID] = &entry
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(t.order) == 0 {
		return nil, nil
	}

	// Second pass: link. Done after the full scan so a forward reference (a
	// child written before its parent) still resolves.
	for _, entry := range t.order {
		if entry.ParentID == nil || *entry.ParentID == "" {
			t.roots = append(t.roots, entry.ID)
			continue
		}
		if _, ok := t.byID[*entry.ParentID]; !ok {
			// Dangling parent: an orphan is a root.
			t.roots = append(t.roots, entry.ID)
			continue
		}
		t.children[*entry.ParentID] = append(t.children[*entry.ParentID], entry.ID)
	}

	t.lastID = t.order[len(t.order)-1].ID
	return t, nil
}

// ActivePath returns the branch NormalizePiFile renders: walk parentId up from
// the last line in file order, then reverse.
//
// This is byte-for-byte the original behaviour, cycle guard included: a
// malformed file whose parent pointers loop truncates that walk rather than
// erroring or hanging.
//
// NOTE the leaf rule is "last line in FILE order", not "deepest leaf in the
// tree". pi's _buildIndex does the same, so this is consistent with how pi
// itself resumes — but it means that after a reload of a genuinely branched
// file, the active path is whichever branch happens to be physically last. Code
// that assumes "the leaf is the arm I was just on" is wrong for such files.
func (t *PiSessionTree) ActivePath() PiBranch {
	if t == nil || t.lastID == "" {
		return PiBranch{}
	}
	return t.branchFromLeaf(t.lastID)
}

// branchFromLeaf walks leaf -> root and returns the reversed path.
func (t *PiSessionTree) branchFromLeaf(leafID string) PiBranch {
	var reversed []string
	seen := make(map[string]bool)
	for cur := t.byID[leafID]; cur != nil; {
		if seen[cur.ID] {
			break // cycle guard (malformed file)
		}
		seen[cur.ID] = true
		reversed = append(reversed, cur.ID)
		if cur.ParentID == nil || *cur.ParentID == "" {
			break
		}
		cur = t.byID[*cur.ParentID]
	}

	path := make([]string, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		path = append(path, reversed[i])
	}
	return PiBranch{
		LeafID:      leafID,
		PathIDs:     path,
		ForkPointID: t.deepestForkOn(path),
	}
}

// deepestForkOn returns the last entry on the path that has more than one
// child, or "" if the path never forks.
func (t *PiSessionTree) deepestForkOn(path []string) string {
	fork := ""
	for _, id := range path {
		if len(t.children[id]) > 1 {
			fork = id
		}
	}
	return fork
}

// Leaves returns every entry with no children, in file order.
func (t *PiSessionTree) Leaves() []string {
	if t == nil {
		return nil
	}
	var leaves []string
	for _, entry := range t.order {
		if len(t.children[entry.ID]) == 0 {
			leaves = append(leaves, entry.ID)
		}
	}
	return leaves
}

// Branches returns one branch per leaf, in file order.
//
// A single-arm (linear) file yields exactly one branch, equal to ActivePath.
func (t *PiSessionTree) Branches() []PiBranch {
	if t == nil {
		return nil
	}
	leaves := t.Leaves()
	branches := make([]PiBranch, 0, len(leaves))
	for _, leaf := range leaves {
		branches = append(branches, t.branchFromLeaf(leaf))
	}
	return branches
}

// Arms returns the branches passing through forkID, as SUFFIXES: each arm's
// PathIDs begin at forkID rather than at the tree root.
//
// ⚠ This is NOT how sweep arms are attributed. Under the fork-per-session
// ruling a sweep arm is a whole FILE — its own ActivePath — because pi's RPC
// wire protocol exposes `fork` (which creates a new sibling session file) and
// no in-place branch method at all. Nothing in the sweep produces a multi-arm
// file.
//
// It is kept because multi-arm files are nonetheless real: in-place
// branch/navigateTree and the TUI's /tree command genuinely produce them, and a
// corpus scanner that cannot read such a file would silently mis-read
// third-party sessions. Model pi's format honestly; just do not build sweep
// attribution on this method.
func (t *PiSessionTree) Arms(forkID string) []PiBranch {
	if t == nil {
		return nil
	}
	if _, ok := t.byID[forkID]; !ok {
		return nil
	}

	var arms []PiBranch
	for _, branch := range t.Branches() {
		idx := -1
		for i, id := range branch.PathIDs {
			if id == forkID {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		suffix := make([]string, len(branch.PathIDs)-idx)
		copy(suffix, branch.PathIDs[idx:])
		arms = append(arms, PiBranch{
			LeafID:      branch.LeafID,
			PathIDs:     suffix,
			ForkPointID: forkID,
		})
	}
	return arms
}

// Normalize maps a branch's entries through the existing per-entry normalizer,
// yielding conversation-ordered UnifiedEntries.
//
// It reuses normalizePiEntry rather than duplicating the type switch, so a
// branch renders identically to the way the active path always has.
func (t *PiSessionTree) Normalize(b PiBranch) []UnifiedEntry {
	if t == nil {
		return nil
	}
	entries := make([]UnifiedEntry, 0, len(b.PathIDs))
	for _, id := range b.PathIDs {
		raw, ok := t.byID[id]
		if !ok {
			continue
		}
		if entry := normalizePiEntry(raw); entry != nil {
			entries = append(entries, *entry)
		}
	}
	return entries
}

// PiCustomEntry is a `custom` (extension state) entry on a branch.
//
// This is the ONLY raw pi payload that crosses the package boundary, and it
// crosses as opaque JSON. Interpreting Data — unmarshalling it into typed
// structs, validating it, recomputing hashes — is deliberately somebody else's
// job (pkg/metrics), so that pkg/transcript stays free of eval/pkg/record.
// pkg/transcript is imported by ten packages; pkg/metrics is where the record
// schema already lives, so the dependency belongs there rather than under every
// normalizer.
type PiCustomEntry struct {
	ID         string
	CustomType string
	Data       json.RawMessage
}

// CustomEntriesOn returns the custom entries along a branch, root -> leaf.
//
// Order is load-bearing for the caller: when a session file carries both a
// copied seed stamp and a variant stamp, "last on the path wins" is what makes
// the variant take effect.
func (t *PiSessionTree) CustomEntriesOn(b PiBranch) []PiCustomEntry {
	if t == nil {
		return nil
	}
	var out []PiCustomEntry
	for _, raw := range t.entriesOn(b) {
		if raw.Type != "custom" {
			continue
		}
		out = append(out, PiCustomEntry{
			ID:         raw.ID,
			CustomType: raw.CustomType,
			Data:       raw.Data,
		})
	}
	return out
}

// EntryTypesOn returns each entry's type along a branch, root -> leaf.
//
// Callers use this to spot structural facts about an arm that the rendered
// transcript hides — most importantly a "compaction" entry, which means the
// arm's prefix is no longer byte-identical to its siblings'.
func (t *PiSessionTree) EntryTypesOn(b PiBranch) []string {
	if t == nil {
		return nil
	}
	types := make([]string, 0, len(b.PathIDs))
	for _, raw := range t.entriesOn(b) {
		types = append(types, raw.Type)
	}
	return types
}

// ModelsOn returns the distinct models named by assistant messages along a
// branch, in first-seen order.
//
// pi records the model PER MESSAGE, so a single arm can legitimately span
// several models (a mid-session /model switch). More than one entry here means
// the arm was not a single controlled condition.
func (t *PiSessionTree) ModelsOn(b PiBranch) []string {
	if t == nil {
		return nil
	}
	var models []string
	seen := make(map[string]bool)
	for _, raw := range t.entriesOn(b) {
		if raw.Type != "message" || len(raw.Message) == 0 {
			continue
		}
		var msg piMessage
		if err := json.Unmarshal(raw.Message, &msg); err != nil {
			continue
		}
		if msg.Model == "" || seen[msg.Model] {
			continue
		}
		seen[msg.Model] = true
		models = append(models, msg.Model)
	}
	return models
}

// entriesOn returns the raw entries along a branch, root -> leaf. Unexported:
// piFileEntry does not cross the package boundary.
func (t *PiSessionTree) entriesOn(b PiBranch) []*piFileEntry {
	if t == nil {
		return nil
	}
	out := make([]*piFileEntry, 0, len(b.PathIDs))
	for _, id := range b.PathIDs {
		if raw, ok := t.byID[id]; ok {
			out = append(out, raw)
		}
	}
	return out
}
