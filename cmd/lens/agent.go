package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hman-pro/projectlens/internal/agentsetup"
	"github.com/hman-pro/projectlens/internal/mcpserver"
	"github.com/spf13/cobra"
)

// bakedCheckout is the projectlens checkout the binary was built from. The
// Makefile sets it with -ldflags "-X main.bakedCheckout=<dir>"; it is empty
// for a binary built any other way.
var bakedCheckout string

// exitError carries a process exit code through cobra's error return. An
// empty message means the command already printed everything it had to say.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

func usageErr(err error) error { return &exitError{code: 2, err: err} }

func failure() error { return &exitError{code: 1} }

// newAgentCmd builds `lens agent`. It never loads configs/index.yaml and
// never opens a database: it only wires agentsetup to the terminal.
func newAgentCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agent",
		Short: "Wire ProjectLens into an agent (Claude Code, Codex) for the current repository",
	}
	root.Args = func(_ *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usageErr(fmt.Errorf("unknown command %q for \"lens agent\" (try: install, uninstall, status, doctor)", args[0]))
		}
		return nil
	}
	root.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr(err) })
	root.AddCommand(
		newAgentInstallCmd(),
		newAgentUninstallCmd(),
		newAgentStatusCmd(),
		newAgentDoctorCmd(),
	)
	return quiet(root)
}

func quiet(c *cobra.Command) *cobra.Command {
	c.SilenceUsage = true
	c.SilenceErrors = true
	return c
}

func maxArgs(n int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) > n {
			return usageErr(fmt.Errorf("too many arguments (expected at most %d)", n))
		}
		return nil
	}
}

func exactArgs(n int, usage string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return usageErr(fmt.Errorf("expected %d argument(s): %s", n, usage))
		}
		return nil
	}
}

func agentEnv() (agentsetup.Env, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return agentsetup.Env{}, fmt.Errorf("get working directory: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return agentsetup.Env{}, fmt.Errorf("get home directory: %w", err)
	}
	return agentsetup.Env{Cwd: cwd, Home: home, BakedCheckout: bakedCheckout, Getenv: os.Getenv}, nil
}

// resolveOpts reads the shared flags. --project and --projects are root
// persistent flags; when the command runs without them, they read as unset.
func resolveOpts(cmd *cobra.Command) agentsetup.ResolveOpts {
	o := agentsetup.ResolveOpts{ProjectsPath: "configs/projects.yaml"}
	if f := cmd.Flags().Lookup("project"); f != nil {
		o.Project = f.Value.String()
	}
	if f := cmd.Flags().Lookup("projects"); f != nil {
		o.ProjectsPath = f.Value.String()
		o.ProjectsExplicit = f.Changed
	}
	o.URL, _ = cmd.Flags().GetString("url")
	o.Port, _ = cmd.Flags().GetInt("port")
	o.Assets, _ = cmd.Flags().GetString("assets")
	return o
}

func dryRunHeader(w io.Writer, dry bool) {
	if dry {
		fmt.Fprintln(w, "Dry run. Nothing was written.")
	}
}

// row is one printed line of install, uninstall, or status output.
type row struct {
	action  string
	item    agentsetup.Item
	detail  string
	snippet string
}

func (r row) path() string {
	if r.item.Kind == agentsetup.KindSkill {
		return r.item.Path + "/"
	}
	return r.item.Path
}

