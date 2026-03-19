package publicbundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/cache"
	finalizepkg "github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/finalize"
	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/model"
	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/phase1"
	provercompile "github.com/testinprod-io/privacy-boost-ceremony/prover/compile"
)

// Verify performs the highest-assurance offline verification for one exported public bundle.
func Verify(bundleDir string, cfg *model.CeremonyConfig, opts VerifyOptions) error {
	// Require a config so the verifier can locate and (re)build the same cached inputs
	// (R1CS + Phase1) that the coordinator would use for this circuit set.
	if cfg == nil {
		return fmt.Errorf("ceremony config is required for verify")
	}

	// Step 1: verify the bundle's internal integrity first.
	if err := VerifyIntegrity(bundleDir, opts); err != nil {
		return err
	}

	// Step 2: load the manifest so we can iterate per-circuit and re-derive keys.
	manifestPath := filepath.Join(bundleDir, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}

	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}

	// Step 3: initialize cache paths used for compile + phase1 reuse.
	cm := cache.New(cfg.StateDir, cfg.Cache.RootDir)
	if err := cm.Ensure(); err != nil {
		return err
	}

	// Step 4: prepare phase1 resolver (ptau fetch + ptau->ph1 conversion).
	ph1 := phase1.New()

	// Step 5: per-circuit deep verification.
	logf(opts,
		"[ceremony][verify-public][keys] start circuits=%d cacheRoot=%s\n",
		len(manifest.Circuits),
		cfg.Cache.RootDir,
	)
	for _, circuit := range manifest.Circuits {
		if err := verifyCircuitKeys(bundleDir, cfg, cm, ph1, circuit, opts); err != nil {
			return fmt.Errorf("verify circuit %s: %w", circuit.CircuitID, err)
		}
	}

	logf(opts, "[ceremony][verify-public][keys] complete\n")
	return nil
}

