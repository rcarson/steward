package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemp writes content to a temp file and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return p
}

// ---- valid config ----

const validYAML = `
defaults:
  poll_interval: 60
  branch: main
  work_dir: /var/lib/steward/stacks
  token: ${STEWARD_DEFAULT_TOKEN}

stacks:
  - name: immich
    repo: https://github.com/mtc/host-services.git
    path: stacks/immich
    branch: main
    token: ${HOST_SERVICES_TOKEN}
    env_file: immich.env
    poll_interval: 60

  - name: nextcloud
    repo: https://github.com/mtc/host-services.git
    path: stacks/nextcloud
    env_file: nextcloud.env
    poll_interval: 120
`

func TestLoad_ValidConfig(t *testing.T) {
	p := writeTemp(t, validYAML)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Stacks) != 2 {
		t.Fatalf("expected 2 stacks, got %d", len(cfg.Stacks))
	}
	if cfg.Stacks[0].Name != "immich" {
		t.Errorf("expected first stack name 'immich', got %q", cfg.Stacks[0].Name)
	}
	if cfg.Stacks[1].Name != "nextcloud" {
		t.Errorf("expected second stack name 'nextcloud', got %q", cfg.Stacks[1].Name)
	}
}

// ---- missing required fields ----

func TestLoad_MissingName(t *testing.T) {
	yml := `
stacks:
  - repo: https://github.com/example/repo.git
    path: stacks/foo
    poll_interval: 30
`
	p := writeTemp(t, yml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error should mention 'name is required', got: %v", err)
	}
}

func TestLoad_MissingRepo(t *testing.T) {
	yml := `
stacks:
  - name: foo
    path: stacks/foo
    poll_interval: 30
`
	p := writeTemp(t, yml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for missing repo, got nil")
	}
	if !strings.Contains(err.Error(), "repo is required") {
		t.Errorf("error should mention 'repo is required', got: %v", err)
	}
}

func TestLoad_MissingPath(t *testing.T) {
	yml := `
stacks:
  - name: foo
    repo: https://github.com/example/repo.git
    poll_interval: 30
`
	p := writeTemp(t, yml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for missing path, got nil")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("error should mention 'path is required', got: %v", err)
	}
}

// ---- per-stack values override defaults ----

func TestLoad_PerStackOverridesDefaults(t *testing.T) {
	yml := `
defaults:
  poll_interval: 60
  branch: main

stacks:
  - name: custom
    repo: https://github.com/example/repo.git
    path: stacks/custom
    branch: develop
    poll_interval: 30
`
	p := writeTemp(t, yml)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := cfg.Stacks[0]
	if s.Branch != "develop" {
		t.Errorf("expected branch 'develop', got %q", s.Branch)
	}
	if s.PollInterval != 30 {
		t.Errorf("expected poll_interval 30, got %d", s.PollInterval)
	}
}

func TestLoad_DefaultsAppliedWhenStackOmits(t *testing.T) {
	yml := `
defaults:
  poll_interval: 45
  branch: staging

stacks:
  - name: myapp
    repo: https://github.com/example/repo.git
    path: stacks/myapp
`
	p := writeTemp(t, yml)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := cfg.Stacks[0]
	if s.Branch != "staging" {
		t.Errorf("expected branch from default 'staging', got %q", s.Branch)
	}
	if s.PollInterval != 45 {
		t.Errorf("expected poll_interval from default 45, got %d", s.PollInterval)
	}
}

// ---- unknown fields ----

func TestLoad_UnknownFieldRejected(t *testing.T) {
	yml := `
stacks:
  - name: foo
    repo: https://github.com/example/repo.git
    path: stacks/foo
    poll_interval: 30
    totally_unknown_field: oops
`
	p := writeTemp(t, yml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

// ---- poll_interval minimum ----

func TestLoad_PollIntervalBelowMinimum(t *testing.T) {
	yml := `
stacks:
  - name: fast
    repo: https://github.com/example/repo.git
    path: stacks/fast
    poll_interval: 5
`
	p := writeTemp(t, yml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for poll_interval below 10, got nil")
	}
	if !strings.Contains(err.Error(), "poll_interval") {
		t.Errorf("error should mention 'poll_interval', got: %v", err)
	}
}

// ---- non-HTTPS repo URL ----

func TestLoad_SSHRepoRejected(t *testing.T) {
	yml := `
stacks:
  - name: sshstack
    repo: git@github.com:example/repo.git
    path: stacks/sshstack
    poll_interval: 30
`
	p := writeTemp(t, yml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for SSH repo URL, got nil")
	}
	if !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("error should mention 'HTTPS', got: %v", err)
	}
}

func TestLoad_GitProtocolRepoRejected(t *testing.T) {
	yml := `
stacks:
  - name: gitstack
    repo: git://github.com/example/repo.git
    path: stacks/gitstack
    poll_interval: 30
`
	p := writeTemp(t, yml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for git:// repo URL, got nil")
	}
	if !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("error should mention 'HTTPS', got: %v", err)
	}
}

