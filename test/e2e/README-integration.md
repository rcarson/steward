# End-to-End Integration Tests

## How to run

```
go test -tags integration -timeout 10m ./test/e2e/...
```

Add `-v` for verbose output:

```
go test -tags integration -timeout 10m -v ./test/e2e/...
```

## Requirements

- **Network access** — the tests connect to `https://github.com/go-git/go-git.git` to fetch a real remote hash and clone a sparse subdirectory.
- **Docker socket** — tests that exercise `compose.Up` require Docker to be installed and the socket to be accessible. Tests that need Docker call `exec.LookPath("docker")` and skip gracefully when Docker is not available.

## What the tests do

| Test | What it exercises |
|------|-------------------|
| `TestE2E_RemoteHash` | Calls `git.Client.RemoteHash` against the real public `go-git/go-git` repository on the `master` branch and verifies the result is a 40-character SHA. No Docker required. |
| `TestE2E_SyncPath` | Calls `git.Client.SyncPath` to perform a sparse clone of the `plumbing/` subdirectory into a temp dir, then verifies the directory exists and contains files. No Docker required. |
| `TestE2E_ComposeUp` | Writes a minimal `compose.yml` that runs `hello-world` into a temp dir, then calls `compose.DockerRunner.Up` and verifies it succeeds. Requires Docker. |
| `TestE2E_FullAgentLoop` | Wires together the real `git.Client`, `compose.DockerRunner`, and `state.FileStore` via `agent.NewStack`. Pre-clones the repo, plants a `compose.yml` in the checked-out path, starts the agent's poll loop, and waits (up to 3 minutes) for the state store to be updated with the remote hash — confirming the full detect → sync → deploy pipeline executed successfully. Requires Docker. |

## Notes

- All temporary directories are created with `t.TempDir()` and cleaned up automatically.
- The full loop test uses a 3-minute context deadline to accommodate slow network or Docker image pull times.
- The `hello-world` image is tiny (~13 kB) and is the standard Docker "smoke test" image.
