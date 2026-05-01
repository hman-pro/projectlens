package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/hman-pro/projectlens/internal/config"
	"github.com/hman-pro/projectlens/internal/storage"
	"github.com/hman-pro/projectlens/internal/storage/writelock"
)

// LockedCmd is the body shape for write-locked subcommands. It receives
// the already-loaded config, the resolved repo path, the open DB, and
// the cobra command — everything the original RunE had access to.
type LockedCmd func(ctx context.Context, cmd *cobra.Command, db *storage.DB, cfg *config.Config, repoPath string) error

// acquireOrExit takes the writer lock or, on ErrBusy, prints the
// holder's identity to stderr and exits with code 75 (sysexits
// EX_TEMPFAIL).
func acquireOrExit(ctx context.Context, db *storage.DB, cmdName string) (*writelock.Lock, error) {
	lock, err := writelock.Acquire(ctx, db, cmdName)
	if err != nil {
		if be, ok := err.(writelock.ErrBusy); ok {
			fmt.Fprintln(os.Stderr, be.Error())
			os.Exit(75)
		}
		return nil, err
	}
	return lock, nil
}

// withWriteLock wraps a mutating command's RunE so it acquires the
// writer lock on entry and releases it on exit.
func withWriteLock(cmdName string, run LockedCmd) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		cfg, repoPath, err := loadCmdConfig(cmd)
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		db, err := storage.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer db.Close()

		lock, err := acquireOrExit(ctx, db, cmdName)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Release(context.Background()) }()

		return run(ctx, cmd, db, cfg, repoPath)
	}
}

// withWriteLockAfterMigrate is the bootstrap variant. bootstrap is the
// command that *creates* the index_locks table, so it must run
// migrations BEFORE attempting Acquire. Migrations are idempotent and
// run unlocked. After migrations succeed, the wrapper acquires the
// lock and runs the rest of bootstrap under it.
func withWriteLockAfterMigrate(cmdName string, run LockedCmd) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		cfg, repoPath, err := loadCmdConfig(cmd)
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		db, err := storage.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer db.Close()

		if err := db.Migrate(ctx, findMigrationsDir()); err != nil {
			return fmt.Errorf("running migrations: %w", err)
		}

		lock, err := acquireOrExit(ctx, db, cmdName)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Release(context.Background()) }()

		return run(ctx, cmd, db, cfg, repoPath)
	}
}

// newUnlockCmd is the operator escape hatch. Drops the bookkeeping row
// and terminates the holder's Postgres backend so the advisory lock
// auto-releases.
func newUnlockCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "unlock",
		Short: "Release the indexer write lock (use --force to override)",
		Long: `Releases the writer lock and deletes its bookkeeping row.

Auto-recovery handles crashed processes; use --force only when
auto-recovery has failed (e.g. a recycled client PID makes the row
look live).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !force {
				return fmt.Errorf("refusing to unlock without --force")
			}
			cfg, _, err := loadCmdConfig(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			db, err := storage.Connect(ctx, cfg.DatabaseURL)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer db.Close()
			return writelock.ForceUnlock(ctx, db)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "required acknowledgement")
	return cmd
}
