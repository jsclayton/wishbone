package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"jsclayton.dev/wishbone/internal/backup"
	"jsclayton.dev/wishbone/internal/store"
)

// migrate applies the pending migrations for serve, in the start order of
// docs/spec.md (Migration, Start order). The caller has opened the database,
// which was the version guard. When a migration is pending, migrate first writes
// an automatic snapshot of the database and images to dataDir/backups. If the
// snapshot fails, it applies no migration. Then it applies the migrations.
func migrate(ctx context.Context, st *store.Store, dataDir string, now func() time.Time, logger *slog.Logger) error {
	return st.MigrateAfter(ctx, func(ctx context.Context, from, to int64) error {
		// A snapshot of a database older than the baseline is never deleted.
		res, err := backup.AutomaticWithBaseline(ctx, st.DB(), dataDir, from, to, store.BaselineVersion, now())
		switch {
		case errors.Is(err, backup.ErrPrune):
			// The new snapshot is whole, so the migrations can still run.
			logger.Error("remove old automatic snapshots failed", slog.Any("error", err))
		case err != nil:
			return fmt.Errorf("snapshot before migrations (none applied): %w", err)
		}
		logger.Info("wrote a snapshot before migrations",
			slog.String("path", filepath.Dir(res.Database)),
			slog.Int64("from", from), slog.Int64("to", to),
			slog.Int("images", res.Images))
		return nil
	})
}
