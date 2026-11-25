package agentsetup

import (
	"errors"
	"fmt"
)

type removalOp struct {
	ItemResult
	kind opKind    // opNone, opRemove, or opDrop
	disk installed // opRemove: what is on disk before the removal
}

// planRecordRemoval decides what happens to one recorded item lens no longer
// wants: uninstall plans every record with it, install plans its orphans. The
// State is record-level (blocked, missing, current, modified); errors are
// unwrapped, callers add their own prefix. It reads only.
func planRecordRemoval(t Target, it Item, rec Record, own map[string]bool, force bool) (removalOp, error) {
	op := removalOp{ItemResult: ItemResult{ItemStatus: ItemStatus{Item: it, Owned: true}}}
	if err := checkLocal(it.Path); err != nil { // LoadManifest already refuses these
		return removalOp{}, err
	}

	// A symlink anywhere in the path, the path itself included: never removed through.
	detail, snippet, err := symlinkBlock(t, it.Path)
	if err != nil {
		return removalOp{}, err
	}
	if detail != "" {
		op.State, op.Detail, op.Action, op.Snippet = StateBlocked, detail, ActionBlocked, snippet
		return op, nil
	}

	disk, err := installedHash(t, it)
	if err != nil {
		return removalOp{}, err
	}
	if op.Tracked, err = t.IsTracked(it.Path); err != nil {
		return removalOp{}, err
	}

	switch {
	case lensAbsent(disk):
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

// symlinkBlock reports, as a detail and snippet, the first symlinked component
// of p (p itself included); both are "" when there is none.
func symlinkBlock(t Target, p string) (detail, snippet string, err error) {
	err = t.CheckContained(p)
	if err == nil {
		return "", "", nil
	}
	if !errors.Is(err, ErrEscapesTarget) {
		return "", "", err
	}
	comp := firstSymlink(t, p)
	detail = "symlinked parent " + comp
	if comp == p {
		detail = detailSymlink
	}
	return detail, symlinkSnippet(comp), nil
}

// removeByHandSnippet is what a developer deletes by hand from a tracked path.
func removeByHandSnippet(it Item) string {
	return fmt.Sprintf("remove the projectlens %s %q from %s by hand", it.Kind, it.ID, it.Path)
}
