// Package artifacts defines artifact storage and hashing primitives used by transcripts.
package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// Store defines artifact storage operations: Root returns the storage root directory;
// Digest computes sha256 for an artifact path (absolute or store-relative).
type Store interface {
	Root() string
	Digest(path string) (string, error)
}

// FileStore is a filesystem-backed implementation of Store.
type FileStore struct {
	root string
}

// NewFileStore initializes filesystem-backed artifact storage root.
func NewFileStore(root string) (*FileStore, error) {
	// Check the operation result and return on error.
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}

	// Return the computed result to the caller.
	return &FileStore{root: root}, nil
}

// Root returns artifact storage root directory.
func (s *FileStore) Root() string {
	// Return the computed result to the caller.
	return s.root
}

// Digest computes sha256 for an artifact path (absolute or store-relative).
func (s *FileStore) Digest(path string) (string, error) {
	// Evaluate the guard condition for this branch.
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.root, path)
	}

	// Open the required input file from disk.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Compute values for the next processing step.
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	// Return the computed result to the caller.
	return hex.EncodeToString(h.Sum(nil)), nil
}
