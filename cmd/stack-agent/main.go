package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/rcarson/stack-agent/internal/agent"
	"github.com/rcarson/stack-agent/internal/compose"
	"github.com/rcarson/stack-agent/internal/config"
	"github.com/rcarson/stack-agent/internal/git"
	"github.com/rcarson/stack-agent/internal/state"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stack-agent", flag.ContinueOnError)
	fs.SetOutput(stderr)

	defaultConfig := "/etc/stack-agent/config.yml"
	if envPath := os.Getenv("STACK_AGENT_CONFIG"); envPath != "" {
		defaultConfig = envPath
	}

	configPath := fs.String("config", defaultConfig, "path to config file")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	// Configure slog based on STACK_AGENT_LOG_LEVEL.
	level := slog.LevelInfo
	if lvlStr := os.Getenv("STACK_AGENT_LOG_LEVEL"); lvlStr != "" {
		switch strings.ToLower(lvlStr) {
		case "debug":
			level = slog.LevelDebug
		case "info":
			level = slog.LevelInfo
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
	}

	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// Load config.
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "stack-agent: failed to load config %q: %v\n", *configPath, err)
		return 1
	}

	if len(cfg.Stacks) == 0 {
		fmt.Fprintf(stderr, "stack-agent: no stacks configured\n")
		return 1
	}

	// Initialize shared dependencies.
	gitClient := git.New()
	composeRunner := compose.NewDockerRunner()

	statePath := filepath.Join(cfg.Stacks[0].WorkDir, ".state.json")
	stateStore, err := state.NewFileStore(statePath)
	if err != nil {
		fmt.Fprintf(stderr, "stack-agent: failed to initialize state store: %v\n", err)
		return 1
	}

	// Set up signal-aware context.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Spawn one goroutine per stack.
	var wg sync.WaitGroup
	for _, stackCfg := range cfg.Stacks {
		wg.Add(1)
		go func(sc config.StackConfig) {
			defer wg.Done()
			agent.NewStack(sc, gitClient, composeRunner, stateStore).Run(ctx)
		}(stackCfg)
	}

	// Block until signal.
	<-ctx.Done()
	stop()

	// Wait for all goroutines to finish.
	wg.Wait()
	slog.Info("shutdown complete")
	return 0
}
