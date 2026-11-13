# Agent Setup UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `lens agent install | uninstall | status | doctor` so a developer can wire Claude Code or Codex into one repository, personally and without touching git state, and can check and diagnose that wiring.

**Architecture:** A new internal package `internal/agentsetup` holds all logic: resolving the target repo, URL and asset directory; editing the four kinds of destination file; a manifest of what was written; state evaluation; and the four operations. `cmd/lens/agent.go` holds only cobra wiring and output formatting. Git is driven by shelling out to `git`; tests run against real temp-dir repositories.

**Tech Stack:** Go 1.26, cobra, `encoding/json`, `os/exec` (git), `github.com/mark3labs/mcp-go` v0.48.0 client (already in `go.mod`). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-11-13-agent-setup-ux-design.md`

## Global Constraints

- Module path: `github.com/hman-pro/projectlens`. Internal packages only.
- No new module dependency. TOML is handled as a marked text block, not parsed with a library.
- Install writes only inside the target root, plus the repository's `info/exclude` under `git rev-parse --git-common-dir`.
- A path git tracks is never written, with or without `--force`.
- `.gitignore` is never edited.
- The `agent` commands never load `configs/index.yaml` and never open a database connection.
- Errors are wrapped with `fmt.Errorf("context: %w", err)`. No package-level mutable state; dependencies are passed in.
- All file writes are atomic: write to a temp file in the same directory, then `os.Rename`.
- The manifest is saved last in every operation.
- MCP server name is `projectlens`. Default port is `8484`. Port env var is `PROJECTLENS_MCP_PORT`.
- Commit messages: one concise line, no body, no trailers.
- Work on branch `agent-setup-ux`, created from `main` before Task 1.

## Deviation from the spec, for the maintainer to confirm

The spec's uninstall test says install then uninstall leaves the work tree byte-identical. This plan guarantees that for files install created and for `.codex/config.toml`. For a pre-existing `.mcp.json` or `settings.local.json`, the file is re-serialized by `encoding/json` (2-space indent, object keys sorted), so the guarantee there is semantic equality of all other content, not byte equality. Byte-preserving JSON edits would need a custom parser.

## Review Focus

1. **Malformed or empty existing JSON.** `.mcp.json` or `settings.local.json` that is not valid JSON: install stops before writing anything and names the file. A zero-byte file is treated as `{}`. Test in Task 4.
2. **Codex config that already defines the server outside the marked block.** An existing `[mcp_servers.projectlens]` table or a `mcp_servers.projectlens` dotted key: reported unmanaged, never duplicated (a duplicate table makes the file invalid TOML). Test in Task 4.
3. **Symlinked paths when comparing directories.** On macOS a temp dir is `/var/...` but git reports `/private/var/...`. Registry `repo_path` matching and the "inside the baked checkout" test must compare after `filepath.EvalSymlinks`. Test in Task 2.
4. **Interrupted install.** Files written, manifest not saved. On the next run those files have no record; any item whose content equals canonical is `current` and gets recorded, so the developer is not forced to `--force`. Test in Task 6.
5. **Skill directories with nested files and executable bits.** Copy preserves relative structure and file mode; the directory hash covers nested files. Test in Task 3.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/agentsetup/types.go` | `Agent`, `Kind`, `State`, `Item`, `Env`, placement table keyed by scope |
| `internal/agentsetup/gitrepo.go` | git queries and the `info/exclude` marked block |
| `internal/agentsetup/resolve.go` | registry lookup, `repo_path` base, target, URL, asset directory |
| `internal/agentsetup/assets.go` | load canonical items from the asset directory; hash and copy skill dirs |
| `internal/agentsetup/edit_json.go` | `.mcp.json` entry and `settings.local.json` hooks |
| `internal/agentsetup/edit_toml.go` | `.codex/config.toml` marked block |
| `internal/agentsetup/manifest.go` | manifest load, save, record helpers |
| `internal/agentsetup/evaluate.go` | per-item state; this is `status` |
| `internal/agentsetup/install.go` | install |
| `internal/agentsetup/uninstall.go` | uninstall |
| `internal/agentsetup/doctor.go` | doctor checks |
| `internal/mcpserver/toolnames.go` | exported list of registered tool names |
| `cmd/lens/agent.go` | cobra commands, flags, output, exit codes |

Each `.go` file has a `_test.go` beside it. Shared test helper `newRepo(t) string` in `internal/agentsetup/testutil_test.go`: runs `git init`, sets `user.name` and `user.email` locally, makes one empty commit, and returns the `EvalSymlinks`-resolved root.

---

### Task 1: Types, placement, and git helpers

**Files:**
- Create: `internal/agentsetup/types.go`, `internal/agentsetup/gitrepo.go`, `internal/agentsetup/testutil_test.go`
- Test: `internal/agentsetup/types_test.go`, `internal/agentsetup/gitrepo_test.go`

**Interfaces:**
- Produces:

```go
type Agent string // AgentClaude = "claude", AgentCodex = "codex"
func ParseAgent(s string) (Agent, error) // error text: `unsupported agent "x" (supported: claude, codex)`

type Kind string  // KindMCP = "mcp", KindSkill = "skill", KindHook = "hook"
type State string // StateMissing, StateCurrent, StateStale, StateModified, StateUnmanaged,
                  // StateManual, StateOrphaned, StateRemovedLocally
                  // values: "missing","current","stale","modified","unmanaged","manual","orphaned","removed-locally"

type Scope string // ScopePersonal = "personal" (the only one implemented)

// Placement returns target-root-relative, slash-separated paths.
type Placement struct {
    MCPFile   string // ".mcp.json" | ".codex/config.toml"
    SkillsDir string // ".claude/skills" | ".agents/skills"
    HooksFile string // ".claude/settings.local.json" | "" for codex
}
func PlacementFor(a Agent, s Scope) (Placement, error)

type Env struct {
    Cwd           string
    Home          string
    BakedCheckout string              // "" when the binary was not built by the Makefile
    Getenv        func(string) string
}

const ManifestPath = ".projectlens/agent-manifest.json"

// gitrepo.go
type Target struct {
    Root           string // absolute, symlinks resolved
    CommonDir      string // absolute git common dir
    LinkedWorktree bool   // git-dir differs from git-common-dir
}
func OpenTarget(dir string) (Target, error)            // dir must be the work-tree root itself
func GitRoot(dir string) (string, error)               // nearest work-tree root above dir
func (t Target) IsTracked(rel string) (bool, error)    // file, or any tracked file under a dir
func (t Target) Exists(rel string) bool                // Lstat, so a dangling symlink exists
func (t Target) IsSymlink(rel string) bool
func (t Target) Abs(rel string) string
func (t Target) SetExcludeBlock(paths []string) error  // sorted, exact replace; empty removes the block
func (t Target) ExcludeBlock() ([]string, error)
```

