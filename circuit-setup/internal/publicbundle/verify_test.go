package publicbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestVerifyBundleSuccess validates that a correctly exported bundle passes verification.
func TestVerifyBundleSuccess(t *testing.T) {
	// Build deterministic fixture and ensure offline verifier accepts it.
	dir := t.TempDir()
	bundle := writeBundleFixture(t, dir)
	if err := VerifyIntegrity(bundle, VerifyOptions{}); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyBundleDetectsTamper ensures hash checks reject modified artifacts.
func TestVerifyBundleDetectsTamper(t *testing.T) {
	// Build fixture first, then mutate one artifact while keeping manifest unchanged.
	dir := t.TempDir()
	bundle := writeBundleFixture(t, dir)

	// Tamper one contribution artifact after manifest generation.
	tamperedPath := filepath.Join(bundle, "circuits", "c1", "contributions", "001.ph2")
	if err := os.WriteFile(tamperedPath, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyIntegrity(bundle, VerifyOptions{}); err == nil {
		t.Fatal("expected verify failure for tampered artifact")
	}
}

// TestVerifyBundleDetectsIndexMismatch ensures verifier enforces explicit contribution indexing.
func TestVerifyBundleDetectsIndexMismatch(t *testing.T) {
	dir := t.TempDir()
	bundle := writeBundleFixture(t, dir)

	manifestPath := filepath.Join(bundle, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Circuits[0].Contributions[0].Index = 9
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, updated, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyIntegrity(bundle, VerifyOptions{}); err == nil {
		t.Fatal("expected verify failure for contribution index mismatch")
	}
}

// TestVerifyBundleDetectsConfigSnapshotTamper ensures config binding is enforced.
func TestVerifyBundleDetectsConfigSnapshotTamper(t *testing.T) {
	dir := t.TempDir()
	bundle := writeBundleFixture(t, dir)
	if err := os.WriteFile(
		filepath.Join(bundle, "config.snapshot.json"),
		[]byte(`{"id":"evil"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := VerifyIntegrity(bundle, VerifyOptions{}); err == nil {
		t.Fatal("expected verify failure for config snapshot hash mismatch")
	}
}

// TestVerifyBundleRequireAnchorWithoutRPC validates strict anchor option input checks.
func TestVerifyBundleRequireAnchorWithoutRPC(t *testing.T) {
	// Build baseline fixture and run strict anchor mode without rpc-url to
	// verify option validation fails before network calls.
	dir := t.TempDir()
	bundle := writeBundleFixture(t, dir)
	if err := VerifyIntegrity(bundle, VerifyOptions{RequireAnchor: true}); err == nil {
		t.Fatal("expected verify failure when anchor is required without rpc url")
	}
}

// TestVerifyBundleRejectsPathTraversal ensures manifest-relative paths cannot escape bundle root.
func TestVerifyBundleRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	bundle := writeBundleFixture(t, dir)

	manifestPath := filepath.Join(bundle, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}

	// Craft an escaping path; verifier must reject before attempting any file IO outside bundle.
	manifest.Circuits[0].Contributions[0].OutputPath = "../../etc/passwd"
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, updated, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := VerifyIntegrity(bundle, VerifyOptions{}); err == nil {
		t.Fatal("expected verify failure for path traversal")
	}
}

// writeBundleFixture creates a minimal but complete public bundle for verifier tests.
func writeBundleFixture(t *testing.T, root string) string {
	t.Helper()

	// Create canonical public bundle directory layout expected by verifier.
	bundle := filepath.Join(root, "public")
	if err := os.MkdirAll(filepath.Join(bundle, "circuits", "c1", "contributions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundle, "circuits", "c1", "keys"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write deterministic artifact/key contents to produce stable digests.
	originPath := filepath.Join(bundle, "circuits", "c1", "origin.ph2")
	c1Path := filepath.Join(bundle, "circuits", "c1", "contributions", "001.ph2")
	c2Path := filepath.Join(bundle, "circuits", "c1", "contributions", "002.ph2")
	finalPath := filepath.Join(bundle, "circuits", "c1", "final.ph2")
	pkPath := filepath.Join(bundle, "circuits", "c1", "keys", "c1.pk")
	vkPath := filepath.Join(bundle, "circuits", "c1", "keys", "c1.vk")
	mustWrite(t, originPath, "origin")
	mustWrite(t, c1Path, "out-1")
	mustWrite(t, c2Path, "out-2")
	mustWrite(t, finalPath, "out-2")
	mustWrite(t, pkPath, "pk")
	mustWrite(t, vkPath, "vk")

	// Compute digests used by manifest and transcript hash construction.
	originHash := mustDigest(t, originPath)
	c1Hash := mustDigest(t, c1Path)
	c2Hash := mustDigest(t, c2Path)
	pkHash := mustDigest(t, pkPath)
	vkHash := mustDigest(t, vkPath)
	configPath := filepath.Join(bundle, "config.snapshot.json")
	mustWrite(t, configPath, `{"id":"test-ceremony"}`)
	configHash := mustDigest(t, configPath)
	specBytes, err := json.Marshal(map[string]any{"id": "c1", "type": "deposit", "depth": 4, "batchSize": 2})
	if err != nil {
		t.Fatal(err)
	}
	specHash := shaHex(specBytes)

	// Compose manifest with consistent chain: origin -> c1 -> c2 -> final.
	manifest := Manifest{
		Version:            ManifestVersion,
		CeremonyID:         "test-ceremony",
		GeneratedAt:        "2026-01-01T00:00:00Z",
		ConfigSnapshotPath: "config.snapshot.json",
		ConfigSnapshotSHA:  configHash,
		Participants:       []string{"alice", "bob"},
		TotalContributions: 2,
		Circuits: []CircuitManifest{
			{
				CircuitID:          "c1",
				CircuitSpecJSON:    string(specBytes),
				CircuitSpecSHA256:  specHash,
				Phase1SHA256:       "phase1-digest",
				DerivedPhase1Power: 1,
				R1CSSHA256:         "r1cs-digest",
				OriginPhase2Path:   "circuits/c1/origin.ph2",
				OriginPhase2SHA:    originHash,
				FinalPhase2Path:    "circuits/c1/final.ph2",
				FinalPhase2SHA:     c2Hash,
				PKPath:             "circuits/c1/keys/c1.pk",
				PKSHA:              pkHash,
				VKPath:             "circuits/c1/keys/c1.vk",
				VKSHA:              vkHash,
				ContributionCount:  2,
				Contributions: []ContributionManifest{
					{
						Index:          1,
						Participant:    "alice",
						CreatedAt:      "2026-01-01T00:00:01Z",
						InputPath:      "circuits/c1/origin.ph2",
						InputSHA256:    originHash,
						OutputPath:     "circuits/c1/contributions/001.ph2",
						OutputSHA256:   c1Hash,
						TranscriptHash: transcriptHash(originHash, c1Hash, "alice"),
					},
					{
						Index:          2,
						Participant:    "bob",
						CreatedAt:      "2026-01-01T00:00:02Z",
						InputPath:      "circuits/c1/contributions/001.ph2",
						InputSHA256:    c1Hash,
						OutputPath:     "circuits/c1/contributions/002.ph2",
						OutputSHA256:   c2Hash,
						TranscriptHash: transcriptHash(c1Hash, c2Hash, "bob"),
					},
				},
			},
		},
	}

	// Compute root hash after all bundle files except manifest are created so
	// fixture root commitment mirrors production export behavior.
	bundleRoot, err := ComputeBundleRootSHA256(bundle, "manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest.BundleRootSHA256 = bundleRoot

	// Persist manifest as verifier entrypoint file.
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle
}

// mustWrite writes fixture file content and fails the current test on error.
func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustDigest computes SHA256 for fixture files and fails the test on error.
func mustDigest(t *testing.T, path string) string {
	t.Helper()
	d, err := digest(path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// shaHex returns lowercase sha256 hex for inline JSON fixture payloads.
func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
