package agentsetup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/hman-pro/projectlens/internal/gitenv"
)

// ErrEscapesTarget reports a path that could reach outside the target work
// tree: a symlinked component, or a path that is not local to the root.
var ErrEscapesTarget = errors.New("path escapes the target work tree")

// Target is a git work tree that wiring is installed into.
type Target struct {
	Root           string // absolute, symlinks resolved
	CommonDir      string // absolute git common dir
	LinkedWorktree bool   // git-dir differs from git-common-dir
	GitDir         string // absolute per-worktree git dir; holds stageDir
}

// stageDir is where writes into the work tree stage their temp file: inside
// the git dir, so a crash between create and rename never leaves a file that
// git status shows. It is per worktree so concurrent runs in linked worktrees
// do not share it. Empty (a Target not from OpenTarget) stages beside the file.
func (t Target) stageDir() string {
	if t.GitDir == "" {
		return ""
	}
	return filepath.Join(t.GitDir, "projectlens-tmp")
}

func runGit(dir string, args ...string) (string, error) {
	// Strict: lens agent has always ignored a caller's command-scope config.
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitenv.ScrubStrict(os.Environ())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// GitRoot returns the nearest work-tree root at or above dir.
func GitRoot(dir string) (string, error) {
	top, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("find git work tree: %w", err)
	}
	root, err := filepath.EvalSymlinks(top)
	if err != nil {
		return "", fmt.Errorf("resolve work tree root: %w", err)
	}
	return root, nil
}

func resolveGitPath(dir, p string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", p, err)
	}
	return r, nil
}

// OpenTarget opens dir, which must be the root of a git work tree.
func OpenTarget(dir string) (Target, error) {
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Target{}, fmt.Errorf("resolve target %s: %w", dir, err)
	}
	root, err := GitRoot(want)
	if err != nil {
		return Target{}, err
	}
	if root != want {
		return Target{}, fmt.Errorf("not the root of a git work tree: %s", dir)
	}
	common, err := runGit(root, "rev-parse", "--git-common-dir")
	if err != nil {
		return Target{}, fmt.Errorf("find git common dir: %w", err)
	}
	gitDir, err := runGit(root, "rev-parse", "--git-dir")
	if err != nil {
		return Target{}, fmt.Errorf("find git dir: %w", err)
	}
	commonAbs, err := resolveGitPath(root, common)
	if err != nil {
		return Target{}, err
	}
	gitDirAbs, err := resolveGitPath(root, gitDir)
	if err != nil {
		return Target{}, err
	}
	return Target{Root: root, CommonDir: commonAbs, LinkedWorktree: commonAbs != gitDirAbs, GitDir: gitDirAbs}, nil
}

// IsTracked reports whether rel is a tracked file, or a directory holding one.
func (t Target) IsTracked(rel string) (bool, error) {
	out, err := runGit(t.Root, "ls-files", "-z", "--", ":(literal)"+rel)
	if err != nil {
		return false, fmt.Errorf("check tracked %s: %w", rel, err)
	}
	return out != "", nil
}

// Exists reports whether rel exists; a dangling symlink counts.
func (t Target) Exists(rel string) bool {
	_, err := os.Lstat(t.Abs(rel))
	return err == nil
}

// IsSymlink reports whether rel itself is a symlink.
func (t Target) IsSymlink(rel string) bool {
	fi, err := os.Lstat(t.Abs(rel))
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// Abs joins a slash-separated target-relative path onto the root.
func (t Target) Abs(rel string) string {
	return filepath.Join(t.Root, filepath.FromSlash(rel))
}

// checkLocal refuses a path that is empty, ".", absolute, holds a ".." segment,
// or starts inside .git. Paths come from files on disk (the manifest, the
// exclude block), so they are validated before anything is joined to the root.
func checkLocal(rel string) error {
	slash := filepath.ToSlash(rel)
	if rel == "" || rel == "." || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return fmt.Errorf("%w: %q is not a path inside it", ErrEscapesTarget, rel)
	}
	for _, seg := range strings.Split(slash, "/") {
		if seg == ".." {
			return fmt.Errorf("%w: %q is not a path inside it", ErrEscapesTarget, rel)
		}
	}
	if first := strings.Split(path.Clean(slash), "/")[0]; strings.EqualFold(first, ".git") {
		return fmt.Errorf("%w: %q is inside .git", ErrEscapesTarget, rel)
	}
	return nil
}

