package compose

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// capturedCmd records the arguments that were passed to a fake cmdConstructor.
type capturedCmd struct {
	name string
	args []string
}

// fakeCmd returns a cmdConstructor that records the invocation and then runs a
// helper binary (os.Executable + "-test.run=TestHelperProcess") to simulate the
// requested exit behaviour without touching a real Docker socket.
//
// When exitCode == 0 the helper exits successfully.
// When exitCode != 0 the helper writes stderrMsg to stderr and exits with exitCode.
func fakeCmd(cap *capturedCmd, exitCode int, stderrMsg string) cmdConstructor {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cap.name = name
		cap.args = args

		// Use the test binary itself as the subprocess, gated on the
		// GO_TEST_HELPER env var so normal test runs are unaffected.
		cs := []string{"-test.run=TestHelperProcess", "--"}
		cs = append(cs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_TEST_HELPER=1",
			"GO_TEST_HELPER_EXIT="+exitCodeStr(exitCode),
			"GO_TEST_HELPER_STDERR="+stderrMsg,
		)
		return cmd
	}
}

func exitCodeStr(code int) string {
	if code == 0 {
		return "0"
	}
	return "1"
}

// TestHelperProcess is the subprocess entry point used by fakeCmd. It is NOT a
// real test — it only runs when the GO_TEST_HELPER env var is set.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER") != "1" {
		return
	}

	if msg := os.Getenv("GO_TEST_HELPER_STDERR"); msg != "" {
		os.Stderr.WriteString(msg) //nolint:errcheck
	}

	if os.Getenv("GO_TEST_HELPER_EXIT") != "0" {
		os.Exit(1)
	}
	os.Exit(0)
}

// notFoundCmd returns a cmdConstructor that simulates docker binary not found
// by returning a cmd that will fail at LookPath (we override LookPath by making
// the runner's newCmd path empty — but it's easier to rely on the real LookPath
// and assume docker is absent; instead we test via a DockerRunner whose newCmd
// is wired to a missing binary name).
//
// Actually, the cleanest approach is to test the real LookPath failure path by
// calling Up on a runner whose PATH doesn't contain docker.  We achieve that by
// temporarily altering PATH in the test process.

// --- Up: correct command construction ---

func TestUp_CommandArgs_NoEnvFile(t *testing.T) {
	var cap capturedCmd
	r := &DockerRunner{newCmd: fakeCmd(&cap, 0, "")}

	// We need LookPath("docker") to succeed. Provide a fake docker on PATH.
	dockerBin := fakeDockerBinary(t)
	t.Setenv("PATH", filepath.Dir(dockerBin))

	err := r.Up(context.Background(), "/srv/myapp/compose.yml", "", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"compose", "--project-name", "myapp", "-f", "/srv/myapp/compose.yml", "up", "-d", "--remove-orphans"}
	assertArgs(t, cap.args, wantArgs)
}

