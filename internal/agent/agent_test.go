package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rcarson/steward/internal/config"
	"github.com/rcarson/steward/internal/metrics"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockGit is a hand-written mock for git.Client.
type mockGit struct {
	mu sync.Mutex

	remoteHashFn func(ctx context.Context, repo, branch, token string) (string, error)
	syncPathFn   func(ctx context.Context, repo, branch, path, workDir, name, token string) error

	remoteHashCalls int
	syncPathCalls   int
}

func (m *mockGit) RemoteHash(ctx context.Context, repo, branch, token string) (string, error) {
	m.mu.Lock()
	m.remoteHashCalls++
	fn := m.remoteHashFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, repo, branch, token)
	}
	return "", nil
}

func (m *mockGit) SyncPath(ctx context.Context, repo, branch, path, workDir, name, token string) error {
	m.mu.Lock()
	m.syncPathCalls++
	fn := m.syncPathFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, repo, branch, path, workDir, name, token)
	}
	return nil
}

// mockCompose is a hand-written mock for compose.Runner.
type mockCompose struct {
	mu sync.Mutex

	upFn              func(ctx context.Context, composePath, envFile, projectName string) error
	findComposeFileFn func(path string) string

	upCalls              int
	findComposeFileCalls int
}

func (m *mockCompose) Up(ctx context.Context, composePath, envFile, projectName string) error {
	m.mu.Lock()
	m.upCalls++
	fn := m.upFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, composePath, envFile, projectName)
	}
	return nil
}

func (m *mockCompose) FindComposeFile(path string) string {
	m.mu.Lock()
	m.findComposeFileCalls++
	fn := m.findComposeFileFn
	m.mu.Unlock()
	if fn != nil {
		return fn(path)
	}
	return filepath.Join(path, "compose.yml")
}

// mockState is a hand-written mock for state.Store.
type mockState struct {
	mu   sync.Mutex
	data map[string]string

	setCalls int
	getCalls int
}

func newMockState() *mockState {
	return &mockState{data: make(map[string]string)}
}

func (m *mockState) Get(name string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls++
	h, ok := m.data[name]
	return h, ok
}

