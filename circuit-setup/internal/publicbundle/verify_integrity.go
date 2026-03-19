package publicbundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// VerifyIntegrity performs deterministic offline integrity verification for one exported public bundle.
//
// It reconstructs hash/linkage invariants directly from local files and compares them with manifest
// commitments. Optional anchor verification extends these checks with Ethereum RPC validation when requested.
//
// This function does NOT re-derive proving/verifying keys from the transcript; use Verify for the full
// (deep) verification that includes cryptographic key derivation.
func VerifyIntegrity(bundleDir string, opts VerifyOptions) error {
	logf(opts,
		"[ceremony][verify-public][integrity] start bundleDir=%s requireAnchor=%t\n",
		bundleDir,
		opts.RequireAnchor,
	)

	// Load the canonical manifest first because every subsequent check
	// (artifact digests, transcript linkage, participant list) is derived from it.
	manifestPath := filepath.Join(bundleDir, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}

	// Decode the bundle schema and fail early on incompatible manifest versions
	// so auditors do not silently validate with the wrong rules.
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if manifest.Version != ManifestVersion {
		return fmt.Errorf("unsupported manifest version %d", manifest.Version)
	}
	logf(opts,
		"[ceremony][verify-public][integrity] manifest_loaded version=%d circuits=%d "+
			"totalContributions=%d participants=%d\n",
		manifest.Version,
		len(manifest.Circuits),
		manifest.TotalContributions,
		len(manifest.Participants),
	)

	// Require config snapshot commitments because verifier binds bundle integrity
	// to the exact ceremony config captured at export time.
	if manifest.ConfigSnapshotPath == "" || manifest.ConfigSnapshotSHA == "" {
		return fmt.Errorf("missing config snapshot binding fields")
	}

	// Require bundle root commitment so anchor-mode verification has one compact
	// value to check against onchain metadata.
	if manifest.BundleRootSHA256 == "" {
		return fmt.Errorf("missing bundle root hash")
	}

	// Recompute config snapshot hash from exported file bytes.
	configPath, err := absBundlePath(bundleDir, manifest.ConfigSnapshotPath)
	if err != nil {
		return err
	}
	configDigest, err := digest(configPath)
	if err != nil {
		return err
	}
	if configDigest != manifest.ConfigSnapshotSHA {
		return fmt.Errorf("config snapshot hash mismatch")
	}
	logf(opts, "[ceremony][verify-public][integrity] config_snapshot_ok\n")

	// Recompute deterministic bundle root from exported files excluding manifest.
	bundleRoot, err := ComputeBundleRootSHA256(bundleDir, "manifest.json")
	if err != nil {
		return err
	}
	if bundleRoot != manifest.BundleRootSHA256 {
		return fmt.Errorf("bundle root hash mismatch")
	}
	logf(opts, "[ceremony][verify-public][integrity] bundle_root_ok\n")

	// Perform optional onchain anchor checks only when caller explicitly enables
	// strict anchor mode.
	if opts.RequireAnchor {
		logf(opts, "[ceremony][verify-public][integrity] anchor_verify_start\n")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := VerifyEthereumAnchor(
			ctx,
			&manifest,
			opts.RPCURL,
			opts.AnchorChainID,
			opts.AnchorTxHash,
			opts.MinConfirmations,
		); err != nil {
			return err
		}
		logf(opts, "[ceremony][verify-public][integrity] anchor_verify_ok\n")
	}

	// Recompute participant and contribution aggregates from circuit contents
	// instead of trusting precomputed counters from manifest fields.
	seenParticipants := map[string]struct{}{}
	totalContributions := 0
	for _, circuit := range manifest.Circuits {
		logf(opts,
			"[ceremony][verify-public][integrity] circuit_start id=%s contributions=%d\n",
			circuit.CircuitID,
			len(circuit.Contributions),
		)
		if err := verifyCircuit(bundleDir, circuit); err != nil {
			return fmt.Errorf("verify circuit %s: %w", circuit.CircuitID, err)
		}
		totalContributions += len(circuit.Contributions)
		for _, rec := range circuit.Contributions {
			seenParticipants[rec.Participant] = struct{}{}
		}
		logf(opts, "[ceremony][verify-public][integrity] circuit_ok id=%s\n", circuit.CircuitID)
	}

	// Ensure ceremony-level contribution count matches reconstructed totals;
	// mismatch indicates omitted or duplicated contribution entries.
	if totalContributions != manifest.TotalContributions {
		return fmt.Errorf(
			"total contribution count mismatch: manifest=%d computed=%d",
			manifest.TotalContributions,
			totalContributions,
		)
	}

	// Compare sorted participant sets to guarantee published identifiers reflect
	// actual contribution records and are not manually edited.
	participants := make([]string, 0, len(seenParticipants))
	for participant := range seenParticipants {
		participants = append(participants, participant)
	}
	sort.Strings(participants)
	if len(participants) != len(manifest.Participants) {
		return fmt.Errorf(
			"participant list size mismatch: manifest=%d computed=%d",
			len(manifest.Participants),
			len(participants),
		)
	}
	for i := range participants {
		if participants[i] != manifest.Participants[i] {
			return fmt.Errorf(
				"participant list mismatch at %d: %s != %s",
				i,
				participants[i],
				manifest.Participants[i],
			)
		}
	}
	logf(opts, "[ceremony][verify-public][integrity] complete\n")
	return nil
}

