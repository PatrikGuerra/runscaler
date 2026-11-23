package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/spf13/viper"

	"github.com/ysya/runscaler/internal/inbox"
)

// exchangeDirEnv turns on the panel inbox (fork addition). Unset, the runner
// behaves exactly like upstream.
const exchangeDirEnv = "RUNNER_EXCHANGE_DIR"

// startInbox is set by runRun when the inbox is on; runManager calls it once
// its logger exists, so inbox messages land in the same log as the manager.
var startInbox func(logger *slog.Logger)

func inboxStarter(ctx context.Context, requestDrain, stopNow func()) func(*slog.Logger) {
	dir := os.Getenv(exchangeDirEnv)
	if dir == "" {
		return nil
	}
	return func(logger *slog.Logger) {
		go inbox.Run(ctx, inbox.Options{
			Dir:        dir,
			ConfigPath: viper.ConfigFileUsed(),
			Version:    version,
			Drain:      requestDrain,
			StopNow:    stopNow,
			Logger:     logger,
		})
	}
}
