package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rcarson/loom/internal/agent"
)

func main() {
	server := flag.String("server", envOr("LOOM_SERVER", "http://localhost:8080"), "loom-server URL")
	token := flag.String("token", envOr("LOOM_TOKEN", ""), "bearer token for server auth")
	nodeName := flag.String("node", envOr("LOOM_NODE_NAME", mustHostname()), "node name")
	region := flag.String("region", envOr("LOOM_REGION", "default"), "node region")
	zone := flag.String("zone", envOr("LOOM_ZONE", "a"), "node zone")
	tags := flag.String("tags", envOr("LOOM_TAGS", ""), "node tags (comma-separated)")
	cpuCores := flag.Int("cpu", 0, "reported CPU cores (0 = auto-detect not yet implemented)")
	memMB := flag.Int("mem", 0, "reported memory in MB (0 = auto-detect not yet implemented)")
	logLevel := flag.String("log-level", envOr("LOOM_LOG_LEVEL", "info"), "log level")
	flag.Parse()

	level := slog.LevelInfo
	level.UnmarshalText([]byte(*logLevel))
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	a, err := agent.New(agent.Config{
		ServerURL: *server,
		Token:     *token,
		NodeName:  *nodeName,
		Region:    *region,
		Zone:      *zone,
		Tags:      *tags,
		CPUCores:  *cpuCores,
		MemoryMB:  *memMB,
	}, logger)
	if err != nil {
		logger.Error("failed to create agent", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	if err := a.Run(ctx); err != nil {
		logger.Error("agent exited", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
