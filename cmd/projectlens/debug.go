package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/storage"
)

// newDebugHoldLockCmd is gated behind PROJECTLENS_DEBUG_HOLD_LOCK=1 (registered
// only when that env var is set). It acquires the writer lock and
// sleeps for --hold to make CLI contention tests deterministic.
func newDebugHoldLockCmd() *cobra.Command {
	var hold time.Duration
	cmd := &cobra.Command{
		Use:    "debug-hold-lock",
		Hidden: true,
		Short:  "[test only] hold the writer lock for --hold duration then release",
	}
	cmd.Flags().DurationVar(&hold, "hold", 3*time.Second, "duration to hold the lock")
	cmd.RunE = withWriteLock("debug-hold-lock",
		func(ctx context.Context, _ *cobra.Command, _ *storage.DB, _ *config.Config, _ string) error {
			fmt.Println("debug-hold-lock: acquired; sleeping", hold)
			select {
			case <-time.After(hold):
			case <-ctx.Done():
			}
			return nil
		})
	return cmd
}