// ---- env var interpolation ----

func TestLoad_EnvVarTokenInterpolated(t *testing.T) {
	const tokenValue = "super-secret-token-xyz"
	t.Setenv("MY_STACK_TOKEN", tokenValue)

	yml := `
stacks:
  - name: envstack
    repo: https://github.com/example/repo.git
    path: stacks/envstack
    token: ${MY_STACK_TOKEN}
    poll_interval: 30
`
	p := writeTemp(t, yml)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Stacks[0].Token != tokenValue {
		t.Errorf("expected token %q, got %q", tokenValue, cfg.Stacks[0].Token)
	}
}

func TestLoad_UnsetEnvVarResolvesToEmpty(t *testing.T) {
	// Make sure the var is definitely unset.
	os.Unsetenv("DEFINITELY_NOT_SET_VAR_XYZ")

	yml := `
stacks:
  - name: notoken
    repo: https://github.com/example/repo.git
    path: stacks/notoken
    token: ${DEFINITELY_NOT_SET_VAR_XYZ}
    poll_interval: 30
`
	p := writeTemp(t, yml)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Stacks[0].Token != "" {
		t.Errorf("expected empty token for unset env var, got %q", cfg.Stacks[0].Token)
	}
}

// ---- token not in error messages ----

func TestLoad_TokenNotInErrorMessages(t *testing.T) {
	const tokenValue = "leaked-secret-do-not-show"
	t.Setenv("ERROR_TEST_TOKEN", tokenValue)

	// poll_interval of 1 will trigger a validation error, giving us a chance
	// to verify the token is not present in the error string.
	yml := `
stacks:
  - name: leaktest
    repo: https://github.com/example/repo.git
    path: stacks/leaktest
    token: ${ERROR_TEST_TOKEN}
    poll_interval: 1
`
	p := writeTemp(t, yml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if strings.Contains(err.Error(), tokenValue) {
		t.Errorf("token value leaked into error message: %v", err)
	}
}

// ---- duplicate stack names ----

func TestLoad_DuplicateNameRejected(t *testing.T) {
	yml := `
stacks:
  - name: dup
    repo: https://github.com/example/repo.git
    path: stacks/dup1
    poll_interval: 30
  - name: dup
    repo: https://github.com/example/repo2.git
    path: stacks/dup2
    poll_interval: 30
`
	p := writeTemp(t, yml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for duplicate stack name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention 'duplicate', got: %v", err)
	}
}

func TestLoad_ImplicitDefaultToken(t *testing.T) {
	t.Setenv("STEWARD_DEFAULT_TOKEN", "implicit-token")

	p := writeTemp(t, `
stacks:
  - name: mystack
    repo: https://github.com/example/repo.git
    path: stacks/mystack
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Stacks[0].Token != "implicit-token" {
		t.Errorf("expected implicit token %q, got %q", "implicit-token", cfg.Stacks[0].Token)
	}
}

func TestLoad_AbsoluteEnvFileAccepted(t *testing.T) {
	p := writeTemp(t, `
stacks:
  - name: mystack
    repo: https://github.com/example/repo.git
    path: stacks/mystack
    env_file: /etc/stacks/mystack.env
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("expected absolute env_file to be accepted, got error: %v", err)
	}
	if cfg.Stacks[0].EnvFile != "/etc/stacks/mystack.env" {
		t.Errorf("expected env_file %q, got %q", "/etc/stacks/mystack.env", cfg.Stacks[0].EnvFile)
	}
}

func TestLoad_ConfigDirDefault(t *testing.T) {
	p := writeTemp(t, `
stacks:
  - name: mystack
    repo: https://github.com/example/repo.git
    path: stacks/mystack
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Stacks[0].ConfigDir != "/opt/steward/config" {
		t.Errorf("expected default config_dir %q, got %q", "/opt/steward/config", cfg.Stacks[0].ConfigDir)
	}
}

func TestLoad_ExplicitTokenOverridesImplicit(t *testing.T) {
	t.Setenv("STEWARD_DEFAULT_TOKEN", "implicit-token")
	t.Setenv("MY_TOKEN", "explicit-token")

	p := writeTemp(t, `
stacks:
  - name: mystack
    repo: https://github.com/example/repo.git
    path: stacks/mystack
    token: ${MY_TOKEN}
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Stacks[0].Token != "explicit-token" {
		t.Errorf("expected explicit token %q, got %q", "explicit-token", cfg.Stacks[0].Token)
	}
}
