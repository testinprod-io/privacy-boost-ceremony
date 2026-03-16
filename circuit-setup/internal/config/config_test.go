// Package config tests guard required fields and production policy defaults.
package config

import (
	"path/filepath"
	"testing"

	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/model"
)

// validBaseConfig returns a minimal valid config for tests.
func validBaseConfig(t *testing.T) *model.CeremonyConfig {
	// Proceed with the next logic block.
	t.Helper()

	// Minimal fields required by Validate.
	return &model.CeremonyConfig{
		ID:         "cfg",
		StateDir:   t.TempDir(),
		AccessMode: model.AccessPrivate,
		Allowlist:  []string{"allowed-gh-login"},
		// Populate fields for this object.
		GitHubAuth: model.GitHubAuthSpec{
			Enabled:  true,
			ClientID: "client-id",
		},
		Phase1: model.Phase1Spec{
			SourceURL: "https://example.com/file.ptau",
			ExpectedSHA256ByPower: map[string]string{
				"16": "hash16",
			},
		},
		Circuits: []model.CircuitSpec{
			// Proceed with the next logic block.
			{ID: "c1", Name: "epoch-small", Type: model.CircuitTypeEpoch, Depth: 4},
		},
	}
}

// TestValidate confirms a minimal valid config passes validation and defaulting.
func TestValidate(t *testing.T) {
	// Compute values for the next processing step.
	cfg := validBaseConfig(t)

	// Validation must succeed with no error.
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
	if cfg.Server.ContributionLeaseTTLMinutes <= 0 {
		t.Fatalf("expected contribution lease ttl default > 0, got %d", cfg.Server.ContributionLeaseTTLMinutes)
	}
}

// TestValidateDefaultsCacheRootToStateDir verifies Cache.RootDir defaults to StateDir when unset.
func TestValidateDefaultsCacheRootToStateDir(t *testing.T) {
	// Compute values for the next processing step.
	cfg := validBaseConfig(t)

	// Validate and assert cache root default.
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
	if cfg.Cache.RootDir != cfg.StateDir {
		t.Fatalf("expected cache root to default to stateDir: got=%s want=%s", cfg.Cache.RootDir, cfg.StateDir)

	}
}

// TestValidateRespectsCacheRootOverride ensures explicitly set Cache.RootDir is preserved.
func TestValidateRespectsCacheRootOverride(t *testing.T) {
	// Compute values for the next processing step.
	cfg := validBaseConfig(t)
	override := t.TempDir()
	cfg.Cache.RootDir = override

	// Validate must not overwrite explicit cache root.
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
	if cfg.Cache.RootDir != override {
		t.Fatalf("expected cache root override preserved: got=%s want=%s", cfg.Cache.RootDir, override)

	}
}

// TestValidateRejectsInvalid confirms circuits with invalid depth are rejected.
func TestValidateRejectsInvalid(t *testing.T) {
	// Compute values for the next processing step.
	cfg := validBaseConfig(t)
	cfg.Circuits = []model.CircuitSpec{
		{ID: "c1", Name: "bad", Type: model.CircuitTypeEpoch, Depth: 0, Phase1Power: 0},
	}

	// Depth 0 must trigger validation error.
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

// TestValidateProductionChecksumPolicy verifies production config with Phase1 checksum passes.
func TestValidateProductionChecksumPolicy(t *testing.T) {
	// Compute values for the next processing step.
	cfg := validBaseConfig(t)
	cfg.ID = "prod"
	cfg.Production = true
	cfg.Phase1 = model.Phase1Spec{
		// Populate fields for this object.
		SourceURL: "https://example.com/file.ptau",
		ExpectedSHA256ByPower: map[string]string{
			"16": "hash16",
		},
	}

	// Production with Phase1 checksum must pass.
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
}

// TestValidateRequiresGitHubAuth ensures GitHub auth must be enabled.
func TestValidateRequiresGitHubAuth(t *testing.T) {
	// Compute values for the next processing step.
	cfg := validBaseConfig(t)
	cfg.GitHubAuth.Enabled = false

	// Disabled GitHub auth must fail validation.
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error for missing github auth")
	}
}

// TestValidateAllowsMissingPhase1Power ensures deposit circuits without phase1Power are accepted.
func TestValidateAllowsMissingPhase1Power(t *testing.T) {
	// Compute values for the next processing step.
	cfg := validBaseConfig(t)
	cfg.ID = "no-power"
	cfg.Circuits = []model.CircuitSpec{
		{ID: "c1", Name: "deposit-small", Type: model.CircuitTypeDeposit, Depth: 4, BatchSize: 2, MaxTrees: 2},
	}

	// Deposit circuits may omit phase1Power.
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid config without phase1Power, got %v", err)
	}
}

// TestValidatePrivateRequiresAllowlist enforces allowlist in private access mode.
func TestValidatePrivateRequiresAllowlist(t *testing.T) {
	// Compute values for the next processing step.
	cfg := validBaseConfig(t)
	cfg.Allowlist = nil

	// Private mode with nil allowlist must fail.
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error for missing allowlist in private mode")
	}
}

// TestValidatePublicRejectsAllowlist ensures allowlist must be empty in public mode.
func TestValidatePublicRejectsAllowlist(t *testing.T) {
	// Compute values for the next processing step.
	cfg := validBaseConfig(t)
	cfg.AccessMode = model.AccessPublic
	cfg.Allowlist = []string{"should-not-be-set"}

	// Public mode with non-empty allowlist must fail.
	if err := Validate(cfg); err == nil {
		t.Fatal("expected validation error for allowlist in public mode")
	}
}

// TestValidatePublicAllowsEmptyAllowlist verifies public mode with nil allowlist passes.
func TestValidatePublicAllowsEmptyAllowlist(t *testing.T) {
	// Compute values for the next processing step.
	cfg := validBaseConfig(t)
	cfg.AccessMode = model.AccessPublic
	cfg.Allowlist = nil

	// Public mode with nil allowlist must pass.
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid public config, got %v", err)
	}
}

func TestValidateNormalizesPhase1SourcePathToAbsolute(t *testing.T) {
	cfg := validBaseConfig(t)
	cfg.Phase1.SourceURL = ""
	cfg.Phase1.SourcePath = "relative.ptau"

	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
	if !filepath.IsAbs(cfg.Phase1.SourcePath) {
		t.Fatalf("expected phase1.sourcePath to be absolute, got %q", cfg.Phase1.SourcePath)
	}
}
