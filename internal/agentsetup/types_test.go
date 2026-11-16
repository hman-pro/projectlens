package agentsetup

import (
	"strings"
	"testing"
)

func TestParseAgent(t *testing.T) {
	if a, err := ParseAgent("claude"); err != nil || a != AgentClaude {
		t.Fatal(a, err)
	}
	if a, err := ParseAgent("codex"); err != nil || a != AgentCodex {
		t.Fatal(a, err)
	}
	_, err := ParseAgent("cursor")
	if err == nil || !strings.Contains(err.Error(), "supported: claude, codex") {
		t.Fatal(err)
	}
	if err.Error() != `unsupported agent "cursor" (supported: claude, codex)` {
		t.Fatal(err)
	}
}

func TestPlacementFor(t *testing.T) {
	c, _ := PlacementFor(AgentClaude, ScopePersonal)
	if c != (Placement{".mcp.json", ".claude/skills", ".claude/settings.local.json"}) {
		t.Fatal(c)
	}
	x, _ := PlacementFor(AgentCodex, ScopePersonal)
	if x != (Placement{".codex/config.toml", ".agents/skills", ""}) {
		t.Fatal(x)
	}
	if _, err := PlacementFor(AgentClaude, Scope("shared")); err == nil {
		t.Fatal("want error")
	}
	if _, err := PlacementFor(Agent("cursor"), ScopePersonal); err == nil {
		t.Fatal("want error")
	}
}
