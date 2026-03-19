package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/model"
	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/publicbundle"
)

func TestLoadVerifyConfigFromBundleUsesSnapshotAndLocalCache(t *testing.T) {
	bundleDir := t.TempDir()

	manifest := publicbundle.Manifest{
		ConfigSnapshotPath: "config.snapshot.json",
	}
	writeJSONFile(t, filepath.Join(bundleDir, "manifest.json"), manifest)

	snapshot := model.CeremonyConfig{
		ID: "ceremony-123",
		Phase1: model.Phase1Spec{
			SourceURL: "https://example.com/powersOfTau28_hez_final_{power}.ptau",
			ExpectedSHA256ByPower: map[string]string{
				"1": "phase1-sha",
			},
		},
		Circuits: []model.CircuitSpec{{
			ID:    "c1",
			Name:  "c1",
			Type:  model.CircuitTypeDeposit,
			Depth: 4,
		}},
	}
	writeJSONFile(t, filepath.Join(bundleDir, "config.snapshot.json"), snapshot)

	cfg, err := loadVerifyConfigFromBundle(bundleDir)
	if err != nil {
		t.Fatalf("loadVerifyConfigFromBundle returned error: %v", err)
	}

	wantCacheRoot := filepath.Join(os.TempDir(), "privacy-boost-ceremony-verify-cache", "ceremony-123")
	if cfg.ID != snapshot.ID {
		t.Fatalf("expected ceremony id %q, got %q", snapshot.ID, cfg.ID)
	}
	if cfg.Phase1.SourceURL != snapshot.Phase1.SourceURL {
		t.Fatalf("expected phase1 source url %q, got %q", snapshot.Phase1.SourceURL, cfg.Phase1.SourceURL)
	}
	if cfg.StateDir != wantCacheRoot {
		t.Fatalf("expected state dir %q, got %q", wantCacheRoot, cfg.StateDir)
	}
	if cfg.Cache.RootDir != wantCacheRoot {
		t.Fatalf("expected cache root %q, got %q", wantCacheRoot, cfg.Cache.RootDir)
	}
	if len(cfg.Circuits) != 1 || cfg.Circuits[0].ID != "c1" {
		t.Fatalf("expected one circuit from snapshot, got %+v", cfg.Circuits)
	}
}

func TestLoadVerifyConfigFromBundleRejectsPathTraversal(t *testing.T) {
	bundleDir := t.TempDir()
	writeJSONFile(t, filepath.Join(bundleDir, "manifest.json"), publicbundle.Manifest{
		ConfigSnapshotPath: "../../secret.json",
	})

	if _, err := loadVerifyConfigFromBundle(bundleDir); err == nil {
		t.Fatal("expected invalid config snapshot path error")
	}
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
