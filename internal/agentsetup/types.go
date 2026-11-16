// Package agentsetup installs and inspects the ProjectLens wiring (MCP config,
// skills, hooks) inside a target repository for supported agents.
package agentsetup

import "fmt"

// Agent identifies a supported coding agent.
type Agent string

const (
	AgentClaude Agent = "claude"
	AgentCodex  Agent = "codex"
)

// ParseAgent validates a user-supplied agent name.
func ParseAgent(s string) (Agent, error) {
	switch Agent(s) {
	case AgentClaude, AgentCodex:
		return Agent(s), nil
	}
	return "", fmt.Errorf("unsupported agent %q (supported: claude, codex)", s)
}

// Kind is the category of an installed item.
type Kind string

const (
	KindMCP   Kind = "mcp"
	KindSkill Kind = "skill"
	KindHook  Kind = "hook"
)

// State describes how an item on disk relates to what lens would install.
type State string

const (
	StateMissing        State = "missing"
	StateCurrent        State = "current"
	StateStale          State = "stale"
	StateModified       State = "modified"
	StateUnmanaged      State = "unmanaged"
	StateManual         State = "manual"
	StateOrphaned       State = "orphaned"
	StateRemovedLocally State = "removed-locally"
	// StateBlocked marks a symlink or foreign definition lens will never write through.
	StateBlocked State = "blocked"
)

// Scope selects where wiring is placed.
type Scope string

// ScopePersonal is the only implemented scope.
const ScopePersonal Scope = "personal"

// Placement holds target-root-relative, slash-separated paths.
type Placement struct {
	MCPFile   string
	SkillsDir string
	HooksFile string // empty when the agent has no hooks file
}

// PlacementFor returns where an agent's wiring lives for a scope.
func PlacementFor(a Agent, s Scope) (Placement, error) {
	if s != ScopePersonal {
		return Placement{}, fmt.Errorf("unsupported scope %q (supported: personal)", s)
	}
	switch a {
	case AgentClaude:
		return Placement{MCPFile: ".mcp.json", SkillsDir: ".claude/skills", HooksFile: ".claude/settings.local.json"}, nil
	case AgentCodex:
		return Placement{MCPFile: ".codex/config.toml", SkillsDir: ".agents/skills"}, nil
	}
	return Placement{}, fmt.Errorf("unsupported agent %q (supported: claude, codex)", a)
}

// Env carries process context so callers can inject it in tests.
type Env struct {
	Cwd           string
	Home          string
	BakedCheckout string // "" when the binary was not built by the Makefile
	Getenv        func(string) string
}

// ManifestPath is the target-root-relative path of the install manifest.
const ManifestPath = ".projectlens/agent-manifest.json"
