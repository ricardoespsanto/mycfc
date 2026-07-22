package app

import (
	"io"
	"log/slog"
	"strings"

	"github.com/cfcoimbra/mycfc/internal/config"
)

func newLogger(cfg config.Config, output io.Writer) *slog.Logger {
	level := new(slog.LevelVar)
	switch strings.ToUpper(cfg.LogLevel) {
	case "DEBUG":
		level.Set(slog.LevelDebug)
	case "WARN":
		level.Set(slog.LevelWarn)
	case "ERROR":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}
	options := &slog.HandlerOptions{Level: level}
	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(output, options))
	}
	return slog.New(slog.NewTextHandler(output, options))
}
