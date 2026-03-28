# stack-agent — Agent Instructions

## Branching and pull requests

- **Never push directly to `main`.** All changes must go through a pull request.
- Branch names should follow the pattern `<type>/<short-description>` (e.g. `feat/metrics-endpoint`, `fix/compose-path`, `ci/update-golangci`).
- Create the GitHub issue first, then create a branch, do the work, open a PR referencing the issue.
- PRs are merged by the human — do not auto-merge unless explicitly asked.

## CI

- The `test` job must pass before a PR can merge (enforced by branch protection).
- Always verify linting locally with the **exact pinned version** of golangci-lint before pushing:
  ```sh
  golangci-lint run ./...
  ```
- Check `.pre-commit-config.yaml` for the current pinned version.

## Testing

- Write tests before implementation (see global CLAUDE.md).
- Run the full test suite before opening a PR:
  ```sh
  go test -race ./...
  ```

## Tooling

- Pre-commit hooks are installed — they run automatically on commit.
