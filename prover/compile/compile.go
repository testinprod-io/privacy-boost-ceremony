package compile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"

	appcircuits "github.com/testinprod-io/privacy-boost-protocol/frontend"
)

type CircuitType string

const (
	CircuitTypeEpoch   CircuitType = "epoch"
	CircuitTypeDeposit CircuitType = "deposit"
	CircuitTypeForced  CircuitType = "forced"
)

type CircuitSpec struct {
	ID           string
	Name         string
	Type         CircuitType
	BatchSize    int
	MaxInputs    int
	MaxInPerTx   int
	MaxOutPerTx  int
	Depth        int
	AuthDepth    int
	MaxTrees     int
	MaxAuthTrees int
	MaxFeeTokens int
}

type CompileResult struct {
	R1CSPath            string
	NbConstraints       int
	DomainSize          uint64
	RequiredPhase1Power int
}

const compileCacheVersion = 1

type compileCacheMetadata struct {
	CacheVersion        int    `json:"cacheVersion"`
	SpecHash            string `json:"specHash"`
	NbConstraints       int    `json:"nbConstraints"`
	DomainSize          uint64 `json:"domainSize"`
	RequiredPhase1Power int    `json:"requiredPhase1Power"`
}

func CompileWithMetadata(spec CircuitSpec, outDir string) (*CompileResult, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	out := filepath.Join(outDir, fmt.Sprintf("%s.r1cs", spec.ID))
	metaPath := out + ".meta.json"
	specHash, err := compileSpecHash(spec)
	if err != nil {
		return nil, err
	}

	// Reuse existing R1CS and metadata when the circuit spec fingerprint matches.
	if meta, err := loadCompileMetadata(metaPath); err == nil {
		if meta.CacheVersion == compileCacheVersion && meta.SpecHash == specHash {
			if _, err := os.Stat(out); err == nil {
				return &CompileResult{
					R1CSPath:            out,
					NbConstraints:       meta.NbConstraints,
					DomainSize:          meta.DomainSize,
					RequiredPhase1Power: meta.RequiredPhase1Power,
				}, nil
			}
		}
	}

	c, err := newCircuit(spec)
	if err != nil {
		return nil, err
	}
	ccs, err := frontend.Compile(fr.Modulus(), r1cs.NewBuilder, c)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", spec.ID, err)
	}
	f, err := os.Create(out)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := ccs.WriteTo(f); err != nil {
		return nil, err
	}
	nb := ccs.GetNbConstraints()
	domain := domainSize(uint64(nb))
	power := int(log2Pow2(domain))

	if err := writeCompileMetadata(metaPath, compileCacheMetadata{
		CacheVersion:        compileCacheVersion,
		SpecHash:            specHash,
		NbConstraints:       nb,
		DomainSize:          domain,
		RequiredPhase1Power: power,
	}); err != nil {
		return nil, err
	}

	return &CompileResult{
		R1CSPath:            out,
		NbConstraints:       nb,
		DomainSize:          domain,
		RequiredPhase1Power: power,
	}, nil
}

func compileSpecHash(spec CircuitSpec) (string, error) {
	b, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func loadCompileMetadata(path string) (*compileCacheMetadata, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta compileCacheMetadata
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func writeCompileMetadata(path string, meta compileCacheMetadata) error {
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func newCircuit(spec CircuitSpec) (frontend.Circuit, error) {
	switch spec.Type {
	case CircuitTypeEpoch:
		maxFee := spec.MaxFeeTokens
		if maxFee == 0 {
			maxFee = spec.BatchSize
		}
		return appcircuits.NewEpochCircuit(spec.BatchSize, spec.MaxInPerTx, spec.MaxOutPerTx, spec.Depth, spec.AuthDepth, maxFee, spec.MaxTrees, spec.MaxAuthTrees), nil
	case CircuitTypeDeposit:
		return appcircuits.NewDepositEpochCircuit(spec.BatchSize, spec.Depth, spec.MaxTrees), nil
	case CircuitTypeForced:
		return appcircuits.NewForcedWithdrawCircuit(spec.MaxInputs, spec.Depth, spec.AuthDepth, spec.MaxTrees, spec.MaxAuthTrees), nil
	default:
		return nil, fmt.Errorf("unsupported circuit type: %s", spec.Type)
	}
}

func domainSize(n uint64) uint64 {
	if n <= 1 {
		return 1
	}
	size := uint64(1)
	for size < n {
		size <<= 1
	}
	return size
}

func log2Pow2(n uint64) uint64 {
	var p uint64
	for n > 1 {
		n >>= 1
		p++
	}
	return p
}