// verifyCircuitKeys performs the "key re-derivation" portion of Verify for one circuit.
func verifyCircuitKeys(
	bundleDir string,
	cfg *model.CeremonyConfig,
	cm *cache.Manager,
	ph1 *phase1.Service,
	circuit CircuitManifest,
	opts VerifyOptions,
) error {
	logf(opts, "[ceremony][verify-public][keys] circuit_start id=%s\n", circuit.CircuitID)

	// Load the circuit spec JSON from the manifest.
	if strings.TrimSpace(circuit.CircuitSpecJSON) == "" {
		return fmt.Errorf("missing circuit spec json")
	}

	var spec model.CircuitSpec
	if err := json.Unmarshal([]byte(circuit.CircuitSpecJSON), &spec); err != nil {
		return fmt.Errorf("parse circuit spec json: %w", err)
	}

	// Ensure we have a stable circuit ID.
	if strings.TrimSpace(spec.ID) == "" {
		spec.ID = circuit.CircuitID
	}

	// Compile (or reuse cached) R1CS for this exact spec.
	compileRes, err := provercompile.CompileWithMetadata(toCompileSpec(spec), cm.CompileDir())
	if err != nil {
		return fmt.Errorf("compile r1cs: %w", err)
	}
	logf(opts,
		"[ceremony][verify-public][keys] r1cs_ready id=%s requiredPhase1Power=%d constraints=%d domain=%d\n",
		circuit.CircuitID,
		compileRes.RequiredPhase1Power,
		compileRes.NbConstraints,
		compileRes.DomainSize,
	)
	if compileRes.RequiredPhase1Power != circuit.DerivedPhase1Power {
		return fmt.Errorf(
			"derived phase1 power mismatch: manifest=%d computed=%d",
			circuit.DerivedPhase1Power,
			compileRes.RequiredPhase1Power,
		)
	}

	r1csDigest, err := digest(compileRes.R1CSPath)
	if err != nil {
		return err
	}
	if r1csDigest != circuit.R1CSSHA256 {
		return fmt.Errorf("r1cs hash mismatch after compilation")
	}

	// Resolve the Phase1 artifact for the derived power.
	_, phase1Path, err := ph1.EnsurePhase1(
		cfg.Phase1,
		cm.Phase1Dir(),
		cm.DeserializeDir(),
		compileRes.RequiredPhase1Power,
	)
	if err != nil {
		return fmt.Errorf("resolve phase1: %w", err)
	}
	logf(opts,
		"[ceremony][verify-public][keys] phase1_ready id=%s power=%d phase1Path=%s\n",
		circuit.CircuitID,
		compileRes.RequiredPhase1Power,
		phase1Path,
	)
	phase1Digest, err := digest(phase1Path)
	if err != nil {
		return err
	}
	if phase1Digest != circuit.Phase1SHA256 {
		return fmt.Errorf("phase1 hash mismatch after resolution")
	}

	// Extract the ordered Phase2 transcript outputs from the public manifest.
	if len(circuit.Contributions) == 0 {
		return fmt.Errorf("no contributions recorded in public manifest")
	}

	contribOuts := make([]string, 0, len(circuit.Contributions))
	for i, rec := range circuit.Contributions {
		p, err := absBundlePath(bundleDir, rec.OutputPath)
		if err != nil {
			return err
		}
		if rec.Index != i+1 {
			return fmt.Errorf("contribution index mismatch: got=%d expected=%d", rec.Index, i+1)
		}
		contribOuts = append(contribOuts, p)
	}

	// Export keys into a temporary directory so verification is side-effect free.
	tmpDir, err := os.MkdirTemp("", "ceremony_verify_*")
	if err != nil {
		return err
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			logf(
				opts,
				"[ceremony][verify-public][keys] cleanup_tmpdir_failed tmpDir=%s err=%v\n",
				tmpDir,
				rmErr,
			)
		}
	}()

	pkTmp := filepath.Join(tmpDir, circuit.CircuitID+".pk")
	vkTmp := filepath.Join(tmpDir, circuit.CircuitID+".vk")

	// Cryptographically derive pk/vk from (phase1, R1CS, transcript outputs).
	if err := finalizepkg.ExportKeysFromPhase2(
		phase1Path,
		compileRes.R1CSPath,
		contribOuts,
		pkTmp,
		vkTmp,
	); err != nil {
		return fmt.Errorf("export keys from transcript: %w", err)
	}
	logf(opts,
		"[ceremony][verify-public][keys] keys_derived id=%s pk=%s vk=%s\n",
		circuit.CircuitID,
		pkTmp,
		vkTmp,
	)

	// Hash the derived keys and compare to the public manifest commitments.
	pkDigest, err := digest(pkTmp)
	if err != nil {
		return err
	}
	vkDigest, err := digest(vkTmp)
	if err != nil {
		return err
	}
	if pkDigest != circuit.PKSHA {
		return fmt.Errorf("pk hash mismatch after key derivation")
	}
	if vkDigest != circuit.VKSHA {
		return fmt.Errorf("vk hash mismatch after key derivation")
	}

	logf(opts, "[ceremony][verify-public][keys] circuit_ok id=%s\n", circuit.CircuitID)
	return nil
}

// toCompileSpec adapts ceremony circuit specs into the prover compiler's circuit model.
func toCompileSpec(c model.CircuitSpec) provercompile.CircuitSpec {
	return provercompile.CircuitSpec{
		ID:           c.ID,
		Name:         c.Name,
		Type:         provercompile.CircuitType(c.Type),
		BatchSize:    c.BatchSize,
		MaxInputs:    c.MaxInputs,
		MaxInPerTx:   c.MaxInPerTx,
		MaxOutPerTx:  c.MaxOutPerTx,
		Depth:        c.Depth,
		AuthDepth:    c.AuthDepth,
		MaxTrees:     c.MaxTrees,
		MaxAuthTrees: c.MaxAuthTrees,
		MaxFeeTokens: c.MaxFeeTokens,
	}
}
