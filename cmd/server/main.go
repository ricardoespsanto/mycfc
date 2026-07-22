package main

import (
	"context"
	"log/slog"
	"os"
	_ "time/tzdata"

	"github.com/cfcoimbra/mycfc/internal/app"
)

func main() {
	application, err := app.New(context.Background())
	if err != nil {
		slog.Error("application startup failed", "error", err)
		os.Exit(1)
	}
	defer application.Close()

	if err := application.Run(context.Background()); err != nil {
		application.Logger.Error("application stopped with error", "error", err)
		os.Exit(1)
	}
}