func (m *mockState) Set(name, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setCalls++
	m.data[name] = hash
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// stackCfg returns a minimal StackConfig with PollInterval 0 (fires instantly).
func stackCfg(name string) config.StackConfig {
	return config.StackConfig{
		Name:         name,
		Repo:         "https://example.com/repo",
		Branch:       "main",
		Path:         "services/app",
		WorkDir:      "/tmp/stacks",
		ConfigDir:    "/tmp/config",
		PollInterval: 0,
	}
}

// runOnce starts s.Run in a goroutine, waits briefly for one poll to complete,
// then cancels the context and waits for Run to return.
func runOnce(s *Stack) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	// Allow one poll iteration then stop.
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestHashUnchanged verifies that compose.Up is NOT called when the remote
// hash equals the stored state hash.
func TestHashUnchanged(t *testing.T) {
	g := &mockGit{remoteHashFn: func(_ context.Context, _, _, _ string) (string, error) {
		return "abc123", nil
	}}
	c := &mockCompose{}
	st := newMockState()
	st.data["test-stack"] = "abc123" // pre-seed matching hash

	s := NewStack(stackCfg("test-stack"), g, c, st, &metrics.NoopRecorder{})
	runOnce(s)

	c.mu.Lock()
	upCalls := c.upCalls
	c.mu.Unlock()

	if upCalls != 0 {
		t.Errorf("expected Up not to be called when hash unchanged, got %d calls", upCalls)
	}
}

// TestHashChanged verifies that SyncPath and Up are called in order when the
// hash differs.
func TestHashChanged(t *testing.T) {
	var mu sync.Mutex
	order := make([]string, 0, 2)

	g := &mockGit{
		remoteHashFn: func(_ context.Context, _, _, _ string) (string, error) {
			return "newHash", nil
		},
		syncPathFn: func(_ context.Context, _, _, _, _, _, _ string) error {
			mu.Lock()
			order = append(order, "sync")
			mu.Unlock()
			return nil
		},
	}
	c := &mockCompose{
		upFn: func(_ context.Context, _, _, _ string) error {
			mu.Lock()
			order = append(order, "up")
			mu.Unlock()
			return nil
		},
	}
	st := newMockState()
	// stored hash is empty → remote "newHash" differs → deploy expected

	s := NewStack(stackCfg("test-stack"), g, c, st, &metrics.NoopRecorder{})
	runOnce(s)

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "sync" || order[1] != "up" {
		t.Errorf("expected [sync up], got %v", order)
	}
}

// TestSyncPathFailure verifies that Up is NOT called and state is NOT updated
// when SyncPath fails.
func TestSyncPathFailure(t *testing.T) {
	g := &mockGit{
		remoteHashFn: func(_ context.Context, _, _, _ string) (string, error) {
			return "newHash", nil
		},
		syncPathFn: func(_ context.Context, _, _, _, _, _, _ string) error {
			return errors.New("sync failed")
		},
	}
	c := &mockCompose{}
	st := newMockState()

	s := NewStack(stackCfg("test-stack"), g, c, st, &metrics.NoopRecorder{})
	runOnce(s)

	c.mu.Lock()
	upCalls := c.upCalls
	c.mu.Unlock()

	st.mu.Lock()
	setCalls := st.setCalls
	hash := st.data["test-stack"]
	st.mu.Unlock()

	if upCalls != 0 {
		t.Errorf("expected Up not called after SyncPath failure, got %d", upCalls)
	}
	if setCalls != 0 {
		t.Errorf("expected state.Set not called after SyncPath failure, got %d", setCalls)
	}
	if hash != "" {
		t.Errorf("expected state hash empty after SyncPath failure, got %q", hash)
	}
}

// TestUpFailure verifies that state hash is NOT updated when compose.Up fails.
func TestUpFailure(t *testing.T) {
	g := &mockGit{
		remoteHashFn: func(_ context.Context, _, _, _ string) (string, error) {
			return "newHash", nil
		},
	}
	c := &mockCompose{
		upFn: func(_ context.Context, _, _, _ string) error {
			return errors.New("compose up failed")
		},
	}
	st := newMockState()

	s := NewStack(stackCfg("test-stack"), g, c, st, &metrics.NoopRecorder{})
	runOnce(s)

	st.mu.Lock()
	setCalls := st.setCalls
	hash := st.data["test-stack"]
	st.mu.Unlock()

	if setCalls != 0 {
		t.Errorf("expected state.Set not called after Up failure, got %d", setCalls)
	}
	if hash != "" {
		t.Errorf("expected state hash empty after Up failure, got %q", hash)
	}
}

// TestUpSuccess verifies that state hash IS updated after a successful deploy.
func TestUpSuccess(t *testing.T) {
	g := &mockGit{
		remoteHashFn: func(_ context.Context, _, _, _ string) (string, error) {
			return "newHash", nil
		},
	}
	c := &mockCompose{}
	st := newMockState()

	s := NewStack(stackCfg("test-stack"), g, c, st, &metrics.NoopRecorder{})
	runOnce(s)

	st.mu.Lock()
	hash := st.data["test-stack"]
	st.mu.Unlock()

	if hash != "newHash" {
		t.Errorf("expected state hash %q after Up success, got %q", "newHash", hash)
	}
}

// TestContextCancellation verifies that Run exits cleanly when ctx is cancelled.
func TestContextCancellation(t *testing.T) {
	// Block in RemoteHash until context is cancelled so we exercise the
	// timer-select cancellation path in Run as well.
	g := &mockGit{
		remoteHashFn: func(ctx context.Context, _, _, _ string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	c := &mockCompose{}
	st := newMockState()

	s := NewStack(stackCfg("test-stack"), g, c, st, &metrics.NoopRecorder{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}

// TestConcurrentStacks verifies that two stacks run concurrently in separate
// goroutines and both execute their poll loops independently.
func TestConcurrentStacks(t *testing.T) {
	var calls1, calls2 atomic.Int64

	makeGit := func(counter *atomic.Int64) *mockGit {
		return &mockGit{
			remoteHashFn: func(_ context.Context, _, _, _ string) (string, error) {
				counter.Add(1)
				return "hash", nil
			},
		}
	}

	st1 := newMockState()
	st2 := newMockState()

	s1 := NewStack(stackCfg("stack1"), makeGit(&calls1), &mockCompose{}, st1, &metrics.NoopRecorder{})
	s2 := NewStack(stackCfg("stack2"), makeGit(&calls2), &mockCompose{}, st2, &metrics.NoopRecorder{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s1.Run(ctx) }()
	go func() { defer wg.Done(); s2.Run(ctx) }()

	// Allow both stacks enough time to execute at least one poll each.
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	if calls1.Load() == 0 {
		t.Error("stack1 never polled")
	}
	if calls2.Load() == 0 {
		t.Error("stack2 never polled")
	}
}

// TestPollIntervalRespected verifies that the poll loop iterates multiple times
// within a short window when PollInterval is 0 (timer fires instantly).
func TestPollIntervalRespected(t *testing.T) {
	const window = 60 * time.Millisecond

	var callCount atomic.Int64
	g := &mockGit{
		remoteHashFn: func(_ context.Context, _, _, _ string) (string, error) {
			callCount.Add(1)
			return "hash", nil
		},
	}

	// PollInterval=0 → time.NewTimer(0) fires immediately, so the loop spins
	// as fast as it can. We expect many iterations within the window.
	s := NewStack(stackCfg("test-stack"), g, &mockCompose{}, newMockState(), &metrics.NoopRecorder{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	time.Sleep(window)
	cancel()
	<-done

	if callCount.Load() < 2 {
		t.Errorf("expected multiple poll iterations within %v window, got %d", window, callCount.Load())
	}
}

// TestNoComposeFile verifies that Up is NOT called when HasComposeFile returns
// false, and the state hash is NOT updated.
func TestNoComposeFile(t *testing.T) {
	g := &mockGit{
		remoteHashFn: func(_ context.Context, _, _, _ string) (string, error) {
			return "newHash", nil
		},
	}
	c := &mockCompose{
		findComposeFileFn: func(_ string) string { return "" },
	}
	st := newMockState()

	s := NewStack(stackCfg("test-stack"), g, c, st, &metrics.NoopRecorder{})
	runOnce(s)

	c.mu.Lock()
	upCalls := c.upCalls
	c.mu.Unlock()

	st.mu.Lock()
	setCalls := st.setCalls
	st.mu.Unlock()

	if upCalls != 0 {
		t.Errorf("expected Up not called when no compose file, got %d", upCalls)
	}
	if setCalls != 0 {
		t.Errorf("expected state not updated when no compose file, got %d", setCalls)
	}
}

// ---------------------------------------------------------------------------
// resolveEnvFile tests
// ---------------------------------------------------------------------------

func TestResolveEnvFile_ExplicitRelativePath(t *testing.T) {
	dir := t.TempDir()
	got := resolveEnvFile(dir, "mystack", "custom.env")
	if got != filepath.Join(dir, "custom.env") {
		t.Errorf("expected %q, got %q", filepath.Join(dir, "custom.env"), got)
	}
}

func TestResolveEnvFile_DefaultExists(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "mystack.env")
	if err := os.WriteFile(envPath, []byte("FOO=bar"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := resolveEnvFile(dir, "mystack", "")
	if got != envPath {
		t.Errorf("expected %q, got %q", envPath, got)
	}
}

func TestResolveEnvFile_DefaultMissing(t *testing.T) {
	dir := t.TempDir()
	got := resolveEnvFile(dir, "mystack", "")
	if got != "" {
		t.Errorf("expected empty string when default env file missing, got %q", got)
	}
}

func TestResolveEnvFile_ExplicitAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	abs := "/etc/stacks/mystack.env"
	got := resolveEnvFile(dir, "mystack", abs)
	if got != abs {
		t.Errorf("expected absolute path %q to be used as-is, got %q", abs, got)
	}
}
