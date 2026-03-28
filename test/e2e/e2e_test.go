//go:build integration

package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rcarson/stack-agent/internal/agent"
	"github.com/rcarson/stack-agent/internal/compose"
	"github.com/rcarson/stack-agent/internal/config"
	"github.com/rcarson/stack-agent/internal/git"
	"github.com/rcarson/stack-agent/internal/state"
)

const (
	e2eRepo   = "https://github.com/go-git/go-git.git"
	e2eBranch = "master"
	e2ePath   = "plumbing"
)

// minimalComposeYML is a compose file that runs hello-world and exits
// successfully, verifying docker compose up works end-to-end.
const minimalComposeYML = `services:
  hello:
    image: hello-world
`

// skipIfNoDocker skips the test if the docker binary is not available on PATH.
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH; skipping test that requires Docker")
	}
}

// TestE2E_RemoteHash verifies that git.Client.RemoteHash returns a valid
// 40-character SHA from a real public remote repository.
func TestE2E_RemoteHash(t *testing.T) {
	c := git.New()
	hash, err := c.RemoteHash(context.Background(), e2eRepo, e2eBranch, "")
	if err != nil {
		t.Fatalf("RemoteHash failed: %v", err)
	}
	if len(hash) != 40 {
		t.Errorf("expected 40-char SHA, got %d chars: %q", len(hash), hash)
	}
	t.Logf("RemoteHash = %q", hash)
}

// TestE2E_SyncPath verifies that git.Client.SyncPath clones a sparse checkout
// of a subdirectory from a real public remote repository.
func TestE2E_SyncPath(t *testing.T) {
	workDir := t.TempDir()
	name := "go-git-e2e"
	c := git.New()

	err := c.SyncPath(context.Background(), e2eRepo, e2eBranch, e2ePath, workDir, name, "")
	if err != nil {
		t.Fatalf("SyncPath failed: %v", err)
	}

	sparseDir := filepath.Join(workDir, name, e2ePath)
	info, statErr := os.Stat(sparseDir)
	if os.IsNotExist(statErr) {
		t.Fatalf("expected sparse path %q to exist after clone", sparseDir)
	}
	if statErr != nil {
		t.Fatalf("stat %q: %v", sparseDir, statErr)
	}
	if !info.IsDir() {
		t.Errorf("expected %q to be a directory", sparseDir)
	}

	entries, err := os.ReadDir(sparseDir)
	if err != nil {
		t.Fatalf("ReadDir %q: %v", sparseDir, err)
	}
	if len(entries) == 0 {
		t.Errorf("expected at least one file under %q, got none", sparseDir)
	}
	t.Logf("SyncPath cloned %d entries under %q", len(entries), sparseDir)
}

// TestE2E_ComposeUp verifies that compose.DockerRunner.Up successfully runs
// a minimal hello-world container via docker compose.
func TestE2E_ComposeUp(t *testing.T) {
	skipIfNoDocker(t)

	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(composePath, []byte(minimalComposeYML), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	r := compose.NewDockerRunner()
	if err := r.Up(context.Background(), composePath, ""); err != nil {
		t.Fatalf("compose Up failed: %v", err)
	}
}

// TestE2E_FullAgentLoop wires together real git.Client, compose.Runner, and
// state.Store to exercise the complete agent poll loop against a real public
// repository and real Docker.
//
// The test pre-seeds the workdir with a cloned repo so that SyncPath's pull
// path is exercised, plants a minimal compose.yml in the checked-out
// subdirectory, and then runs agent.Stack.Run until the state store is updated
// with the remote hash — confirming the full deploy pipeline executed.
func TestE2E_FullAgentLoop(t *testing.T) {
	skipIfNoDocker(t)

	workDir := t.TempDir()
	stateFile := filepath.Join(t.TempDir(), "state.json")
	stackName := "e2e-stack"
	gitClient := git.New()

	// Step 1: clone the sparse path into the workdir so we can plant a compose
	// file inside it before the agent's poll begins.
	t.Log("cloning sparse path...")
	if err := gitClient.SyncPath(
		context.Background(), e2eRepo, e2eBranch, e2ePath, workDir, stackName, "",
	); err != nil {
		t.Fatalf("initial SyncPath: %v", err)
	}

	// Step 2: write a minimal compose.yml into the checked-out subdirectory so
	// HasComposeFile returns true and Up has something to deploy.
	composeDir := filepath.Join(workDir, stackName, e2ePath)
	if err := os.MkdirAll(composeDir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", composeDir, err)
	}
	if err := os.WriteFile(
		filepath.Join(composeDir, "compose.yml"),
		[]byte(minimalComposeYML),
		0o644,
	); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}

	// Step 3: fetch the live remote hash so we can verify state is updated.
	t.Log("fetching remote hash...")
	remoteHash, err := gitClient.RemoteHash(context.Background(), e2eRepo, e2eBranch, "")
	if err != nil {
		t.Fatalf("RemoteHash: %v", err)
	}
	t.Logf("remote hash = %q", remoteHash)

	// Step 4: build the agent Stack with real dependencies.
	// PollInterval is set to a large value — we cancel the context once the
	// first poll completes rather than relying on the timer.
	cfg := config.StackConfig{
		Name:         stackName,
		Repo:         e2eRepo,
		Branch:       e2eBranch,
		Path:         e2ePath,
		WorkDir:      workDir,
		PollInterval: 3600, // effectively infinite; we cancel manually
	}

	store, err := state.NewFileStore(stateFile)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	stack := agent.NewStack(cfg, gitClient, compose.NewDockerRunner(), store)

	// Step 5: run the agent in the background and wait until the state store
	// reflects the remote hash (meaning a successful deploy), or until a
	// 3-minute deadline expires.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		stack.Run(ctx)
	}()

	// Poll the state store every second until the hash appears or the context
	// is cancelled.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			<-done
			// Check one final time before failing.
			storedHash, found := store.Get(stackName)
			if !found || storedHash != remoteHash {
				t.Errorf("agent loop did not complete within deadline: stored=%q, want=%q", storedHash, remoteHash)
			}
			return

		case <-ticker.C:
			storedHash, found := store.Get(stackName)
			if found && storedHash == remoteHash {
				t.Logf("state updated to %q — full agent loop succeeded", storedHash)
				cancel() // stop the agent
				<-done
				return
			}
		}
	}
}
