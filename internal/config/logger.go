package config

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func (c Config) resolveLogPath() string {
	if c.Settings.LogFile != "" {
		return c.Settings.LogFile
	}
	switch c.Settings.Log {
	case 1:
		return filepath.Join(scriptDir(), "debug.log")
	case 2:
		return filepath.Join(mustGetwd(), "debug.log")
	case 3:
		return filepath.Join(mustGetwd(), fmt.Sprintf("debug-%d-%d.log", c.Proxy.Port, time.Now().UnixNano()))
	default:
		return ""
	}
}

func NewLogger(cfg Config) (*slog.Logger, io.Closer, error) {
	output := io.Writer(io.Discard)
	closer := io.Closer(nopCloser{})
	if cfg.Settings.Log != 0 {
		switch cfg.Settings.Log {
		case 4:
			output = os.Stdout
		default:
			path := cfg.resolveLogPath()
			if path == "" {
				break
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, nil, fmt.Errorf("create log directory: %w", err)
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return nil, nil, fmt.Errorf("open log file %s: %w", path, err)
			}
			output = file
			closer = file
		}
	}
	handler := slog.NewTextHandler(output, &slog.HandlerOptions{Level: cfg.Settings.LogLevel})
	return slog.New(handler), closer, nil
}
