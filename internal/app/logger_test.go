package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/config"
)

func TestNewLoggerUsesConfiguredLevelAndJSONInProduction(t *testing.T) {
	var output bytes.Buffer
	logger := newLogger(config.Config{AppEnv: "production", LogLevel: "debug"}, &output)
	logger.Debug("checked", "key", "value")
	if !strings.Contains(output.String(), `"msg":"checked"`) || !strings.Contains(output.String(), `"key":"value"`) {
		t.Fatalf("output=%q", output.String())
	}
	_ = slog.Default()
}

func TestNewLoggerAppliesDevelopmentLevelsAndTextFormat(t *testing.T) {
	for _, tc := range []struct {
		level, suppressed string
	}{
		{"WARN", "info"},
		{"ERROR", "warn"},
		{"unexpected", "debug"},
	} {
		t.Run(tc.level, func(t *testing.T) {
			var output bytes.Buffer
			logger := newLogger(config.Config{AppEnv: "development", LogLevel: tc.level}, &output)
			logger.Debug("debug")
			logger.Info("info")
			logger.Warn("warn")
			logger.Error("error")
			body := output.String()
			if strings.Contains(body, tc.suppressed) || strings.Contains(body, `"msg"`) || !strings.Contains(body, "error") {
				t.Fatalf("level=%s output=%q", tc.level, body)
			}
		})
	}
}
