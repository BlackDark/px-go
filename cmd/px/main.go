package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BlackDark/px-go/internal/config"
	"github.com/BlackDark/px-go/internal/platform"
	"github.com/BlackDark/px-go/internal/proxy"
	"github.com/BlackDark/px-go/internal/version"
	keyring "github.com/zalando/go-keyring"
	"golang.org/x/term"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	if cfg.Special.Version {
		fmt.Printf("px %s %s %s\n", version.Version, version.Commit, version.Date)
		return nil
	}
	if cfg.Special.HealthCheck {
		return config.HealthCheck(5*time.Second, cfg.Proxy.Port)
	}
	if cfg.Special.Save {
		path := cfg.Special.ConfigPath
		if path == "" {
			path = filepath.Join(".", "px.ini")
		}
		return cfg.Save(path)
	}
	if cfg.Special.Password {
		return setPassword(cfg)
	}
	if cfg.Special.ClientPassword {
		return setClientPassword(cfg)
	}
	logger, closer, err := config.NewLogger(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	if cfg.Special.Restart {
		_ = quit(cfg.Proxy.Port)
		time.Sleep(500 * time.Millisecond)
	}
	if cfg.Special.Quit {
		return quit(cfg.Proxy.Port)
	}
	if cfg.Special.Install {
		return platform.Current().Install(cfg.Special.ConfigPath)
	}
	if cfg.Special.Uninstall {
		return platform.Current().Uninstall()
	}

	if !cfg.Settings.Quiet {
		logStartupInfo(logger, cfg)
	}

	server, err := proxy.New(cfg, logger)
	if err != nil {
		return err
	}
	if cfg.Special.TestURL != "" {
		return runTestMode(cfg, server)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Start(ctx)
}

func logStartupInfo(logger *slog.Logger, cfg config.Config) {
	logger.Info("starting px",
		"version", version.Version,
		"port", cfg.Proxy.Port,
		"config", cfg.Special.ConfigPath,
	)
	logger.Info("listen configuration",
		"listen", strings.Join(cfg.ListenAddresses(), ", "),
		"gateway", cfg.Proxy.Gateway,
		"hostonly", cfg.Proxy.HostOnly,
	)
	if len(cfg.Proxy.Server) > 0 {
		logger.Info("upstream proxy", "servers", strings.Join(cfg.Proxy.Server, ", "))
	}
	if cfg.Proxy.PAC != "" {
		logger.Info("PAC file configured", "pac", cfg.Proxy.PAC)
	}
	if len(cfg.Proxy.Server) == 0 && cfg.Proxy.PAC == "" {
		logger.Info("no upstream configured, will use platform proxy discovery")
	}
	if cfg.Proxy.NoProxy != "" {
		logger.Info("noproxy (direct connect)", "rules", cfg.Proxy.NoProxy)
	}
	if cfg.Proxy.Allow != "" && cfg.Proxy.Allow != "*.*.*.*" {
		logger.Info("client allow rules", "allow", cfg.Proxy.Allow)
	}
	if cfg.Proxy.Username != "" {
		logger.Info("upstream auth", "username", cfg.Proxy.Username, "auth", cfg.Proxy.Auth)
	} else {
		logger.Info("upstream auth", "method", cfg.Proxy.Auth, "credentials", "SSPI/system")
	}
	logger.Info("settings",
		"threads", cfg.Settings.Threads,
		"idle", cfg.Settings.Idle,
		"socktimeout", cfg.Settings.SockTimeout,
		"log_level", cfg.Settings.LogLevel.String(),
	)
}

func quit(port int) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/PxQuit", port))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("quit failed: %s %s", resp.Status, string(body))
	}
	return nil
}

func runTestMode(cfg config.Config, server *proxy.Server) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Start(ctx) }()
	for i := 0; i < 25; i++ {
		if err := config.HealthCheck(500*time.Millisecond, cfg.Proxy.Port); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	target := cfg.Special.TestURL
	if target == "all" || target == "1" || target == "true" {
		target = "http://httpbin.org/get"
	}
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", cfg.Proxy.Port))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get(target)
	if err != nil {
		cancel()
		_ = server.Shutdown(context.Background())
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("test request failed: %s", resp.Status)
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	case <-time.After(2 * time.Second):
	}
	return nil
}

func setPassword(cfg config.Config) error {
	username := cfg.Proxy.Username
	if username == "" {
		return fmt.Errorf("--username is required to set password")
	}
	fmt.Printf("Setting password for '%s'\n", username)
	fmt.Print("Enter password: ")
	pwd, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return err
	}
	if len(pwd) == 0 {
		return fmt.Errorf("password cannot be empty")
	}
	if err := keyring.Set(config.ServiceName, username, string(pwd)); err != nil {
		return err
	}
	fmt.Println("Saved successfully")
	return nil
}

func setClientPassword(cfg config.Config) error {
	username := cfg.Client.Username
	if username == "" {
		return fmt.Errorf("--client-username is required to set client password")
	}
	fmt.Printf("Setting client password for '%s'\n", username)
	fmt.Print("Enter password: ")
	pwd, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return err
	}
	if len(pwd) == 0 {
		return fmt.Errorf("password cannot be empty")
	}
	if err := keyring.Set(config.ClientServiceName, username, string(pwd)); err != nil {
		return err
	}
	fmt.Println("Saved successfully")
	return nil
}
