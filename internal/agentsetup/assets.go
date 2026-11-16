package agentsetup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Item identifies one installable thing in a target repository.
type Item struct {
	Agent Agent
	Kind  Kind
	ID    string // skill name | hook ID | "projectlens" for the MCP entry
	Path  string // target-relative: the skill dir, the hooks file, or the MCP file
}

// Canon is an Item as defined by the canonical assets, with its hash.
type Canon struct {
	Item
	Hash string
	// exactly one of these is set, by Kind
	SkillSrc string // absolute source directory
	Hook     Hook
	URL      string
}

// Hook is one canonical hook definition.
type Hook struct {
	ID      string
	Event   string // "SessionStart" | "PreToolUse" | "Stop"
	Matcher string
	Type    string // "command"
	Command string // full command including the ID prefix
}

// Hash covers the whole owned definition, so a changed event, matcher, or type is a change.
func (h Hook) Hash() string {
	return HashString(h.Event + "\x00" + h.Matcher + "\x00" + h.Type + "\x00" + h.Command)
}

var hookIDRe = regexp.MustCompile(`^: projectlens-hook=([a-z0-9-]+); `)

// HookID parses the leading ID marker of a hook command.
func HookID(command string) (string, bool) {
	m := hookIDRe.FindStringSubmatch(command)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// HashString returns the hex sha256 of s.
func HashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// MCPHash is the hash of the MCP entry for a server URL.
func MCPHash(url string) string { return HashString("mcp\n" + url) }

// HashDir hashes every regular file under dir (path and content), in sorted
// slash-separated relative path order. Symlinks are an error.
func HashDir(dir string) (string, error) {
	var rels []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink not allowed: %s", p)
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			rels = append(rels, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk %s: %w", dir, err)
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", rel, err)
		}
		sum := sha256.Sum256(data)
		io.WriteString(h, rel+"\x00"+hex.EncodeToString(sum[:])+"\n")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyDir copies src to dst preserving structure and file modes. dst must not exist.
func CopyDir(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("destination already exists: %s", dst)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dst, err)
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", p, err)
		}
		switch {
		case d.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
			return nil
		case info.Mode().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return fmt.Errorf("read %s: %w", p, err)
			}
			if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
				return fmt.Errorf("write %s: %w", target, err)
			}
			// WriteFile is subject to umask; set the mode exactly.
			if err := os.Chmod(target, info.Mode().Perm()); err != nil {
				return fmt.Errorf("chmod %s: %w", target, err)
			}
			return nil
		}
		return fmt.Errorf("unsupported file type: %s", p)
	})
}

// LoadCanonical returns the canonical items for an agent: the MCP item, skills
// sorted by name, then (Claude only) hooks in snippet order.
func LoadCanonical(assetsDir string, a Agent, url string) ([]Canon, error) {
	pl, err := PlacementFor(a, ScopePersonal)
	if err != nil {
		return nil, err
	}
	absAssets, err := filepath.Abs(assetsDir)
	if err != nil {
		return nil, fmt.Errorf("resolve assets dir: %w", err)
	}

	out := []Canon{{
		Item: Item{Agent: a, Kind: KindMCP, ID: "projectlens", Path: pl.MCPFile},
		Hash: MCPHash(url),
		URL:  url,
	}}

	skillsRoot := filepath.Join(absAssets, "skills")
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return nil, fmt.Errorf("read skills dir: %w", err)
	}
	for _, e := range entries { // ReadDir returns entries sorted by name
		if !e.IsDir() {
			continue
		}
		src := filepath.Join(skillsRoot, e.Name())
		hash, err := HashDir(src)
		if err != nil {
			return nil, fmt.Errorf("hash skill %s: %w", e.Name(), err)
		}
		out = append(out, Canon{
			Item:     Item{Agent: a, Kind: KindSkill, ID: e.Name(), Path: path.Join(pl.SkillsDir, e.Name())},
			Hash:     hash,
			SkillSrc: src,
		})
	}

	if a == AgentClaude {
		hooks, err := loadClaudeHooks(filepath.Join(absAssets, "claude", "settings-snippet.json"))
		if err != nil {
			return nil, err
		}
		for _, h := range hooks {
			out = append(out, Canon{
				Item: Item{Agent: a, Kind: KindHook, ID: h.ID, Path: pl.HooksFile},
				Hash: h.Hash(),
				Hook: h,
			})
		}
	}
	return out, nil
}

// loadClaudeHooks parses the snippet, keeping event order as written.
func loadClaudeHooks(file string) ([]Hook, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read hook snippet: %w", err)
	}
	type group struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	}
	var doc struct {
		Hooks json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse hook snippet: %w", err)
	}

	// Decode the events object token by token to preserve snippet order.
	dec := json.NewDecoder(strings.NewReader(string(doc.Hooks)))
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("parse hook snippet hooks: %w", err)
	}
	var hooks []Hook
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("parse hook snippet event: %w", err)
		}
		event, _ := tok.(string)
		var groups []group
		if err := dec.Decode(&groups); err != nil {
			return nil, fmt.Errorf("parse hook snippet event %s: %w", event, err)
		}
		for _, g := range groups {
			for _, h := range g.Hooks {
				id, ok := HookID(h.Command)
				if !ok {
					return nil, fmt.Errorf("hook in event %s has no projectlens-hook ID marker", event)
				}
				if seen[id] {
					return nil, fmt.Errorf("duplicate hook ID %q (event %s)", id, event)
				}
				seen[id] = true
				hooks = append(hooks, Hook{ID: id, Event: event, Matcher: g.Matcher, Type: h.Type, Command: h.Command})
			}
		}
	}
	return hooks, nil
}
