// Command vyomm-api is VYOMM's HTTP API server: the first runnable VYOMM
// binary in this Go rewrite. It wires together configuration, structured
// logging, SQLite-backed persistence (with restart restoration and
// retention pruning), runbook retrieval, and Prometheus metrics behind the
// HTTP surface defined in API_CONTRACT.md.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/api"
	"github.com/GrandRegentSarva/VYOMM/internal/config"
	"github.com/GrandRegentSarva/VYOMM/internal/observability/logging"
	"github.com/GrandRegentSarva/VYOMM/internal/observability/metrics"
	"github.com/GrandRegentSarva/VYOMM/internal/persistence"
	"github.com/GrandRegentSarva/VYOMM/internal/runbooks"
)

// version is the build-time version string. It is a var (not const) so it
// can be overridden via -ldflags "-X main.version=..." in real builds;
// the Dockerfile does not currently set this, so "dev" is the honest
// default rather than a fabricated release number.
var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Logging isn't configured yet if config itself failed to load, so
		// this is the one place a plain stderr write (not structured JSON)
		// is acceptable — there's no valid log level/mode to structure it with.
		os.Stderr.WriteString("config error: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger := logging.New(logging.Options{
		Service: "vyomm-api",
		Mode:    string(cfg.EnvironmentMode),
		Level:   logging.LevelFromString(cfg.LogLevel),
	})

	runID := newRunID()
	logger.Info("starting vyomm-api", "event", "startup.begin", "run_id", runID, "version", version)

	runbookLib, err := runbooks.Load(runbookPath())
	if err != nil {
		logger.Error("failed to load runbooks", "event", "startup.runbooks.failed", "error", err)
		os.Exit(1)
	}

	store, err := persistence.NewStore(cfg.SQLitePath, cfg.RetentionDuration(), time.Now().UTC())
	if err != nil {
		logger.Error("failed to open persistence store", "event", "startup.store.failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	metricsRegistry := metrics.New()

	server := &api.Server{
		Store:    store,
		Runbooks: runbookLib,
		Metrics:  metricsRegistry,
		Config:   cfg,
		Logger:   logger,
		Version:  version,
		RunID:    runID,
	}

	httpServer := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           server.NewMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	stopPruning := startRetentionPruning(store, cfg.RetentionDuration(), logger)
	defer stopPruning()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		logger.Info("listening", "event", "startup.listening", "addr", cfg.APIAddr, "mode", string(cfg.EnvironmentMode))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "event", "http.server.error", "error", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down", "event", "shutdown.begin")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "event", "shutdown.error", "error", err)
	}
	logger.Info("shutdown complete", "event", "shutdown.complete")
}

// startRetentionPruning runs Store.Prune on a fixed interval so telemetry
// never grows unbounded (the confirmed defect in the original Python
// implementation). It returns a stop function for graceful shutdown.
func startRetentionPruning(store *persistence.Store, retention time.Duration, logger *slog.Logger) func() {
	interval := retention / 4
	if interval < time.Minute {
		interval = time.Minute
	}
	if interval > time.Hour {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				pruned, err := store.Prune(time.Now().UTC())
				if err != nil {
					logger.Error("retention prune failed", "event", "persistence.prune.failed", "error", err)
					continue
				}
				if pruned > 0 {
					logger.Info("retention prune completed", "event", "persistence.prune.completed", "rows_pruned", pruned)
				}
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}

func runbookPath() string {
	if p := os.Getenv("VYOMM_RUNBOOK_PATH"); p != "" {
		return p
	}
	return "./runbooks"
}

func newRunID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "run-unavailable"
	}
	return "run-" + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}
