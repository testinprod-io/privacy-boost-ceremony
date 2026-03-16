// Package config loads and validates ceremony configuration files.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/model"
)

// Load reads a JSON config file, parses it, validates, and applies defaults.
func Load(path string) (*model.CeremonyConfig, error) {
	// Compute values for the next processing step.
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Declare local variables used in this stage.
	var cfg model.CeremonyConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse config json: %w", err)
	}

	// Check the operation result and return on error.
	if err := Validate(&cfg); err != nil {
		return nil, err
	}

	// Return the computed result to the caller.
	return &cfg, nil
}

// Validate enforces required fields, assigns defaults, and checks security constraints.
func Validate(cfg *model.CeremonyConfig) error {
	// Required top-level fields.
	if cfg.ID == "" {
		return fmt.Errorf("config.id is required")
	}
	if cfg.StateDir == "" {
		return fmt.Errorf("config.stateDir is required")

	}
	if len(cfg.Circuits) == 0 {
		return fmt.Errorf("at least one circuit is required")
	}

	// Default access mode.
	if cfg.AccessMode == "" {
		cfg.AccessMode = model.AccessPrivate

	}

	// Server defaults.
	if cfg.Server.ListenAddr == "" {
		cfg.Server.ListenAddr = "127.0.0.1:8787"
	}
	if cfg.Server.SessionTTLMinutes <= 0 {
		cfg.Server.SessionTTLMinutes = int((4 * time.Hour) / time.Minute)
	}
	if cfg.Server.ContributionLeaseTTLMinutes <= 0 {
		cfg.Server.ContributionLeaseTTLMinutes = int((60 * time.Minute) / time.Minute)
	}

	// Artifacts and cache defaults.
	if cfg.Artifacts.Provider == "" {
		cfg.Artifacts.Provider = "filesystem"
	}
	if cfg.Artifacts.RootDir == "" {
		cfg.Artifacts.RootDir = filepath.Join(cfg.StateDir, "artifacts")
	}

	// Evaluate the guard condition for this branch.
	if cfg.Cache.RootDir == "" {
		cfg.Cache.RootDir = cfg.StateDir
	}

	// Normalize important directories to absolute paths so downstream components
	// never interpret cache/artifact inputs relative to the wrong root.
	//
	// Paths in JSON configs can remain relative (for sharing across machines), but
	// runtime resolution uses the current working directory as the base.
	var err error
	cfg.StateDir, err = filepath.Abs(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("resolve stateDir to absolute path: %w", err)
	}
	cfg.Cache.RootDir, err = filepath.Abs(cfg.Cache.RootDir)
	if err != nil {
		return fmt.Errorf("resolve cache.rootDir to absolute path: %w", err)
	}
	cfg.Artifacts.RootDir, err = filepath.Abs(cfg.Artifacts.RootDir)
	if err != nil {
		return fmt.Errorf("resolve artifacts.rootDir to absolute path: %w", err)
	}
	if cfg.Phase1.SourcePath != "" {
		cfg.Phase1.SourcePath, err = filepath.Abs(cfg.Phase1.SourcePath)
		if err != nil {
			return fmt.Errorf("resolve phase1.sourcePath to absolute path: %w", err)
		}
	}

	// Phase1 source and checksum policy.
	if cfg.Phase1.SourceURL == "" && cfg.Phase1.SourcePath == "" {
		return fmt.Errorf("phase1.sourceUrl or phase1.sourcePath is required")
	}
	if len(cfg.Phase1.ExpectedSHA256ByPower) == 0 {
		return fmt.Errorf("phase1.expectedSha256ByPower is required")
	}

	// GitHub auth must be enabled; client ID required.
	if !cfg.GitHubAuth.Enabled {
		return fmt.Errorf("githubAuth.enabled must be true")
	}

	// Evaluate the guard condition for this branch.
	if cfg.GitHubAuth.ClientID == "" {
		return fmt.Errorf("githubAuth.clientId is required")
	}

	// Access mode and allowlist consistency.
	if cfg.AccessMode == model.AccessPrivate && len(cfg.Allowlist) == 0 {
		return fmt.Errorf("allowlist is required in private mode")
	}

	// Evaluate the guard condition for this branch.
	if cfg.AccessMode == model.AccessPublic && len(cfg.Allowlist) > 0 {
		return fmt.Errorf("allowlist must be empty in public mode")
	}

	// Per-circuit validation.
	for _, c := range cfg.Circuits {
		if c.ID == "" || c.Name == "" {
			return fmt.Errorf("each circuit needs id and name")
		}

		// Evaluate the guard condition for this branch.
		if c.Depth <= 0 {
			return fmt.Errorf("circuit %s depth must be > 0", c.ID)
		}
	}

	// Finish successfully after all steps complete.
	return nil
}