func printRows(w io.Writer, rows []row, actionWidth int) {
	// Hooks share one file; summarize them as one line per action.
	type hookGroup struct {
		idx   int
		count int
	}
	groups := map[[2]string]*hookGroup{}
	var lines []row
	var counts []int
	for _, r := range rows {
		if r.item.Kind == agentsetup.KindHook {
			key := [2]string{r.action, r.detail}
			if g, ok := groups[key]; ok {
				counts[g.idx]++
				if r.snippet != "" && !strings.Contains(lines[g.idx].snippet, r.snippet) {
					lines[g.idx].snippet += "\n" + r.snippet
				}
				continue
			}
			groups[key] = &hookGroup{idx: len(lines)}
			lines = append(lines, r)
			counts = append(counts, 1)
			continue
		}
		lines = append(lines, r)
		counts = append(counts, 0)
	}
	for i, r := range lines {
		detail := r.detail
		if r.item.Kind == agentsetup.KindHook {
			n := counts[i]
			noun := "hooks"
			if n == 1 {
				noun = "hook"
			}
			detail = fmt.Sprintf("%d %s", n, noun)
			if r.detail != "" {
				detail += "; " + r.detail
			}
		}
		printRow(w, r.action, r.path(), detail, actionWidth)
		if r.snippet != "" {
			for _, l := range strings.Split(strings.TrimRight(r.snippet, "\n"), "\n") {
				fmt.Fprintf(w, "      %s\n", l)
			}
		}
	}
}

func printRow(w io.Writer, action, path, detail string, actionWidth int) {
	line := fmt.Sprintf("  %-*s %-34s", actionWidth, action, path)
	if detail != "" {
		line += " (" + detail + ")"
	}
	fmt.Fprintln(w, strings.TrimRight(line, " "))
}

func mcpKey(a agentsetup.Agent) string {
	if a == agentsetup.AgentCodex {
		return "mcp_servers.projectlens"
	}
	return "mcpServers.projectlens"
}

func itemDetail(it agentsetup.Item, detail string) string {
	if it.Kind == agentsetup.KindMCP {
		if detail != "" {
			return mcpKey(it.Agent) + "; " + detail
		}
		return mcpKey(it.Agent)
	}
	return detail
}

func excludeDisplay(t agentsetup.Target) string {
	abs := filepath.Join(t.CommonDir, "info", "exclude")
	if rel, err := filepath.Rel(t.Root, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return abs
}

func printJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	fmt.Fprintln(w, string(data))
	return nil
}

func parseAgentArg(s string) (agentsetup.Agent, error) {
	a, err := agentsetup.ParseAgent(s)
	if err != nil {
		return "", usageErr(err)
	}
	return a, nil
}

func openAgentTarget(cmd *cobra.Command) (agentsetup.Env, agentsetup.ResolveOpts, agentsetup.Target, error) {
	env, err := agentEnv()
	if err != nil {
		return env, agentsetup.ResolveOpts{}, agentsetup.Target{}, err
	}
	o := resolveOpts(cmd)
	t, err := agentsetup.ResolveTarget(env, o)
	if err != nil {
		return env, o, t, fmt.Errorf("resolve target: %w", err)
	}
	return env, o, t, nil
}

func newAgentInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <claude|codex>",
		Short: "Install the MCP entry, skills, and hooks for an agent into the current repository",
		Args:  exactArgs(1, "<claude|codex>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := parseAgentArg(args[0])
			if err != nil {
				return err
			}
			dry, _ := cmd.Flags().GetBool("dry-run")
			force, _ := cmd.Flags().GetBool("force")
			env, o, t, err := openAgentTarget(cmd)
			if err != nil {
				return err
			}
			assets, err := agentsetup.ResolveAssets(env, o)
			if err != nil {
				return err
			}
			url, err := agentsetup.ResolveURL(env, o, t)
			if errors.Is(err, agentsetup.ErrInvalidFlag) {
				return usageErr(err)
			}
			if err != nil {
				return fmt.Errorf("resolve url: %w", err)
			}
			var codexSnippet []byte
			if a == agentsetup.AgentCodex {
				codexSnippet, err = os.ReadFile(filepath.Join(assets, "codex", "AGENTS.md.snippet"))
				if err != nil {
					return fmt.Errorf("read codex AGENTS.md snippet: %w", err)
				}
			}
			res, err := agentsetup.Install(t, assets, agentsetup.InstallOpts{Agent: a, URL: url, DryRun: dry, Force: force})
			if err != nil {
				return fmt.Errorf("install %s: %w", a, err)
			}

			w := cmd.OutOrStdout()
			dryRunHeader(w, dry)
			fmt.Fprintf(w, "Target: %s   URL: %s\n\n", t.Root, url)
			rows := make([]row, 0, len(res.Items))
			changed, mcpInPlace := false, false
			for _, it := range res.Items {
				switch it.Action {
				case agentsetup.ActionWritten, agentsetup.ActionUpdated, agentsetup.ActionAdopted:
					changed = true
				}
				if it.Kind == agentsetup.KindMCP {
					switch it.Action {
					case agentsetup.ActionWritten, agentsetup.ActionUpdated, agentsetup.ActionAdopted, agentsetup.ActionCurrent:
						mcpInPlace = true
					}
				}
				rows = append(rows, row{action: string(it.Action), item: it.Item, detail: itemDetail(it.Item, it.Detail), snippet: it.Snippet})
			}
			printRows(w, rows, 9)
			if n := len(res.ExcludePaths); n > 0 {
				action := string(agentsetup.ActionCurrent)
				if changed {
					action = string(agentsetup.ActionWritten)
				}
				printRow(w, action, excludeDisplay(t), fmt.Sprintf("%d paths", n), 9)
			}
			if res.WorktreeNotice {
				fmt.Fprintln(w, "\nNote: this is a linked worktree; the exclude entries above apply to every worktree of this repository.")
			}
			fmt.Fprintln(w)
			// The agent can load the server only once its MCP entry is in place.
			switch {
			case !mcpInPlace:
				fmt.Fprintf(w, "Next: resolve the items above, then re-run: lens agent install %s\n", a)
			case a == agentsetup.AgentCodex:
				fmt.Fprintln(w, "Next: open Codex in this repo, trust the project, then run: lens agent doctor codex")
			default:
				fmt.Fprintln(w, "Next: restart Claude Code in this repo and approve the \"projectlens\" server.")
				fmt.Fprintln(w, "      Then run: lens agent doctor claude")
			}
			if a == agentsetup.AgentCodex {
				fmt.Fprintln(w, "\nCodex has no hooks. For always-on guidance, paste this into AGENTS.md:")
				fmt.Fprintln(w, strings.TrimRight(string(codexSnippet), "\n"))
			}
			return nil
		},
	}
	cmd.Flags().String("url", "", "MCP server URL (default: derived from the project registry and port)")
	cmd.Flags().Int("port", 0, "MCP server port (default: $PROJECTLENS_MCP_PORT or 8484)")
	cmd.Flags().String("assets", "", "path to the projectlens agent/ directory")
	cmd.Flags().Bool("dry-run", false, "show what would change without writing anything")
	cmd.Flags().Bool("force", false, "overwrite items that were changed or are not managed")
	return quiet(cmd)
}

func newAgentUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall <claude|codex|all>",
		Short: "Remove what install recorded for an agent",
		Args:  exactArgs(1, "<claude|codex|all>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			var agents []agentsetup.Agent
			if args[0] == "all" {
				agents = []agentsetup.Agent{agentsetup.AgentClaude, agentsetup.AgentCodex}
			} else {
				a, err := parseAgentArg(args[0])
				if err != nil {
					return err
				}
				agents = []agentsetup.Agent{a}
			}
			dry, _ := cmd.Flags().GetBool("dry-run")
			force, _ := cmd.Flags().GetBool("force")
			_, _, t, err := openAgentTarget(cmd)
			if err != nil {
				return err
			}
			res, err := agentsetup.Uninstall(t, agentsetup.UninstallOpts{Agents: agents, DryRun: dry, Force: force})
			if err != nil {
				return fmt.Errorf("uninstall: %w", err)
			}
			w := cmd.OutOrStdout()
			dryRunHeader(w, dry)
			if res.Nothing {
				lead := "nothing installed"
				if len(agents) == 1 {
					lead = string(agents[0]) + " is not installed"
				}
				printNotInstalled(w, t, lead, res.Unmanaged, res.Recoverable)
				return nil
			}
			fmt.Fprintf(w, "Target: %s\n\n", t.Root)
			rows := make([]row, 0, len(res.Items))
			for _, it := range res.Items {
				rows = append(rows, row{action: string(it.Action), item: it.Item, detail: itemDetail(it.Item, it.Detail), snippet: it.Snippet})
			}
			printRows(w, rows, 9)
			return nil
		},
	}
	cmd.Flags().Bool("dry-run", false, "show what would be removed without changing anything")
	cmd.Flags().Bool("force", false, "remove items even when they were changed")
	return quiet(cmd)
}