- Exclude block format, exact:

```
# >>> projectlens agent wiring (managed by `lens agent`) >>>
/.mcp.json
# <<< projectlens agent wiring <<<
```

- [ ] **Step 1: Create the branch**

Run: `git switch -c agent-setup-ux`

- [ ] **Step 2: Write the failing tests**

```go
func TestParseAgent(t *testing.T) {
    if a, err := ParseAgent("claude"); err != nil || a != AgentClaude { t.Fatal(a, err) }
    _, err := ParseAgent("cursor")
    if err == nil || !strings.Contains(err.Error(), "supported: claude, codex") { t.Fatal(err) }
}

func TestPlacementFor(t *testing.T) {
    c, _ := PlacementFor(AgentClaude, ScopePersonal)
    if c != (Placement{".mcp.json", ".claude/skills", ".claude/settings.local.json"}) { t.Fatal(c) }
    x, _ := PlacementFor(AgentCodex, ScopePersonal)
    if x != (Placement{".codex/config.toml", ".agents/skills", ""}) { t.Fatal(x) }
    if _, err := PlacementFor(AgentClaude, Scope("shared")); err == nil { t.Fatal("want error") }
}

func TestOpenTarget_NotARepo(t *testing.T)        // OpenTarget(t.TempDir()) returns an error
func TestOpenTarget_Subdirectory(t *testing.T)    // OpenTarget(root+"/sub") errors; GitRoot(root+"/sub") == root
func TestIsTracked(t *testing.T)                  // committed file true; untracked false; dir with one tracked file true
func TestExcludeBlock_RoundTrip(t *testing.T) {
    // pre-existing exclude content "*.log\n" is kept byte for byte around the block
    // Set([]string{"/.mcp.json","/.claude/skills/a/"}) then ExcludeBlock() returns them sorted
    // Set(nil) restores the file to exactly "*.log\n"
    // after Set, `git status --porcelain` no longer lists an untracked .mcp.json
}
func TestLinkedWorktree(t *testing.T) {
    // `git worktree add ../wt`; OpenTarget(wt).LinkedWorktree == true
    // OpenTarget(wt).CommonDir == main repo's .git; SetExcludeBlock writes there
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run 'TestParseAgent|TestPlacementFor|TestOpenTarget|TestIsTracked|TestExcludeBlock|TestLinkedWorktree'`
Expected: FAIL, build error (undefined identifiers).

- [ ] **Step 4: Implement `types.go` and `gitrepo.go`**

Git calls use `exec.Command("git", "-C", dir, ...)`. `OpenTarget` compares `rev-parse --show-toplevel` (after `EvalSymlinks`) with `dir` and errors with `not the root of a git work tree: <dir>` when they differ. `CommonDir` is `rev-parse --git-common-dir` made absolute. `SetExcludeBlock` creates `<CommonDir>/info/` when missing.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/agentsetup/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agentsetup
git commit -m "feat(agentsetup): add types, placement table, and git helpers"
```

---

### Task 2: Resolving target, URL, and assets

**Files:**
- Create: `internal/agentsetup/resolve.go`
- Test: `internal/agentsetup/resolve_test.go`

**Interfaces:**
- Consumes: `Env`, `Target`, `OpenTarget`, `GitRoot` (Task 1); `projects.LoadRegistry`, `projects.Registry`, `projects.Project` from `internal/projects`.
- Produces:

```go
type ResolveOpts struct {
    Project          string // --project
    ProjectsPath     string // --projects value
    ProjectsExplicit bool   // cmd.Flags().Changed("projects")
    URL              string // --url
    Port             int    // --port; 0 means unset
    Assets           string // --assets
}

// FindRegistry returns path == "" and reg == nil when there is no registry.
func FindRegistry(env Env, o ResolveOpts) (path string, reg *projects.Registry, err error)

// ResolveRepoPath returns the absolute, symlink-resolved repo path for p.
func ResolveRepoPath(env Env, registryPath string, p projects.Project) (string, error)

func ResolveTarget(env Env, o ResolveOpts) (Target, error)
func ResolveURL(env Env, o ResolveOpts, t Target) (string, error)
func ResolveAssets(env Env, o ResolveOpts) (string, error)

var ErrRelativeRepoPath // wrapped by ResolveRepoPath
```

Rules, all from the spec's "Target repository and URL resolution" and "Locating the canonical assets":

- `FindRegistry` order: explicit `--projects` (missing file is an error); `<Cwd>/configs/projects.yaml`; `<BakedCheckout>/configs/projects.yaml`; none.
- `ResolveRepoPath`: absolute path used as is. Relative path: registry file inside `BakedCheckout` gives base `BakedCheckout`; else registry file equal to `<Cwd>/configs/projects.yaml` gives base `Cwd`; else error wrapping `ErrRelativeRepoPath` with text `registry <path>: project "<slug>" has relative repo_path "<p>"; use an absolute repo_path`.
- `ResolveTarget`: with `--project`, `OpenTarget(ResolveRepoPath(...))`; otherwise `OpenTarget(GitRoot(Cwd))`. Outside a repo with no `--project`: `not inside a git repository; run from the target repo or pass --project <slug>`.
- `ResolveURL` order: `--url`; `--project` gives `http://localhost:<port>/<slug>/mcp`; registry exists and exactly one project's resolved repo path equals `t.Root` gives that slug's URL; registry exists and none match gives the error below; no registry gives `http://localhost:<port>/mcp`. Projects whose repo path cannot be resolved are skipped during matching. Port: `o.Port`, else `PROJECTLENS_MCP_PORT`, else 8484.
- No-match error, exact: `registry <path> has no project for <root>; the server only serves /<slug>/mcp in multi-project mode: pass --project <slug> or --url <url>`.
- `ResolveAssets`: `--assets`; else `<BakedCheckout>/agent`. The directory must contain `skills/`. Otherwise: `canonical agent assets not found; pass --assets <path-to-projectlens>/agent`.

- [ ] **Step 1: Write the failing tests**

Helper `writeRegistry(t, dir, yaml string) string` writes `<dir>/configs/projects.yaml`. Registries need `database_url` and a valid `storage_schema` to pass `Validate`.

