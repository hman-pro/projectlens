package main

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
)

func TestOpenCmdStorageRejectsProjectAndRepo(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.PersistentFlags().String("project", "", "")
	cmd.PersistentFlags().String("projects", "configs/projects.yaml", "")
	cmd.PersistentFlags().String("repo", "", "")
	cmd.PersistentFlags().String("config", "configs/index.yaml", "")
	cmd.PersistentFlags().String("db", "", "")
	_ = cmd.ParseFlags([]string{"--project", "foo", "--repo", "/tmp"})
	_, err := validateMutex(cmd)
	if err == nil {
		t.Fatal("expected mutex error")
	}
}

func TestValidateMutexAllowsRepoAlone(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.PersistentFlags().String("project", "", "")
	cmd.PersistentFlags().String("projects", "configs/projects.yaml", "")
	cmd.PersistentFlags().String("repo", "", "")
	_ = cmd.ParseFlags([]string{"--repo", "/tmp"})
	if _, err := validateMutex(cmd); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

var _ = context.Background