// targetAgents picks the agents a status or doctor run covers: the argument,
// or every agent in the manifest.
func targetAgents(args []string, m *agentsetup.Manifest) ([]agentsetup.Agent, error) {
	if len(args) == 1 {
		a, err := parseAgentArg(args[0])
		if err != nil {
			return nil, err
		}
		return []agentsetup.Agent{a}, nil
	}
	return m.InstalledAgents(), nil
}

// printNotInstalled reports a run that found no agent to act on. With no
// manifest, paths the exclude block still hides may be live wiring, so it
// points to the install that recovers them instead of saying nothing is
// installed. Otherwise it prints lead and the created paths that stay excluded.
func printNotInstalled(w io.Writer, t agentsetup.Target, lead string, unmanaged, recoverable []string) {
	if len(recoverable) > 0 {
		fmt.Fprintf(w, "no manifest in %s, but %s still hides paths lens writes:\n", t.Root, excludeDisplay(t))
		for _, p := range recoverable {
			fmt.Fprintf(w, "  %s\n", p)
		}
		fmt.Fprintln(w, "They may still hold ProjectLens wiring. To take them back under management:")
		for _, a := range agentsetup.RecoveryAgents(recoverable) {
			fmt.Fprintf(w, "  run: lens agent install %s\n", a)
		}
		return
	}
	fmt.Fprintln(w, lead)
	printUnmanaged(w, unmanaged, false)
}

// printNothingInstalled is printNotInstalled for status and doctor.
func printNothingInstalled(w io.Writer, t agentsetup.Target, m *agentsetup.Manifest) error {
	recoverable, err := agentsetup.RecoverablePaths(t)
	if err != nil {
		return fmt.Errorf("read exclude block: %w", err)
	}
	printNotInstalled(w, t, "nothing installed in "+t.Root+"\nrun: lens agent install <claude|codex>", m.UnreferencedPaths(), recoverable)
	return nil
}

// printUnmanaged lists created paths that stay excluded from git although no
// record refers to them. With no agent installed, deleting such a path leaves
// its exclude entry until the next uninstall, so the hint names it.
func printUnmanaged(w io.Writer, paths []string, installed bool) {
	if len(paths) == 0 {
		return
	}
	fmt.Fprintln(w, "\nExcluded from git but not managed (left by an earlier install):")
	for _, p := range paths {
		fmt.Fprintf(w, "  %s\n", p)
	}
	if installed {
		fmt.Fprintln(w, "Re-own with: lens agent install <agent>, or delete the path.")
	} else {
		fmt.Fprintln(w, "Re-own with: lens agent install <agent>, or delete the path, then run: lens agent uninstall all")
	}
}

func newAgentStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [claude|codex]",
		Short: "Show whether installed items match the canonical ones",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			if len(args) == 1 {
				if _, err := parseAgentArg(args[0]); err != nil {
					return err
				}
			}
			env, o, t, err := openAgentTarget(cmd)
			if err != nil {
				return err
			}
			m, err := agentsetup.LoadManifest(t)
			if err != nil {
				return fmt.Errorf("load manifest: %w", err)
			}
			agents, err := targetAgents(args, m)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if len(agents) == 0 {
				// With --json, stdout stays a JSON array and the text goes to stderr.
				tw := w
				if asJSON {
					tw = cmd.ErrOrStderr()
				}
				if err := printNothingInstalled(tw, t, m); err != nil {
					return err
				}
				if asJSON {
					if err := printJSON(w, []agentsetup.ItemStatus{}); err != nil {
						return err
					}
				}
				return failure()
			}
			assets, err := agentsetup.ResolveAssets(env, o)
			if err != nil {
				return err
			}

			all := []agentsetup.ItemStatus{}
			unhealthy := false
			if !asJSON {
				fmt.Fprintf(w, "Target: %s\n", t.Root)
			}
			for _, a := range agents {
				url := ""
				if ar := m.Agents[a]; ar != nil && ar.URL != "" {
					url = ar.URL
				} else if url, err = agentsetup.ResolveURL(env, o, t); err != nil {
					return fmt.Errorf("resolve url: %w", err)
				}
				canon, err := agentsetup.LoadCanonical(assets, a, url)
				if err != nil {
					return fmt.Errorf("load canonical assets: %w", err)
				}
				sts, err := agentsetup.Evaluate(t, a, canon, m)
				if err != nil {
					return fmt.Errorf("evaluate %s: %w", a, err)
				}
				for _, st := range sts {
					if st.State != agentsetup.StateCurrent {
						unhealthy = true
					}
				}
				all = append(all, sts...)
				if !asJSON {
					fmt.Fprintf(w, "\n%s\n", a)
					rows := make([]row, 0, len(sts))
					for _, st := range sts {
						rows = append(rows, row{action: string(st.State), item: st.Item, detail: itemDetail(st.Item, st.Detail)})
					}
					printRows(w, rows, 15)
				}
			}
			if asJSON {
				if err := printJSON(w, all); err != nil {
					return err
				}
			} else {
				printUnmanaged(w, m.UnreferencedPaths(), true)
			}
			if unhealthy {
				return failure()
			}
			return nil
		},
	}
	cmd.Flags().String("assets", "", "path to the projectlens agent/ directory")
	cmd.Flags().Bool("json", false, "print the item statuses as JSON")
	return quiet(cmd)
}

func newAgentDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor [claude|codex]",
		Short: "Check that an installed agent can reach the ProjectLens server",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			if len(args) == 1 {
				if _, err := parseAgentArg(args[0]); err != nil {
					return err
				}
			}
			env, o, t, err := openAgentTarget(cmd)
			if err != nil {
				return err
			}
			m, err := agentsetup.LoadManifest(t)
			if err != nil {
				return fmt.Errorf("load manifest: %w", err)
			}
			agents, err := targetAgents(args, m)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if len(agents) == 0 {
				// With --json, stdout stays a JSON array and the text goes to stderr.
				tw := w
				if asJSON {
					tw = cmd.ErrOrStderr()
				}
				if err := printNothingInstalled(tw, t, m); err != nil {
					return err
				}
				if asJSON {
					if err := printJSON(w, []agentsetup.Check{}); err != nil {
						return err
					}
				}
				return failure()
			}
			assets, assetsErr := agentsetup.ResolveAssets(env, o)
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			checks := agentsetup.Doctor(ctx, env, t, m, agentsetup.DoctorOpts{
				Agents:      agents,
				URLOverride: o.URL,
				AssetsDir:   assets,
				AssetsErr:   assetsErr,
			}, agentsetup.DoctorDeps{ExpectedTools: mcpserver.ToolNames()})
			if checks == nil {
				checks = []agentsetup.Check{}
			}
			failed := false
			for _, c := range checks {
				if c.Result == agentsetup.CheckFail {
					failed = true
				}
			}
			if asJSON {
				if err := printJSON(w, checks); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					line := fmt.Sprintf("%-8s %s", c.Result, c.Name)
					if c.Agent != "" {
						line += " [" + string(c.Agent) + "]"
					}
					if c.Detail != "" {
						line += ": " + c.Detail
					}
					fmt.Fprintln(w, line)
					if c.Next != "" {
						fmt.Fprintf(w, "         Next: %s\n", c.Next)
					}
				}
			}
			if failed {
				return failure()
			}
			return nil
		},
	}
	cmd.Flags().String("url", "", "MCP server URL to check instead of the installed one")
	cmd.Flags().String("assets", "", "path to the projectlens agent/ directory")
	cmd.Flags().Bool("json", false, "print the checks as JSON")
	return quiet(cmd)
}
