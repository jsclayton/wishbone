package main

import (
	"context"
	"log/slog"

	"jsclayton.dev/wishbone/internal/setup"
)

// announceSetup runs at server start. While the instance has no users, it makes
// a new setup code and writes it to the log, where only the operator reads it.
// An older code stops working. With users, it logs nothing.
func announceSetup(ctx context.Context, svc *setup.Service, logger *slog.Logger, publicURL string) error {
	code, err := svc.Prepare(ctx)
	if err != nil {
		return err
	}
	if code != "" {
		logger.Info("this instance has no users yet: open it in a browser and enter this setup code",
			slog.String("setup_code", code), slog.String("url", publicURL))
	}
	return nil
}
