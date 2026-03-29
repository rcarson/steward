package agent

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/rcarson/steward/internal/compose"
	"github.com/rcarson/steward/internal/config"
	"github.com/rcarson/steward/internal/git"
	"github.com/rcarson/steward/internal/metrics"
	"github.com/rcarson/steward/internal/state"
)

// Stack is a single stack poller. One goroutine per configured stack.
type Stack struct {
	cfg      config.StackConfig
	git      git.Client
	compose  compose.Runner
	state    state.Store
	recorder metrics.Recorder
	log      *slog.Logger
}

// NewStack constructs a Stack with the given dependencies.
func NewStack(cfg config.StackConfig, g git.Client, c compose.Runner, s state.Store, rec metrics.Recorder) *Stack {
	return &Stack{
		cfg:      cfg,
		git:      g,
		compose:  c,
		state:    s,
		recorder: rec,
		log:      slog.With("stack", cfg.Name),
	}
}

// Run blocks, running the poll loop until ctx is cancelled.
// A random jitter up to one full poll interval is applied before the first
// poll to spread startup load across all stacks.
func (s *Stack) Run(ctx context.Context) {
	interval := time.Duration(s.cfg.PollInterval) * time.Second
	var jitter time.Duration
	if interval > 0 {
		jitter = time.Duration(rand.Int64N(int64(interval)))
	}
	t := time.NewTimer(jitter)
	select {
	case <-ctx.Done():
		t.Stop()
		return
	case <-t.C:
	}

	for {
		s.poll(ctx)

		t := time.NewTimer(time.Duration(s.cfg.PollInterval) * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// poll performs one iteration of the poll loop.
func (s *Stack) poll(ctx context.Context) {
	start := time.Now()

	// Step 1: fetch remote hash.
	newHash, err := s.git.RemoteHash(ctx, s.cfg.Repo, s.cfg.Branch, s.cfg.Token)
	if err != nil {
		s.log.Error("agent: remote hash error", "err", err)
		s.recorder.RecordPoll(s.cfg.Name, "error")
		return
	}

	// Step 2: compare against stored state.
	oldHash, _ := s.state.Get(s.cfg.Name)
	if oldHash == newHash {
		s.log.Debug("agent: no change", "hash", newHash)
		s.recorder.RecordPoll(s.cfg.Name, "no_change")
		return
	}

	// Step 3: sync the path.
	if err := s.git.SyncPath(ctx, s.cfg.Repo, s.cfg.Branch, s.cfg.Path, s.cfg.WorkDir, s.cfg.Name, s.cfg.Token); err != nil {
		s.log.Error("agent: sync path error", "err", err)
		s.recorder.RecordPoll(s.cfg.Name, "error")
		return
	}

	// Step 4: resolve compose file path.
	composeDir := filepath.Join(s.cfg.WorkDir, s.cfg.Name, s.cfg.Path)
	var composePath string
	if s.cfg.ComposeFile != "" {
		composePath = filepath.Join(composeDir, s.cfg.ComposeFile)
	} else {
		composePath = s.compose.FindComposeFile(composeDir)
	}
	if composePath == "" {
		s.log.Error("agent: no compose file found", "dir", composeDir)
		s.recorder.RecordPoll(s.cfg.Name, "error")
		return
	}

	// Step 5: resolve env file from config_dir.
	envFile := resolveEnvFile(s.cfg.ConfigDir, s.cfg.Name, s.cfg.EnvFile)

	// Step 6: run compose up.
	if err := s.compose.Up(ctx, composePath, envFile, s.cfg.Name); err != nil {
		s.log.Error("agent: compose up error", "err", err)
		s.recorder.RecordPoll(s.cfg.Name, "error")
		return
	}

	// Step 7: update state only after successful deploy.
	if err := s.state.Set(s.cfg.Name, newHash); err != nil {
		s.log.Error("agent: state set error", "err", err)
		// Continue — deploy succeeded even if we failed to persist the hash.
	}

	// Step 8: log success and record metrics.
	s.log.Info("agent: deploy success",
		"old_hash", oldHash,
		"new_hash", newHash,
		"duration", time.Since(start).String(),
	)
	s.recorder.RecordPoll(s.cfg.Name, "success")
	s.recorder.RecordDeploy(s.cfg.Name, "success", time.Since(start))
}

// resolveEnvFile returns the absolute path to the env file for a stack.
// If envFile is set and absolute, it is used as-is. If relative, it is
// resolved relative to configDir. Otherwise, {configDir}/{name}.env is
// used if it exists; empty string if not.
func resolveEnvFile(configDir, name, envFile string) string {
	if envFile != "" {
		if filepath.IsAbs(envFile) {
			return envFile
		}
		return filepath.Join(configDir, envFile)
	}
	candidate := filepath.Join(configDir, name+".env")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	slog.Debug("agent: no env file found, deploying without one", "stack", name, "checked", candidate)
	return ""
}
