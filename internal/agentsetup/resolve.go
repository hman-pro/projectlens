package agentsetup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hman-pro/projectlens/internal/projects"
)

const defaultMCPPort = 8484

// ErrRelativeRepoPath reports a relative repo_path that cannot be anchored.
var ErrRelativeRepoPath = errors.New("relative repo_path")

// relativeRepoPathError carries the exact user-facing text and wraps ErrRelativeRepoPath.
type relativeRepoPathError struct{ registry, slug, repoPath string }

func (e relativeRepoPathError) Error() string {
	return fmt.Sprintf("registry %s: project %q has relative repo_path %q; use an absolute repo_path", e.registry, e.slug, e.repoPath)
}

func (e relativeRepoPathError) Unwrap() error { return ErrRelativeRepoPath }

// ResolveOpts carries the flags that influence target, URL, and asset resolution.
type ResolveOpts struct {
	Project          string // --project
	ProjectsPath     string // --projects value
	ProjectsExplicit bool   // cmd.Flags().Changed("projects")
	URL              string // --url
	Port             int    // --port; 0 means unset
	Assets           string // --assets
}

func registryIn(dir string) string {
	return filepath.Join(dir, "configs", "projects.yaml")
}

// FindRegistry returns path == "" and reg == nil when there is no registry.
func FindRegistry(env Env, o ResolveOpts) (path string, reg *projects.Registry, err error) {
	candidates := []string{}
	if o.ProjectsExplicit {
		candidates = append(candidates, o.ProjectsPath)
	} else {
		candidates = append(candidates, registryIn(env.Cwd))
		if env.BakedCheckout != "" {
			candidates = append(candidates, registryIn(env.BakedCheckout))
		}
	}
	for _, c := range candidates {
		if !o.ProjectsExplicit {
			if fi, statErr := os.Stat(c); statErr != nil || fi.IsDir() {
				continue
			}
		}
		reg, err := projects.LoadRegistry(c)
		if err != nil {
			return "", nil, fmt.Errorf("load registry: %w", err)
		}
		return c, reg, nil
	}
	return "", nil, nil
}

// evalLoose resolves symlinks in the longest existing prefix of path, so a
// registry file that does not exist yet still compares correctly.
func evalLoose(path string) (string, error) {
	path = filepath.Clean(path)
	rest := ""
	for cur := path; ; {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(r, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// isInside reports whether path is dir or lies under it, after resolving symlinks.
func isInside(dir, path string) bool {
	d, err := evalLoose(dir)
	if err != nil {
		return false
	}
	p, err := evalLoose(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(d, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ResolveRepoPath returns the absolute, symlink-resolved repo path for p.
func ResolveRepoPath(env Env, registryPath string, p projects.Project) (string, error) {
	path := p.RepoPath
	if !filepath.IsAbs(path) {
		var base string
		switch {
		case env.BakedCheckout != "" && isInside(env.BakedCheckout, registryPath):
			base = env.BakedCheckout
		case env.Cwd != "" && sameFile(registryPath, registryIn(env.Cwd)):
			base = env.Cwd
		default:
			return "", relativeRepoPathError{registry: registryPath, slug: p.Slug, repoPath: p.RepoPath}
		}
		path = filepath.Join(base, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve repo_path of project %q: %w", p.Slug, err)
	}
	return resolved, nil
}

func sameFile(a, b string) bool {
	ra, err := evalLoose(a)
	if err != nil {
		return false
	}
	rb, err := evalLoose(b)
	if err != nil {
		return false
	}
	return ra == rb
}

// ResolveTarget picks the git work tree to install into.
func ResolveTarget(env Env, o ResolveOpts) (Target, error) {
	if o.Project != "" {
		path, reg, err := FindRegistry(env, o)
		if err != nil {
			return Target{}, err
		}
		if reg == nil {
			return Target{}, fmt.Errorf("--project %q given but no projects registry found; pass --projects <path>", o.Project)
		}
		p, err := reg.Find(o.Project)
		if err != nil {
			return Target{}, fmt.Errorf("registry %s: %w", path, err)
		}
		repo, err := ResolveRepoPath(env, path, p)
		if err != nil {
			return Target{}, err
		}
		return OpenTarget(repo)
	}
	root, err := GitRoot(env.Cwd)
	if err != nil {
		return Target{}, fmt.Errorf("not inside a git repository; run from the target repo or pass --project <slug>: %w", err)
	}
	return OpenTarget(root)
}

func (o ResolveOpts) port(env Env) (int, error) {
	if o.Port != 0 {
		return o.Port, nil
	}
	if env.Getenv != nil {
		if s := env.Getenv("PROJECTLENS_MCP_PORT"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 || n > 65535 {
				return 0, fmt.Errorf("invalid PROJECTLENS_MCP_PORT %q", s)
			}
			return n, nil
		}
	}
	return defaultMCPPort, nil
}

// ResolveURL decides the MCP URL written into agent config.
func ResolveURL(env Env, o ResolveOpts, t Target) (string, error) {
	if o.URL != "" {
		return o.URL, nil
	}
	port, err := o.port(env)
	if err != nil {
		return "", err
	}
	slugURL := func(slug string) string {
		return fmt.Sprintf("http://localhost:%d/%s/mcp", port, slug)
	}
	if o.Project != "" {
		return slugURL(o.Project), nil
	}
	path, reg, err := FindRegistry(env, o)
	if err != nil {
		return "", err
	}
	if reg == nil {
		return fmt.Sprintf("http://localhost:%d/mcp", port), nil
	}
	var matches []string
	for _, p := range reg.Projects {
		repo, err := ResolveRepoPath(env, path, p)
		if err != nil {
			continue
		}
		if repo == t.Root {
			matches = append(matches, p.Slug)
		}
	}
	if len(matches) == 1 {
		return slugURL(matches[0]), nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("registry %s has several projects for %s (%s): pass --project <slug> or --url <url>",
			path, t.Root, strings.Join(matches, ", "))
	}
	return "", fmt.Errorf("registry %s has no project for %s; the server only serves /<slug>/mcp in multi-project mode: pass --project <slug> or --url <url>",
		path, t.Root)
}

// ResolveAssets locates the canonical agent assets directory.
func ResolveAssets(env Env, o ResolveOpts) (string, error) {
	dir := o.Assets
	if dir == "" && env.BakedCheckout != "" {
		dir = filepath.Join(env.BakedCheckout, "agent")
	}
	if dir != "" {
		if fi, err := os.Stat(filepath.Join(dir, "skills")); err == nil && fi.IsDir() {
			return dir, nil
		}
	}
	return "", errors.New("canonical agent assets not found; pass --assets <path-to-projectlens>/agent")
}
