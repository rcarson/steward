//go:build integration

package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const minimalComposeFile = `services:
  hello:
    image: hello-world
`

func TestIntegration_Up(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(composePath, []byte(minimalComposeFile), 0o644); err != nil {
		t.Fatalf("failed to write compose file: %v", err)
	}

	r := NewDockerRunner()
	if err := r.Up(context.Background(), composePath, ""); err != nil {
		t.Fatalf("Up failed: %v", err)
	}
}
