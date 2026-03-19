// Package phase1 resolves, verifies, and converts phase1 artifacts for setup.
package phase1

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/consensys/gnark/backend/groth16/bn254/mpcsetup"
	deserializer "github.com/worldcoin/ptau-deserializer/deserialize"

	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/artifacts"
	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/model"
)

type Service struct{}

// New creates a phase1 service.
func New() *Service {
	return &Service{}
}

// EnsurePhase1 ensures a gnark-compatible `.ph1` exists for the given power and returns both
// the resolved source path and the final `.ph1` path.
func (s *Service) EnsurePhase1(
	spec model.Phase1Spec,
	ptauCacheDir string,
	deserializeDir string,
	power int,
) (sourcePath string, phase1Path string, err error) {
	sourcePath, err = s.EnsurePtau(spec, ptauCacheDir, power)
	if err != nil {
		return "", "", err
	}

	// If the resolved source is already a gnark-compatible phase1 binary, no conversion is needed.
	if strings.HasSuffix(strings.ToLower(sourcePath), ".ph1") {
		return sourcePath, sourcePath, nil
	}

	// Otherwise convert from snarkjs `.ptau` into gnark `.ph1` and return the cached output path.
	phase1Path, err = s.ConvertPtauToPhase1(sourcePath, deserializeDir)
	if err != nil {
		return "", "", err
	}
	return sourcePath, phase1Path, nil
}

// EnsurePtau resolves a power-matched phase1 source and returns local cached ptau/ph1 path.
func (s *Service) EnsurePtau(spec model.Phase1Spec, cacheDir string, power int) (string, error) {
	// Derive canonical file name and resolve power-aware source tokens.
	name := fmt.Sprintf("powersOfTau28_hez_final_%02d.ptau", power)
	sourcePath := resolvePowerToken(spec.SourcePath, power)
	sourceURL := resolvePowerToken(spec.SourceURL, power)

	// Expected checksum for this power.
	expectedHash, err := expectedHashForPower(spec, power)
	if err != nil {
		return "", err
	}

	// If source is already .ph1, verify and return directly.
	if strings.HasSuffix(strings.ToLower(sourcePath), ".ph1") {
		if err := verifyHash(sourcePath, expectedHash); err != nil {
			return "", err
		}
		return sourcePath, nil
	}

	// Return cached ptau when present and valid.
	dst := filepath.Join(cacheDir, name)

	// Reuse cache when checksum matches; otherwise remove stale/corrupt cache and refresh.
	if _, err := os.Stat(dst); err == nil {
		if err := verifyHash(dst, expectedHash); err != nil {
			_ = os.Remove(dst)
		} else {
			return dst, nil
		}
	}

	// Copy local source into cache for stable naming.
	if sourcePath != "" {
		if err := artifacts.CopyFile(sourcePath, dst); err != nil {
			return "", err
		}
		if err := verifyHash(dst, expectedHash); err != nil {
			_ = os.Remove(dst)
			return "", err
		}
		return dst, nil
	}

	// Download from remote URL into cache.
	if sourceURL == "" {
		return "", fmt.Errorf("phase1 sourceURL or sourcePath required")
	}

	// Fetch remote artifact.
	resp, err := http.Get(sourceURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download ptau status %d", resp.StatusCode)
	}

	// Stream remote artifact atomically into cache destination.
	if err := artifacts.WriteReaderToFileAtomic(resp.Body, dst); err != nil {
		return "", err
	}
	if err := verifyHash(dst, expectedHash); err != nil {
		_ = os.Remove(dst)
		return "", err
	}

	return dst, nil
}

var ptauPowerPattern = regexp.MustCompile(`powersOfTau28_hez_final_\d+\.ptau`)

// resolvePowerToken replaces {power} placeholders or rewrites known ptau filename suffixes.
func resolvePowerToken(input string, power int) string {
	// Empty input yields empty output.
	if input == "" {
		return ""
	}

	// Substitute {power} token with zero-padded power.
	if strings.Contains(input, "{power}") {
		return strings.ReplaceAll(input, "{power}", fmt.Sprintf("%02d", power))
	}

	// Rewrite known ptau filename suffix to target power.
	return ptauPowerPattern.ReplaceAllString(input, fmt.Sprintf("powersOfTau28_hez_final_%02d.ptau", power))
}

// expectedHashForPower requires explicit per-power hash entries.
func expectedHashForPower(spec model.Phase1Spec, power int) (string, error) {
	// Check per-power hash map when present.
	if len(spec.ExpectedSHA256ByPower) == 0 {
		return "", fmt.Errorf("phase1.expectedSha256ByPower is required")
	}
	if v := spec.ExpectedSHA256ByPower[fmt.Sprintf("%d", power)]; v != "" {
		return v, nil
	}

	return "", fmt.Errorf("missing phase1.expectedSha256ByPower entry for power %d", power)
}

// ConvertPtauToPhase1 deserializes snarkjs ptau into gnark-compatible phase1 format.
func (s *Service) ConvertPtauToPhase1(ptauPath, outDir string) (string, error) {
	// Ensure output dir exists.
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}

	// Reuse existing .ph1 when present.
	out := filepath.Join(outDir, filepath.Base(ptauPath)+".ph1")
	if _, err := os.Stat(out); err == nil {
		if err := validatePhase1File(out); err == nil {
			return out, nil
		}
		// Replace stale/incompatible cache entries.
		_ = os.Remove(out)
	}

	// Stream-convert and persist phase1 from ptau. This supports both older and
	// current ptau-deserializer APIs.
	ptauFile, err := deserializer.InitPtau(ptauPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = ptauFile.Close() }()
	if err := deserializer.WritePhase1FromPtauFile(ptauFile, out); err != nil {
		return "", err
	}

	// Fail early if the generated cache entry cannot be loaded by gnark setup.
	if err := validatePhase1File(out); err != nil {
		_ = os.Remove(out)
		return "", err
	}
	return out, nil
}

// validatePhase1File ensures the cached phase1 artifact can be decoded by gnark.
func validatePhase1File(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var ph1 mpcsetup.Phase1
	if _, err := ph1.ReadFrom(f); err != nil {
		return fmt.Errorf("invalid phase1 artifact %s: %w", path, err)
	}
	return nil
}

// verifyHash compares file hash with expected digest.
func verifyHash(path, expected string) error {
	// Reject empty expected hash; caller must provide explicit per-power digest.
	if expected == "" {
		return fmt.Errorf("expected sha256 is required")
	}

	sum, err := fileSHA256(path)
	if err != nil {
		return err
	}

	// Report mismatch.
	if sum != expected {
		return fmt.Errorf("sha256 mismatch: got %s expected %s", sum, expected)
	}
	return nil
}

// fileSHA256 computes lowercase hex sha256 digest for a file path.
func fileSHA256(path string) (string, error) {
	// Open and stream into hash.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Hash file contents.
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