```go
func TestFindRegistry_Order(t *testing.T)
    // explicit wins over cwd; cwd wins over baked; baked used when cwd has none; ("", nil, nil) when none
    // explicit path that does not exist: error

func TestResolveRepoPath(t *testing.T) {
    // absolute repo_path: returned as is regardless of registry location
    // registry in baked checkout, repo_path ".", Cwd = some other repo  -> baked checkout root
    // registry at <Cwd>/configs/projects.yaml, BakedCheckout "", repo_path "." -> Cwd
    // registry in a third dir, BakedCheckout "", repo_path "." -> errors.Is(err, ErrRelativeRepoPath)
}

func TestResolveRepoPath_NeverCwdForForeignRegistry(t *testing.T) {
    // the spec's P1 case: Cwd=/shop, ProjectsExplicit path=/pl/configs/projects.yaml,
    // BakedCheckout=/pl, repo_path "."  -> /pl, and never /shop
}

func TestResolveRepoPath_SymlinkedDirs(t *testing.T)
    // baked checkout given through a symlink still counts as "inside the baked checkout"

func TestResolveURL(t *testing.T) {
    // --url wins over everything
    // --project shop -> http://localhost:8484/shop/mcp
    // Port 9000 -> http://localhost:9000/shop/mcp ; env PROJECTLENS_MCP_PORT=9100 used when Port==0
    // no --project, registry has a project whose repo path == target root -> that slug URL
    // no --project, registry exists, no match -> error containing "only serves /<slug>/mcp"
    // no registry -> http://localhost:8484/mcp
}

func TestResolveTarget(t *testing.T)
    // Cwd in a subdirectory of a repo -> repo root
    // Cwd outside any repo, no --project -> error containing "pass --project"
    // --project whose repo_path is not a git work-tree root -> error

func TestResolveAssets(t *testing.T)
    // --assets wins; baked used otherwise; dir without skills/ -> error containing "--assets"
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run 'TestFindRegistry|TestResolve'`
Expected: FAIL, undefined identifiers.

- [ ] **Step 3: Implement `resolve.go`**

"Inside" is tested on `EvalSymlinks`-resolved paths with `filepath.Rel` not starting with `..`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/agentsetup/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agentsetup
git commit -m "feat(agentsetup): resolve target repo, MCP URL, and asset directory"
```

---

### Task 3: Canonical assets and hook IDs

**Files:**
- Create: `internal/agentsetup/assets.go`
- Modify: `agent/claude/settings-snippet.json` (prefix each of the four `command` strings)
- Test: `internal/agentsetup/assets_test.go`

**Interfaces:**
- Consumes: `Agent`, `Kind`, `Placement` (Task 1).
- Produces:

```go
type Item struct {
    Agent Agent
    Kind  Kind
    ID    string // skill name | hook ID | "projectlens" for the MCP entry
    Path  string // target-relative: the skill dir, the hooks file, or the MCP file
}

type Canon struct {
    Item
    Hash string
    // exactly one of these is set, by Kind
    SkillSrc string // absolute source directory
    Hook     Hook
    URL      string
}

type Hook struct {
    ID      string
    Event   string // "SessionStart" | "PreToolUse" | "Stop"
    Matcher string
    Command string // full command including the ID prefix
}

func LoadCanonical(assetsDir string, a Agent, url string) ([]Canon, error)
func HookID(command string) (id string, ok bool)   // parses the leading marker
func HashString(s string) string                    // hex sha256
func HashDir(dir string) (string, error)
func CopyDir(src, dst string) error                 // dst must not exist
func MCPHash(url string) string                     // HashString("mcp\n" + url)
```

- Hook ID marker, exact: the command starts with `: projectlens-hook=<id>; ` where `<id>` matches `[a-z0-9-]+`.
- The four IDs, in snippet order: `session-index-status`, `pre-edit-impact`, `stop-capture-knowledge`, `stop-compliance-audit`.
- Hook hash is `HashString(command)`.
- `HashDir`: for every regular file under `dir`, sorted by slash-separated relative path, feed `relpath + "\x00" + hex sha256 of content + "\n"` into one sha256. Symlinks inside a skill directory are an error.
- `LoadCanonical` for Claude returns: one MCP item, one skill item per directory under `skills/`, four hook items. For Codex: one MCP item and the skill items. Order: MCP, skills sorted by name, hooks in snippet order.
- A snippet hook without a valid ID marker is an error naming the event. Duplicate IDs are an error.

- [ ] **Step 1: Write the failing tests**

```go
func TestHookID(t *testing.T) {
    id, ok := HookID(": projectlens-hook=pre-edit-impact; echo hi")
    if !ok || id != "pre-edit-impact" { t.Fatal(id, ok) }
    if _, ok := HookID("echo hi"); ok { t.Fatal("no marker") }
    if _, ok := HookID(": projectlens-hook=Bad_ID; echo"); ok { t.Fatal("bad id") }
}

func TestHashDir_NestedAndStable(t *testing.T)
    // same tree in two temp dirs -> equal hash; changing a nested file changes it;
    // renaming a file changes it; a symlink inside -> error

func TestCopyDir_PreservesModeAndNesting(t *testing.T)
    // src has a/b/run.sh mode 0755 and SKILL.md 0644; after CopyDir modes match and HashDir(src)==HashDir(dst)

func TestLoadCanonical_RealAssets(t *testing.T) {
    // assetsDir = repo's agent/ (../../agent from the package dir)
    // claude: 1 mcp + 2 skills + 4 hooks; hook IDs equal the four listed above, in order
    // claude paths: ".mcp.json", ".claude/skills/capture-knowledge", ".claude/skills/use-projectlens",
    //               ".claude/settings.local.json" x4
    // codex: 1 mcp (".codex/config.toml") + 2 skills under ".agents/skills/"; no hooks
    // MCP Canon.Hash == MCPHash(url)
}

func TestLoadCanonical_HookWithoutID(t *testing.T)   // fixture snippet lacking a marker -> error
func TestLoadCanonical_DuplicateHookID(t *testing.T) // -> error
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run 'TestHookID|TestHashDir|TestCopyDir|TestLoadCanonical'`
Expected: FAIL, undefined identifiers.

- [ ] **Step 3: Add the ID prefix to the four commands in `agent/claude/settings-snippet.json`**

Each `"command": "echo '<system-reminder>..."` becomes `"command": ": projectlens-hook=<id>; echo '<system-reminder>..."`. Nothing else in the file changes.

- [ ] **Step 4: Implement `assets.go`**

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/agentsetup/ && python3 -m json.tool agent/claude/settings-snippet.json >/dev/null`
Expected: PASS, and the JSON check exits 0.

- [ ] **Step 6: Commit**

```bash
git add internal/agentsetup agent/claude/settings-snippet.json
git commit -m "feat(agentsetup): load canonical assets and give hooks stable IDs"
```

---

### Task 4: Destination file editors

**Files:**
- Create: `internal/agentsetup/edit_json.go`, `internal/agentsetup/edit_toml.go`
- Test: `internal/agentsetup/edit_json_test.go`, `internal/agentsetup/edit_toml_test.go`

**Interfaces:**
- Consumes: `Hook`, `HookID`, `HashString`, `MCPHash` (Task 3).
- Produces:

