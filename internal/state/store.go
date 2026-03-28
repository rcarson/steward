package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store persists the last-deployed commit hash per stack name.
type Store interface {
	Get(name string) (hash string, found bool)
	Set(name string, hash string) error
}

// FileStore is a Store backed by a JSON file on disk.
type FileStore struct {
	mu   sync.RWMutex
	path string
	data map[string]string
}

// NewFileStore loads existing state from path if present, or creates an empty
// state if the file does not exist. Returns an error if the file exists but
// cannot be read or parsed.
func NewFileStore(path string) (*FileStore, error) {
	fs := &FileStore{
		path: path,
		data: make(map[string]string),
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fs, nil
		}
		return nil, fmt.Errorf("state: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if err := json.NewDecoder(f).Decode(&fs.data); err != nil {
		return nil, fmt.Errorf("state: decode %s: %w", path, err)
	}

	return fs, nil
}

// Get returns the stored hash for name and whether it was found.
// It is safe for concurrent use.
func (fs *FileStore) Get(name string) (string, bool) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	hash, found := fs.data[name]
	return hash, found
}

// Set stores hash for name and writes the updated state to disk atomically.
// It writes to a temp file in the same directory and then renames it over the
// target file to avoid leaving a corrupt state file on crash.
func (fs *FileStore) Set(name, hash string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.data[name] = hash

	dir := filepath.Dir(fs.path)
	tmp, err := os.CreateTemp(dir, ".state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("state: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if err := json.NewEncoder(tmp).Encode(fs.data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("state: encode to temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("state: close temp file: %w", err)
	}

	if err := os.Rename(tmpName, fs.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("state: rename temp to %s: %w", fs.path, err)
	}

	return nil
}
