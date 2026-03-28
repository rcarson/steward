package git

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

// Client provides git operations used by stack-agent.
type Client interface {
	// RemoteHash returns the current HEAD commit hash for the given repo+branch.
	// Uses go-git's remote.List() — no local clone required.
	RemoteHash(ctx context.Context, repo, branch, token string) (string, error)

	// SyncPath ensures a sparse checkout of path exists under workDir/name.
	// Creates on first call (clone), updates on subsequent calls (pull).
	SyncPath(ctx context.Context, repo, branch, path, workDir, name, token string) error
}

// client is the production implementation of Client.
type client struct{}

// New returns a new production Client.
func New() Client {
	return &client{}
}

// authFromToken returns HTTP basic auth from a token, or nil if token is empty.
func authFromToken(token string) *githttp.BasicAuth {
	if token == "" {
		return nil
	}
	return &githttp.BasicAuth{
		Username: "x-token",
		Password: token,
	}
}

// scrubToken replaces all occurrences of token in s with "***".
func scrubToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}

// RemoteHash returns the HEAD commit hash for the given repo+branch without
// cloning the repository.
func (c *client) RemoteHash(ctx context.Context, repo, branch, token string) (string, error) {
	rem := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{repo},
	})

	refs, err := rem.ListContext(ctx, &git.ListOptions{
		Auth: authFromToken(token),
	})
	if err != nil {
		return "", fmt.Errorf("git: remote list %q: %w",
			scrubToken(repo, token),
			fmt.Errorf("%s", scrubToken(err.Error(), token)),
		)
	}

	target := plumbing.NewBranchReferenceName(branch)
	for _, ref := range refs {
		if ref.Name() == target {
			return ref.Hash().String(), nil
		}
	}

	return "", fmt.Errorf("git: branch %q not found in remote %q",
		branch, scrubToken(repo, token))
}

// SyncPath ensures a sparse checkout of path under workDir/name is up to date.
// On first call it clones; on subsequent calls it pulls.
func (c *client) SyncPath(ctx context.Context, repo, branch, path, workDir, name, token string) error {
	localPath := filepath.Join(workDir, name)

	_, statErr := os.Stat(filepath.Join(localPath, ".git"))
	if os.IsNotExist(statErr) {
		return c.cloneSparse(ctx, repo, branch, path, localPath, token)
	}
	return c.pullExisting(ctx, branch, path, localPath, token)
}

// cloneSparse performs a blobless sparse clone checking out only path.
func (c *client) cloneSparse(ctx context.Context, repo, branch, path, localPath, token string) error {
	slog.Info("git: cloning repo", "repo", scrubToken(repo, token), "branch", branch, "path", path, "dest", localPath)

	r, err := git.PlainCloneContext(ctx, localPath, false, &git.CloneOptions{
		URL:           repo,
		Auth:          authFromToken(token),
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Depth:         1,
		NoCheckout:    true,
	})
	if err != nil {
		return fmt.Errorf("git: clone %q: %w",
			scrubToken(repo, token),
			fmt.Errorf("%s", scrubToken(err.Error(), token)),
		)
	}

	w, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("git: worktree: %w", err)
	}

	if err := w.Checkout(&git.CheckoutOptions{
		Branch:                    plumbing.NewBranchReferenceName(branch),
		SparseCheckoutDirectories: []string{path},
	}); err != nil {
		return fmt.Errorf("git: sparse checkout %q: %w", path,
			fmt.Errorf("%s", scrubToken(err.Error(), token)))
	}

	c.warnIfPathMissing(localPath, path)
	return nil
}

// pullExisting opens an existing repo and pulls the latest changes.
func (c *client) pullExisting(ctx context.Context, branch, path, localPath, token string) error {
	slog.Info("git: pulling repo", "dest", localPath, "branch", branch)

	r, err := git.PlainOpen(localPath)
	if err != nil {
		return fmt.Errorf("git: open %q: %w", localPath, err)
	}

	w, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("git: worktree: %w", err)
	}

	pullErr := w.PullContext(ctx, &git.PullOptions{
		Auth:          authFromToken(token),
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Depth:         1,
		Force:         true,
	})
	if pullErr != nil && pullErr != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("git: pull %q: %w",
			localPath,
			fmt.Errorf("%s", scrubToken(pullErr.Error(), token)),
		)
	}

	if pullErr == git.NoErrAlreadyUpToDate {
		slog.Debug("git: already up to date", "dest", localPath)
	}

	c.warnIfPathMissing(localPath, path)
	return nil
}

// warnIfPathMissing logs a warning when path does not exist after sync.
func (c *client) warnIfPathMissing(localPath, path string) {
	full := filepath.Join(localPath, filepath.FromSlash(path))
	if _, err := os.Stat(full); os.IsNotExist(err) {
		slog.Warn("git: path not found in repo after sync", "path", path, "local", localPath)
	}
}
