package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/BlackDark/px-go/internal/config"
	"github.com/BlackDark/px-go/internal/proxy"
	"github.com/BlackDark/px-go/internal/version"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if cfg.Special.Version {
		fmt.Printf("px %s (commit: %s, built: %s)\n", version.Version, version.Commit, version.Date)
		return
	}

	if cfg.Special.HealthCheck {
		if err := config.HealthCheck(cfg.Settings.SockTimeout, cfg.Proxy.Port); err != nil {
			fmt.Fprintf(os.Stderr, "health check failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("ok")
		return
	}

	logger, closer, err := config.NewLogger(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = closer.Close() }()

	logger.Info("starting px", "version", version.Version, "port", cfg.Proxy.Port)

	srv, err := proxy.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create server", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutting down")
		cancel()
	}()

	if err := srv.Start(ctx); err != nil {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}

	logger.Info("stopped", slog.String("reason", "clean shutdown"))
}