```go
// .mcp.json — owns mcpServers.projectlens only
func ReadMCPJSON(path string) (hash string, present bool, err error)
func WriteMCPJSON(path, url string) error
func RemoveMCPJSON(path string) (empty bool, err error)

// settings.local.json — owns hook entries carrying a ProjectLens hook ID
func ReadHooks(path string) (map[string]string, error) // id -> HashString(command)
func WriteHook(path string, h Hook) error
func RemoveHook(path, id string) (empty bool, err error)

// .codex/config.toml — owns the marked block
func ReadCodexBlock(path string) (hash string, present bool, foreign bool, err error)
func WriteCodexBlock(path, url string) error
func RemoveCodexBlock(path string) (empty bool, err error)

var ErrInvalidJSON // wrapped with the file path
```

Behavior:

- All `Read*` functions return "not present" for a missing file, without error.
- JSON files are decoded into `map[string]json.RawMessage` at each level the editor touches, so unrelated values pass through untouched. Output is `json.MarshalIndent` with 2 spaces and a trailing newline. A zero-byte or whitespace-only file is `{}`. Anything else that fails to parse returns an error wrapping `ErrInvalidJSON`, and the file is not modified.
- `.mcp.json` entry written, exact: `{"type": "http", "url": "<url>"}`.
- `ReadMCPJSON` hash: `MCPHash(url)` when the entry is exactly an object with `type` equal to `"http"` or `"streamable-http"` and a string `url` and no other keys; otherwise `HashString` of the entry's compact JSON. This makes any extra key or a different URL compare as different.
- `empty` from a remove means the remaining document is `{}` or holds only an empty `mcpServers` or `hooks` object. The remover leaves the file in place; the caller decides whether to delete it.
- Hooks layout is `hooks.<Event>` = array of `{ "matcher": string, "hooks": [ {"type":"command","command": string} ] }`. `ReadHooks` scans every event and group. `WriteHook`: when an entry with the same ID exists anywhere, replace its `command` in place; otherwise append it to the first group of that event whose `matcher` equals `h.Matcher`, creating the group and the event as needed. `RemoveHook` deletes the entry, then its group when the group's `hooks` is empty, then the event key when its array is empty, then `hooks` when empty.
- Codex block written, exact:

```
# >>> projectlens (managed by `lens agent`) >>>
[mcp_servers.projectlens]
url = "<url>"
# <<< projectlens <<<
```

- `WriteCodexBlock` replaces an existing marked block in place; otherwise appends it, preceded by one blank line when the file is non-empty. `RemoveCodexBlock` removes the block and that one blank line, so write then remove restores the original bytes. `empty` means only whitespace remains.
- `ReadCodexBlock` hash: `MCPHash(url)` when the block body is exactly the two lines above for some URL; otherwise `HashString` of the body. `foreign` is true when, outside the marked block, a line matches `^\s*\[mcp_servers\.projectlens\]` or `^\s*mcp_servers\.projectlens\b` or `^\s*\[mcp_servers\]` followed later by a `projectlens` key before the next table header.

- [ ] **Step 1: Write the failing tests**

```go
// edit_json_test.go
func TestMCPJSON_CreateReadRemove(t *testing.T) {
    // Write on a missing file creates it; Read -> (MCPHash(url), true)
    // Remove -> empty == true
}
func TestMCPJSON_PreservesOtherServers(t *testing.T) {
    // start: {"mcpServers":{"other":{"command":"x","args":["a"]}},"extra":[1,2]}
    // after Write: "other" and "extra" deep-equal the originals; projectlens present
    // after Remove: document deep-equals the start; empty == false
}
func TestMCPJSON_DifferentEntryHashesDiffer(t *testing.T)
    // entry with an added "headers" key, and entry with another URL: hash != MCPHash(url)
func TestMCPJSON_StreamableHTTPAliasIsCurrent(t *testing.T)
    // {"type":"streamable-http","url":U} -> hash == MCPHash(U)
func TestJSON_InvalidIsRefusedUntouched(t *testing.T)
    // file content "{not json": Write returns errors.Is(err, ErrInvalidJSON) with the path in the text;
    // file bytes unchanged. Same for WriteHook.
func TestJSON_EmptyFileTreatedAsObject(t *testing.T)   // zero-byte file: Write succeeds

func TestHooks_WriteAllThenRead(t *testing.T)
    // write the four canonical hooks; ReadHooks returns four IDs with HashString(command)
    // the two Stop hooks share one group; PreToolUse group has matcher "Edit|Write|MultiEdit"
func TestHooks_PreservesForeignHooksAndKeys(t *testing.T)
    // start has permissions:{...} and a foreign Stop hook; after write+remove of ours,
    // document deep-equals the start
func TestHooks_ReplaceInPlaceNeverDuplicates(t *testing.T)
    // write hook, hand-edit its command text keeping the marker, WriteHook again:
    // exactly one entry with that ID
func TestHooks_RemoveCleansEmptyContainers(t *testing.T)
    // file created by us only: after removing all four, empty == true

// edit_toml_test.go
func TestCodexBlock_AppendAndRemoveRestoresBytes(t *testing.T)
    // original "model = \"x\"\n[other]\nk = 1\n": Write then Remove -> identical bytes
func TestCodexBlock_CreateOnMissingFile(t *testing.T)  // Remove -> empty == true
func TestCodexBlock_ReplaceInPlace(t *testing.T)        // second Write with another URL: one block, new URL
func TestCodexBlock_EditedBodyHashDiffers(t *testing.T)
func TestCodexBlock_ForeignTableDetected(t *testing.T)
    // "[mcp_servers.projectlens]\nurl = \"z\"\n" with no markers -> present false, foreign true
    // "mcp_servers.projectlens = { url = \"z\" }" -> foreign true
    // "[mcp_servers.other]" only -> foreign false
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run 'TestMCPJSON|TestJSON|TestHooks|TestCodexBlock'`
Expected: FAIL, undefined identifiers.

- [ ] **Step 3: Implement `edit_json.go` and `edit_toml.go`**

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/agentsetup/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agentsetup
git commit -m "feat(agentsetup): add editors for MCP config, hooks, and Codex config"
```

---

### Task 5: Manifest and state evaluation (`status`)

**Files:**
- Create: `internal/agentsetup/manifest.go`, `internal/agentsetup/evaluate.go`
- Test: `internal/agentsetup/manifest_test.go`, `internal/agentsetup/evaluate_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1, 3, 4.
- Produces:

