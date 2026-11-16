package agentsetup

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

// journal applies journaled changes for install and uninstall: every item write
// or removal is recorded as pending in the manifest before it happens, and the
// exclude block is written around each manifest save so nothing lens creates is
// ever visible to git.
type journal struct {
	t      Target
	m      *Manifest
	name   string // "install" or "uninstall", named in the interruption error
	failAt int    // see InstallOpts.failAt
	steps  int
	skills map[string]bool // every known skill path, so an absent one still gets a directory pattern
	own    map[string]bool // skill siblings listed in this work tree's exclude block when the run started

	// dirsBeforeDelete removes the empty CreatedDirs entries before a save that
	// deletes the manifest, so a crash cannot leave them with nothing recording them.
	dirsBeforeDelete bool

	// onSave is a test seam called inside save: after the widening block write
	// ("block widened") and after the manifest is written or deleted ("manifest saved").
	onSave func(stage string)
}

// step counts one journal step and fails the run when it reaches failAt.
func (j *journal) step() error {
	j.steps++
	if j.failAt != 0 && j.steps == j.failAt {
		return fmt.Errorf("%s interrupted at step %d", j.name, j.steps)
	}
	return nil
}

func (j *journal) hook(stage string) {
	if j.onSave != nil {
		j.onSave(stage)
	}
}

// putRecord replaces the record of the same item, or appends it.
func (j *journal) putRecord(a Agent, rec Record) {
	ar := j.m.Agents[a]
	if ar == nil {
		ar = &AgentRecord{}
		j.m.Agents[a] = ar
	}
	for i := range ar.Items {
		if ar.Items[i].Kind == rec.Kind && ar.Items[i].ID == rec.ID {
			ar.Items[i] = rec
			return
		}
	}
	ar.Items = append(ar.Items, rec)
}

func (j *journal) dropRecord(a Agent, kind Kind, id string) {
	ar := j.m.Agents[a]
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

// save widens the exclude block (with extra, for a skill sibling in use),
// writes or deletes the manifest, then sets the block from what Save kept. The
// block is widened first so the manifest and any new path are never visible to
// git, and narrowed last so nothing still on disk becomes visible. An agent
// with no records left is deleted from the manifest.
func (j *journal) save(extra []string) error {
	for a, ar := range j.m.Agents {
		if ar == nil || len(ar.Items) == 0 {
			delete(j.m.Agents, a)
		}
	}
	live, _ := j.m.livePaths(j.t)
	willExist := len(j.m.Agents) > 0 || len(live) > 0
	if !willExist && j.dirsBeforeDelete {
		if err := j.removeEmptyDirs(); err != nil {
			return err
		}
	}
	// The first write only widens the block: it keeps every pattern already
	// there and adds what is about to exist. Only the write after Save narrows
	// it, once the manifest and every dropped path are really gone.
	cur, err := j.t.ExcludeBlock()
	if err != nil {
		return fmt.Errorf("update exclude block: %w", err)
	}
	pre := append(excludePatterns(j.t, live, j.skills, willExist || j.t.Exists(ManifestPath), extra), cur...)
	sort.Strings(pre)
	if err := j.t.SetExcludeBlock(compactStrings(pre)); err != nil {
		return fmt.Errorf("update exclude block: %w", err)
	}
	j.hook("block widened")
	if err := j.m.Save(j.t); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}
	j.hook("manifest saved")
	if !j.t.Exists(ManifestPath) {
		extra = nil
	}
	if err := j.t.SetExcludeBlock(excludePatterns(j.t, j.m.CreatedPaths, j.skills, j.t.Exists(ManifestPath), extra)); err != nil {
		return fmt.Errorf("update exclude block: %w", err)
	}
	return nil
}

// remove journals and removes one recorded item, then drops its record.
// diskHash is what is on disk now: it becomes the record's Hash, so an
// interrupted forced removal is finished by the next run without --force.
func (j *journal) remove(it Item, diskHash string) error {
	if err := j.t.CheckContained(it.Path); err != nil {
		return fmt.Errorf("remove %s: %w", it.Path, err)
	}
	rec, _ := j.m.Record(it.Agent, it.Kind, it.ID)
	rec.Hash = diskHash
	rec.Pending = &Pending{Op: "remove"}
	j.putRecord(it.Agent, rec)
	var extra []string
	if it.Kind == KindSkill {
		extra = skillSiblingPatterns(it.Path)
	}
	if err := j.save(extra); err != nil {
		return err
	}
	if err := j.step(); err != nil {
		return err
	}
	if err := j.removeItem(it); err != nil {
		return err
	}
	if err := j.step(); err != nil {
		return err
	}
	j.dropRecord(it.Agent, it.Kind, it.ID)
	return j.save(nil)
}

