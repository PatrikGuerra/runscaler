// Command panel is the web UI for a runscaler fork. It runs in its own
// container without the Docker socket: it reads runscaler's /healthz and log,
// keeps a history in SQLite, and only asks for changes through the exchange
// directory (see internal/inbox).
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ysya/runscaler/internal/panel"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	listen := flag.String("listen", envOr("PANEL_LISTEN", "0.0.0.0:8090"), "HTTP listen address")
	dataDir := flag.String("data-dir", envOr("PANEL_DATA_DIR", "/panel-data"), "directory for panel.db")
	exchangeDir := flag.String("exchange-dir", envOr("PANEL_EXCHANGE_DIR", "/exchange"), "directory shared with runscaler")
	healthURL := flag.String("health-url", envOr("PANEL_HEALTH_URL", "http://runscaler:8089/healthz"), "runscaler /healthz URL")
	pollInterval := flag.Duration("poll-interval", durationEnv("PANEL_POLL_INTERVAL", 5*time.Second), "healthz poll interval")
	retention := flag.Duration("history-retention", durationEnv("PANEL_HISTORY_RETENTION", 90*24*time.Hour), "how long to keep history events")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*listen, *dataDir, *exchangeDir, *healthURL, *pollInterval, *retention, logger); err != nil {
		logger.Error("panel stopped", "err", err)
		os.Exit(1)
	}
}

func durationEnv(key string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil && d > 0 {
		return d
	}
	return def
}

func run(listen, dataDir, exchangeDir, healthURL string, pollInterval, retention time.Duration, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	store, err := panel.OpenStore(filepath.Join(dataDir, "panel.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	auth, setupToken, err := panel.NewAuth(ctx, store)
	if err != nil {
		return err
	}
	if setupToken != "" {
		// Printed once per start until an admin exists; anyone who can read the
		// container log can claim the panel, which is the same trust as the host.
		logger.Warn("panel: no admin yet; open the panel and use this one-time setup token", "setupToken", setupToken)
	}

	poller := panel.NewPoller(healthURL, store, logger)
	go poller.Run(ctx, pollInterval)
	go housekeeping(ctx, store, retention, logger)

	srv := &http.Server{
		Addr:              listen,
		Handler:           (&panel.Server{ExchangeDir: exchangeDir, Store: store, Auth: auth, Poller: poller, Logger: logger}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	logger.Info("panel: listening", "addr", listen, "health", healthURL)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func housekeeping(ctx context.Context, store *panel.Store, retention time.Duration, logger *slog.Logger) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if n, err := store.PruneEvents(ctx, time.Now().Add(-retention)); err != nil {
			logger.Warn("panel: prune events failed", "err", err)
		} else if n > 0 {
			logger.Info("panel: pruned old events", "count", n)
		}
		if err := store.PruneSessions(ctx); err != nil {
			logger.Warn("panel: prune sessions failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
