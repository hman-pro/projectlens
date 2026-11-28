package agentsetup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Action is what install did, or would do, with one item.
type Action string

const (
	ActionWritten Action = "written"
	ActionCurrent Action = "current"
	ActionUpdated Action = "updated"
	ActionAdopted Action = "adopted"
	ActionSkipped Action = "skipped"
	ActionManual  Action = "manual"
	ActionBlocked Action = "blocked"
	ActionRemoved Action = "removed"
)

const (
	detailPreexisting = "pre-existing, not managed"
	detailRecovered   = "recovered path; content differs from canonical"
	snippetForeign    = "remove your existing mcp_servers.projectlens definition from .codex/config.toml, then re-run"

	// A skill is copied into ".<name>.lens-tmp" and renamed into place; the
	// copy it replaces is first renamed aside to ".<name>.lens-old" and removed
	// last. The real path is therefore only ever whole-old, absent, or whole-new.
	skillTempSuffix  = ".lens-tmp"
	skillAsideSuffix = ".lens-old"
)

// InstallOpts configures one install run.
type InstallOpts struct {
	Agent  Agent
	URL    string // already resolved
	DryRun bool
	Force  bool

	// failAt interrupts the run for tests (0 = never). Every journaled operation
	// has two steps, counted across the run: 2k-1 is "journal saved, item not
	// yet changed", 2k is "item changed, final save not yet done".
	failAt int
	// onSave, when set, is called inside every journal save (tests only).
	onSave func(stage string)
}

// ItemResult is one item's state before install and what install did with it.
type ItemResult struct {
	ItemStatus        // the state before install
	Action     Action // what install did, or with DryRun would do
	Snippet    string // for "manual" and "blocked": what to do by hand
}

// InstallResult reports an install run.
type InstallResult struct {
	Items          []ItemResult
	ExcludePaths   []string // the full exclude block after this run
	WorktreeNotice bool     // target is a linked worktree and the exclude block is non-empty
}

// opKind is the change install makes for one item.
type opKind int

const (
	opNone   opKind = iota
	opWrite         // journaled write of the canonical content
	opRecord        // record canonical content already on disk (no write)
	opRemove        // journaled removal of an orphan
	opDrop          // drop the record of an orphan that is already gone
)

type installOp struct {
	ItemResult
	kind  opKind
	canon Canon     // opWrite, opRecord
	disk  installed // opWrite, opRemove: what is on disk before the change
}

// Install wires an agent into t. Preflight (manifest writability, parsing and
// evaluating every item) completes before anything is changed.
func Install(t Target, assetsDir string, o InstallOpts) (InstallResult, error) {
	if err := CheckManifestWritable(t); err != nil {
		return InstallResult{}, err
	}
	canon, err := LoadCanonical(assetsDir, o.Agent, o.URL)
	if err != nil {
		return InstallResult{}, fmt.Errorf("load canonical assets: %w", err)
	}
	m, err := loadInstallManifest(t)
	if err != nil {
		return InstallResult{}, err
	}
	own, err := ownSiblings(t)
	if err != nil {
		return InstallResult{}, err
	}
	statuses, err := Evaluate(t, o.Agent, canon, m)
	if err != nil {
		return InstallResult{}, fmt.Errorf("evaluate %s: %w", o.Agent, err)
	}
	ops, err := planInstall(t, o, canon, statuses, m, own)
	if err != nil {
		return InstallResult{}, err
	}

	var res InstallResult
	for _, op := range ops {
		res.Items = append(res.Items, op.ItemResult)
	}
	skills := skillPaths(canon, m)
	if o.DryRun {
		res.ExcludePaths = projectExclude(t, o.Agent, m, ops, skills)
	} else {
		// A run that crashed after deleting the manifest may have left patterns
		// of paths that are gone.
		if err := pruneStaleBlock(t); err != nil {
			return InstallResult{}, err
		}
		r := &installer{
			journal: &journal{t: t, m: m, name: "install", failAt: o.failAt, skills: skills, own: own, onSave: o.onSave},
			agent:   o.Agent, url: o.URL, force: o.Force,
		}
		if err := r.run(ops); err != nil {
			return InstallResult{}, err
		}
		if res.ExcludePaths, err = t.ExcludeBlock(); err != nil {
			return InstallResult{}, err
		}
	}
	res.WorktreeNotice = t.LinkedWorktree && len(res.ExcludePaths) > 0
	return res, nil
}

