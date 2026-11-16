package agentsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

const manifestVersion = 1

// ErrManifestTracked reports that git tracks the manifest, so lens must not write it.
var ErrManifestTracked = errors.New(ManifestPath + " is tracked by git")

// Record is one item lens installed for an agent.
type Record struct {
	Kind    Kind     `json:"kind"`
	ID      string   `json:"id"`
	Path    string   `json:"path"`
	Hash    string   `json:"hash"` // hash last known to be on disk; "" before the first write completes
	Pending *Pending `json:"pending,omitempty"`
}

// Pending names an operation that was journaled but may not have completed.
type Pending struct {
	Op   string `json:"op"`             // "write" | "remove"
	Hash string `json:"hash,omitempty"` // for "write": the hash the write will produce
}

// AgentRecord holds what lens installed for one agent.
type AgentRecord struct {
	URL   string   `json:"url"`
	Items []Record `json:"items"`
}

// Manifest is the record of what lens wrote into a work tree.
type Manifest struct {
	Version int                    `json:"version"` // 1
	Agents  map[Agent]*AgentRecord `json:"agents"`
	// Path-level ownership for the whole work tree. Entries leave only when the path is gone.
	CreatedPaths []string `json:"created_paths"` // files and skill dirs install created; drives exclude and deletion
	CreatedDirs  []string `json:"created_dirs"`  // parent dirs install created; removed on uninstall when empty
}

func emptyManifest() *Manifest {
	return &Manifest{Version: manifestVersion, Agents: map[Agent]*AgentRecord{}}
}

// LoadManifest reads the manifest; a missing file is an empty manifest.
func LoadManifest(t Target) (*Manifest, error) {
	data, err := os.ReadFile(t.Abs(ManifestPath))
	if os.IsNotExist(err) {
		return emptyManifest(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", ManifestPath, err)
	}
	if m.Version != manifestVersion {
		return nil, fmt.Errorf("parse manifest %s: unsupported version %d", ManifestPath, m.Version)
	}
	if m.Agents == nil {
		m.Agents = map[Agent]*AgentRecord{}
	}
	return &m, nil
}

// existingPaths returns the sorted, de-duplicated entries of paths that exist on
// disk or for which keep reports true.
func existingPaths(t Target, paths []string, keep func(string) bool) []string {
	out := []string{}
	for _, p := range paths {
		if t.Exists(p) || keep(p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return compactStrings(out)
}

// pendingWritePaths returns the paths of every record, of any agent, with a
// journaled write.
func (m *Manifest) pendingWritePaths() []string {
	var out []string
	for _, ar := range m.Agents {
		if ar == nil {
			continue
		}
		for _, r := range ar.Items {
			if r.Pending != nil && r.Pending.Op == "write" {
				out = append(out, r.Path)
			}
		}
	}
	return out
}

func compactStrings(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// livePaths returns the CreatedPaths and CreatedDirs entries Save keeps: those
// that exist, plus those a pending write may be about to create.
func (m *Manifest) livePaths(t Target) (paths, dirs []string) {
	pending := m.pendingWritePaths()
	paths = existingPaths(t, m.CreatedPaths, func(p string) bool {
		for _, w := range pending {
			if w == p {
				return true
			}
		}
		return false
	})
	dirs = existingPaths(t, m.CreatedDirs, func(d string) bool {
		for _, w := range pending {
			if strings.HasPrefix(w, d+"/") {
				return true
			}
		}
		return false
	})
	return paths, dirs
}

// Save prunes CreatedPaths/CreatedDirs entries that no longer exist on disk, then
// writes the manifest. A pending write may be about to create a path, so while a
// record (of any agent) has Pending{Op: "write"}, an absent CreatedPaths entry
// equal to its Path, and an absent CreatedDirs entry that is an ancestor of its
// Path, are kept. When no agent and no CreatedPaths entry remain, it deletes the
// manifest file and .projectlens/ if that is left empty.
func (m *Manifest) Save(t Target) error {
	if err := CheckManifestWritable(t); err != nil {
		return err
	}
	m.Version = manifestVersion
	if m.Agents == nil {
		m.Agents = map[Agent]*AgentRecord{}
	}
	m.CreatedPaths, m.CreatedDirs = m.livePaths(t)

	if len(m.Agents) == 0 && len(m.CreatedPaths) == 0 {
		return removeManifest(t)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := writeFileAtomic(t.Abs(ManifestPath), append(data, '\n')); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func removeManifest(t Target) error {
	if err := os.Remove(t.Abs(ManifestPath)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove manifest: %w", err)
	}
	dir := t.Abs(path.Dir(ManifestPath))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	if len(entries) > 0 {
		return nil
	}
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", dir, err)
	}
	return nil
}

// Record returns the record for one item of an agent.
func (m *Manifest) Record(a Agent, kind Kind, id string) (Record, bool) {
	if m == nil {
		return Record{}, false
	}
	ar, ok := m.Agents[a]
	if !ok || ar == nil {
		return Record{}, false
	}
	for _, r := range ar.Items {
		if r.Kind == kind && r.ID == id {
			return r, true
		}
	}
	return Record{}, false
}

// InstalledAgents returns the agents that have a record, sorted.
func (m *Manifest) InstalledAgents() []Agent {
	if m == nil {
		return nil
	}
	out := make([]Agent, 0, len(m.Agents))
	for a := range m.Agents {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Created reports whether install created path.
func (m *Manifest) Created(p string) bool {
	if m == nil {
		return false
	}
	for _, c := range m.CreatedPaths {
		if c == p {
			return true
		}
	}
	return false
}

// CheckManifestWritable must be called by install and uninstall before any mutation.
// It fails with ErrManifestTracked when ManifestPath is tracked by git, and with
// ErrEscapesTarget when ".projectlens" or the manifest file is a symlink.
func CheckManifestWritable(t Target) error {
	if err := t.CheckContained(ManifestPath); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	tracked, err := t.IsTracked(ManifestPath)
	if err != nil {
		return err
	}
	if tracked {
		return fmt.Errorf("refusing to write: %w", ErrManifestTracked)
	}
	return nil
}

// RecoverCreated is used when the manifest is missing but this work tree's exclude
// block exists: it returns the block's paths that are present on disk, without the
// leading "/" and trailing "/", excluding ManifestPath.
func RecoverCreated(t Target) ([]string, error) {
	block, err := t.ExcludeBlock()
	if err != nil {
		return nil, fmt.Errorf("recover created paths: %w", err)
	}
	var out []string
	for _, pattern := range block {
		p := strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/")
		if p == "" || p == ManifestPath || !t.Exists(p) {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return compactStrings(out), nil
}
