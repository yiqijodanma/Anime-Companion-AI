package logging

import (
	"log/slog"
	"os"
)

func New(name string) (*slog.Logger, error) {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(handler).With("component", name), nil
}
