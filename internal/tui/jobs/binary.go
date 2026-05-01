package jobs

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ResolveBinary returns the absolute path to the projectlens binary the
// runner should invoke. Resolution order:
//  1. PROJECTLENS_BINARY env var (must be executable).
//  2. A sibling of os.Executable() named "projectlens".
//  3. PATH lookup for "projectlens".
func ResolveBinary() (string, error) {
	if v := os.Getenv("PROJECTLENS_BINARY"); v != "" {
		if err := isExecutable(v); err != nil {
			return "", fmt.Errorf("PROJECTLENS_BINARY=%q: %w", v, err)
		}
		return v, nil
	}
	if self, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(self), "projectlens")
		if err := isExecutable(sibling); err == nil {
			return sibling, nil
		}
	}
	if path, err := exec.LookPath("projectlens"); err == nil {
		return path, nil
	}
	return "", errors.New("projectlens binary not found (set PROJECTLENS_BINARY, place it next to projectlens-tui, or add to PATH)")
}

func isExecutable(p string) error {
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("is a directory")
	}
	if info.Mode()&0o111 == 0 {
		return errors.New("not executable")
	}
	return nil
}