// loadInstallManifest loads the manifest. When the file is missing, every path
// in this work tree's exclude block that still exists becomes a CreatedPaths
// entry, so its exclusion survives the loss of the manifest.
func loadInstallManifest(t Target) (*Manifest, error) {
	existed := t.Exists(ManifestPath)
	m, err := LoadManifest(t)
	if err != nil {
		return nil, err
	}
	if existed {
		return m, nil
	}
	recovered, err := RecoverCreated(t)
	if err != nil {
		return nil, err
	}
	for _, p := range recovered {
		if !isSkillSibling(p) {
			m.CreatedPaths = append(m.CreatedPaths, p)
		}
	}
	return m, nil
}

// planInstall decides the action for every evaluated item. It reads only. own
// holds the skill siblings lens created (see ownSiblings).
func planInstall(t Target, o InstallOpts, canon []Canon, sts []ItemStatus, m *Manifest, own map[string]bool) ([]installOp, error) {
	byItem := make(map[string]Canon, len(canon))
	for _, c := range canon {
		byItem[itemKeyOf(c.Kind, c.ID)] = c
	}
	ops := make([]installOp, 0, len(sts))
	for _, st := range sts {
		var op installOp
		var err error
		c, ok := byItem[itemKeyOf(st.Kind, st.ID)]
		switch {
		case ok && st.State != StateOrphaned && c.Item == st.Item:
			op, err = planCanonical(t, o, c, st, m, own)
		case !ok && st.State == StateOrphaned:
			op, err = planOrphan(t, o, st, m, own)
		default:
			err = fmt.Errorf("plan %s %s: status %q does not match the canonical items", st.Kind, st.ID, st.State)
		}
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func planCanonical(t Target, o InstallOpts, c Canon, st ItemStatus, m *Manifest, own map[string]bool) (installOp, error) {
	op := installOp{ItemResult: ItemResult{ItemStatus: st}, canon: c}
	set := func(a Action, k opKind) (installOp, error) {
		op.Action, op.kind = a, k
		return op, nil
	}
	write := func(a Action) (installOp, error) {
		if blockForeignSibling(t, own, c.Item, &op.ItemResult) {
			return op, nil
		}
		disk, err := installedHash(t, c.Item)
		if err != nil {
			return installOp{}, err
		}
		op.disk = disk
		return set(a, opWrite)
	}
	// An unrecorded item in a path install created is install's own (recovery).
	recovered := !st.Owned && m.Created(c.Path)

	switch st.State {
	case StateBlocked:
		op.Snippet = blockedSnippet(st)
		return set(ActionBlocked, opNone)
	case StateManual:
		op.Snippet = manualSnippet(c)
		return set(ActionManual, opNone)
	case StateCurrent:
		switch {
		case st.foreign || (st.Tracked && !st.Owned):
			// Never adopted: uninstall could not remove it.
		case st.Owned:
			return set(ActionCurrent, opRecord) // also completes a finished journal
		case recovered:
			return set(ActionCurrent, opRecord)
		case o.Force:
			return set(ActionAdopted, opRecord)
		}
		if !st.Owned {
			op.Detail = detailPreexisting
		}
		return set(ActionCurrent, opNone)
	case StateMissing:
		return write(ActionWritten)
	case StateStale:
		return write(ActionUpdated)
	case StateRemovedLocally:
		if o.Force {
			return write(ActionWritten)
		}
		return set(ActionSkipped, opNone)
	case StateModified, StateUnmanaged:
		if recovered && st.Detail == "" {
			op.Detail = detailRecovered
		}
		if o.Force {
			return write(ActionUpdated)
		}
		return set(ActionSkipped, opNone)
	}
	return installOp{}, fmt.Errorf("install %s %s: unexpected state %q", c.Kind, c.ID, st.State)
}

// planOrphan decides what to do with a record whose item is no longer canonical.
// The decision is the one uninstall makes (planRecordRemoval); install reports
// the State as orphaned whatever the disk holds.
func planOrphan(t Target, o InstallOpts, st ItemStatus, m *Manifest, own map[string]bool) (installOp, error) {
	rec, _ := m.Record(o.Agent, st.Kind, st.ID)
	r, err := planRecordRemoval(t, st.Item, rec, own, o.Force)
	if err != nil {
		return installOp{}, fmt.Errorf("orphan %s %s: %w", st.Kind, st.ID, err)
	}
	r.State = StateOrphaned
	return installOp{ItemResult: r.ItemResult, kind: r.kind, disk: r.disk}, nil
}

func symlinkSnippet(comp string) string {
	return comp + " is a symlink; lens will not write through it"
}

func blockedSnippet(st ItemStatus) string {
	switch {
	case st.foreign:
		return snippetForeign
	case st.symlinkAt != "":
		return symlinkSnippet(st.symlinkAt)
	}
	return ""
}

// manualSnippet is what a developer adds by hand to a tracked destination.
func manualSnippet(c Canon) string {
	switch {
	case c.Kind == KindSkill:
		return "copy " + c.SkillSrc + " to " + c.Path
	case c.Kind == KindHook:
		return indentJSON(map[string]any{"hooks": map[string]any{c.Hook.Event: []any{
			map[string]any{"matcher": c.Hook.Matcher, "hooks": []any{
				map[string]string{"type": c.Hook.Type, "command": c.Hook.Command}}},
		}}})
	case c.Agent == AgentCodex:
		return codexBlock(c.URL)
	}
	return indentJSON(map[string]any{"mcpServers": map[string]any{
		mcpServerName: map[string]string{"type": "http", "url": c.URL}}})
}

func indentJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v) // maps of strings always encode
	return strings.TrimRight(buf.String(), "\n")
}

