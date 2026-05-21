package applog

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tagawa0525/app_man/internal/config"
)

// New constructs a slog.Logger configured from cfg and tagged with the given
// binary name. The logger writes to <cfg.BaseDir>/<binaryName>.log and always
// carries `binary` and `pid` attributes. The returned cleanup closes the
// underlying log file; callers should defer it (and tests should register it
// with t.Cleanup) to avoid leaking file descriptors.
//
// TODO(PR2): mirror output to os.Stderr when running interactively, and
// integrate log rotation (lumberjack.v2 or custom daily rotation).
func New(cfg config.LoggingConfig, binaryName string) (*slog.Logger, func() error, error) {
	if err := os.MkdirAll(cfg.BaseDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log dir %s: %w", cfg.BaseDir, err)
	}
	logPath := filepath.Join(cfg.BaseDir, binaryName+".log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %s: %w", logPath, err)
	}

	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}

	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "text":
		handler = slog.NewTextHandler(f, opts)
	default:
		handler = slog.NewJSONHandler(f, opts)
	}

	logger := slog.New(handler).With(
		slog.String("binary", binaryName),
		slog.Int("pid", os.Getpid()),
	)
	return logger, f.Close, nil
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