// verifyCircuit validates one circuit transcript chain and key artifacts.
//
// The function enforces step ordering, input/output digest adjacency, transcript
// hash linkage, and final key/hash consistency for a single circuit subtree.
func verifyCircuit(bundleDir string, circuit CircuitManifest) error {
	// Circuit spec commitment must remain consistent with its JSON payload.
	if circuit.CircuitSpecJSON == "" || circuit.CircuitSpecSHA256 == "" {
		return fmt.Errorf("missing circuit spec commitment fields")
	}
	if circuit.Phase1SHA256 == "" || circuit.DerivedPhase1Power <= 0 || circuit.R1CSSHA256 == "" {
		return fmt.Errorf("missing setup commitment fields")
	}
	specHash := sha256.Sum256([]byte(circuit.CircuitSpecJSON))
	if hex.EncodeToString(specHash[:]) != circuit.CircuitSpecSHA256 {
		return fmt.Errorf("circuit spec hash mismatch")
	}

	// Start from the published origin artifact; this is the root hash that the
	// first contribution input must match.
	originPath, err := absBundlePath(bundleDir, circuit.OriginPhase2Path)
	if err != nil {
		return err
	}
	originHash, err := digest(originPath)
	if err != nil {
		return err
	}
	if originHash != circuit.OriginPhase2SHA {
		return fmt.Errorf("origin hash mismatch")
	}

	// Walk the ordered contribution chain and enforce adjacency constraints:
	// each step input must equal previous step output.
	currentDigest := originHash
	for i, rec := range circuit.Contributions {
		// Enforce explicit step numbering to detect malformed/reordered manifests
		// even before hash-chain errors are evaluated.
		if rec.Index != i+1 {
			return fmt.Errorf("contribution %d index mismatch: got=%d expected=%d", i+1, rec.Index, i+1)
		}

		inputPath, err := absBundlePath(bundleDir, rec.InputPath)
		if err != nil {
			return err
		}
		inputDigest, err := digest(inputPath)
		if err != nil {
			return err
		}
		if inputDigest != rec.InputSHA256 {
			return fmt.Errorf("contribution %d input hash mismatch", i+1)
		}
		if rec.InputSHA256 != currentDigest {
			return fmt.Errorf("contribution %d input does not match prior output", i+1)
		}

		// Validate the produced output artifact hash for this contribution.
		outputPath, err := absBundlePath(bundleDir, rec.OutputPath)
		if err != nil {
			return err
		}
		outputDigest, err := digest(outputPath)
		if err != nil {
			return err
		}
		if outputDigest != rec.OutputSHA256 {
			return fmt.Errorf("contribution %d output hash mismatch", i+1)
		}

		// Recompute transcript hash linkage from input/output digests plus participant
		// to detect tampering in transcript metadata fields.
		expectedTranscript := transcriptHash(rec.InputSHA256, rec.OutputSHA256, rec.Participant)
		if expectedTranscript != rec.TranscriptHash {
			return fmt.Errorf("contribution %d transcript hash mismatch", i+1)
		}

		// Move transcript head to this contribution output for next iteration.
		currentDigest = rec.OutputSHA256
	}

	// Verify the recorded count is consistent with concrete contribution entries.
	if len(circuit.Contributions) != circuit.ContributionCount {
		return fmt.Errorf("contribution count mismatch")
	}

	// Confirm final phase2 digest matches both the manifest field and the computed
	// transcript head from the ordered contribution chain.
	finalPath, err := absBundlePath(bundleDir, circuit.FinalPhase2Path)
	if err != nil {
		return err
	}
	finalDigest, err := digest(finalPath)
	if err != nil {
		return err
	}
	if finalDigest != circuit.FinalPhase2SHA {
		return fmt.Errorf("final phase2 hash mismatch")
	}
	if currentDigest != circuit.FinalPhase2SHA {
		return fmt.Errorf("final phase2 does not match transcript head")
	}

	// Validate proving and verifying key digests to ensure public key material
	// corresponds to the finalized artifacts in this bundle.
	pkPath, err := absBundlePath(bundleDir, circuit.PKPath)
	if err != nil {
		return err
	}
	pkDigest, err := digest(pkPath)
	if err != nil {
		return err
	}
	if pkDigest != circuit.PKSHA {
		return fmt.Errorf("pk hash mismatch")
	}

	vkPath, err := absBundlePath(bundleDir, circuit.VKPath)
	if err != nil {
		return err
	}
	vkDigest, err := digest(vkPath)
	if err != nil {
		return err
	}
	if vkDigest != circuit.VKSHA {
		return fmt.Errorf("vk hash mismatch")
	}

	return nil
}

