# steward

**Continuous reconciliation for Docker Compose stacks.**

A lightweight per-host daemon written in Go that watches Git repositories for changes to Docker Compose stacks and reconciles the running state automatically. Each host runs its own instance — there is no coordinator, no database, and no UI. State is minimal and local.

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
mkdir -p /opt/steward/data
```

**2. Write a config file.**

```bash
cp config.example.yaml /opt/steward/config.yaml
# Edit /opt/steward/config.yaml for your stacks
```

**3. Create a `.env` file with any tokens (chmod 600).**

```bash
# /opt/steward/.env
STEWARD_DEFAULT_TOKEN=ghp_abc123...
chmod 600 /opt/steward/.env
```

**4. Deploy with Docker Compose.**

```bash
cd /opt/steward && docker compose up -d
```

The `examples/docker-compose/compose.yaml` in this repository is the deployment manifest for the agent itself. Copy it to `/opt/steward/compose.yaml` and adjust the volume mounts if needed:

```yaml
services:
  steward:
    image: ghcr.io/rcarson/steward:latest
    restart: unless-stopped
    ports:
      - "2112:2112"
    environment:
      - STEWARD_DEFAULT_TOKEN=${STEWARD_DEFAULT_TOKEN}
      - STEWARD_LOG_LEVEL=info
      - STEWARD_HTTP_ADDR=:2112
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://localhost:2112/healthz || exit 1"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./config.yaml:/opt/steward/config.yaml:ro
      - ./data:/opt/steward/data
```

View logs with:

```bash
docker logs -f steward
```

## Configuration

The config file is read from `/opt/steward/config.yaml` (or `config.yml`) by default, preferring `.yaml`. Override the path with the `--config` flag or the `STEWARD_CONFIG` environment variable.

```yaml
# Global defaults (all overridable per stack)
defaults:
  poll_interval: 60
  branch: main
  work_dir: /var/lib/steward/stacks
  token: ${STEWARD_DEFAULT_TOKEN}  # optional

stacks:
  - name: immich
    repo: https://github.com/example/host-services.git
    path: stacks/immich
    branch: main
    token: ${MY_REPO_TOKEN}
    env_file: immich.env
    poll_interval: 60

  - name: nextcloud
    repo: https://github.com/example/host-services.git
    path: stacks/nextcloud
    env_file: nextcloud.env
    poll_interval: 120
```

### `defaults` fields

| Field | Type | Default | Description |
|---|---|---|---|
| `poll_interval` | int | `60` | Polling interval in seconds. Applied to all stacks unless overridden. |
| `branch` | string | `main` | Git branch to track. |
| `work_dir` | string | `/opt/steward/data` | Directory where repos are checked out and state is stored. |
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
# compose.yaml environment section
environment:
  - STEWARD_DEFAULT_TOKEN=${STEWARD_DEFAULT_TOKEN}
```

```yaml
# config.yaml
stacks:
  - name: mystack
    token: ${STEWARD_DEFAULT_TOKEN}
```

Token resolution order: per-stack `token` → `defaults.token` → empty (public repo).

### Config file location

| Method | Example |
|---|---|
| Default path | `/opt/steward/config.yaml` (falls back to `config.yml`) |
| `--config` flag | `steward --config /etc/steward/config.yaml` |
| Environment variable | `STEWARD_CONFIG=/etc/steward/config.yaml` |

## Environment variables

| Variable | Description |
|---|---|
| `STEWARD_CONFIG` | Path to the config file. Equivalent to `--config`. |
| `STEWARD_LOG_LEVEL` | Log verbosity: `debug`, `info`, `warn`, or `error`. Defaults to `info`. |
| `STEWARD_HTTP_ADDR` | Listen address for the HTTP server exposing `/healthz` and `/metrics`. Defaults to `:2112`. Change if port 2112 is already in use on the host (e.g. `STEWARD_HTTP_ADDR=:9100`). |
| `STEWARD_DEFAULT_TOKEN` | Default auth token for private repos. Used when no per-stack `token` is set. |

## Observability

All output goes to stdout/stderr using Go's `log/slog` (structured logging).

Every successful deploy logs: stack name, old hash, new hash, and duration.

Every error logs: stack name, operation, and error string. Tokens are redacted from all error messages before logging.

Set `STEWARD_LOG_LEVEL=debug` to see hash comparisons and poll timing.

steward exposes two HTTP endpoints on `STEWARD_HTTP_ADDR` (default `:2112`):

| Endpoint | Description |
|---|---|
| `GET /healthz` | Returns `{"status":"ok","version":"...","uptime":"..."}` |
| `GET /metrics` | Prometheus metrics — scrape with your Prometheus instance |

See `examples/monitoring/` for a Grafana dashboard and Prometheus scrape config.

## Building

```bash
# Build the binary
go build -o steward ./cmd/steward

# Build the container image
docker build -t steward .
```

The Dockerfile uses a two-stage build: Go 1.26 Alpine builder, Alpine 3.21 runtime. The binary runs as root (required for Docker socket access). No git binary is included in the image — go-git is pure Go.

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
- **Web UI** — there is none. Use `docker logs` and the Prometheus metrics endpoint.