// removeItem deletes an item from disk. A merged file left empty is deleted
// only when install created it.
func (j *journal) removeItem(it Item) error {
	if err := j.t.CheckContained(it.Path); err != nil {
		return fmt.Errorf("remove %s: %w", it.Path, err)
	}
	abs := j.t.Abs(it.Path)
	var empty bool
	var err error
	switch {
	case it.Kind == KindSkill:
		return j.removeSkillDir(it.Path)
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
	if empty && j.m.Created(it.Path) {
		if err := j.t.CheckContained(it.Path); err != nil {
			return fmt.Errorf("remove %s: %w", it.Path, err)
		}
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", it.Path, err)
		}
	}
	return nil
}

// removeSkillDir renames a skill directory aside, then removes the aside copy,
// so the real path is never left holding a partial directory.
func (j *journal) removeSkillDir(skill string) error {
	aside := skillAsidePath(skill)
	if err := j.removeSkillSibling(aside, true); err != nil {
		return err
	}
	if err := j.t.CheckContained(skill); err != nil {
		return fmt.Errorf("remove %s: %w", skill, err)
	}
	if err := j.t.CheckContained(aside); err != nil {
		return fmt.Errorf("remove %s: %w", skill, err)
	}
	if err := os.Rename(j.t.Abs(skill), j.t.Abs(aside)); err != nil {
		return fmt.Errorf("move %s aside: %w", skill, err)
	}
	if err := j.t.CheckContained(aside); err != nil {
		return fmt.Errorf("remove %s: %w", aside, err)
	}
	if err := os.RemoveAll(j.t.Abs(aside)); err != nil { // does not follow links inside
		return fmt.Errorf("remove %s: %w", aside, err)
	}
	return nil
}

// removeSkillSibling deletes a leftover temp or aside sibling, but only one lens
// created: lens lists a sibling's pattern in the exclude block before creating
// it, so a sibling is lens's only when its pattern was there when the run
// started. Anything else (not lens's, tracked, or reached through a symlink) is
// left alone; strict makes it an error.
func (j *journal) removeSkillSibling(rel string, strict bool) error {
	if !j.t.Exists(rel) {
		return nil
	}
	refuse := func(why error) error {
		if strict {
			return fmt.Errorf("remove %s: %w", rel, why)
		}
		return nil
	}
	if !j.own[rel] {
		return refuse(errors.New("not created by lens"))
	}
	tracked, err := j.t.IsTracked(rel)
	if err != nil {
		return fmt.Errorf("remove %s: %w", rel, err)
	}
	if tracked {
		return refuse(errors.New("path is tracked by git"))
	}
	if err := j.t.CheckContained(rel); err != nil {
		return refuse(err)
	}
	if err := os.RemoveAll(j.t.Abs(rel)); err != nil {
		return fmt.Errorf("remove %s: %w", rel, err)
	}
	return nil
}

// removeSkillSiblings deletes both leftover siblings of a skill.
func (j *journal) removeSkillSiblings(skill string, strict bool) error {
	for _, rel := range []string{skillTempPath(skill), skillAsidePath(skill)} {
		if err := j.removeSkillSibling(rel, strict); err != nil {
			return err
		}
	}
	return nil
}

// cleanSiblings removes the leftovers of a crash: every sibling listed in the
// block when the run started, beside any agent's skill, recorded or not (after
// manifest loss). Once the block is rewritten they would no longer be excluded.
func (j *journal) cleanSiblings() error {
	sibs := make([]string, 0, len(j.own))
	for p := range j.own {
		sibs = append(sibs, p)
	}
	sort.Strings(sibs)
	for _, p := range sibs {
		if err := j.removeSkillSibling(p, false); err != nil {
			return err
		}
	}
	return nil
}

