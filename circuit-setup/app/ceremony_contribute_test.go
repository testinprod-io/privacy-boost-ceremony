package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadInputArtifactSendsQueryAndHeaderAuth(t *testing.T) {
	const token = "session-token-123"
	const payload = "phase2-input"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("sessionToken"); got != token {
			t.Fatalf("expected sessionToken query param %q, got %q", token, got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("expected Authorization header %q, got %q", "Bearer "+token, got)
		}
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "input.ph2")
	err := downloadInputArtifact(
		srv.URL,
		"/v1/contribute/input/s1/s1-lease",
		token,
		dst,
		verbosity{quiet: true},
	)
	if err != nil {
		t.Fatalf("downloadInputArtifact returned error: %v", err)
	}

	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read downloaded artifact: %v", err)
	}
	if string(b) != payload {
		t.Fatalf("expected payload %q, got %q", payload, string(b))
	}
}

// TestWriteContributionReceiptKeepsLocalArtifactMetadata verifies that the
// contributor receipt now preserves the local artifact audit trail by default.
//
// The receipt is the contributor's durable record after a successful run. This
// test ensures the saved JSON includes both the original transcript fields and
// the richer local metadata such as file paths, digests, and byte sizes.
func TestWriteContributionReceiptKeepsLocalArtifactMetadata(t *testing.T) {
	stateDir := t.TempDir()
	receiptPath := filepath.Join(stateDir, "contribution-receipt.json")

	// Seed one contributed circuit result with representative local artifact
	// metadata so the test exercises the enriched receipt shape.
	results := []circuitResult{{
		id:                "s1",
		status:            "contributed",
		hashHex:           "hash-1",
		createdAt:         "2026-01-01T00:00:00Z",
		leaseID:           "lease-1",
		inputDownloadPath: "/v1/contribute/input/s1/lease-1",
		inputPath:         filepath.Join(stateDir, "input.ph2"),
		outputPath:        filepath.Join(stateDir, "output.ph2"),
		inputSHA256:       "input-sha",
		outputSHA256:      "output-sha",
		inputBytes:        11,
		outputBytes:       22,
	}}

	// Write the receipt and decode it through the public JSON surface so the
	// assertions check the serialized format contributors actually keep.
	if err := writeContributionReceipt(
		receiptPath,
		"alice",
		"https://coordinator.example",
		stateDir,
		results,
	); err != nil {
		t.Fatalf("writeContributionReceipt returned error: %v", err)
	}

	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}

	var receipt struct {
		Participant    string `json:"participant"`
		CoordinatorURL string `json:"coordinatorUrl"`
		StateDir       string `json:"stateDir"`
		GeneratedAt    string `json:"generatedAt"`
		Circuits       []struct {
			CircuitID         string `json:"circuitId"`
			Status            string `json:"status"`
			Hash              string `json:"hash"`
			CreatedAt         string `json:"createdAt"`
			LeaseID           string `json:"leaseId"`
			InputDownloadPath string `json:"inputDownloadPath"`
			InputPath         string `json:"inputPath"`
			InputSHA256       string `json:"inputSha256"`
			InputBytes        int64  `json:"inputBytes"`
			OutputPath        string `json:"outputPath"`
			OutputSHA256      string `json:"outputSha256"`
			OutputBytes       int64  `json:"outputBytes"`
		} `json:"circuits"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatalf("decode receipt json: %v", err)
	}

	if receipt.Participant != "alice" {
		t.Fatalf("expected participant alice, got %q", receipt.Participant)
	}
	if receipt.CoordinatorURL != "https://coordinator.example" {
		t.Fatalf("expected coordinator url preserved, got %q", receipt.CoordinatorURL)
	}
	if receipt.StateDir != stateDir {
		t.Fatalf("expected state dir %q, got %q", stateDir, receipt.StateDir)
	}
	if receipt.GeneratedAt == "" {
		t.Fatal("expected generatedAt to be populated")
	}
	if len(receipt.Circuits) != 1 {
		t.Fatalf("expected one circuit entry, got %d", len(receipt.Circuits))
	}

	entry := receipt.Circuits[0]
	if entry.CircuitID != "s1" || entry.LeaseID != "lease-1" {
		t.Fatalf("expected circuit metadata preserved, got %+v", entry)
	}
	if entry.InputSHA256 != "input-sha" || entry.OutputSHA256 != "output-sha" {
		t.Fatalf("expected artifact digests preserved, got %+v", entry)
	}
	if entry.InputBytes != 11 || entry.OutputBytes != 22 {
		t.Fatalf("expected artifact sizes preserved, got %+v", entry)
	}
}

// TestCleanupContributionArtifactsRemovesLocalFiles verifies that declining the
// receipt can clean up local input/output artifacts after a successful run.
//
// The contributor CLI now ties artifact retention to the same prompt that asks
// whether to save the richer receipt. This test protects the cleanup path that
// runs when the user answers "no".
func TestCleanupContributionArtifactsRemovesLocalFiles(t *testing.T) {
	stateDir := t.TempDir()
	inputPath := filepath.Join(stateDir, "input.ph2")
	outputPath := filepath.Join(stateDir, "output.ph2")

	// Materialize representative contribution artifacts so the helper can remove
	// the exact file set recorded in the circuit result.
	for _, path := range []string{inputPath, outputPath} {
		if err := os.WriteFile(path, []byte("artifact"), 0o644); err != nil {
			t.Fatalf("write test file %s: %v", path, err)
		}
	}

	// Run cleanup through the helper and then assert that both local files are gone.
	if err := cleanupContributionArtifacts([]circuitResult{{
		inputPath:  inputPath,
		outputPath: outputPath,
	}}); err != nil {
		t.Fatalf("cleanupContributionArtifacts returned error: %v", err)
	}

	for _, path := range []string{inputPath, outputPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, got err=%v", path, err)
		}
	}
}
