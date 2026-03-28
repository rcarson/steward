//go:build integration

package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const (
	integrationRepo   = "https://github.com/go-git/go-git.git"
	integrationBranch = "master"
	integrationPath   = "plumbing"
)

func TestIntegration_RemoteHash(t *testing.T) {
	c := New()
	hash, err := c.RemoteHash(context.Background(), integrationRepo, integrationBranch, "")
	if err != nil {
		t.Fatalf("RemoteHash failed: %v", err)
	}
	if len(hash) != 40 {
		t.Errorf("expected 40-char SHA, got %d chars: %q", len(hash), hash)
	}
	t.Logf("RemoteHash(%q, %q) = %q", integrationRepo, integrationBranch, hash)
}

func TestIntegration_SyncPath_Clone(t *testing.T) {
	workDir := t.TempDir()
	name := "go-git-sparse"
	c := New()

	err := c.SyncPath(context.Background(), integrationRepo, integrationBranch, integrationPath, workDir, name, "")
	if err != nil {
		t.Fatalf("SyncPath (clone) failed: %v", err)
	}

	// Verify the sparse path was checked out.
	sparseDir := filepath.Join(workDir, name, integrationPath)
	if _, err := os.Stat(sparseDir); os.IsNotExist(err) {
		t.Errorf("expected sparse path %q to exist after clone", sparseDir)
	}
}

func TestIntegration_SyncPath_Pull(t *testing.T) {
	workDir := t.TempDir()
	name := "go-git-sparse-pull"
	c := New()

	// First call: clone.
	if err := c.SyncPath(context.Background(), integrationRepo, integrationBranch, integrationPath, workDir, name, ""); err != nil {
		t.Fatalf("SyncPath (initial clone) failed: %v", err)
	}

	// Second call: pull (should be a no-op or update, not an error).
	if err := c.SyncPath(context.Background(), integrationRepo, integrationBranch, integrationPath, workDir, name, ""); err != nil {
		t.Fatalf("SyncPath (pull) failed: %v", err)
	}
}