// removeEmptyDirs removes each CreatedDirs entry that is now an empty directory,
// deepest first. Entries reached through a symlink, replaced by a file, or still
// holding something are left; Save keeps those that exist.
func (j *journal) removeEmptyDirs() error {
	dirs := append([]string(nil), j.m.CreatedDirs...)
	sort.Slice(dirs, func(a, b int) bool {
		da, db := strings.Count(dirs[a], "/"), strings.Count(dirs[b], "/")
		if da != db {
			return da > db
		}
		return dirs[a] < dirs[b]
	})
	// contained reports false for a path reached through a symlink; any other
	// failure is an error.
	contained := func(d string) (bool, error) {
		err := j.t.CheckContained(d)
		if errors.Is(err, ErrEscapesTarget) {
			return false, nil
		}
		return err == nil, err
	}
	for _, d := range dirs {
		if ok, err := contained(d); !ok {
			if err != nil {
				return fmt.Errorf("remove %s: %w", d, err)
			}
			continue
		}
		fi, err := os.Lstat(j.t.Abs(d))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("stat %s: %w", d, err)
		}
		if !fi.IsDir() {
			continue
		}
		entries, err := os.ReadDir(j.t.Abs(d))
		if err != nil {
			return fmt.Errorf("read %s: %w", d, err)
		}
		if len(entries) > 0 {
			continue
		}
		if ok, err := contained(d); !ok {
			if err != nil {
				return fmt.Errorf("remove %s: %w", d, err)
			}
			continue
		}
		if err := os.Remove(j.t.Abs(d)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", d, err)
		}
	}
	return nil
}

// excludePatterns builds this work tree's exclude block: "/" + path for each
// created path, the manifest, and extra. A directory gets a trailing "/"; a
// known skill path always does, so a symlink a developer put there is never
// hidden by it.
func excludePatterns(t Target, created []string, skills map[string]bool, manifest bool, extra []string) []string {
	var out []string
	for _, p := range created {
		dir := skills[p]
		if fi, err := os.Lstat(t.Abs(p)); !dir && err == nil {
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

func skillSibling(skill, suffix string) string {
	return path.Join(path.Dir(skill), "."+path.Base(skill)+suffix)
}

func skillTempPath(skill string) string  { return skillSibling(skill, skillTempSuffix) }
func skillAsidePath(skill string) string { return skillSibling(skill, skillAsideSuffix) }

// skillSiblingPatterns are the exclude patterns for a skill's temp and aside
// siblings, listed while a write or removal of that skill is journaled.
func skillSiblingPatterns(skill string) []string {
	return []string{"/" + skillTempPath(skill) + "/", "/" + skillAsidePath(skill) + "/"}
}

func isSkillSibling(p string) bool {
	base := path.Base(p)
	return strings.HasPrefix(base, ".") &&
		(strings.HasSuffix(base, skillTempSuffix) || strings.HasSuffix(base, skillAsideSuffix))
}

// ownSiblings returns the skill siblings whose pattern is in this work tree's
// exclude block: the only siblings lens may delete in this run.
func ownSiblings(t Target) (map[string]bool, error) {
	block, err := t.ExcludeBlock()
	if err != nil {
		return nil, err
	}
	own := map[string]bool{}
	for _, pattern := range block {
		if !strings.HasPrefix(pattern, "/") || !strings.HasSuffix(pattern, "/") {
			continue
		}
		p := strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/")
		if isSkillSibling(p) && checkLocal(p) == nil {
			own[p] = true
		}
	}
	return own, nil
}

// foreignSibling returns the first temp or aside sibling of skill that exists
// but is not lens's, or "". Such a skill is never written or removed: the
// journal would exclude that name, and a later run would take it for lens's.
func foreignSibling(t Target, own map[string]bool, skill string) string {
	for _, s := range []string{skillTempPath(skill), skillAsidePath(skill)} {
		if t.Exists(s) && !own[s] {
			return s
		}
	}
	return ""
}

// blockForeignSibling marks r blocked, and reports true, when it is a skill
// whose temp or aside name is taken by something lens did not create.
func blockForeignSibling(t Target, own map[string]bool, it Item, r *ItemResult) bool {
	if it.Kind != KindSkill {
		return false
	}
	sib := foreignSibling(t, own, it.Path)
	if sib == "" {
		return false
	}
	r.Action, r.Detail = ActionBlocked, "foreign sibling "+sib
	r.Snippet = "move " + sib + " away; lens uses that name to replace or remove " + it.Path + ", then re-run"
	return true
}

// matchesRecord reports whether disk holds what rec says lens last left there,
// so an interrupted run is never taken for a local edit: the recorded hash, the
// hash of a journaled write that finished, or, for a journaled removal (forced
// or not), the content it decided to remove.
func matchesRecord(rec Record, disk installed) bool {
	recorded := rec.Hash
	if rec.Pending != nil && rec.Pending.Op == "write" && disk.hash == rec.Pending.Hash {
		recorded = rec.Pending.Hash
	}
	pendingRemove := rec.Pending != nil && rec.Pending.Op == "remove" && disk.hash == rec.Hash
	return (disk.hash == recorded && !disk.dup) || pendingRemove
}
