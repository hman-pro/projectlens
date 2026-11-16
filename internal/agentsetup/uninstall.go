package agentsetup

import "fmt"

// UninstallOpts configures one uninstall run.
type UninstallOpts struct {
	Agents []Agent // one agent, or both for "all"
	DryRun bool
	Force  bool

	// failAt interrupts the run for tests (0 = never), with the same two steps
	// per journaled removal as InstallOpts.failAt.
	failAt int
	// onSave, when set, is called inside every journal save (tests only).
	onSave func(stage string)
}

// UninstallResult reports an uninstall run.
type UninstallResult struct {
	Items   []ItemResult // Action is "removed", "skipped", "manual", or "blocked"
	Nothing bool         // no agent in Agents had a manifest record
}

type uninstallOp struct {
	ItemResult
	kind opKind    // opNone, opRemove, or opDrop
	disk installed // opRemove: what is on disk before the removal
}

// Uninstall removes what install recorded for o.Agents, working from the
// manifest alone. Preflight (manifest writability, reading every recorded item)
// completes before anything is changed.
func Uninstall(t Target, o UninstallOpts) (UninstallResult, error) {
	if err := CheckManifestWritable(t); err != nil {
		return UninstallResult{}, err
	}
	m, err := LoadManifest(t)
	if err != nil {
		return UninstallResult{}, err
	}
	own, err := ownSiblings(t)
	if err != nil {
		return UninstallResult{}, err
	}

	var agents []Agent
	seen := map[Agent]bool{}
	for _, a := range o.Agents {
		if !seen[a] && m.Agents[a] != nil {
			agents = append(agents, a)
		}
		seen[a] = true
	}
	if len(agents) == 0 {
		return UninstallResult{Nothing: true}, nil
	}

	var ops []uninstallOp
	for _, a := range agents {
		for _, rec := range m.Agents[a].Items {
			op, err := planRemoval(t, a, rec, own, o.Force)
			if err != nil {
				return UninstallResult{}, err
			}
			ops = append(ops, op)
		}
	}
	var res UninstallResult
	for _, op := range ops {
		res.Items = append(res.Items, op.ItemResult)
	}
	if o.DryRun {
		return res, nil
	}

	j := &journal{
		t: t, m: m, name: "uninstall", failAt: o.failAt,
		skills: skillPaths(nil, m), own: own, dirsBeforeDelete: true, onSave: o.onSave,
	}
	if err := j.cleanSiblings(); err != nil {
		return UninstallResult{}, err
	}
	for _, op := range ops {
		switch op.kind {
		case opRemove:
			if err := j.remove(op.Item, op.disk.hash); err != nil {
				return UninstallResult{}, err
			}
		case opDrop:
			if err := dropAbsent(j, op); err != nil {
				return UninstallResult{}, err
			}
		}
	}
	if err := j.removeEmptyDirs(); err != nil {
		return UninstallResult{}, err
	}
	if err := j.save(nil); err != nil {
		return UninstallResult{}, err
	}
	return res, nil
}

// planRemoval decides what uninstall does with one record. It reads only.
func planRemoval(t Target, a Agent, rec Record, own map[string]bool, force bool) (uninstallOp, error) {
	it := Item{Agent: a, Kind: rec.Kind, ID: rec.ID, Path: rec.Path}
	op := uninstallOp{ItemResult: ItemResult{ItemStatus: ItemStatus{Item: it, Owned: true}}}
	if err := checkLocal(it.Path); err != nil { // LoadManifest already refuses these
		return uninstallOp{}, fmt.Errorf("uninstall %s %s: %w", it.Kind, it.ID, err)
	}

	// A symlink anywhere in the path, the path itself included: never removed through.
	fail := func(err error) (uninstallOp, error) {
		return uninstallOp{}, fmt.Errorf("uninstall %s %s: %w", it.Kind, it.ID, err)
	}
	detail, snippet, err := symlinkBlock(t, it.Path)
	if err != nil {
		return fail(err)
	}
	if detail != "" {
		op.State, op.Detail, op.Action, op.Snippet = StateBlocked, detail, ActionBlocked, snippet
		return op, nil
	}

	disk, err := installedHash(t, it)
	if err != nil {
		return fail(err)
	}
	if op.Tracked, err = t.IsTracked(it.Path); err != nil {
		return fail(err)
	}

	switch {
	case !disk.present || disk.foreign: // a foreign Codex definition means the managed block is gone
		op.State = StateMissing
		if disk.foreign {
			op.Detail = detailForeign
		}
	case matchesRecord(rec, disk):
		op.State = StateCurrent
	default:
		op.State, op.Detail = StateModified, disk.detail
		if disk.dup {
			op.Detail = detailDuplicates
		}
	}

	switch {
	case op.State == StateMissing:
		// Nothing of lens's is there: drop the record, even for a tracked path.
		op.Action, op.kind = ActionRemoved, opDrop
	case op.Tracked:
		op.Action, op.Snippet = ActionManual, removeByHandSnippet(it)
	case op.State == StateModified && !force:
		op.Action = ActionSkipped
	case blockForeignSibling(t, own, it, &op.ItemResult):
	default:
		op.Action, op.kind, op.disk = ActionRemoved, opRemove, disk
	}
	return op, nil
}

// dropAbsent drops the record of an item that is no longer on disk. For a merged
// file that is untracked, the remover runs too: it writes nothing when the entry
// is absent, and deletes a file install created that a crash left empty.
func dropAbsent(j *journal, op uninstallOp) error {
	if op.Kind != KindSkill && !op.Tracked {
		if err := j.removeItem(op.Item); err != nil {
			return err
		}
	}
	j.dropRecord(op.Agent, op.Kind, op.ID)
	return nil
}
