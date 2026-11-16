package agentsetup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
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

	// skillTempSuffix names the sibling a skill is copied into before it is
	// renamed into place: ".<name>.lens-tmp" in the same parent.
	skillTempSuffix = ".lens-tmp"
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
	disk  installed // opWrite: what is on disk before the write
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
	statuses, err := Evaluate(t, o.Agent, canon, m)
	if err != nil {
		return InstallResult{}, fmt.Errorf("evaluate %s: %w", o.Agent, err)
	}
	ops, err := planInstall(t, o, canon, statuses, m)
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
		r := &installer{t: t, m: m, agent: o.Agent, url: o.URL, force: o.Force, failAt: o.failAt, skills: skills}
		if err := r.run(canon, ops); err != nil {
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
		if !isSkillTemp(p) {
			m.CreatedPaths = append(m.CreatedPaths, p)
		}
	}
	return m, nil
}

// planInstall decides the action for every evaluated item. It reads only.
func planInstall(t Target, o InstallOpts, canon []Canon, sts []ItemStatus, m *Manifest) ([]installOp, error) {
	ops := make([]installOp, 0, len(sts))
	for i, st := range sts {
		var op installOp
		var err error
		if i < len(canon) { // Evaluate lists canonical items first, in order
			op, err = planCanonical(t, o, canon[i], st, m)
		} else {
			op, err = planOrphan(t, o, st, m)
		}
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func planCanonical(t Target, o InstallOpts, c Canon, st ItemStatus, m *Manifest) (installOp, error) {
	op := installOp{ItemResult: ItemResult{ItemStatus: st}, canon: c}
	set := func(a Action, k opKind) (installOp, error) {
		op.Action, op.kind = a, k
		return op, nil
	}
	write := func(a Action) (installOp, error) {
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
		op.Snippet = blockedSnippet(st.Detail)
		return set(ActionBlocked, opNone)
	case StateManual:
		op.Snippet = manualSnippet(c)
		return set(ActionManual, opNone)
	case StateCurrent:
		foreign, err := isForeignCodex(t, c)
		if err != nil {
			return installOp{}, err
		}
		switch {
		case foreign || (st.Tracked && !st.Owned):
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
func planOrphan(t Target, o InstallOpts, st ItemStatus, m *Manifest) (installOp, error) {
	op := installOp{ItemResult: ItemResult{ItemStatus: st}}
	if err := checkLocal(st.Path); err != nil { // LoadManifest already refuses these
		return installOp{}, fmt.Errorf("orphan %s %s: %w", st.Kind, st.ID, err)
	}
	if err := t.CheckContained(st.Path); err != nil {
		if !errors.Is(err, ErrEscapesTarget) {
			return installOp{}, err
		}
		comp := firstSymlink(t, st.Path)
		op.Detail = "symlinked parent " + comp
		if comp == st.Path {
			op.Detail = detailSymlink
		}
		op.Action, op.Snippet = ActionBlocked, symlinkSnippet(comp)
		return op, nil
	}
	tracked, err := t.IsTracked(st.Path)
	if err != nil {
		return installOp{}, err
	}
	if tracked {
		op.Tracked = true
		op.Action = ActionManual
		op.Snippet = fmt.Sprintf("remove the projectlens %s %q from %s by hand", st.Kind, st.ID, st.Path)
		return op, nil
	}
	disk, err := installedHash(t, st.Item)
	if err != nil {
		return installOp{}, err
	}
	if !disk.present {
		op.Action, op.kind = ActionRemoved, opDrop
		return op, nil
	}
	rec, _ := m.Record(o.Agent, st.Kind, st.ID)
	recorded := rec.Hash
	if rec.Pending != nil && rec.Pending.Op == "write" && disk.hash == rec.Pending.Hash {
		recorded = rec.Pending.Hash
	}
	if (disk.hash == recorded && !disk.dup) || o.Force {
		op.Action, op.kind = ActionRemoved, opRemove
		return op, nil
	}
	op.Action = ActionSkipped
	return op, nil
}

func isForeignCodex(t Target, c Canon) (bool, error) {
	if c.Kind != KindMCP || c.Agent != AgentCodex {
		return false, nil
	}
	st, err := ReadCodexConfig(t.Abs(c.Path))
	if err != nil {
		return false, err
	}
	return st.Foreign, nil
}

func symlinkSnippet(comp string) string {
	return comp + " is a symlink; lens will not write through it"
}

func blockedSnippet(detail string) string {
	if detail == detailForeign {
		return snippetForeign
	}
	for _, prefix := range []string{"symlinked parent ", "symlinked file "} {
		if comp, ok := strings.CutPrefix(detail, prefix); ok {
			return symlinkSnippet(comp)
		}
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

// excludePatterns builds this work tree's exclude block: "/" + path for each
// created path (directories get a trailing "/"), the manifest, and extra.
func excludePatterns(t Target, created []string, skills map[string]bool, manifest bool, extra []string) []string {
	var out []string
	for _, p := range created {
		dir := skills[p]
		if fi, err := os.Lstat(t.Abs(p)); err == nil {
			dir = fi.IsDir()
		}
		pattern := "/" + p
		if dir {
			pattern += "/"
		}
		out = append(out, pattern)
	}
	if manifest {
		out = append(out, "/"+ManifestPath)
	}
	out = append(out, extra...)
	sort.Strings(out)
	return compactStrings(out)
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

func skillTempPath(skill string) string {
	return path.Join(path.Dir(skill), "."+path.Base(skill)+skillTempSuffix)
}

func isSkillTemp(p string) bool {
	base := path.Base(p)
	return strings.HasPrefix(base, ".") && strings.HasSuffix(base, skillTempSuffix)
}

// installer applies planned operations, journaling each change in the manifest.
type installer struct {
	t      Target
	m      *Manifest
	agent  Agent
	url    string
	force  bool
	failAt int
	steps  int
	skills map[string]bool
}

func (r *installer) run(canon []Canon, ops []installOp) error {
	for _, c := range canon {
		if c.Kind == KindSkill {
			if err := r.removeSkillTemp(c.Path, false); err != nil {
				return err
			}
		}
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
			r.dropRecord(op.Kind, op.ID)
		}
		if err != nil {
			return err
		}
	}
	return r.save(nil)
}

// step counts one journal step and fails the run when it reaches failAt.
func (r *installer) step() error {
	r.steps++
	if r.failAt != 0 && r.steps == r.failAt {
		return fmt.Errorf("install interrupted at step %d", r.steps)
	}
	return nil
}

func (r *installer) setRecord(rec Record) {
	ar := r.m.Agents[r.agent]
	if ar == nil {
		ar = &AgentRecord{}
		r.m.Agents[r.agent] = ar
	}
	ar.URL = r.url
	for i := range ar.Items {
		if ar.Items[i].Kind == rec.Kind && ar.Items[i].ID == rec.ID {
			ar.Items[i] = rec
			return
		}
	}
	ar.Items = append(ar.Items, rec)
}

func (r *installer) dropRecord(kind Kind, id string) {
	ar := r.m.Agents[r.agent]
	if ar == nil {
		return
	}
	kept := ar.Items[:0]
	for _, rec := range ar.Items {
		if rec.Kind != kind || rec.ID != id {
			kept = append(kept, rec)
		}
	}
	ar.Items = kept
}

// save writes the exclude block (with extra, for a temp path in use), the
// manifest, then the block again from what Save kept. The block is written
// first so the manifest and any new path are never visible to git.
func (r *installer) save(extra []string) error {
	if ar := r.m.Agents[r.agent]; ar != nil && len(ar.Items) == 0 {
		delete(r.m.Agents, r.agent)
	}
	live, _ := r.m.livePaths(r.t)
	willExist := len(r.m.Agents) > 0 || len(live) > 0
	if err := r.t.SetExcludeBlock(excludePatterns(r.t, live, r.skills, willExist, extra)); err != nil {
		return fmt.Errorf("update exclude block: %w", err)
	}
	if err := r.m.Save(r.t); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}
	if !r.t.Exists(ManifestPath) {
		extra = nil
	}
	if err := r.t.SetExcludeBlock(excludePatterns(r.t, r.m.CreatedPaths, r.skills, r.t.Exists(ManifestPath), extra)); err != nil {
		return fmt.Errorf("update exclude block: %w", err)
	}
	return nil
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
		extra = []string{"/" + skillTempPath(c.Path) + "/"}
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
		err = WriteHook(abs, c.Hook)
	case c.Agent == AgentCodex:
		err = WriteCodexBlock(abs, c.URL)
	default:
		err = WriteMCPJSON(abs, c.URL)
	}
	if err != nil {
		return fmt.Errorf("write %s %s: %w", c.Kind, c.ID, err)
	}
	return nil
}

// removeSkillTemp deletes a leftover temp sibling of a skill. strict makes an
// uncontained or tracked temp path an error; otherwise it is left alone.
func (r *installer) removeSkillTemp(skill string, strict bool) error {
	tmp := skillTempPath(skill)
	if !r.t.Exists(tmp) {
		return nil
	}
	if err := r.t.CheckContained(tmp); err != nil {
		if strict {
			return fmt.Errorf("remove %s: %w", tmp, err)
		}
		return nil
	}
	tracked, err := r.t.IsTracked(tmp)
	if err != nil {
		return err
	}
	if tracked {
		if strict {
			return fmt.Errorf("remove %s: path is tracked by git", tmp)
		}
		return nil
	}
	if err := os.RemoveAll(r.t.Abs(tmp)); err != nil {
		return fmt.Errorf("remove %s: %w", tmp, err)
	}
	return nil
}

// writeSkill copies the skill into a temp sibling, removes the old directory
// (or, with --force, the symlink itself), and renames the copy into place.
func (r *installer) writeSkill(c Canon, replaceLink bool) error {
	tmp := skillTempPath(c.Path)
	if err := r.removeSkillTemp(c.Path, true); err != nil {
		return err
	}
	abs, absTmp := r.t.Abs(c.Path), r.t.Abs(tmp)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", path.Dir(c.Path), err)
	}
	if err := r.checkDest(tmp, false); err != nil {
		return err
	}
	if err := CopyDir(c.SkillSrc, absTmp); err != nil {
		os.RemoveAll(absTmp)
		return fmt.Errorf("copy skill: %w", err)
	}
	if r.t.Exists(c.Path) {
		if err := r.checkDest(c.Path, replaceLink); err != nil {
			return err
		}
		remove := os.RemoveAll // does not follow links inside the directory
		if replaceLink {
			remove = os.Remove // the link itself, never its target
		}
		if err := remove(abs); err != nil {
			return fmt.Errorf("remove old %s: %w", c.Path, err)
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
	return nil
}

// remove journals and removes one orphaned item, then drops its record.
func (r *installer) remove(op installOp) error {
	if err := r.checkDest(op.Path, false); err != nil {
		return err
	}
	rec, _ := r.m.Record(r.agent, op.Kind, op.ID)
	rec.Pending = &Pending{Op: "remove"}
	r.setRecord(rec)
	if err := r.save(nil); err != nil {
		return err
	}
	if err := r.step(); err != nil {
		return err
	}
	if err := removeItem(r.t, r.m, op.Item); err != nil {
		return err
	}
	if err := r.step(); err != nil {
		return err
	}
	r.dropRecord(op.Kind, op.ID)
	return r.save(nil)
}

// removeItem deletes an item from disk. A merged file left empty is deleted
// only when install created it.
func removeItem(t Target, m *Manifest, it Item) error {
	if err := t.CheckContained(it.Path); err != nil {
		return fmt.Errorf("remove %s: %w", it.Path, err)
	}
	abs := t.Abs(it.Path)
	var empty bool
	var err error
	switch {
	case it.Kind == KindSkill:
		if err := os.RemoveAll(abs); err != nil {
			return fmt.Errorf("remove %s: %w", it.Path, err)
		}
		return nil
	case it.Kind == KindHook:
		empty, err = RemoveHook(abs, it.ID)
	case it.Agent == AgentCodex:
		empty, err = RemoveCodexBlock(abs)
	default:
		empty, err = RemoveMCPJSON(abs)
	}
	if err != nil {
		return fmt.Errorf("remove %s %s: %w", it.Kind, it.ID, err)
	}
	if empty && m.Created(it.Path) {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", it.Path, err)
		}
	}
	return nil
}