// skillPaths is every skill path install knows of, so a CreatedPaths entry that
// does not exist yet still gets a directory pattern.
func skillPaths(canon []Canon, m *Manifest) map[string]bool {
	out := map[string]bool{}
	for _, c := range canon {
		if c.Kind == KindSkill {
			out[c.Path] = true
		}
	}
	for _, ar := range m.Agents {
		if ar == nil {
			continue
		}
		for _, r := range ar.Items {
			if r.Kind == KindSkill {
				out[r.Path] = true
			}
		}
	}
	return out
}

// projectExclude computes, without writing, the exclude block a real run would leave.
func projectExclude(t Target, a Agent, m *Manifest, ops []installOp, skills map[string]bool) []string {
	created, _ := m.livePaths(t)
	set := map[string]bool{}
	for _, p := range created {
		set[p] = true
	}
	records := map[string]bool{}
	others := false
	for agent, ar := range m.Agents {
		if ar == nil {
			continue
		}
		for _, r := range ar.Items {
			if agent == a {
				records[itemKeyOf(r.Kind, r.ID)] = true
			} else {
				others = true
			}
		}
	}
	for _, op := range ops {
		key := itemKeyOf(op.Kind, op.ID)
		switch op.kind {
		case opWrite:
			if !t.Exists(op.Path) {
				set[op.Path] = true
			}
			records[key] = true
		case opRecord:
			records[key] = true
		case opRemove, opDrop:
			if op.Kind == KindSkill {
				delete(set, op.Path)
			}
			delete(records, key)
		}
	}
	var paths []string
	for p := range set {
		paths = append(paths, p)
	}
	if !others && len(records) == 0 && len(paths) == 0 {
		return nil
	}
	return excludePatterns(t, paths, skills, true, nil)
}

func itemKeyOf(k Kind, id string) string { return string(k) + "\x00" + id }

// installer applies planned operations for one agent, journaling each change.
type installer struct {
	*journal
	agent Agent
	url   string
	force bool
}

func (r *installer) run(ops []installOp) error {
	// Leftovers of a crash, for this agent's skills and every recorded skill of
	// any agent: once the block is rewritten they would no longer be excluded.
	if err := r.cleanSiblings(); err != nil {
		return err
	}
	for _, op := range ops {
		var err error
		switch op.kind {
		case opWrite:
			err = r.write(op)
		case opRecord:
			r.setRecord(Record{Kind: op.canon.Kind, ID: op.canon.ID, Path: op.canon.Path, Hash: op.canon.Hash})
		case opRemove:
			err = r.remove(op)
		case opDrop:
			r.dropRecord(r.agent, op.Kind, op.ID)
		}
		if err != nil {
			return err
		}
	}
	return r.save(nil)
}

// setRecord stores rec for this agent and sets the agent's URL.
func (r *installer) setRecord(rec Record) {
	r.putRecord(r.agent, rec)
	r.m.Agents[r.agent].URL = r.url
}

// checkDest is the containment check at the point of use. replaceLink allows
// the destination itself to be a symlink (a skill link replaced with --force).
func (r *installer) checkDest(rel string, replaceLink bool) error {
	check := r.t.CheckContained
	if replaceLink {
		check = r.t.CheckParentsContained
	}
	if err := check(rel); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}

// missingParents lists the ancestors of rel, from the root down, that do not exist.
func (r *installer) missingParents(rel string) []string {
	var out []string
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		if p := strings.Join(parts[:i], "/"); !r.t.Exists(p) {
			out = append(out, p)
		}
	}
	return out
}

