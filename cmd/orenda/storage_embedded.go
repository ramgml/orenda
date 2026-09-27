// Package main — embedded postgres lifecycle wiring (T363).
//
// When storage.driver=postgres and storage.postgres.embedded=true, the
// binary owns a local postgres cluster: it starts the postmaster before
// any database access (serve, migrate, user commands) and stops it on
// server shutdown / command exit. External postgres — remote server or
// Docker — is the same code path with embedded=false and never touches
// this file (wiki:storage-adapters D7).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/storage/postgres"
)

// embeddedRequested reports whether the config asks for the embedded
// postgres runtime.
func embeddedRequested(cfg *config.Config) bool {
	return cfg.Storage.Driver == "postgres" && cfg.Storage.Postgres.Embedded
}

// startEmbeddedIfConfiguredCLI is the CLI variant of the lifecycle
// wrapper: postmaster output goes through a command-built logger and
// paths resolve against the working directory, matching openCLIDB.
func startEmbeddedIfConfiguredCLI(cfg *config.Config) (func(), error) {
	if !embeddedRequested(cfg) {
		return func() {}, nil
	}
	logger, err := buildLogger(cfg)
	if err != nil {
		return func() {}, fmt.Errorf("logger: %w", err)
	}
	return startEmbeddedIfConfigured(cfg, logger, ".")
}

// startEmbeddedIfConfigured starts the embedded postgres cluster when
// the config requests it and returns a stop func (no-op when embedded
// is off, safe to defer unconditionally). baseDir anchors the data
// directory the same way the database path resolves.
//
// The postmaster logs into the structured zap logger — never stdout —
// via a line-splitting writer; postgres chatter would otherwise leak
// into command output.
func startEmbeddedIfConfigured(cfg *config.Config, logger *zap.Logger, baseDir string) (func(), error) {
	if !embeddedRequested(cfg) {
		return func() {}, nil
	}
	p := cfg.Storage.Postgres
	opts, err := postgres.BuildEmbeddedOptions(p.Database, p.EmbeddedPort, p.User, p.Password)
	if err != nil {
		return func() {}, err
	}
	opts.DataPath = filepath.Join(cfg.ResolveDataDir(baseDir), "postgres")
	opts.RuntimePath, err = postgres.ScratchRuntimePath()
	if err != nil {
		return func() {}, err
	}
	opts.BinariesRepositoryURL = p.BinariesURL
	opts.Logs = zapLineWriter(logger, "postgres-embedded")

	logger.Info("starting embedded postgres",
		zap.String("data_path", opts.DataPath),
		zap.Int("port", opts.Port),
		zap.String("database", opts.Database))
	cluster, err := postgres.StartEmbedded(opts)
	if err != nil {
		_ = os.RemoveAll(opts.RuntimePath)
		return func() {}, err
	}
	var stopped bool
	return func() {
		if stopped {
			return
		}
		stopped = true
		if err := cluster.Stop(); err != nil {
			logger.Warn("embedded postgres stop", zap.Error(err))
		} else {
			logger.Info("embedded postgres stopped")
		}
	}, nil
}

// zapLineWriter wraps logger so every complete line written becomes one
// structured log entry.
func zapLineWriter(logger *zap.Logger, source string) io.Writer {
	return &lineWriter{logger: logger.With(zap.String("source", source))}
}

type lineWriter struct {
	logger *zap.Logger
}

func (w *lineWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			w.logger.Info(line)
		}
	}
	return len(p), nil
}
