// Package cache manages coordinator cache directories and selective cleanup scopes.
package cache

import (
	"fmt"
	"os"
	"path/filepath"
)

type Manager struct {
	StateRoot string
	CacheRoot string
}

// New creates a cache manager with state and cache roots; cacheRoot defaults to stateRoot when empty.
func New(stateRoot, cacheRoot string) *Manager {
	// Evaluate the guard condition for this branch.
	if cacheRoot == "" {
		cacheRoot = stateRoot
	}

	// Manager holds both roots for path computation.
	return &Manager{
		StateRoot: stateRoot,
		CacheRoot: cacheRoot,
	}
}

// Ensure creates all cache, artifact, and transcript directories required by the coordinator.
func (m *Manager) Ensure() error {
	// Create each directory in layout; layout must exist before setup/finalize phases run.
	for _, p := range []string{
		m.Phase1Dir(),
		m.CompileDir(),
		m.DeserializeDir(),
		m.Phase2OriginDir(),
		m.FinalizeEvalsDir(),
		m.ArtifactsDir(),
		m.TranscriptsDir(),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			// Return the computed result to the caller.
			return fmt.Errorf("mkdir %s: %w", p, err)
		}
	}

	// Finish successfully after all steps complete.
	return nil
}

// Phase1Dir returns phase1 cache directory path.
func (m *Manager) Phase1Dir() string { return filepath.Join(m.CacheRoot, "cache", "phase1") }

// CompileDir returns compiled circuit cache directory path.
func (m *Manager) CompileDir() string { return filepath.Join(m.CacheRoot, "cache", "compile") }

// DeserializeDir returns ptau->ph1 conversion cache directory path.
func (m *Manager) DeserializeDir() string { return filepath.Join(m.CacheRoot, "cache", "deserialize") }

// Phase2OriginDir returns reusable setup origin cache directory path.
func (m *Manager) Phase2OriginDir() string {
	// Reusable phase2 origin artifacts for setup.
	return filepath.Join(m.CacheRoot, "cache", "phase2-origin")
}

// Phase2OriginPath returns reusable setup origin cache file path.
func (m *Manager) Phase2OriginPath(cacheKey string) string {
	// Filename uses .ph2 extension for phase2 binary.
	return filepath.Join(m.Phase2OriginDir(), cacheKey+".ph2")
}

// FinalizeEvalsDir returns persisted finalize evaluation cache directory path.
func (m *Manager) FinalizeEvalsDir() string {
	// Return the computed result to the caller.
	return filepath.Join(m.CacheRoot, "cache", "finalize-evals")
}

// FinalizeEvalPath returns persisted finalize evaluation cache file path.
func (m *Manager) FinalizeEvalPath(cacheKey string) string {
	// Filename uses .bin extension for serialized evaluation data.
	return filepath.Join(m.FinalizeEvalsDir(), cacheKey+".bin")
}

// ArtifactsDir returns phase2 artifact output directory path.
func (m *Manager) ArtifactsDir() string { return filepath.Join(m.StateRoot, "artifacts") }

// TranscriptsDir returns transcript output directory path.
func (m *Manager) TranscriptsDir() string { return filepath.Join(m.StateRoot, "transcripts") }

// Clear removes cached data for the given scope (phase1, compile, deserialize, phase2-origin, finalize-evals, all).
func (m *Manager) Clear(scope string) error {
	// Each scope maps to a directory or subtree to remove.
	switch scope {
	case "phase1":
		// Return the computed result to the caller.
		return os.RemoveAll(m.Phase1Dir())
	case "compile":
		// Return the computed result to the caller.
		return os.RemoveAll(m.CompileDir())
	case "deserialize":
		// Return the computed result to the caller.
		return os.RemoveAll(m.DeserializeDir())
	case "phase2-origin":
		// Return the computed result to the caller.
		return os.RemoveAll(m.Phase2OriginDir())
	case "finalize-evals":
		// Return the computed result to the caller.
		return os.RemoveAll(m.FinalizeEvalsDir())
	case "all":
		// Return the computed result to the caller.
		return os.RemoveAll(filepath.Join(m.CacheRoot, "cache"))
	default:
		// Return the computed result to the caller.
		return fmt.Errorf("unknown clear scope: %s", scope)
	}
}
