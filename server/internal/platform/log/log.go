package log

import (
	"log/slog"
	"os"
)

func New(dev bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if dev {
		opts.Level = slog.LevelDebug
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
