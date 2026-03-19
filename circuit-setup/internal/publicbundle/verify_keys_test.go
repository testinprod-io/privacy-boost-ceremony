package publicbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/consensys/gnark/backend/groth16/bn254/mpcsetup"

	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/artifacts"
	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/config"
	finalizepkg "github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/finalize"
	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/model"
	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/phase2"
	provercompile "github.com/testinprod-io/privacy-boost-ceremony/prover/compile"
)

// TestVerifyDerivesKeysFromTranscript exercises the "deep" part of public verification:
// pk/vk are re-derived from (phase1, compiled R1CS, ordered Phase2 transcript outputs).
func TestVerifyDerivesKeysFromTranscript(t *testing.T) {
	// Arrange: build a minimal circuit spec and compute its required Phase1 power.
	compileDir := t.TempDir()
	circuit := model.CircuitSpec{
		ID:        "c1",
		Name:      "c1",
		Type:      model.CircuitTypeDeposit,
		BatchSize: 1,
		Depth:     4,
		MaxTrees:  2,
	}
	compileRes, err := provercompile.CompileWithMetadata(provercompile.CircuitSpec{
		ID:        circuit.ID,
		Name:      circuit.Name,
		Type:      provercompile.CircuitTypeDeposit,
		BatchSize: circuit.BatchSize,
		Depth:     circuit.Depth,
		MaxTrees:  circuit.MaxTrees,
	}, compileDir)
	if err != nil {
		t.Fatalf("compile metadata: %v", err)
	}
	power := compileRes.RequiredPhase1Power

	// Arrange: generate a small local `.ph1` with enough capacity for this circuit size.
	stateDir := t.TempDir()
	cacheRoot := filepath.Join(stateDir, "cache-root")
	phase1Path := filepath.Join(stateDir, "test.ph1")
	writePhase1(t, phase1Path, uint64(1)<<uint(power))
	phase1SHA := sha256FileHex(t, phase1Path)

	// Arrange: write a config that points Phase1 to the local `.ph1` and pins its checksum.
	cfgPath := filepath.Join(stateDir, "ceremony.config.json")
	cfg := &model.CeremonyConfig{
		ID:         "verify-keys-test",
		Title:      "verify keys test",
		AccessMode: model.AccessPublic,
		StateDir:   stateDir,
		Server: model.ServerSpec{
			ListenAddr: "127.0.0.1:0",
		},
		GitHubAuth: model.GitHubAuthSpec{
			Enabled:  true,
			ClientID: "test-client-id",
		},
		Artifacts: model.ArtifactStoreSpec{
			Provider: "filesystem",
			RootDir:  filepath.Join(stateDir, "artifacts"),
		},
		Cache: model.CacheSpec{
			RootDir: cacheRoot,
		},
		Phase1: model.Phase1Spec{
			SourcePath: phase1Path,
			ExpectedSHA256ByPower: map[string]string{
				strconv.Itoa(power): phase1SHA,
			},
		},
		Circuits: []model.CircuitSpec{circuit},
	}
	if err := os.WriteFile(cfgPath, mustMarshalJSON(t, cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// Arrange: build a real transcript and derive keys from it.
	bundleDir := filepath.Join(stateDir, "public")
	originPath := filepath.Join(bundleDir, "circuits", circuit.ID, "origin.ph2")
	contribPath := filepath.Join(bundleDir, "circuits", circuit.ID, "contributions", "001.ph2")
	finalPath := filepath.Join(bundleDir, "circuits", circuit.ID, "final.ph2")
	pkPath := filepath.Join(bundleDir, "circuits", circuit.ID, "keys", circuit.ID+".pk")
	vkPath := filepath.Join(bundleDir, "circuits", circuit.ID, "keys", circuit.ID+".vk")
	if err := os.MkdirAll(filepath.Dir(contribPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pkPath), 0o755); err != nil {
		t.Fatal(err)
	}

	engine := phase2.NewEngine()
	if _, err := engine.InitializeAndCapture(loaded.Phase1.SourcePath, compileRes.R1CSPath, originPath); err != nil {
		t.Fatalf("initialize origin: %v", err)
	}
	if err := engine.Contribute(originPath, contribPath); err != nil {
		t.Fatalf("contribute: %v", err)
	}
	if err := artifacts.CopyFile(contribPath, finalPath); err != nil {
		t.Fatalf("copy final artifact: %v", err)
	}
	if err := finalizepkg.ExportKeysFromPhase2(
		loaded.Phase1.SourcePath,
		compileRes.R1CSPath,
		[]string{contribPath},
		pkPath,
		vkPath,
	); err != nil {
		t.Fatalf("export keys: %v", err)
	}

	// Arrange: create the public bundle manifest expected by the verifier.
	configSnapshotPath := filepath.Join(bundleDir, "config.snapshot.json")
	configSnapshotBytes, err := json.MarshalIndent(loaded, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configSnapshotPath, configSnapshotBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	originHash := mustDigest(t, originPath)
	contribHash := mustDigest(t, contribPath)
	pkHash := mustDigest(t, pkPath)
	vkHash := mustDigest(t, vkPath)
	configHash := mustDigest(t, configSnapshotPath)
	specBytes, err := json.Marshal(circuit)
	if err != nil {
		t.Fatal(err)
	}

	manifest := Manifest{
		Version:            ManifestVersion,
		CeremonyID:         loaded.ID,
		GeneratedAt:        "2026-01-01T00:00:00Z",
		ConfigSnapshotPath: "config.snapshot.json",
		ConfigSnapshotSHA:  configHash,
		Participants:       []string{"alice"},
		TotalContributions: 1,
		Circuits: []CircuitManifest{
			{
				CircuitID:          circuit.ID,
				CircuitSpecJSON:    string(specBytes),
				CircuitSpecSHA256:  shaHex(specBytes),
				Phase1SHA256:       phase1SHA,
				DerivedPhase1Power: power,
				R1CSSHA256:         mustDigest(t, compileRes.R1CSPath),
				OriginPhase2Path:   filepath.ToSlash(filepath.Join("circuits", circuit.ID, "origin.ph2")),
				OriginPhase2SHA:    originHash,
				FinalPhase2Path:    filepath.ToSlash(filepath.Join("circuits", circuit.ID, "final.ph2")),
				FinalPhase2SHA:     contribHash,
				PKPath:             filepath.ToSlash(filepath.Join("circuits", circuit.ID, "keys", circuit.ID+".pk")),
				PKSHA:              pkHash,
				VKPath:             filepath.ToSlash(filepath.Join("circuits", circuit.ID, "keys", circuit.ID+".vk")),
				VKSHA:              vkHash,
				ContributionCount:  1,
				Contributions: []ContributionManifest{
					{
						Index:          1,
						Participant:    "alice",
						CreatedAt:      "2026-01-01T00:00:01Z",
						InputPath:      filepath.ToSlash(filepath.Join("circuits", circuit.ID, "origin.ph2")),
						InputSHA256:    originHash,
						OutputPath:     filepath.ToSlash(filepath.Join("circuits", circuit.ID, "contributions", "001.ph2")),
						OutputSHA256:   contribHash,
						TranscriptHash: transcriptHash(originHash, contribHash, "alice"),
					},
				},
			},
		},
	}

	bundleRoot, err := ComputeBundleRootSHA256(bundleDir, "manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest.BundleRootSHA256 = bundleRoot

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Act + assert: deep verification should succeed against the real transcript.
	if err := Verify(bundleDir, loaded, VerifyOptions{}); err != nil {
		t.Fatalf("verify deep: %v", err)
	}
}

func writePhase1(t *testing.T, path string, n uint64) {
	t.Helper()

	p1 := mpcsetup.NewPhase1(n)
	p1.Initialize(n)
	p1.Contribute()

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if _, err := p1.WriteTo(f); err != nil {
		t.Fatal(err)
	}
}

func sha256FileHex(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mustMarshalJSON(t *testing.T, cfg *model.CeremonyConfig) []byte {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
