package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

// Runner provides docker compose operations used by stack-agent.
type Runner interface {
	// Up runs: docker compose [--project-name <projectName>] -f <composePath> [--env-file <envFile>] up -d --remove-orphans
	// projectName is optional; pass empty string to omit it and let Docker derive the name from the directory.
	Up(ctx context.Context, composePath, envFile, projectName string) error

	// FindComposeFile returns the full path to the compose file found under path,
	// checking compose.yml, compose.yaml, docker-compose.yml, docker-compose.yaml
	// in that order. Returns empty string if none exist.
	FindComposeFile(path string) string
}

// cmdConstructor is a function that builds an exec.Cmd. It is a field on
// DockerRunner so tests can inject a fake without touching the real docker binary.
type cmdConstructor func(ctx context.Context, name string, args ...string) *exec.Cmd

// DockerRunner is the production implementation of Runner.
type DockerRunner struct {
	newCmd cmdConstructor
}

// NewDockerRunner returns a new DockerRunner backed by the real exec.CommandContext.
func NewDockerRunner() *DockerRunner {
	return &DockerRunner{
		newCmd: exec.CommandContext,
	}
}

// Up runs docker compose up for the given compose file, optionally loading an
// env-file and setting a project name. It returns a wrapped error that includes
// stderr output on non-zero exit.
func (r *DockerRunner) Up(ctx context.Context, composePath, envFile, projectName string) error {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("compose: docker binary not found: %w", err)
	}

	args := []string{"compose"}
	if projectName != "" {
		args = append(args, "--project-name", projectName)
	}
	args = append(args, "-f", composePath)
	if envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	args = append(args, "up", "-d", "--remove-orphans")

	slog.Info("compose: running docker compose up", "composePath", composePath, "envFile", envFile, "projectName", projectName)

	cmd := r.newCmd(ctx, dockerPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("compose: docker compose up failed (exit %d): %s",
				exitErr.ExitCode(), stderrStr)
		}
		return fmt.Errorf("compose: docker compose up: %w", err)
	}

	slog.Info("compose: docker compose up completed", "composePath", composePath)
	return nil
}

// FindComposeFile returns the path of the first compose file found under path,
// checking compose.yml, compose.yaml, docker-compose.yml, docker-compose.yaml.
// Returns empty string if none exist.
func (r *DockerRunner) FindComposeFile(path string) string {
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		full := filepath.Join(path, name)
		if _, err := os.Stat(full); err == nil {
			return full
		}
	}
	return ""
}