func (t Target) checkComponents(rel string, includeLeaf bool) error {
	if err := checkLocal(rel); err != nil {
		return err
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if !includeLeaf {
		parts = parts[:len(parts)-1]
	}
	cur := t.Root
	for _, p := range parts {
		cur = filepath.Join(cur, p)
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("stat %s: %w", cur, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			name, _ := filepath.Rel(t.Root, cur)
			return fmt.Errorf("%w: %s is a symlink", ErrEscapesTarget, filepath.ToSlash(name))
		}
	}
	return nil
}

// CheckContained returns ErrEscapesTarget when rel is not local to the root
// (see checkLocal) or, naming the offending component, when any component of rel
// (including rel itself) is a symlink. Components that do not exist yet are fine.
func (t Target) CheckContained(rel string) error { return t.checkComponents(rel, true) }

// CheckParentsContained is CheckContained for every component except rel itself.
func (t Target) CheckParentsContained(rel string) error { return t.checkComponents(rel, false) }

const (
	blockStartPrefix = "# >>> projectlens agent wiring: "
	blockStartSuffix = " >>>"
	blockEndPrefix   = "# <<< projectlens agent wiring: "
	blockEndSuffix   = " <<<"
)

func (t Target) excludePath() string { return filepath.Join(t.CommonDir, "info", "exclude") }

// segment is either verbatim text (root == "") or a wiring block for root.
type segment struct {
	text  string // verbatim bytes, including the markers for a block
	root  string
	paths []string
}

func parseExclude(content string) []segment {
	var segs []segment
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			segs = append(segs, segment{text: plain.String()})
			plain.Reset()
		}
	}
	lines := strings.SplitAfter(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trim, blockStartPrefix) && strings.HasSuffix(trim, blockStartSuffix) &&
			len(trim) >= len(blockStartPrefix)+len(blockStartSuffix) {
			root := trim[len(blockStartPrefix) : len(trim)-len(blockStartSuffix)]
			end := blockEndPrefix + root + blockEndSuffix
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimRight(lines[j], "\r\n") != end {
					continue
				}
				flush()
				var paths []string
				for _, l := range lines[i+1 : j] {
					if p := strings.TrimRight(l, "\r\n"); p != "" {
						paths = append(paths, p)
					}
				}
				segs = append(segs, segment{text: strings.Join(lines[i:j+1], ""), root: root, paths: paths})
				i = j
				goto next
			}
		}
		plain.WriteString(line)
	next:
	}
	flush()
	return segs
}

func (t Target) readExclude() (string, error) {
	b, err := os.ReadFile(t.excludePath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read exclude file: %w", err)
	}
	return string(b), nil
}

// ExcludeBlock returns this work tree's block of patterns, sorted.
func (t Target) ExcludeBlock() ([]string, error) {
	content, err := t.readExclude()
	if err != nil {
		return nil, err
	}
	for _, s := range parseExclude(content) {
		if s.root == t.Root {
			out := append([]string(nil), s.paths...)
			sort.Strings(out)
			return out, nil
		}
	}
	return nil, nil
}

// SetExcludeBlock replaces only this work tree's block (empty removes it).
// Blocks of other work trees are kept verbatim unless their directory is gone.
func (t Target) SetExcludeBlock(paths []string) error {
	content, err := t.readExclude()
	if err != nil {
		return err
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	var newBlock string
	if len(sorted) > 0 {
		newBlock = blockStartPrefix + t.Root + blockStartSuffix + "\n" +
			strings.Join(sorted, "\n") + "\n" +
			blockEndPrefix + t.Root + blockEndSuffix + "\n"
	}

	var out strings.Builder
	placed := false
	for _, s := range parseExclude(content) {
		switch {
		case s.root == "":
			out.WriteString(s.text)
		case s.root == t.Root:
			out.WriteString(newBlock)
			placed = true
		default:
			if _, err := os.Stat(s.root); err != nil && os.IsNotExist(err) {
				continue // work tree is gone: prune
			}
			out.WriteString(s.text)
		}
	}
	if !placed && newBlock != "" {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteString("\n")
		}
		out.WriteString(newBlock)
	}
	if out.String() == content {
		return nil
	}
	return writeFileAtomic(t.excludePath(), []byte(out.String()))
}

// writeFileAtomic replaces path by renaming a temp file created beside it.
func writeFileAtomic(path string, data []byte) error {
	return writeFileStaged("", path, data)
}

// writeFileStaged replaces path by renaming a temp file created in stage (or
// beside path when stage is empty). When stage is on another file system the
// rename fails with EXDEV, and the write falls back to a temp file beside path.
func writeFileStaged(stage, path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmpDir := dir
	if stage != "" {
		if err := os.MkdirAll(stage, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", stage, err)
		}
		tmpDir = stage
	}
	tmp, err := os.CreateTemp(tmpDir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm() // keep an existing file's permissions
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		if stage != "" && errors.Is(err, syscall.EXDEV) {
			return writeFileStaged("", path, data)
		}
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