```go
type Record struct {
    Kind Kind   `json:"kind"`
    ID   string `json:"id"`
    Path string `json:"path"`
    Hash string `json:"hash"`
}
type AgentRecord struct {
    URL          string   `json:"url"`
    Items        []Record `json:"items"`
    CreatedPaths []string `json:"created_paths"` // files and skill dirs install created; drives exclude and deletion
    CreatedDirs  []string `json:"created_dirs"`  // parent dirs install created; removed on uninstall when empty
}
type Manifest struct {
    Version int                    `json:"version"` // 1
    Agents  map[Agent]*AgentRecord `json:"agents"`
}
func LoadManifest(t Target) (*Manifest, error) // missing file -> empty manifest, no error
func (m *Manifest) Save(t Target) error        // deletes the file and an empty .projectlens/ when no agents remain
func (m *Manifest) Record(a Agent, kind Kind, id string) (Record, bool)
func (m *Manifest) InstalledAgents() []Agent    // sorted

type ItemStatus struct {
    Item
    State   State
    Tracked bool
    Detail  string // e.g. "symlink", "foreign [mcp_servers.projectlens] table"
}

// installedHash reads the owned entry from disk for one item.
func installedHash(t Target, it Item) (hash string, present bool, detail string, err error)

// Evaluate returns one status per canonical item, then one per orphaned record.
func Evaluate(t Target, a Agent, canon []Canon, m *Manifest) ([]ItemStatus, error)
```

State rules, in this order:

1. Path tracked by git: `current` when present and hash equals canonical, else `manual`. `Tracked` is true. The manifest is not consulted.
2. Skill path is a symlink: `unmanaged`, detail `symlink`.
3. Codex MCP with `foreign` true and no marked block: `unmanaged`, detail naming the table.
4. Not present: `removed-locally` when the item is a hook that has a record; otherwise `missing`.
5. Hash equals canonical: `current`, with or without a record.
6. No record: `unmanaged`.
7. Hash equals recorded: `stale`.
8. Otherwise: `modified`.

Orphans: each record for agent `a` with no canonical item of the same kind and ID gets a status with state `orphaned`.

- [ ] **Step 1: Write the failing tests**

```go
func TestManifest_RoundTripAndCleanup(t *testing.T)
    // Save then Load deep-equal; Save with no agents removes the file and .projectlens/

// evaluate_test.go uses a fixture asset dir (two tiny skills, a two-hook snippet) and newRepo(t).
func TestEvaluate_States(t *testing.T) {
    // table test, one row per rule above, for each kind where the rule applies:
    //   nothing on disk                              -> missing
    //   disk == canonical, no record                 -> current
    //   disk == canonical, record                    -> current
    //   disk == record, canonical changed            -> stale
    //   disk edited, record present                  -> modified
    //   disk present and different, no record        -> unmanaged
    //   skill dir is a symlink                       -> unmanaged, Detail "symlink"
    //   hook recorded, marker removed from file      -> removed-locally
    //   hook recorded, command edited, marker kept   -> modified
    //   record for a skill absent from canonical     -> orphaned
}
func TestEvaluate_TrackedPaths(t *testing.T) {
    // .mcp.json committed with the exact expected entry -> current, Tracked true
    // .mcp.json committed without the entry             -> manual, Tracked true
    // .mcp.json committed with a different URL          -> manual
    // a tracked file under a skill dir, content equal   -> current, Tracked true
}
func TestEvaluate_CodexForeignTable(t *testing.T)   // -> unmanaged
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run 'TestManifest|TestEvaluate'`
Expected: FAIL, undefined identifiers.

- [ ] **Step 3: Implement `manifest.go` and `evaluate.go`**

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/agentsetup/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agentsetup
git commit -m "feat(agentsetup): add manifest and per-item state evaluation"
```

---

### Task 6: Install

**Files:**
- Create: `internal/agentsetup/install.go`
- Test: `internal/agentsetup/install_test.go`

**Interfaces:**
- Consumes: Tasks 1 to 5.
- Produces:

```go
type InstallOpts struct {
    Agent  Agent
    URL    string // already resolved
    DryRun bool
    Force  bool
}
type Action string // "written","current","updated","skipped","manual","removed"
type ItemResult struct {
    ItemStatus          // the state before install
    Action  Action
    Snippet string      // for "manual": the exact entry to add or replace
}
type InstallResult struct {
    Items          []ItemResult
    ExcludePaths   []string // the full exclude block after this run
    WorktreeNotice bool     // target is a linked worktree and the exclude block is non-empty
}
func Install(t Target, assetsDir string, o InstallOpts) (InstallResult, error)
```

Action per prior state:

| State | Without `--force` | With `--force` |
|---|---|---|
| missing | write: `written` | same |
| current | record if unrecorded: `current` | same |
| stale | overwrite: `updated` | same |
| modified, unmanaged | `skipped` | overwrite: `updated` |
| removed-locally | `skipped` | write: `written` |
| manual (tracked) | `manual` with `Snippet` | same; never written |
| orphaned, disk equals record | delete: `removed` | same |
| orphaned, disk differs | `skipped` | delete: `removed` |

Rules:

- Evaluate first, for all items, before any write. A JSON parse error in any destination file aborts with nothing written.
- A skill that is a symlink and is being overwritten: `os.Remove` the link itself, then `CopyDir`. Never open a path through the link for writing.
- Overwriting a skill directory: copy to a sibling temp directory, remove the old directory, rename.
- "Created" tracking: before writing a file or skill directory that does not exist, append its path to `CreatedPaths`; append each parent directory that had to be created to `CreatedDirs`. Paths already listed stay listed. A path that existed before the first install is never added.
- Exclude block after install: the union, over all agents in the manifest, of `"/" + path` for every `CreatedPaths` entry (skill directories get a trailing `/`), plus `/.projectlens/agent-manifest.json`.
- Manifest: set `Agents[a].URL`, upsert a record for every item now equal to canonical, drop records for removed orphans, save last.
- `DryRun`: compute and return the same result; write nothing, including the manifest and the exclude block.
- `Snippet` for a tracked MCP file is the exact JSON object or TOML block; for a tracked hooks file, the hook's JSON entry.

- [ ] **Step 1: Write the failing tests**

Helper `porcelain(t, root) string` returns `git status --porcelain` output.

```go
func TestInstall_CleanRepo_Claude(t *testing.T) {
    // all items "written"; files exist; manifest records URL and every item
    // porcelain before == porcelain after (both empty)
    // ExcludePaths == ["/.claude/settings.local.json","/.claude/skills/<a>/","/.claude/skills/<b>/",
    //                  "/.mcp.json","/.projectlens/agent-manifest.json"]
}
func TestInstall_CleanRepo_Codex(t *testing.T)   // .codex/config.toml and .agents/skills/*; no hooks file
func TestInstall_Idempotent(t *testing.T)         // second run: every Action "current"; every file's bytes equal the first run's
func TestInstall_DryRunWritesNothing(t *testing.T)
    // tree listing, exclude file, and absence of .projectlens/ identical before and after
