package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rcarson/loom/internal/server"
	"github.com/rcarson/loom/internal/store"
)

func main() {
	addr := flag.String("addr", envOr("LOOM_ADDR", ":8080"), "listen address")
	dbPath := flag.String("db", envOr("LOOM_DB", "/opt/loom/data/loom.db"), "SQLite database path")
	token := flag.String("token", envOr("LOOM_TOKEN", ""), "bearer token for API auth (empty = no auth)")
	logLevel := flag.String("log-level", envOr("LOOM_LOG_LEVEL", "info"), "log level (debug|info|warn|error)")
	flag.Parse()

	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	st, err := store.Open(*dbPath)
	if err != nil {
		logger.Error("failed to open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	srv := server.New(st, *token, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	if err := srv.Run(ctx, *addr); err != nil {
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
