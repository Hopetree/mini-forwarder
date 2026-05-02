// mini-forwarder is a production-grade TCP port forwarding service.
//
// It runs on a jump server (machine B) and transparently relays TCP traffic
// from clients (machine C) to a target (machine A).
//
// Usage:
//
//	mini-forwarder -config /etc/mini-forwarder/forwarder.yaml
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"go.uber.org/zap"

	"github.com/Hopetree/mini-forwarder/internal/config"
	"github.com/Hopetree/mini-forwarder/internal/forwarder"
	"github.com/Hopetree/mini-forwarder/internal/logger"
)

// Build-time variables, injected via -ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/forwarder.yaml", "path to YAML config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("mini-forwarder %s (commit=%s, built=%s)\n", Version, GitCommit, BuildDate)
		os.Exit(0)
	}

	// -- Load configuration --
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// -- Initialize logger --
	if err := logger.Init(cfg.LogLevel, cfg.LogFormat); err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	log := logger.L.Named("main")
	log.Info("starting mini-forwarder",
		zap.String("version", Version),
		zap.String("commit", GitCommit),
		zap.String("config", *configPath),
		zap.Int("rules", len(cfg.Forwards)),
	)

	// -- Start forwarder manager --
	ctx, cancel := context.WithCancel(context.Background())

	mgr := forwarder.NewManager(cfg)
	if err := mgr.Start(ctx); err != nil {
		log.Fatal("failed to start forwarder manager", zap.Error(err))
	}

	// -- Config hot-reload --
	if cfg.HotReload {
		setupHotReload(mgr, *configPath, log)
	}

	// -- Wait for shutdown signal --
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for sig := range sigCh {
		if sig == syscall.SIGHUP {
			log.Info("SIGHUP received", zap.Bool("hot_reload", cfg.HotReload))
			if !cfg.HotReload {
				if err := reloadConfig(mgr, *configPath, log); err != nil {
					log.Error("SIGHUP reload failed", zap.Error(err))
				}
			}
			continue
		}

		log.Info("received shutdown signal", zap.String("signal", sig.String()))
		break
	}

	// -- Graceful shutdown with configurable timeout --
	shutdownTimeout := cfg.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = 30 * time.Second
	}

	log.Info("shutting down", zap.Duration("timeout", shutdownTimeout))

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		mgr.StopAll()
		close(done)
	}()

	select {
	case <-done:
		log.Info("graceful shutdown complete")
	case <-shutdownCtx.Done():
		log.Warn("graceful shutdown timed out, forcing exit")
		os.Exit(1)
	}

	cancel()
}

// setupHotReload uses viper's built-in file watcher to trigger a config
// reload when the YAML file changes.
func setupHotReload(mgr *forwarder.Manager, configPath string, log *zap.Logger) {
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")
	v.WatchConfig()

	v.OnConfigChange(func(e fsnotify.Event) {
		log.Info("config file changed, reloading",
			zap.String("file", e.Name),
			zap.String("op", e.Op.String()),
		)

		if err := reloadConfig(mgr, configPath, log); err != nil {
			log.Error("config reload failed", zap.Error(err))
			return
		}

		log.Info("config reloaded successfully")
	})
}

// reloadConfig loads the config from disk and applies it to the manager.
func reloadConfig(mgr *forwarder.Manager, configPath string, log *zap.Logger) error {
	newCfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := mgr.Reload(newCfg); err != nil {
		return fmt.Errorf("apply config: %w", err)
	}
	log.Info("config reloaded",
		zap.Int("rules", len(newCfg.Forwards)),
	)
	return nil
}
