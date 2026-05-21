package applog

import (
	"fmt"
	"log/slog"

	"github.com/tagawa0525/app_man/internal/config"
)

// New constructs a slog.Logger configured from cfg and tagged with the given
// binary name. Returned logger writes JSON (or text, per cfg.Format) to
// <cfg.BaseDir>/<binaryName>.log and mirrors output to os.Stderr.
func New(cfg config.LoggingConfig, binaryName string) (*slog.Logger, error) {
	return nil, fmt.Errorf("not implemented")
}
