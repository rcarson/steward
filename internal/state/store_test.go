package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestGet_UnknownName verifies that Get returns found=false for a key that has
// never been stored.
func TestGet_UnknownName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewFileStore(filepath.Join(dir, ".state.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	_, found := store.Get("nonexistent")
	if found {
		t.Error("expected found=false for unknown name, got true")
	}
}

// TestSet_ThenGet verifies that a value stored with Set is returned by Get.
func TestSet_ThenGet(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewFileStore(filepath.Join(dir, ".state.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	const name = "immich"
	const want = "a3f1c9d"

	if err := store.Set(name, want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, found := store.Get(name)
	if !found {
		t.Fatalf("expected found=true after Set, got false")
	}
	if got != want {
		t.Errorf("Get = %q, want %q", got, want)
	}
}

// TestSet_AtomicWrite verifies that Set writes through a temp file and then
// renames it, so the final file contains valid JSON with the expected content.
func TestSet_AtomicWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, ".state.json")

	store, err := NewFileStore(statePath)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	if err := store.Set("nextcloud", "b72e01a"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// The state file must exist after Set.
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("ReadFile after Set: %v", err)
	}

	var state map[string]string
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("state file is not valid JSON: %v", err)
	}

	got, ok := state["nextcloud"]
	if !ok {
		t.Fatal("key 'nextcloud' missing from state file")
	}
	if got != "b72e01a" {
		t.Errorf("state file has hash %q, want %q", got, "b72e01a")
	}

	// No temp files should be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != ".state.json" {
			t.Errorf("unexpected leftover file after atomic write: %s", e.Name())
		}
	}
}

// TestConcurrentReads verifies that multiple goroutines can call Get
// simultaneously without a data race.
func TestConcurrentReads(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewFileStore(filepath.Join(dir, ".state.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	if err := store.Set("immich", "deadbeef"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			hash, found := store.Get("immich")
			if !found || hash != "deadbeef" {
				// t.Error is goroutine-safe.
				t.Errorf("concurrent Get: got (%q, %v), want (deadbeef, true)", hash, found)
			}
		}()
	}

	wg.Wait()
}

// TestNewFileStore_LoadsExistingState verifies that an existing state file is
// read correctly when NewFileStore is called (simulating a process restart).
func TestNewFileStore_LoadsExistingState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, ".state.json")

	initial := map[string]string{
		"immich":    "a3f1c9d",
		"nextcloud": "b72e01a",
	}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Simulate restart by opening a new FileStore against the existing file.
	store, err := NewFileStore(statePath)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	for name, want := range initial {
		got, found := store.Get(name)
		if !found {
			t.Errorf("Get(%q): expected found=true after load", name)
			continue
		}
		if got != want {
			t.Errorf("Get(%q) = %q, want %q", name, got, want)
		}
	}
}