// write journals, writes, and records one canonical item.
func (r *installer) write(op installOp) error {
	c := op.canon
	replaceLink := c.Kind == KindSkill && r.t.IsSymlink(c.Path)
	if replaceLink && !r.force {
		return fmt.Errorf("write %s: %w", c.Path, ErrEscapesTarget)
	}
	if err := r.checkDest(c.Path, replaceLink); err != nil {
		return err
	}

	rec, _ := r.m.Record(r.agent, c.Kind, c.ID)
	rec.Kind, rec.ID, rec.Path = c.Kind, c.ID, c.Path
	rec.Hash = "" // nothing on disk before the first write
	if op.disk.present {
		rec.Hash = op.disk.hash // what is on disk now
	}
	rec.Pending = &Pending{Op: "write", Hash: c.Hash}
	r.setRecord(rec)
	if !r.t.Exists(c.Path) {
		r.m.CreatedPaths = append(r.m.CreatedPaths, c.Path)
		r.m.CreatedDirs = append(r.m.CreatedDirs, r.missingParents(c.Path)...)
	}
	var extra []string
	if c.Kind == KindSkill {
		extra = skillSiblingPatterns(c.Path)
	}
	if err := r.save(extra); err != nil {
		return err
	}
	if err := r.step(); err != nil {
		return err
	}

	if err := r.writeItem(c, replaceLink); err != nil {
		return err
	}
	if err := r.step(); err != nil {
		return err
	}

	rec.Hash, rec.Pending = c.Hash, nil
	r.setRecord(rec)
	return r.save(nil)
}

func (r *installer) writeItem(c Canon, replaceLink bool) error {
	if err := r.checkDest(c.Path, replaceLink); err != nil {
		return err
	}
	abs := r.t.Abs(c.Path)
	var err error
	switch {
	case c.Kind == KindSkill:
		err = r.writeSkill(c, replaceLink)
	case c.Kind == KindHook:
		err = WriteHook(r.t.stageDir(), abs, c.Hook)
	case c.Agent == AgentCodex:
		err = WriteCodexBlock(r.t.stageDir(), abs, c.URL)
	default:
		err = WriteMCPJSON(r.t.stageDir(), abs, c.URL)
	}
	if err != nil {
		return fmt.Errorf("write %s %s: %w", c.Kind, c.ID, err)
	}
	return nil
}

// writeSkill copies the skill into a temp sibling, moves the old directory aside
// (or, with --force, removes the symlink itself), renames the copy into place,
// and finally removes the aside copy.
func (r *installer) writeSkill(c Canon, replaceLink bool) error {
	tmp, aside := skillTempPath(c.Path), skillAsidePath(c.Path)
	if err := r.removeSkillSiblings(c.Path, true); err != nil {
		return err
	}
	abs, absTmp, absAside := r.t.Abs(c.Path), r.t.Abs(tmp), r.t.Abs(aside)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", path.Dir(c.Path), err)
	}
	if err := r.checkDest(tmp, false); err != nil {
		return err
	}
	if err := CopyDir(c.SkillSrc, absTmp); err != nil {
		err = fmt.Errorf("copy skill: %w", err)
		if rmErr := os.RemoveAll(absTmp); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("clean up %s: %w", tmp, rmErr))
		}
		return err
	}
	movedAside := false
	if r.t.Exists(c.Path) {
		if err := r.checkDest(c.Path, replaceLink); err != nil {
			return err
		}
		if replaceLink {
			if err := os.Remove(abs); err != nil { // the link itself, never its target
				return fmt.Errorf("remove link %s: %w", c.Path, err)
			}
		} else {
			if err := r.checkDest(aside, false); err != nil {
				return err
			}
			if err := os.Rename(abs, absAside); err != nil {
				return fmt.Errorf("move %s aside: %w", c.Path, err)
			}
			movedAside = true
		}
	}
	if err := r.checkDest(c.Path, false); err != nil {
		return err
	}
	if err := r.checkDest(tmp, false); err != nil {
		return err
	}
	if err := os.Rename(absTmp, abs); err != nil {
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	if movedAside {
		if err := r.checkDest(aside, false); err != nil {
			return err
		}
		if err := os.RemoveAll(absAside); err != nil { // does not follow links inside
			return fmt.Errorf("remove %s: %w", aside, err)
		}
	}
	return nil
}

// remove journals and removes one orphaned item, then drops its record.
func (r *installer) remove(op installOp) error {
	if ar := r.m.Agents[r.agent]; ar != nil {
		ar.URL = r.url
	}
	return r.journal.remove(op.Item, op.disk.hash)
}
