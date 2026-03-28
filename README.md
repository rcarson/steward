# stack-agent

A lightweight per-host daemon written in Go that watches Git repositories for changes to Docker Compose stacks and reconciles the running state automatically. Each host runs its own agent instance — there is no coordinator, no database, and no UI. State is minimal and local.

## How it works

On startup the agent spawns one goroutine per configured stack. Each goroutine runs an independent poll loop:

1. Fetch the current remote HEAD hash for the configured repo and branch.
2. Compare against the last-deployed hash stored in `{work_dir}/.state.json`.
3. If the hash is unchanged, wait `poll_interval` seconds and repeat.
4. If the hash has changed, sparse-checkout the configured subdirectory of the repo.
5. Run `docker compose up -d --remove-orphans` against the checked-out path.
6. On success, persist the new hash to state. On failure, leave state unchanged so the next poll retries.

A failure in one stack never blocks the others. Transient errors (network, git timeout, compose failure) are logged and retried on the next poll.

Git operations use [go-git](https://github.com/go-git/go-git), a pure Go implementation — no `git` binary is required in the image or on developer machines.

## Quick start

**1. Create the directory layout on the host.**

```bash
mkdir -p /opt/stack-agent/data
```

**2. Write a config file.**

```bash
cp config.example.yaml /opt/stack-agent/config.yaml
# Edit /opt/stack-agent/config.yaml for your stacks
```

**3. Create a `.env` file with any tokens (chmod 600).**

```bash
# /opt/stack-agent/.env
HOST_SERVICES_TOKEN=ghp_abc123...
chmod 600 /opt/stack-agent/.env
```

**4. Deploy with Docker Compose.**

```bash
cd /opt/stack-agent && docker compose up -d
```

The `examples/docker-compose/compose.yaml` in this repository is the deployment manifest for the agent itself. Copy it to `/opt/stack-agent/compose.yaml` and adjust the volume mounts if needed:

```yaml
services:
  stack-agent:
    image: ghcr.io/rcarson/stack-agent:latest
    restart: unless-stopped
    environment:
      - HOST_SERVICES_TOKEN=${HOST_SERVICES_TOKEN}
      - STACK_AGENT_LOG_LEVEL=info
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /opt/stack-agent/config.yaml:/etc/stack-agent/config.yaml:ro
      - /opt/stack-agent/data:/var/lib/stack-agent
```

View logs with:

```bash
docker logs -f stack-agent
```

## Configuration

The config file is read from `/opt/stack-agent/config.yaml` (or `config.yml`) by default, preferring `.yaml`. Override the path with the `--config` flag or the `STACK_AGENT_CONFIG` environment variable.

```yaml
# Global defaults (all overridable per stack)
defaults:
  poll_interval: 60
  branch: main
  work_dir: /var/lib/stack-agent/stacks
  token: ${STACK_AGENT_DEFAULT_TOKEN}  # optional

stacks:
  - name: immich
    repo: https://github.com/example/host-services.git
    path: stacks/immich
    branch: main
    token: ${HOST_SERVICES_TOKEN}
    env_file: /etc/stacks/immich.env
    poll_interval: 60

  - name: nextcloud
    repo: https://github.com/example/host-services.git
    path: stacks/nextcloud
    env_file: /etc/stacks/nextcloud.env
    poll_interval: 120
```

### `defaults` fields

| Field | Type | Default | Description |
|---|---|---|---|
| `poll_interval` | int | `60` | Polling interval in seconds. Applied to all stacks unless overridden. |
| `branch` | string | `main` | Git branch to track. |
| `work_dir` | string | `/opt/stack-agent/data` | Directory where repos are checked out and state is stored. |
| `token` | string | _(empty)_ | Auth token for private repos. Supports `${ENV_VAR}` interpolation. |

### Per-stack fields

| Field | Required | Description |
|---|---|---|
| `name` | yes | Unique identifier for this stack on the host. |
| `repo` | yes | HTTPS URL of the Git repository. SSH is not supported. |
| `path` | yes | Subdirectory within the repo that contains the `compose.yml`. |
| `branch` | no | Overrides `defaults.branch`. |
| `token` | no | Overrides `defaults.token`. |
| `env_file` | no | Relative path (from `work_dir`) to an env file passed to compose as `--env-file`. Defaults to `{name}.env` in `work_dir` if that file exists. |
| `poll_interval` | no | Overrides `defaults.poll_interval`. Minimum: `10` seconds. |

### Token and environment variable interpolation

Any config value can reference a host environment variable using `${VAR_NAME}` syntax. Interpolation happens at load time. An unset variable resolves to an empty string (treated as no token — suitable for public repos).

Tokens are never written to disk inside the container and never appear in log output. Pass them into the container via environment variables:

```yaml
# compose.yml environment section
environment:
  - HOST_SERVICES_TOKEN=${HOST_SERVICES_TOKEN}
```

```yaml
# config.yml
stacks:
  - name: mystack
    token: ${HOST_SERVICES_TOKEN}
```

Token resolution order: per-stack `token` → `defaults.token` → empty (public repo).

### Config file location

| Method | Example |
|---|---|
| Default path | `/opt/stack-agent/config.yaml` (falls back to `config.yml`) |
| `--config` flag | `stack-agent --config /etc/stack-agent/config.yaml` |
| Environment variable | `STACK_AGENT_CONFIG=/etc/stack-agent/config.yaml` |

## Environment variables

| Variable | Description |
|---|---|
| `STACK_AGENT_CONFIG` | Path to the config file. Equivalent to `--config`. |
| `STACK_AGENT_LOG_LEVEL` | Log verbosity: `debug`, `info`, `warn`, or `error`. Defaults to `info`. |

## Observability

All output goes to stdout/stderr using Go's `log/slog` (structured logging).

Every successful deploy logs: stack name, old hash, new hash, and duration.

Every error logs: stack name, operation, and error string. Tokens are redacted from all error messages before logging.

Set `STACK_AGENT_LOG_LEVEL=debug` to see hash comparisons and poll timing. There is no metrics endpoint in v1 — `docker logs stack-agent` is the intended interface.

## Building

```bash
# Build the binary
go build -o stack-agent ./cmd/stack-agent

# Build the container image
docker build -t stack-agent .
```

The Dockerfile uses a two-stage build: Go 1.26 Alpine builder, Alpine 3.21 runtime. The binary runs as a non-root user (`agent`, uid 1000). No git binary is included in the image — go-git is pure Go.

## Running tests

```bash
# Run all unit tests
go test ./...

# Run with race detector
go test -race ./...
```

Unit tests use interface mocks for `git.Client`, `compose.Runner`, and `state.Store` so the core agent logic runs without real network or Docker dependencies.

Integration tests are excluded from the default run and require external dependencies:

```bash
# git integration tests — requires a real HTTPS remote and a token in the environment
go test -tags integration ./internal/git/...

# compose integration tests — requires a real Docker socket
go test -tags integration ./internal/compose/...
```

## Non-goals

- **Image updates** — use [Watchtower](https://github.com/containrrr/watchtower) or similar.
- **Multi-host coordination** — each host is fully autonomous.
- **Rollback** — out of scope for v1. Fix forward by updating the repo.
- **Web UI or REST API** — there is none. Use `docker logs`.
