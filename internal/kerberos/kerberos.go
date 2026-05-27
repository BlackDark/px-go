package kerberos

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Manager struct {
	principal string
	password  string
	cachePath string
	logger    *slog.Logger
}

func New(principal, password string, logger *slog.Logger) (*Manager, error) {
	if principal == "" {
		return nil, errors.New("kerberos principal is required")
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = "."
	}
	cacheDir = filepath.Join(cacheDir, "px-go")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		principal: principal,
		password:  password,
		cachePath: filepath.Join(cacheDir, fmt.Sprintf("krb5cc_px_%d", os.Getpid())),
		logger:    logger,
	}, nil
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	go func() {
		_ = m.Ensure(ctx)
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.Ensure(ctx)
			}
		}
	}()
}

func (m *Manager) Ensure(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if err := m.klist(ctx); err == nil {
		return nil
	}
	if err := m.renew(ctx); err == nil {
		return nil
	}
	return m.kinit(ctx)
}

func (m *Manager) Env() []string {
	if m == nil {
		return nil
	}
	return []string{"KRB5CCNAME=FILE:" + m.cachePath}
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	return os.Remove(m.cachePath)
}

func (m *Manager) klist(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "klist", "-s")
	cmd.Env = append(os.Environ(), m.Env()...)
	return cmd.Run()
}

func (m *Manager) renew(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "kinit", "-R")
	cmd.Env = append(os.Environ(), m.Env()...)
	return cmd.Run()
}

func (m *Manager) kinit(ctx context.Context) error {
	if strings.TrimSpace(m.password) == "" {
		return errors.New("kerberos password is required")
	}
	cmd := exec.CommandContext(ctx, "kinit", m.principal)
	cmd.Env = append(os.Environ(), m.Env()...)
	cmd.Stdin = strings.NewReader(m.password + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		m.logger.Debug("kinit failed", "err", err, "output", string(out))
		return err
	}
	return nil
}
