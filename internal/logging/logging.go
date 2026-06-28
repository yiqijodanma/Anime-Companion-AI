package logging

import (
	"log/slog"
	"os"
	"path/filepath"
)

type appendFileWriter struct {
	path string
}

func (w appendFileWriter) Write(p []byte) (int, error) {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Write(p)
}

func New(name string) (*slog.Logger, error) {
	if err := os.MkdirAll("log", 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join("log", name+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return slog.New(slog.NewJSONHandler(appendFileWriter{path: path}, &slog.HandlerOptions{Level: slog.LevelInfo})), nil
}
