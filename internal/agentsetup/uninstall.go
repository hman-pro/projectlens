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

	// Set only with Nothing:
	Unmanaged   []string // no agent installed at all: created paths kept, still excluded (see Manifest.UnreferencedPaths)
	Recoverable []string // no manifest, but the block hides these existing paths (see RecoverablePaths)
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
		return uninstallNothing(t, m, own, o)
	}

	var ops []removalOp
	for _, a := range agents {
		for _, rec := range m.Agents[a].Items {
			it := Item{Agent: a, Kind: rec.Kind, ID: rec.ID, Path: rec.Path}
			op, err := planRecordRemoval(t, it, rec, own, o.Force)
			if err != nil {
				return UninstallResult{}, fmt.Errorf("uninstall %s %s: %w", rec.Kind, rec.ID, err)
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

// uninstallNothing handles a run where no requested agent has a record. It
// still finishes the lifecycle: a manifest kept alive only by CreatedPaths is
// pruned, and deleted with its block once those paths are gone. Without a
// manifest, it reports the paths the block still hides, for recovery.
func uninstallNothing(t Target, m *Manifest, own map[string]bool, o UninstallOpts) (UninstallResult, error) {
	if !o.DryRun {
		// A run that crashed after deleting the manifest may have left patterns of
		// paths that are gone; with no manifest, nothing else can remove them.
		if err := pruneStaleBlock(t); err != nil {
			return UninstallResult{}, err
		}
		if t.Exists(ManifestPath) {
			j := &journal{
				t: t, m: m, name: "uninstall", failAt: o.failAt,
				skills: skillPaths(nil, m), own: own, dirsBeforeDelete: true, onSave: o.onSave,
			}
			if err := j.cleanSiblings(); err != nil {
				return UninstallResult{}, err
			}
			if err := j.removeEmptyDirs(); err != nil {
				return UninstallResult{}, err
			}
			if err := j.save(nil); err != nil {
				return UninstallResult{}, err
			}
		}
	}
	recoverable, err := RecoverablePaths(t)
	if err != nil {
		return UninstallResult{}, err
	}
	res := UninstallResult{Nothing: true, Recoverable: recoverable}
	if len(m.Agents) == 0 {
		res.Unmanaged = m.UnreferencedPaths()
	}
	return res, nil
}

// dropAbsent drops the record of an item that is no longer on disk. For a merged
// file that is untracked, the remover runs too: it writes nothing when the entry
// is absent, and deletes a file install created that a crash left empty.
func dropAbsent(j *journal, op removalOp) error {
	if op.Kind != KindSkill && !op.Tracked {
		if err := j.removeItem(op.Item); err != nil {
			return err
		}
	}
	j.dropRecord(op.Agent, op.Kind, op.ID)
	return nil
}
