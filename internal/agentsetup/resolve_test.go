package agentsetup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hman-pro/projectlens/internal/projects"
)

func writeRegistry(t *testing.T, dir, yaml string) string {
	t.Helper()
	p := filepath.Join(dir, "configs", "projects.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func regYAML(entries ...[3]string) string {
	s := "database_url: postgres://u:p@localhost/db\nprojects:\n"
	for _, e := range entries {
		s += "  - slug: " + e[0] + "\n    storage_schema: " + e[1] + "\n    repo_path: " + e[2] + "\n"
	}
	return s
}

func evalDir(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func envFor(cwd, baked string, vars map[string]string) Env {
	return Env{Cwd: cwd, BakedCheckout: baked, Getenv: func(k string) string { return vars[k] }}
}

func TestFindRegistry_Order(t *testing.T) {
	explicitDir, cwdDir, bakedDir := t.TempDir(), t.TempDir(), t.TempDir()
	ex := writeRegistry(t, explicitDir, regYAML([3]string{"a", "a", "/x"}))
	cw := writeRegistry(t, cwdDir, regYAML([3]string{"b", "b", "/x"}))
	bk := writeRegistry(t, bakedDir, regYAML([3]string{"c", "c", "/x"}))

	path, reg, err := FindRegistry(envFor(cwdDir, bakedDir, nil), ResolveOpts{ProjectsPath: ex, ProjectsExplicit: true})
	if err != nil || path != ex || reg.Projects[0].Slug != "a" {
		t.Fatalf("explicit: %q %v %v", path, reg, err)
	}
	path, reg, err = FindRegistry(envFor(cwdDir, bakedDir, nil), ResolveOpts{})
	if err != nil || path != cw || reg.Projects[0].Slug != "b" {
		t.Fatalf("cwd: %q %v %v", path, reg, err)
	}
	path, reg, err = FindRegistry(envFor(t.TempDir(), bakedDir, nil), ResolveOpts{})
	if err != nil || path != bk || reg.Projects[0].Slug != "c" {
		t.Fatalf("baked: %q %v %v", path, reg, err)
	}
	path, reg, err = FindRegistry(envFor(t.TempDir(), t.TempDir(), nil), ResolveOpts{})
	if err != nil || path != "" || reg != nil {
		t.Fatalf("none: %q %v %v", path, reg, err)
	}
	_, _, err = FindRegistry(envFor(cwdDir, bakedDir, nil), ResolveOpts{ProjectsPath: filepath.Join(t.TempDir(), "nope.yaml"), ProjectsExplicit: true})
	if err == nil {
		t.Fatal("expected error for missing explicit registry")
	}
}

func TestResolveRepoPath(t *testing.T) {
	baked, cwd, third := evalDir(t, t.TempDir()), evalDir(t, t.TempDir()), evalDir(t, t.TempDir())

	abs := projects.Project{Slug: "s", RepoPath: cwd}
	got, err := ResolveRepoPath(envFor(cwd, baked, nil), filepath.Join(third, "configs", "projects.yaml"), abs)
	if err != nil || got != cwd {
		t.Fatalf("absolute: %q %v", got, err)
	}

	rel := projects.Project{Slug: "s", RepoPath: "."}
	got, err = ResolveRepoPath(envFor(cwd, baked, nil), filepath.Join(baked, "configs", "projects.yaml"), rel)
	if err != nil || got != baked {
		t.Fatalf("baked: %q %v", got, err)
	}

	got, err = ResolveRepoPath(envFor(cwd, "", nil), filepath.Join(cwd, "configs", "projects.yaml"), rel)
	if err != nil || got != cwd {
		t.Fatalf("cwd: %q %v", got, err)
	}

	_, err = ResolveRepoPath(envFor(cwd, "", nil), filepath.Join(third, "configs", "projects.yaml"), rel)
	if !errors.Is(err, ErrRelativeRepoPath) {
		t.Fatalf("third: want ErrRelativeRepoPath, got %v", err)
	}
	if !strings.Contains(err.Error(), `project "s" has relative repo_path "."; use an absolute repo_path`) {
		t.Fatalf("error text: %v", err)
	}
}

func TestResolveRepoPath_NeverCwdForForeignRegistry(t *testing.T) {
	shop, pl := evalDir(t, t.TempDir()), evalDir(t, t.TempDir())
	reg := writeRegistry(t, pl, regYAML([3]string{"pl", "pl", "."}))
	got, err := ResolveRepoPath(envFor(shop, pl, nil), reg, projects.Project{Slug: "pl", RepoPath: "."})
	if err != nil {
		t.Fatal(err)
	}
	if got != pl || got == shop {
		t.Fatalf("got %q, want %q", got, pl)
	}
}

func TestResolveRepoPath_SymlinkedDirs(t *testing.T) {
	baked := evalDir(t, t.TempDir())
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(baked, link); err != nil {
		t.Fatal(err)
	}
	other := evalDir(t, t.TempDir())
	got, err := ResolveRepoPath(envFor(other, link, nil), filepath.Join(baked, "configs", "projects.yaml"), projects.Project{Slug: "s", RepoPath: "."})
	if err != nil || got != baked {
		t.Fatalf("registry via real path, baked via link: %q %v", got, err)
	}
	got, err = ResolveRepoPath(envFor(other, baked, nil), filepath.Join(link, "configs", "projects.yaml"), projects.Project{Slug: "s", RepoPath: "."})
	if err != nil || got != baked {
		t.Fatalf("registry via link, baked via real path: %q %v", got, err)
	}
}

func TestResolveURL(t *testing.T) {
	repo := newRepo(t)
	tgt, err := OpenTarget(repo)
	if err != nil {
		t.Fatal(err)
	}
	noReg := envFor(t.TempDir(), "", nil)

	got, err := ResolveURL(noReg, ResolveOpts{URL: "http://x/y", Project: "shop", Port: 1}, tgt)
	if err != nil || got != "http://x/y" {
		t.Fatalf("url: %q %v", got, err)
	}
	got, _ = ResolveURL(noReg, ResolveOpts{Project: "shop"}, tgt)
	if got != "http://localhost:8484/shop/mcp" {
		t.Fatalf("project: %q", got)
	}
	got, _ = ResolveURL(noReg, ResolveOpts{Project: "shop", Port: 9000}, tgt)
	if got != "http://localhost:9000/shop/mcp" {
		t.Fatalf("port: %q", got)
	}
	envPort := envFor(t.TempDir(), "", map[string]string{"PROJECTLENS_MCP_PORT": "9100"})
	got, _ = ResolveURL(envPort, ResolveOpts{Project: "shop"}, tgt)
	if got != "http://localhost:9100/shop/mcp" {
		t.Fatalf("env port: %q", got)
	}
	got, _ = ResolveURL(envPort, ResolveOpts{Project: "shop", Port: 9000}, tgt)
	if got != "http://localhost:9000/shop/mcp" {
		t.Fatalf("flag beats env: %q", got)
	}

	// no registry
	got, err = ResolveURL(noReg, ResolveOpts{}, tgt)
	if err != nil || got != "http://localhost:8484/mcp" {
		t.Fatalf("no registry: %q %v", got, err)
	}

	// registry with a matching project (plus one unresolvable relative one that is skipped)
	cwd := t.TempDir()
	writeRegistry(t, cwd, regYAML([3]string{"rel", "rel", "."}, [3]string{"shop", "shop", repo}))
	got, err = ResolveURL(envFor(cwd, "", nil), ResolveOpts{}, tgt)
	// "." resolves to cwd (registry in cwd), which is not the repo, so shop is the only match.
	if err != nil || got != "http://localhost:8484/shop/mcp" {
		t.Fatalf("match: %q %v", got, err)
	}

	// registry exists, no match
	cwd2 := t.TempDir()
	reg := writeRegistry(t, cwd2, regYAML([3]string{"other", "other", t.TempDir()}))
	_, err = ResolveURL(envFor(cwd2, "", nil), ResolveOpts{}, tgt)
	if err == nil || !strings.Contains(err.Error(), "only serves /<slug>/mcp") {
		t.Fatalf("no match: %v", err)
	}
	want := "registry " + reg + " has no project for " + repo + "; the server only serves /<slug>/mcp in multi-project mode: pass --project <slug> or --url <url>"
	if err.Error() != want {
		t.Fatalf("error text:\n got %q\nwant %q", err.Error(), want)
	}
}

func TestResolveTarget(t *testing.T) {
	repo := newRepo(t)
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	tgt, err := ResolveTarget(envFor(sub, "", nil), ResolveOpts{})
	if err != nil || tgt.Root != repo {
		t.Fatalf("subdir: %v %v", tgt, err)
	}

	_, err = ResolveTarget(envFor(t.TempDir(), "", nil), ResolveOpts{})
	if err == nil || !strings.Contains(err.Error(), "pass --project") {
		t.Fatalf("outside repo: %v", err)
	}

	// --project whose repo_path is not a work-tree root
	cwd := t.TempDir()
	notRoot := evalDir(t, t.TempDir())
	writeRegistry(t, cwd, regYAML([3]string{"shop", "shop", notRoot}))
	_, err = ResolveTarget(envFor(cwd, "", nil), ResolveOpts{Project: "shop"})
	if err == nil {
		t.Fatal("expected error for non-repo project path")
	}

	// --project happy path
	cwd2 := t.TempDir()
	writeRegistry(t, cwd2, regYAML([3]string{"shop", "shop", repo}))
	tgt, err = ResolveTarget(envFor(cwd2, "", nil), ResolveOpts{Project: "shop"})
	if err != nil || tgt.Root != repo {
		t.Fatalf("project: %v %v", tgt, err)
	}

	// --project without any registry
	_, err = ResolveTarget(envFor(t.TempDir(), "", nil), ResolveOpts{Project: "shop"})
	if err == nil {
		t.Fatal("expected error without registry")
	}
}

func TestResolveAssets(t *testing.T) {
	flagDir, bakedDir := t.TempDir(), t.TempDir()
	for _, d := range []string{flagDir, filepath.Join(bakedDir, "agent")} {
		if err := os.MkdirAll(filepath.Join(d, "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ResolveAssets(envFor("", bakedDir, nil), ResolveOpts{Assets: flagDir})
	if err != nil || got != flagDir {
		t.Fatalf("flag: %q %v", got, err)
	}
	got, err = ResolveAssets(envFor("", bakedDir, nil), ResolveOpts{})
	if err != nil || got != filepath.Join(bakedDir, "agent") {
		t.Fatalf("baked: %q %v", got, err)
	}
	_, err = ResolveAssets(envFor("", bakedDir, nil), ResolveOpts{Assets: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "--assets") {
		t.Fatalf("no skills: %v", err)
	}
	_, err = ResolveAssets(envFor("", "", nil), ResolveOpts{})
	if err == nil || !strings.Contains(err.Error(), "--assets") {
		t.Fatalf("none: %v", err)
	}
}

func TestResolveURL_RejectsBadFlagsAndEnv(t *testing.T) {
	repo := newRepo(t)
	tgt := mustTarget(t, repo)
	noReg := envFor(t.TempDir(), "", nil)
	for _, u := range []string{
		`http://x/"y`, `http://x/\y`, "http://x/\ny", "http://x/\x7fy", "ftp://x/mcp", "x/mcp",
		"http:///mcp", "http://%zz/mcp", "localhost:8484/mcp",
		"http://:8484/mcp", "http://[]:8484/mcp", "http://x:0/mcp", "http://x:65536/mcp",
	} {
		if _, err := ResolveURL(noReg, ResolveOpts{URL: u}, tgt); !errors.Is(err, ErrInvalidFlag) {
			t.Errorf("--url %q: got %v, want ErrInvalidFlag", u, err)
		}
	}
	for _, p := range []int{-1, 65536} {
		if _, err := ResolveURL(noReg, ResolveOpts{Port: p}, tgt); !errors.Is(err, ErrInvalidFlag) {
			t.Errorf("--port %d: got %v, want ErrInvalidFlag", p, err)
		}
		// --url does not exempt --port from the range check.
		if _, err := ResolveURL(noReg, ResolveOpts{URL: "http://localhost:8484/mcp", Port: p}, tgt); !errors.Is(err, ErrInvalidFlag) {
			t.Errorf("--url with --port %d: got %v, want ErrInvalidFlag", p, err)
		}
	}
	for _, u := range []string{"http://localhost:8484/mcp", "https://lens.example.com:9000/shop/mcp", "http://[::1]:8484/mcp", "http://localhost/mcp"} {
		if got, err := ResolveURL(noReg, ResolveOpts{URL: u}, tgt); err != nil || got != u {
			t.Errorf("--url %q: %q %v", u, got, err)
		}
	}

	// An invalid PROJECTLENS_MCP_PORT is reported, and is not a flag error.
	for _, v := range []string{"abc", "0", "-5", "70000"} {
		_, err := ResolveURL(envFor(t.TempDir(), "", map[string]string{"PROJECTLENS_MCP_PORT": v}), ResolveOpts{}, tgt)
		if err == nil || err.Error() != `invalid PROJECTLENS_MCP_PORT "`+v+`"` {
			t.Errorf("PROJECTLENS_MCP_PORT=%s: %v", v, err)
		}
	}

	// Several registered projects for one repository: the caller must choose.
	cwd := t.TempDir()
	reg := writeRegistry(t, cwd, regYAML([3]string{"shop", "shop", repo}, [3]string{"shop2", "shop2", repo}))
	_, err := ResolveURL(envFor(cwd, "", nil), ResolveOpts{}, tgt)
	want := "registry " + reg + " has several projects for " + repo + " (shop, shop2): pass --project <slug> or --url <url>"
	if err == nil || err.Error() != want {
		t.Fatalf("several matches:\n got %v\nwant %q", err, want)
	}
}
