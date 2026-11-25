package agentsetup

import (
	"errors"
	"strings"
)

// Detail strings shown with a status.
const (
	detailSymlink    = "symlink"
	detailForeign    = "defined outside the managed block"
	detailDuplicates = "duplicate entries"
	detailSymlinkIn  = "contains a symlink"

	// hashSymlinkIn stands in for the hash of a skill directory holding a symlink.
	// It is not hex, so it never equals a canonical or recorded hash.
	hashSymlinkIn = "unhashable: contains a symlink"
)

// ItemStatus is the evaluated state of one item.
type ItemStatus struct {
	Item
	State   State  `json:"state"`
	Owned   bool   `json:"owned"` // the manifest has a record for this item
	Tracked bool   `json:"tracked"`
	Detail  string `json:"detail"` // e.g. "symlink", "symlinked parent .agents", "defined outside the managed block"
}

// installed is what installedHash finds on disk for one item.
type installed struct {
	hash       string
	present    bool
	detail     string // skill: "symlink" (the directory is a link) or "contains a symlink"
	dup        bool   // hook: the ID appears more than once
	foreign    bool   // Codex MCP: defined outside the managed block
	foreignURL string // Codex MCP: the foreign definition's url, when a plain string
}

// installedHash reads the owned entry from disk for one item. The caller has
// already checked that no parent of it.Path is a symlink, and that it.Path is
// not a symlink unless it is a skill directory, which is never read through.
func installedHash(t Target, it Item) (installed, error) {
	abs := t.Abs(it.Path)
	switch it.Kind {
	case KindSkill:
		if !t.Exists(it.Path) {
			return installed{}, nil
		}
		if t.IsSymlink(it.Path) {
			return installed{present: true, detail: detailSymlink}, nil
		}
		h, err := HashDir(abs)
		if errors.Is(err, ErrSymlinkInSkill) {
			return installed{hash: hashSymlinkIn, present: true, detail: detailSymlinkIn}, nil
		}
		if err != nil {
			return installed{}, err
		}
		return installed{hash: h, present: true}, nil

	case KindHook:
		hooks, err := ReadHooks(abs)
		if err != nil {
			return installed{}, err
		}
		ih, ok := hooks[it.ID]
		if !ok {
			return installed{}, nil
		}
		return installed{hash: ih.Hash(), present: true, dup: ih.Dup}, nil

	case KindMCP:
		if it.Agent == AgentCodex {
			st, err := ReadCodexConfig(abs)
			if err != nil {
				return installed{}, err
			}
			switch {
			case st.Foreign && st.BlockPresent:
				// Two definitions of one table: Codex cannot load this file.
				return installed{}, invalidTOML(abs, errors.New("mcp_servers.projectlens is defined both inside and outside the managed block"))
			case st.Foreign:
				in := installed{present: true, foreign: true, foreignURL: st.ForeignURL}
				if st.ForeignURL != "" {
					in.hash = MCPHash(st.ForeignURL)
				}
				return in, nil
			case st.BlockPresent:
				return installed{hash: st.BlockHash, present: true}, nil
			}
			return installed{}, nil
		}
		h, present, err := ReadMCPJSON(abs)
		if err != nil {
			return installed{}, err
		}
		return installed{hash: h, present: present}, nil
	}
	return installed{}, errors.New("unknown item kind " + string(it.Kind))
}

// firstSymlink returns the first component of rel, from the root down, that is a symlink.
func firstSymlink(t Target, rel string) string {
	parts := strings.Split(rel, "/")
	for i := range parts {
		if p := strings.Join(parts[:i+1], "/"); t.IsSymlink(p) {
			return p
		}
	}
	return rel
}

// Evaluate returns one status per canonical item, then one per orphaned record.
// It reads only; neither the manifest nor the disk is modified.
func Evaluate(t Target, a Agent, canon []Canon, m *Manifest) ([]ItemStatus, error) {
	out := make([]ItemStatus, 0, len(canon))
	known := map[Kind]map[string]bool{}
	for _, c := range canon {
		if known[c.Kind] == nil {
			known[c.Kind] = map[string]bool{}
		}
		known[c.Kind][c.ID] = true
		rec, owned := m.Record(a, c.Kind, c.ID)
		st, err := evaluateItem(t, c, rec, owned)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if m != nil && m.Agents[a] != nil {
		for _, r := range m.Agents[a].Items {
			if known[r.Kind][r.ID] {
				continue
			}
			out = append(out, ItemStatus{
				Item:  Item{Agent: a, Kind: r.Kind, ID: r.ID, Path: r.Path},
				State: StateOrphaned,
				Owned: true,
			})
		}
	}
	return out, nil
}

// evaluateItem applies the ordered state rules to one canonical item.
func evaluateItem(t Target, c Canon, rec Record, owned bool) (ItemStatus, error) {
	st := ItemStatus{Item: c.Item, Owned: owned}
	result := func(s State, detail string) (ItemStatus, error) {
		st.State, st.Detail = s, detail
		return st, nil
	}

	// 1. A symlinked parent, or a symlinked file for anything but a skill.
	if err := t.CheckParentsContained(c.Path); err != nil {
		if !errors.Is(err, ErrEscapesTarget) {
			return ItemStatus{}, err
		}
		return result(StateBlocked, "symlinked parent "+firstSymlink(t, c.Path))
	}
	if c.Kind != KindSkill && t.IsSymlink(c.Path) {
		return result(StateBlocked, "symlinked file "+c.Path)
	}

	disk, err := installedHash(t, c.Item)
	if err != nil {
		return ItemStatus{}, err
	}

	// 2. Tracked by git: never written; the manifest is not consulted.
	tracked, err := t.IsTracked(c.Path)
	if err != nil {
		return ItemStatus{}, err
	}
	if tracked {
		st.Tracked = true
		if disk.present && disk.hash == c.Hash {
			return result(StateCurrent, "")
		}
		return result(StateManual, disk.detail)
	}

	// 3. Symlinked skill directory.
	if disk.detail == detailSymlink {
		return result(StateUnmanaged, detailSymlink)
	}

	// 4. Codex MCP defined outside the managed block.
	if disk.foreign {
		if disk.foreignURL == c.URL {
			return result(StateCurrent, "")
		}
		return result(StateBlocked, detailForeign)
	}

	// 5. Not present. A first write that never happened, or a remove that
	// finished, is not a local removal.
	if !disk.present {
		journaled := owned && rec.Pending != nil &&
			((rec.Pending.Op == "write" && rec.Hash == "") || rec.Pending.Op == "remove")
		if owned && !journaled && c.Kind == KindHook {
			return result(StateRemovedLocally, "")
		}
		return result(StateMissing, "")
	}

	// 5a. A write that finished before the journal was cleared counts as recorded.
	recorded := rec.Hash
	if rec.Pending != nil && rec.Pending.Op == "write" && disk.hash == rec.Pending.Hash {
		recorded = rec.Pending.Hash
	}

	switch {
	case c.Kind == KindHook && disk.dup: // 6
		// A forced dedupe journals the duplicated content as Hash; if it was
		// interrupted, the rewrite it promised is still owed, so it is stale.
		if owned && rec.Pending != nil && rec.Pending.Op == "write" && disk.hash == rec.Hash {
			return result(StateStale, "")
		}
		return result(StateModified, detailDuplicates)
	case disk.hash == c.Hash: // 7
		return result(StateCurrent, "")
	case !owned: // 8
		return result(StateUnmanaged, disk.detail)
	case disk.hash == recorded: // 9
		return result(StateStale, "")
	}
	return result(StateModified, disk.detail) // 10
}
