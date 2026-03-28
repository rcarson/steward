# stack-agent: Implementation Specification

A lightweight per-host daemon written in Go that watches Git repositories for
changes to Docker Compose stacks and reconciles running state automatically.

---

## Goals

- Each host runs its own agent instance, fully autonomous
- Agent watches one or more `{repo, path}` pairs for changes
- On detected change, deploys the stack via `docker compose up -d --remove-orphans`
- No coordinator, no database, no UI — state is minimal and local
- Packaged as a single Docker/Podman container

---

## Non-Goals

- Image update management (Watchtower handles this)
- Multi-host coordination
- Rollback (out of scope for v1)
- A web UI or REST API

---

## Project Layout

```
stack-agent/
├── cmd/
│   └── stack-agent/
│       └── main.go          # Entry point, signal handling, wiring
├── internal/
│   ├── config/
│   │   ├── config.go        # Config struct, loader
│   │   └── config_test.go
│   ├── git/
│   │   ├── client.go        # go-git wrapper: RemoteHash, SyncPath, token auth
│   │   └── client_test.go
│   ├── compose/
│   │   ├── runner.go        # Shells out to docker compose
│   │   └── runner_test.go
│   ├── state/
│   │   ├── store.go         # Persists last-deployed commit hash per stack
│   │   └── store_test.go
│   └── agent/
│       ├── agent.go         # Orchestrates the poll loop per stack
│       └── agent_test.go
├── config.example.yml
├── Dockerfile
├── compose.yml              # For running stack-agent itself
└── README.md
```

---

## Configuration

Defined in `/etc/stack-agent/config.yml` (path overridable via `--config` flag
or `STACK_AGENT_CONFIG` env var).

```yaml
# Global defaults (all overridable per stack)
defaults:
  poll_interval: 60          # seconds
  branch: main
  work_dir: /var/lib/stack-agent/stacks
  token: ${STACK_AGENT_DEFAULT_TOKEN}  # optional, env var interpolated

stacks:
  - name: immich
    repo: https://github.com/mtc/host-services.git
    path: stacks/immich
    branch: main             # optional, overrides default
    token: ${HOST_SERVICES_TOKEN}      # optional, overrides default token
    env_file: /etc/stacks/immich.env
    poll_interval: 60

  - name: nextcloud
    repo: https://github.com/mtc/host-services.git
    path: stacks/nextcloud
    env_file: /etc/stacks/nextcloud.env
    poll_interval: 120
    # no token: inherits default, or omitted for public repos
```

### Config Rules

- `name` must be unique across all stacks on the host
- `repo` must be an HTTPS URL — SSH is not supported
- `path` is the subdirectory within the repo to watch
- `token` is optional; omit for public repos. Supports `${ENV_VAR}` interpolation — the actual secret should live in the environment, not hardcoded in the file
- `env_file` is optional; if set, passed to compose as `--env-file`
- `poll_interval` is in seconds, minimum 10
- Token resolution order: per-stack `token` → `defaults.token` → empty (public repo)

---

## Packages

### `internal/config`

Loads and validates `config.yml`. Merges defaults into each stack entry.