func TestInstall_PreexistingUntrackedStaysVisible(t *testing.T) {
    // developer's own untracked .mcp.json and .claude/settings.local.json exist
    // porcelain before == porcelain after, and both still list those two files
    // neither path appears in ExcludePaths or CreatedPaths
    // their other content is preserved
}
func TestInstall_TrackedFileNeverWritten(t *testing.T) {
    // committed .mcp.json without our entry: Action "manual", Snippet contains the URL,
    // file bytes unchanged, also with Force
    // repeat for committed .claude/settings.local.json and .codex/config.toml
}
func TestInstall_StaleIsUpdated(t *testing.T)        // change a fixture skill file, reinstall: "updated"
func TestInstall_ModifiedIsSkippedThenForced(t *testing.T)
func TestInstall_EditedHookNotDuplicated(t *testing.T) {
    // edit a hook's command text keeping its marker; reinstall -> "skipped";
    // ReadHooks shows exactly one entry for that ID, still the edited text
}
func TestInstall_HookMarkerRemovedNotReadded(t *testing.T) {
    // strip the marker from one hook; reinstall -> "skipped" (state removed-locally);
    // the file has no new entry for that ID
}
func TestInstall_SymlinkedSkill(t *testing.T) {
    // .claude/skills/<a> is a symlink to an outside dir with a sentinel file
    // without Force: "skipped", link intact
    // with Force: path is now a real directory; the outside dir and its sentinel are untouched
}
func TestInstall_OrphanRemoved(t *testing.T)   // delete a fixture skill, reinstall: "removed", dir and exclude entry gone
func TestInstall_InvalidJSONAbortsBeforeAnyWrite(t *testing.T)
    // .claude/settings.local.json is "{bad"; install errors; no skills dir, no manifest, exclude untouched
func TestInstall_RecoversAfterInterruptedRun(t *testing.T)
    // install, delete the manifest, reinstall without Force: every Action "current", manifest restored
func TestInstall_LinkedWorktree(t *testing.T) {
    // files land in the worktree; the exclude block is in the main repo's .git/info/exclude;
    // WorktreeNotice true; nothing else created outside the worktree root
}
func TestInstall_CodexForeignTable(t *testing.T)  // "skipped"; file has exactly one [mcp_servers.projectlens] header
func TestInstall_BothAgents(t *testing.T)          // claude then codex: exclude block is the union; both recorded
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run TestInstall`
Expected: FAIL, undefined `Install`.

- [ ] **Step 3: Implement `install.go`**

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/agentsetup/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agentsetup
git commit -m "feat(agentsetup): implement install"
```

---

### Task 7: Uninstall

**Files:**
- Create: `internal/agentsetup/uninstall.go`
- Test: `internal/agentsetup/uninstall_test.go`

**Interfaces:**
- Consumes: Tasks 1, 4, 5.
- Produces:

```go
type UninstallOpts struct {
    Agents []Agent // one agent, or both for "all"
    DryRun bool
    Force  bool
}
type UninstallResult struct {
    Items   []ItemResult // Action is "removed", "skipped", or "manual"
    Nothing bool         // no agent in Agents had a manifest record
}
func Uninstall(t Target, o UninstallOpts) (UninstallResult, error)
```

Rules:

- Works from the manifest alone; takes no asset directory.
- Per record: tracked path gives `manual`, with `Snippet` naming the entry to delete by hand. Disk hash equals recorded hash gives `removed`. Different gives `skipped`, or `removed` with `Force`. Already absent gives `removed`.
- Removing a merged entry uses the Task 4 removers. When the remover reports `empty` and the file is in `CreatedPaths`, delete the file. Otherwise leave it.
- After items: remove each `CreatedDirs` entry that is now empty, deepest first.
- A skipped item keeps its record and its `CreatedPaths` entry, so the manifest and exclude entry for it survive. When every record of an agent is gone, delete the agent from the manifest.
- Rewrite the exclude block from what remains; `Manifest.Save` removes the manifest and `.projectlens/` when no agents remain, and the exclude block is then removed entirely.
- `DryRun` writes nothing.

- [ ] **Step 1: Write the failing tests**

```go
func TestUninstall_RestoresCleanRepoExactly(t *testing.T) {
    // snapshot: recursive listing with file bytes of the work tree, plus bytes of info/exclude
    // install claude, install codex, uninstall both -> snapshot identical; .claude, .agents, .codex,
    // .projectlens do not exist
}
func TestUninstall_KeepsPreexistingFilesAndContent(t *testing.T) {
    // pre-existing untracked .mcp.json with another server, and .codex/config.toml with other keys
    // install then uninstall: .mcp.json JSON deep-equals the original; config.toml bytes identical
}
func TestUninstall_OneAgentLeavesOtherWorking(t *testing.T)
    // install both, uninstall claude: codex items all "current" under Evaluate; exclude block lists only codex paths
func TestUninstall_ModifiedSkippedThenForced(t *testing.T)
    // edited skill file: "skipped", dir still present, record and exclude entry kept; Force removes it
func TestUninstall_TrackedNeverTouched(t *testing.T)   // "manual", bytes unchanged
func TestUninstall_DryRun(t *testing.T)                 // nothing changes on disk
func TestUninstall_NothingInstalled(t *testing.T)       // Nothing == true, no error
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run TestUninstall`
Expected: FAIL, undefined `Uninstall`.

- [ ] **Step 3: Implement `uninstall.go`**

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/agentsetup/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agentsetup
git commit -m "feat(agentsetup): implement uninstall"
```

---

### Task 8: Doctor

**Files:**
- Create: `internal/agentsetup/doctor.go`, `internal/mcpserver/toolnames.go`
- Test: `internal/agentsetup/doctor_test.go`, `internal/mcpserver/toolnames_test.go`

**Interfaces:**
- Consumes: Tasks 1 to 5; `client.NewStreamableHttpClient`, `(*client.Client).Start`, `.Initialize`, `.ListTools`, `.Close` from `github.com/mark3labs/mcp-go/client`.
- Produces:

```go
// internal/mcpserver/toolnames.go
func ToolNames() []string // names from New(nil, nil, 0, "").toolRegistry(), sorted