// absBundlePath resolves one manifest-relative path to an absolute filesystem path.
//
// Converting through absolute paths prevents accidental dependence on the current
// process working directory during digest and existence checks.
func absBundlePath(bundleDir, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("empty bundle path")
	}

	// Resolve manifest-relative path against the bundle root.
	bundleAbs, err := filepath.Abs(bundleDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(bundleAbs, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}

	// Prevent path traversal: the resolved path must remain within bundle root.
	relToBundle, err := filepath.Rel(bundleAbs, abs)
	if err != nil {
		return "", err
	}
	if relToBundle == ".." || strings.HasPrefix(relToBundle, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes bundle directory: %s", rel)
	}
	return abs, nil
}

// ComputeBundleRootSHA256 computes a deterministic hash over all bundle files.
//
// Each file contributes one `relative_path:digest` entry, entries are sorted, and
// the final hash is computed from the newline-joined entry list.
func ComputeBundleRootSHA256(bundleDir string, skipRelPaths ...string) (string, error) {
	// Build skip set of slash-normalized relative paths to keep hashing stable
	// across OS-specific path separators.
	skip := map[string]struct{}{}
	for _, p := range skipRelPaths {
		skip[filepath.ToSlash(strings.TrimSpace(p))] = struct{}{}
	}

	// Collect path:digest entries for every file in the bundle.
	entries := make([]string, 0, 32)
	err := filepath.WalkDir(bundleDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(bundleDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, ok := skip[rel]; ok {
			return nil
		}
		fileDigest, err := digest(path)
		if err != nil {
			return err
		}
		entries = append(entries, fmt.Sprintf("%s:%s", rel, fileDigest))
		return nil
	})
	if err != nil {
		return "", err
	}

	// Canonicalize ordering before final hash so bundle root is deterministic.
	sort.Strings(entries)
	joined := strings.Join(entries, "\n")
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:]), nil
}

// digest computes SHA256 for one file via streaming IO.
//
// Streaming avoids loading large `.ph2` artifacts fully into memory and keeps
// verifier memory usage stable as artifact size grows.
func digest(path string) (string, error) {
	// Open and stream file data so verification scales to large phase2 artifacts
	// without loading full files in memory.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Hash the full content and return lowercase hex string expected by manifests.
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// transcriptHash recomputes one transcript linkage hash for a contribution step.
//
// The hash binds both artifact transition and participant identifier so manifest
// records cannot be swapped or reassigned without changing the digest.
func transcriptHash(prevDigest, nextDigest, participant string) string {
	// Bind both artifact transition and participant identity into one deterministic
	// hash so transcript records cannot be swapped without detection.
	sum := sha256.Sum256([]byte(prevDigest + "|" + nextDigest + "|" + participant))
	return hex.EncodeToString(sum[:])
}