func TestUp_CommandArgs_WithEnvFile(t *testing.T) {
	var cap capturedCmd
	r := &DockerRunner{newCmd: fakeCmd(&cap, 0, "")}

	dockerBin := fakeDockerBinary(t)
	t.Setenv("PATH", filepath.Dir(dockerBin))

	err := r.Up(context.Background(), "/srv/myapp/compose.yml", "/etc/myapp/.env", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{
		"compose", "--project-name", "myapp",
		"-f", "/srv/myapp/compose.yml",
		"--env-file", "/etc/myapp/.env",
		"up", "-d", "--remove-orphans",
	}
	assertArgs(t, cap.args, wantArgs)
}

func TestUp_CommandArgs_ProjectNameOmittedWhenEmpty(t *testing.T) {
	var cap capturedCmd
	r := &DockerRunner{newCmd: fakeCmd(&cap, 0, "")}

	dockerBin := fakeDockerBinary(t)
	t.Setenv("PATH", filepath.Dir(dockerBin))

	err := r.Up(context.Background(), "/srv/myapp/compose.yml", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, arg := range cap.args {
		if arg == "--project-name" {
			t.Errorf("--project-name should not appear when projectName is empty, found at index %d", i)
		}
	}
}

// --- Up: --env-file omitted when envFile is empty ---

func TestUp_EnvFileOmittedWhenEmpty(t *testing.T) {
	var cap capturedCmd
	r := &DockerRunner{newCmd: fakeCmd(&cap, 0, "")}

	dockerBin := fakeDockerBinary(t)
	t.Setenv("PATH", filepath.Dir(dockerBin))

	if err := r.Up(context.Background(), "/srv/myapp/compose.yml", "", "myapp"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, arg := range cap.args {
		if arg == "--env-file" {
			t.Error("--env-file should not appear when envFile is empty")
		}
	}
}

// --- Up: wrapped error contains stderr on non-zero exit ---

func TestUp_NonZeroExit_ReturnsWrappedStderr(t *testing.T) {
	const stderrMsg = "service failed to start: port already in use"

	var cap capturedCmd
	r := &DockerRunner{newCmd: fakeCmd(&cap, 1, stderrMsg)}

	dockerBin := fakeDockerBinary(t)
	t.Setenv("PATH", filepath.Dir(dockerBin))

	err := r.Up(context.Background(), "/srv/myapp/compose.yml", "", "myapp")
	if err == nil {
		t.Fatal("expected error on non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), stderrMsg) {
		t.Errorf("error %q does not contain stderr %q", err.Error(), stderrMsg)
	}
}

// --- Up: docker binary not found ---

func TestUp_DockerNotFound_ReturnsClearError(t *testing.T) {
	r := NewDockerRunner()
	// Clear PATH so docker cannot be found.
	t.Setenv("PATH", "")

	err := r.Up(context.Background(), "/srv/myapp/compose.yml", "", "myapp")
	if err == nil {
		t.Fatal("expected error when docker is not found, got nil")
	}
	if !strings.Contains(err.Error(), "docker") {
		t.Errorf("error %q does not mention 'docker'", err.Error())
	}
	// Must wrap exec.ErrNotFound or similar.
	var lookPathErr *exec.Error
	if !errors.As(err, &lookPathErr) {
		// exec.LookPath may wrap different types depending on Go version; a
		// string check is a safe fallback.
		if !strings.Contains(strings.ToLower(err.Error()), "not found") &&
			!strings.Contains(strings.ToLower(err.Error()), "no such file") {
			t.Errorf("expected 'not found' or 'no such file' in error, got: %v", err)
		}
	}
}

// --- FindComposeFile ---

func TestFindComposeFile_YML(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(expected, []byte("version: '3'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewDockerRunner()
	if got := r.FindComposeFile(dir); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestFindComposeFile_YAML(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(expected, []byte("version: '3'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewDockerRunner()
	if got := r.FindComposeFile(dir); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestFindComposeFile_DockerComposeYML(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(expected, []byte("version: '3'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewDockerRunner()
	if got := r.FindComposeFile(dir); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestFindComposeFile_DockerComposeYAML(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "docker-compose.yaml")
	if err := os.WriteFile(expected, []byte("version: '3'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewDockerRunner()
	if got := r.FindComposeFile(dir); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestFindComposeFile_Neither(t *testing.T) {
	dir := t.TempDir()
	r := NewDockerRunner()
	if got := r.FindComposeFile(dir); got != "" {
		t.Errorf("expected empty string for empty dir, got %q", got)
	}
}

// --- helpers ---

// assertArgs checks that got matches want element-by-element.
func assertArgs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("args length: got %d want %d\n  got:  %v\n  want: %v", len(got), len(want), got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("args[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

// fakeDockerBinary creates a tiny shell script that exits 0 in t.TempDir(),
// named "docker", and returns its path. This is used to satisfy exec.LookPath.
func fakeDockerBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("fakeDockerBinary: %v", err)
	}
	return bin
}