// internal/agentsetup/doctor.go
type CheckResult string // "pass","fail","skipped","manual"
type Check struct {
    Name   string // one of the eight names below
    Agent  Agent  // "" for checks that are not per agent
    Result CheckResult
    Detail string
    Next   string // required when Result is "fail" or "manual"
}
type DoctorDeps struct {
    ExpectedTools []string
    ListTools     func(ctx context.Context, url string) ([]string, error) // nil -> MCPListTools
    HTTPGet       func(ctx context.Context, url string) (status int, err error) // nil -> default client, 5s timeout
    LookPath      func(string) (string, error) // nil -> exec.LookPath
}
type DoctorOpts struct {
    Agents      []Agent
    URLOverride string // --url
    AssetsDir   string // "" when ResolveAssets failed
    AssetsErr   error
}
func Doctor(ctx context.Context, env Env, t Target, m *Manifest, o DoctorOpts, d DoctorDeps) []Check
func MCPListTools(ctx context.Context, url string) ([]string, error)
```

Check names, in order, and their rules:

| Name | Rule | `Next` on failure |
|---|---|---|
| `assets-found` | `AssetsErr == nil` | the `--assets` text from Task 2 |
| `items-current` | per agent: every `Evaluate` status is `current`. `skipped` when assets were not found | `run: lens agent install <agent>`; for `manual` items, `add the printed entry to <path>` |
| `server-reachable` | per distinct URL (recorded, or `URLOverride`). `HTTPGet` error is a failure; 404 on a URL whose path is not `/mcp` is a failure; 503 is a failure; any other status passes | refused: `start the server: make mcp` (or `make docker-up`); 404: `slug is not registered in configs/projects.yaml`; 503: `run: lens migrate --project <slug>` |
| `tools-match` | `ListTools` names equal `ExpectedTools` as sets. `skipped` when `server-reachable` failed | `Detail` lists missing and extra names; `rebuild and restart the server, or check the URL points at ProjectLens` |
| `installed-url-matches` | per agent: URL read from the installed MCP entry equals the recorded URL. `skipped` when the entry is absent | `run: lens agent install <agent>` |
| `not-shadowed` | Claude: `<Home>/.claude.json` has no `mcpServers.projectlens` with a different URL. Codex: `<Home>/.codex/config.toml` has no `[mcp_servers.projectlens]` with a different `url`. Missing or unparseable home file passes. Read-only | `remove or update the projectlens entry in <file>` |
| `agent-will-load` | Codex: `pass` when `<Home>/.codex/config.toml` contains a `[projects."<t.Root>"]` table with `trust_level = "trusted"`; otherwise `manual`. Claude: always `manual` | Codex: `open Codex in <root> and trust the project`; Claude: `run /mcp in Claude Code in <root> and approve "projectlens"` |
| `lens-on-path` | `LookPath("lens")` succeeds | `add <dir of the running binary> to PATH, or run: make install` |

- With an empty `o.Agents`, per-agent checks are omitted and the only checks returned are `assets-found` and `lens-on-path`; the caller reports "nothing installed".
- `Doctor` never writes any file.

- [ ] **Step 1: Write the failing tests**

Test server helper: `newMCPServer(t, names ...string) string` builds a `server.NewMCPServer("projectlens","1.0.0")` with one no-op tool per name and returns `httptest.NewServer(server.NewStreamableHTTPServer(s)).URL + "/mcp"`.

```go
func TestToolNames(t *testing.T)   // in internal/mcpserver: non-empty, sorted, contains "find_symbol" and "index_status"

func TestDoctor_Healthy(t *testing.T) {
    // install claude against the test server URL; ExpectedTools = the server's names
    // every check is pass, except agent-will-load which is manual
}
func TestDoctor_ConnectionRefused(t *testing.T)
    // URL on a closed port: server-reachable fail with Next containing "start the server";
    // tools-match skipped
func TestDoctor_SlugNotRegistered(t *testing.T)   // HTTPGet stub returns 404 for /shop/mcp: Next contains "not registered"
func TestDoctor_SchemaNotMigrated(t *testing.T)   // 503: Next contains "lens migrate --project shop"
func TestDoctor_ToolMismatch(t *testing.T)
    // server has ["a","b"], expected ["a","c"]: fail; Detail names missing "c" and extra "b"
func TestDoctor_InstalledURLDrift(t *testing.T)   // hand-edit .mcp.json URL: installed-url-matches fail
func TestDoctor_Shadowed(t *testing.T)
    // Home has .claude.json with mcpServers.projectlens at another URL: not-shadowed fail, file bytes unchanged
func TestDoctor_CodexTrusted(t *testing.T)        // trusted table present -> pass; absent -> manual
func TestDoctor_NotInstalledIsNotAnItemFailure(t *testing.T)
    // empty Agents: no items-current check in the result
func TestDoctor_URLOverride(t *testing.T)         // probes the override, not the recorded URL
func TestDoctor_EveryFailureHasNext(t *testing.T) // over all failing fixtures above: Next != "" for fail and manual
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agentsetup/ -run TestDoctor && go test ./internal/mcpserver/ -run TestToolNames`
Expected: FAIL, undefined identifiers.

- [ ] **Step 3: Implement `toolnames.go` and `doctor.go`**

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/agentsetup/ ./internal/mcpserver/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agentsetup internal/mcpserver
git commit -m "feat(agentsetup): implement doctor checks"
```

---

### Task 9: CLI wiring, exit codes, and the baked checkout path

**Files:**
- Create: `cmd/lens/agent.go`
- Modify: `cmd/lens/main.go` (register `newAgentCmd()`; map exit codes after `rootCmd.Execute()`), `Makefile` (`build-lens`, `install`)
- Test: `cmd/lens/agent_test.go`

**Interfaces:**
- Consumes: all of `internal/agentsetup`; `mcpserver.ToolNames`.
- Produces:

```go
var bakedCheckout string // set by -ldflags "-X main.bakedCheckout=<dir>"

type exitError struct { code int; err error } // implements error and Unwrap
func newAgentCmd() *cobra.Command
```

Commands and flags, exact:

```
lens agent install   <claude|codex>      [--url] [--port] [--assets] [--dry-run] [--force]
lens agent uninstall <claude|codex|all>  [--dry-run] [--force]
lens agent status    [claude|codex]      [--assets] [--json]
lens agent doctor    [claude|codex]      [--url] [--assets] [--json]
```

`--project` and `--projects` are the existing root persistent flags; `ProjectsExplicit` is `cmd.Flags().Changed("projects")`.

Behavior:

- `Env` is built from `os.Getwd`, `os.UserHomeDir`, `bakedCheckout`, `os.Getenv`.
- Wrong argument count or an unsupported agent returns `exitError{2}`.
- `status` and `doctor` with no agent use `Manifest.InstalledAgents()`. With none installed they print `nothing installed in <root>` and `run: lens agent install <claude|codex>`, then return `exitError{1}`.
- `status` with a named agent that has no manifest record still evaluates it (everything `missing`).
- `status` returns `exitError{1}` when any state is not `current`; `doctor` when any check is `fail`. `manual` checks do not fail doctor.
- `uninstall` with `Nothing` prints `nothing installed` and exits 0.
- `main.go`: after `rootCmd.Execute()` returns an error, print it, then exit with the `exitError` code when `errors.As` finds one, else 1.
- `--json` prints the `[]ItemStatus` or `[]Check` as indented JSON and nothing else on stdout.

Text output for install, exact shape (two leading spaces, action padded to 9, path padded to 34):

```
Target: /Users/me/src/shop   URL: http://localhost:8484/mcp

  written   .mcp.json                          (mcpServers.projectlens)
  written   .claude/skills/use-projectlens/
  written   .claude/settings.local.json        (4 hooks)
  written   .git/info/exclude                  (5 paths)

Next: restart Claude Code in this repo and approve the "projectlens" server.
      Then run: lens agent doctor claude
```