**Outcomes tested:**
- Valid config loads without error
- Missing required fields (`name`, `repo`, `path`) return a descriptive error
- Per-stack values override defaults
- Unknown fields cause a parse error (strict mode via `yaml.KnownFields`)
- `poll_interval` below minimum (10s) is rejected
- `repo` with a non-HTTPS URL (SSH, git://) is rejected with a clear error
- `${ENV_VAR}` token references are interpolated from the environment at load time
- Unset env var in a token field resolves to empty string (public repo), not an error
- Token value does NOT appear in any error messages returned by the config package

---

### `internal/git`

Wraps Git operations using [`go-git`](https://github.com/go-git/go-git) — a
pure Go implementation with no dependency on a system git binary. HTTPS with
token auth is the only supported transport.

Token auth is handled via `go-git`'s `http.BasicAuth`. The token never touches
a command line, env var, or log entry — it lives in a Go struct in memory and
is scrubbed from any error messages before they are returned.

```go
import "github.com/go-git/go-git/v5/plumbing/transport/http"

func authFromToken(token string) *http.BasicAuth {
    if token == "" {
        return nil  // public repo, no auth
    }
    return &http.BasicAuth{
        Username: "x-token",  // accepted by GitHub, GitLab, Gitea, Forgejo, Gogs
        Password: token,
    }
}
```

#### Interface

```go
type Client interface {
    // Returns the current HEAD commit hash for the given repo+branch.
    // Uses go-git's remote.List() — no local clone required.
    RemoteHash(ctx context.Context, repo, branch, token string) (string, error)

    // Ensures a sparse checkout of path exists under workDir/name.
    // Creates on first call (clone), updates on subsequent calls (pull).
    SyncPath(ctx context.Context, repo, branch, path, workDir, name, token string) error
}
```

#### Sparse Checkout

`go-git` supports sparse checkout via `CloneOptions.Filter` and
`SparseCheckoutPatterns`. Only the files under the configured `path` are
written to disk — the rest of the repo is not materialised locally. This keeps
disk usage low and clone times fast for large repos.

```go
cloneOpts := &git.CloneOptions{
    URL:               repo,
    Auth:              authFromToken(token),
    ReferenceName:     plumbing.NewBranchReferenceName(branch),
    SingleBranch:      true,
    Depth:             1,
    Filter:            object.BlobNoneFilter, // blobless clone
}
```

After cloning, sparse checkout patterns are applied so only `path/**` is
checked out. On subsequent `SyncPath` calls a `pull` is performed with the same
auth.

#### Token Scrubbing

Any error returned from `go-git` that contains the repo URL may embed the token
if it was somehow included. Before returning errors to callers, the
implementation replaces the token string with `***`:

```go
func scrubToken(s, token string) string {
    if token == "" {
        return s
    }
    return strings.ReplaceAll(s, token, "***")
}
```

**Outcomes tested:**
- `RemoteHash` returns a valid 40-char SHA on success
- `RemoteHash` returns a wrapped error on network failure
- `RemoteHash` with empty token succeeds against a public repo
- `SyncPath` creates the local directory on first call (clone + sparse checkout)
- `SyncPath` pulls latest on subsequent calls without re-cloning
- `SyncPath` handles the case where `path` does not exist in the repo
- Token does NOT appear in any returned error messages (scrubbing verified)
- Token does NOT appear in any log output
- `go-git` typed errors (e.g. `git.NoErrAlreadyUpToDate`) are handled gracefully, not surfaced as errors

---

### `internal/state`

Persists last-deployed commit hash per stack to a local file. This is the only
persistent state the agent needs.

Storage: a simple JSON file at `{work_dir}/.state.json`

```json
{
  "immich":    "a3f1c9d...",
  "nextcloud": "b72e01a..."
}
```

#### Interface

```go
type Store interface {
    Get(name string) (hash string, found bool)
    Set(name string, hash string) error
}
```

**Outcomes tested:**
- `Get` on unknown name returns `found = false`
- `Set` then `Get` returns the stored hash
- State file is written atomically (write to temp, rename) — no corrupt state on crash
- Concurrent reads are safe (reads are protected)
- Existing state file is loaded correctly on agent restart

---

### `internal/compose`

Shells out to `docker compose`. Accepts an `--env-file` path if configured.

#### Interface

```go
type Runner interface {
    // Runs: docker compose -f <composePath> [--env-file <envFile>] up -d --remove-orphans
    Up(ctx context.Context, composePath, envFile string) error

    // Returns true if a compose.yml or compose.yaml exists at the given path
    HasComposeFile(path string) bool
}
```

**Outcomes tested:**
- `Up` constructs the correct command (verified via exec capture in tests)
- `Up` returns a wrapped error containing stderr on non-zero exit
- `--env-file` is omitted when `envFile` is empty string
- `HasComposeFile` returns true for `compose.yml` and `compose.yaml`, false otherwise
- `docker compose` binary not found returns a clear error

---

### `internal/agent`

Core poll loop. One `Stack` goroutine per configured stack. Each runs
independently — a failure or slow poll in one stack does not block others.

#### Poll Loop (per stack)

```
1. Call git.RemoteHash(repo, branch)
   → on error: log, increment error counter, wait poll_interval, retry

2. Compare against state.Get(name)
   → if equal: nothing to do, wait poll_interval

3. Call git.SyncPath(repo, branch, path, workDir, name)
   → on error: log, wait poll_interval, retry

4. Derive composePath = workDir/name/path/compose.yml
   → if no compose file found: log error, wait poll_interval

5. Call compose.Up(composePath, envFile)
   → on error: log, wait poll_interval (do NOT update state hash)

6. Call state.Set(name, newHash)

7. Log success with stack name, new hash, duration

8. Wait poll_interval
```

Note: state hash is only updated **after** a successful compose deploy. A
failed deploy will be retried on the next poll.

**Outcomes tested:**

- Hash unchanged → compose `Up` is NOT called
- Hash changed → `SyncPath` and `Up` are called in order
- `SyncPath` failure → `Up` is NOT called, state hash NOT updated
- `Up` failure → state hash NOT updated (next poll retries)
- `Up` success → state hash IS updated
- Context cancellation (shutdown) → goroutine exits cleanly
- Each stack runs in its own goroutine (verified by concurrent execution in test)
- Poll interval is respected between iterations

---

### `cmd/stack-agent`

Entry point. Responsibilities:

- Parse `--config` flag / env var
- Load and validate config
- Initialize all dependencies (git client, state store, compose runner)
- Spawn one `agent.Stack` goroutine per configured stack
- Block on `SIGTERM` / `SIGINT`, then cancel context and wait for goroutines to finish

**Outcomes tested:**
- Missing config file exits with code 1 and message
- Invalid config exits with code 1 and message
- SIGTERM triggers clean shutdown (goroutines exit, no hang)

---

## Deployment

The agent ships as a container and is deployed as its own compose stack on each host.

```yaml
# /opt/stacks/stack-agent/compose.yml
services:
  stack-agent:
    image: ghcr.io/mtc/stack-agent:latest
    restart: unless-stopped
    environment:
      - HOST_SERVICES_TOKEN=${HOST_SERVICES_TOKEN}  # token passed from host env
      - STACK_AGENT_LOG_LEVEL=info
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /etc/stack-agent/config.yml:/etc/stack-agent/config.yml:ro
      - /etc/stacks:/etc/stacks:ro          # host-local env files
      - /var/lib/stack-agent:/var/lib/stack-agent  # state + work dir
```

Tokens are passed into the container via environment variables and referenced
in `config.yml` using `${ENV_VAR}` interpolation. They are never written to
disk inside the container and never appear in logs.

The host-side `.env` file for the compose stack holds the actual token values:

```bash
# /opt/stacks/stack-agent/.env  (chmod 600, outside the repo)
HOST_SERVICES_TOKEN=ghp_abc123...
```

No git binary is required in the image — `go-git` is pure Go.

---

## Observability

- All output to stdout/stderr (structured with `log/slog`)
- Log level configurable via `STACK_AGENT_LOG_LEVEL` (debug, info, warn, error)
- Every deploy logs: stack name, old hash, new hash, duration, success/failure
- Every error logs: stack name, operation, error string

No metrics endpoint in v1. `docker logs stack-agent` is sufficient.

---

## Error Handling Philosophy

- Transient errors (network, git timeout) → log and retry next poll
- Compose deploy failure → log stderr, do not update state, retry next poll
- Configuration errors → fatal at startup
- The agent should never crash due to a single stack failing

---

## Build & Test

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Build binary
go build -o stack-agent ./cmd/stack-agent

# Build container
docker build -t stack-agent .
```

Tests use interface mocks for `git.Client`, `compose.Runner`, and `state.Store`
so the core agent logic can be tested without real network or docker dependencies.

The `internal/git` package has integration tests (build tag `//go:build integration`)
that run against a real HTTPS git remote and require a valid token in the environment.
The `internal/compose` package has integration tests that require a real Docker socket.
Both are excluded from the default `go test ./...` run.

Since `go-git` is pure Go, no git binary is needed in the container image or
on developer machines to run the standard test suite.

---

## v1 Milestones

| Milestone | Scope |
|-----------|-------|
| M1 | `config`, `state` packages + tests |
| M2 | `git` package + unit tests; integration test with real repo |
| M3 | `compose` package + unit tests; integration test with real compose |
| M4 | `agent` package + tests using mocks |
| M5 | `cmd` wiring, Dockerfile, compose.yml, README |
| M6 | End-to-end test: real git repo + real compose stack deployed by agent |

---

## Future Considerations (v2+)

- Webhook receiver as an alternative to polling (faster deploys)
- `stack-agent status` CLI subcommand to show current state
- Slack/webhook notification on deploy success or failure
- Support for multiple repos (already supported by config structure)
- Health check endpoint (`/healthz`) for container orchestration
