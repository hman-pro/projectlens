package agentsetup

import (
	"os"
	"path/filepath"
	"testing"
)

func noBetaFixture() evalFixture {
	f := defaultFixture()
	f.beta = ""
	return f
}

// An orphan that differs from its record carries the detail uninstall reports,
// while install still calls its state orphaned.
func TestInstall_OrphanModifiedCarriesDetail(t *testing.T) {
	tg := mustTarget(t, newRepo(t))
	runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	// beta becomes an orphan: a second skill ID the fixture no longer has.
	if err := os.Symlink(".", tg.Abs(".claude/skills/beta/loop")); err != nil {
		t.Fatal(err)
	}

	res := runInstall(t, tg, noBetaFixture().dir(t), claudeOpts())
	r := resultFor(t, res, KindSkill, "beta")
	if r.Action != ActionSkipped || r.State != StateOrphaned || r.Detail != "contains a symlink" {
		t.Fatalf("install: %+v", r)
	}

	ures := runUninstall(t, tg, UninstallOpts{Agents: []Agent{AgentClaude}, DryRun: true})
	u := uresultFor(t, ures, KindSkill, "beta")
	if u.Action != ActionSkipped || u.State != StateModified || u.Detail != "contains a symlink" {
		t.Fatalf("uninstall: %+v", u)
	}
}

// Install's orphan plan and uninstall's plan for the same record are one
// decision: everything but State matches.
func TestRemovalParity_InstallOrphanMatchesUninstall(t *testing.T) {
	installBoth := func(t *testing.T, tg Target) {
		runInstall(t, tg, defaultFixture().dir(t), claudeOpts())
	}
	rows := []struct {
		name  string
		force bool
		agent Agent
		prep  func(t *testing.T, root string, tg Target) // before install of the reduced canon
	}{
		{"missing", false, AgentClaude, func(t *testing.T, root string, tg Target) {
			installBoth(t, tg)
			if err := os.RemoveAll(tg.Abs(".claude/skills/beta")); err != nil {
				t.Fatal(err)
			}
		}},
		{"foreign codex definition", false, AgentCodex, func(t *testing.T, root string, tg Target) {
			write(t, tg.Abs(".codex/config.toml"), "[mcp_servers.projectlens]\nurl = \""+evalURLOther+"\"\n")
			seed := manifestWith(AgentCodex, Record{Kind: KindMCP, ID: "legacy", Path: ".codex/config.toml", Hash: MCPHash(evalURL)})
			if err := seed.Save(tg); err != nil {
				t.Fatal(err)
			}
		}},
		{"tracked with entry present", false, AgentClaude, func(t *testing.T, root string, tg Target) {
			installBoth(t, tg)
			git(t, root, "add", "-f", "--", ".claude/skills/beta/SKILL.md")
			git(t, root, "commit", "-q", "-m", "tracked")
		}},
		{"modified without force", false, AgentClaude, func(t *testing.T, root string, tg Target) {
			installBoth(t, tg)
			write(t, tg.Abs(".claude/skills/beta/SKILL.md"), "edited")
		}},
		{"modified with force", true, AgentClaude, func(t *testing.T, root string, tg Target) {
			installBoth(t, tg)
			write(t, tg.Abs(".claude/skills/beta/SKILL.md"), "edited")
		}},
		{"modified, symlink inside", false, AgentClaude, func(t *testing.T, root string, tg Target) {
			installBoth(t, tg)
			if err := os.Symlink(".", tg.Abs(".claude/skills/beta/loop")); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlinked parent", false, AgentClaude, func(t *testing.T, root string, tg Target) {
			installBoth(t, tg)
			outside := t.TempDir()
			if err := os.Rename(tg.Abs(".claude/skills"), filepath.Join(outside, "skills")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "skills"), tg.Abs(".claude/skills")); err != nil {
				t.Fatal(err)
			}
		}},
		{"foreign sibling", false, AgentClaude, func(t *testing.T, root string, tg Target) {
			installBoth(t, tg)
			write(t, tg.Abs(".claude/skills/.beta.lens-old/notes.md"), "mine")
		}},
		{"unchanged", false, AgentClaude, func(t *testing.T, root string, tg Target) {
			installBoth(t, tg)
		}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			build := func() Target {
				root := newRepo(t)
				tg := mustTarget(t, root)
				row.prep(t, root, tg)
				return tg
			}
			kind, id := KindSkill, "beta"
			if row.agent == AgentCodex {
				kind, id = KindMCP, "legacy"
			}

			// Claude rows reduce the canon to drop beta; the Codex canon keeps
			// its projectlens MCP and the seeded "legacy" record is the orphan.
			assets := noBetaFixture().dir(t)
			if row.agent == AgentCodex {
				assets = defaultFixture().dir(t)
			}
			io := InstallOpts{Agent: row.agent, URL: evalURL, Force: row.force, DryRun: true}
			ires, err := Install(build(), assets, io)
			if err != nil {
				t.Fatal(err)
			}
			uo := UninstallOpts{Agents: []Agent{row.agent}, DryRun: true, Force: row.force}
			ures := runUninstall(t, build(), uo)

			i := resultFor(t, ires, kind, id)
			u := uresultFor(t, ures, kind, id)
			if i.State != StateOrphaned {
				t.Errorf("install state %q, want orphaned", i.State)
			}
			if i.Action != u.Action || i.Detail != u.Detail || i.Snippet != u.Snippet || i.Tracked != u.Tracked {
				t.Errorf("install %+v\nuninstall %+v", i, u)
			}
		})
	}
}