- Hooks are summarized on one line per action. The exclude line shows the exclude file path relative to the target root when it is inside it, absolute otherwise.
- A `manual` item prints its `Snippet` indented beneath the line.
- `WorktreeNotice` prints: `Note: this is a linked worktree; the exclude entries above apply to every worktree of this repository.`
- `--dry-run` prefixes the first line with `Dry run. Nothing was written.`
- `install codex` ends with the contents of `<assets>/codex/AGENTS.md.snippet` under the line `Codex has no hooks. For always-on guidance, paste this into AGENTS.md:`, and its `Next:` line is `Next: open Codex in this repo, trust the project, then run: lens agent doctor codex`.

Makefile: `build-lens` and `install` pass `-ldflags '-X main.bakedCheckout=$(CURDIR) $(LDFLAGS)'`. The other two build targets are unchanged.

- [ ] **Step 1: Write the failing tests**

Tests build the command with `newAgentCmd()`, set `bakedCheckout` to the repo root for the test and restore it, `t.Chdir` into a `git init` temp repo, and capture stdout.

```go
func TestAgentCmd_UnsupportedAgentExits2(t *testing.T)
    // `install cursor`: errors.As to *exitError with code 2; message contains "supported: claude, codex"
func TestAgentCmd_InstallMissingArgExits2(t *testing.T)
func TestAgentCmd_InstallThenStatusExits0(t *testing.T)
    // `install claude --url http://localhost:1/mcp`, then `status` -> nil error; output lists "current" rows
func TestAgentCmd_StatusNothingInstalledExits1(t *testing.T)
    // output contains "nothing installed" and "lens agent install"
func TestAgentCmd_StatusStaleExits1(t *testing.T)
func TestAgentCmd_StatusJSON(t *testing.T)        // stdout parses as a JSON array whose elements have "State"
func TestAgentCmd_DryRunOutput(t *testing.T)      // starts with "Dry run. Nothing was written."; no .mcp.json
func TestAgentCmd_UninstallAll(t *testing.T)      // after install of both: exit 0; `status` then exits 1 with "nothing installed"
func TestAgentCmd_UninstallNothingExits0(t *testing.T)
func TestAgentCmd_InstallCodexPrintsSnippet(t *testing.T)  // output contains "Codex has no hooks"
func TestAgentCmd_DoesNotNeedIndexConfig(t *testing.T)
    // run in a repo with no configs/ directory and --config pointing at a missing file: install succeeds
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/lens/ -run TestAgentCmd`
Expected: FAIL, undefined `newAgentCmd`.

- [ ] **Step 3: Implement `cmd/lens/agent.go`, register it in `main.go`, add the exit-code mapping, and update the Makefile**

- [ ] **Step 4: Run to verify pass**

Run: `make build && make vet && make test`
Expected: all three succeed.

- [ ] **Step 5: Verify against this repository by hand**

```bash
git status --porcelain > "$TMPDIR/before.txt"
./bin/lens agent install claude --dry-run
./bin/lens agent install claude
git status --porcelain | diff "$TMPDIR/before.txt" -     # expect no output
./bin/lens agent status                              # expect exit 0
./bin/lens agent uninstall all
git status --porcelain | diff "$TMPDIR/before.txt" -     # expect no output
ls .claude .mcp.json .projectlens 2>&1               # expect "No such file" for each
```

- [ ] **Step 6: Commit**

```bash
git add cmd/lens Makefile
git commit -m "feat(cli): add lens agent install, uninstall, status, and doctor"
```

---

### Task 10: Documentation and agent snippets

**Files:**
- Modify: `docs/AGENT_SETUP.md`, `docs/operations.md`, `docs/tasks.md`, `agent/codex/AGENTS.md.snippet`, `agent/claude/CLAUDE.md.snippet`, `agent/claude/mcp-config.json`, `agent/codex/config.toml.snippet`

**Interfaces:**
- Consumes: the commands and flags from Task 9.

- [ ] **Step 1: Rewrite `agent/codex/AGENTS.md.snippet`**

It is printed verbatim by `install codex`, so it must be true in any target repo. Remove: the hardcoded `http://localhost:8484/mcp`; every `agent/skills/...` path; the statement that Codex does not load skills; the hand-maintained tool list. Keep: the mandatory rule to call ProjectLens before reading or searching for structure, behavior, history, data flow and impact questions; the instruction to pass `source: "codex"` to `save_knowledge`. Refer to the two skills by name (`use-projectlens`, `capture-knowledge`) as installed skills.

- [ ] **Step 2: Update the other agent assets**

- `agent/claude/CLAUDE.md.snippet`: replace the "Install in your repo" steps (symlink, merge into `.claude/settings.json`) with `lens agent install claude`. Replace `agent/skills/...` paths with skill names.
- `agent/claude/mcp-config.json`: `"type": "http"`.
- `agent/codex/config.toml.snippet`: direct form, `url = "http://localhost:8484/mcp"`, no `command`, no `mcp-remote`; comment says `lens agent install codex` writes this into `.codex/config.toml`.

- [ ] **Step 3: Update `docs/AGENT_SETUP.md`**

- Claude Code and Codex sections: the install command, what it writes (the placement table from the spec), the approval or trust step, and `lens agent doctor`.
- Remove every `mcp-remote` and `npx` instruction.
- "Install the Skills" and "Install the Hooks": replace the manual `ln -s` and `cp` steps with a pointer to the install command; keep the "at a glance" tables.
- Keep Cursor and "Other MCP-compatible agents" as manual steps.
- Add an "Uninstall" subsection and a "Linked worktrees" note (shared exclude file; install per worktree).
- Troubleshooting: lead each entry with `lens agent doctor`.
- Fix the skill name in the ASCII diagram (`use-projectlens`) and delete the "Migrating from the private `projectlens` build" section, whose old and new names are identical.

- [ ] **Step 4: Update `docs/operations.md`**

Add an `agent` subsection to the CLI reference: the four commands, every flag, the exit codes (0, 1, 2), and a link to `docs/AGENT_SETUP.md` for the walkthrough.

- [ ] **Step 5: Update `docs/tasks.md`**

Move "Agent setup UX" from Active to Recently Done with evidence `cmd/lens/agent.go`, `internal/agentsetup/`. Promote "End-to-end smoke test" to Active. Update "Suggested Sequence".

- [ ] **Step 6: Verify**

Run: `make test && grep -rn 'mcp-remote' docs/AGENT_SETUP.md docs/operations.md agent/ ; ./bin/lens agent install codex --dry-run | tail -20`
Expected: tests pass (the Task 3 real-assets test still loads the snippet); `grep` prints nothing; the dry run ends with the rewritten snippet.

- [ ] **Step 7: Commit**

```bash
git add docs agent
git commit -m "docs: document lens agent commands and retire manual agent setup"
```
