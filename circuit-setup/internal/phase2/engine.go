// Package phase2 provides a gnark-native phase2 engine implementation.
package phase2

import (
	"fmt"
	"os"

	"github.com/consensys/gnark/backend/groth16/bn254/mpcsetup"
	cs_bn254 "github.com/consensys/gnark/constraint/bn254"
)

type Engine struct{}

// NewEngine returns default gnark-native phase2 engine.
func NewEngine() *Engine {
	// Return the computed result to the caller.
	return &Engine{}
}

// InitializeAndCapture creates the origin artifact and returns setup evaluations for finalize cache.
func (e *Engine) InitializeAndCapture(phase1Path, r1csPath, outPath string) (*mpcsetup.Phase2Evaluations, error) {
	// Load phase1 artifact and seal commons for phase2.
	phase1File, err := os.Open(phase1Path)
	if err != nil {
		return nil, err
	}
	defer phase1File.Close()

	// Declare local variables used in this stage.
	var ph1 mpcsetup.Phase1
	if _, err := ph1.ReadFrom(phase1File); err != nil {
		return nil, err
	}

	commons := ph1.Seal([]byte("privacy-boost-phase1"))

	// Load r1cs constraint system.
	r1csFile, err := os.Open(r1csPath)
	if err != nil {
		return nil, err
	}
	defer r1csFile.Close()

	var r1cs cs_bn254.R1CS
	if _, err := r1cs.ReadFrom(r1csFile); err != nil {
		return nil, err
	}

	// Verify phase1 tau domain meets circuit size requirements.
	requiredN := nextPow2DomainSize(uint64(r1cs.GetNbConstraints()))
	if len(commons.G1.Tau) < int((requiredN*2)-1) {
		return nil, fmt.Errorf(
			"phase1 capacity insufficient for r1cs constraints: tau=%d required_min=%d",
			len(commons.G1.Tau),
			(requiredN*2)-1,
		)
	}

	// Initialize phase2 origin and capture evaluations for finalize cache.
	var ph2 mpcsetup.Phase2
	evals := ph2.Initialize(&r1cs, &commons)

	// Persist phase2 artifact to disk.
	out, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	if _, err := ph2.WriteTo(out); err != nil {
		return nil, err
	}

	return &evals, nil
}

// Contribute applies one random contribution to an existing phase2 artifact.
func (e *Engine) Contribute(inputPath, outputPath string) error {
	// Read current phase2 artifact from input.
	in, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer in.Close()

	// Declare local variables used in this stage.
	var ph2 mpcsetup.Phase2
	if _, err := ph2.ReadFrom(in); err != nil {
		return err
	}

	// Apply random contribution to produce the next artifact.
	ph2.Contribute()

	// Persist contributed artifact to output path.
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Serialize data to the current writer.
	_, err = ph2.WriteTo(out)
	return err
}

// Verify checks latest artifact is a valid transition from previous artifact.
func (e *Engine) Verify(latestPath, originPath string) error {
	// Open origin and latest phase2 artifacts.
	prevF, err := os.Open(originPath)
	if err != nil {
		return err
	}
	defer prevF.Close()

	// Open the required input file from disk.
	nextF, err := os.Open(latestPath)
	if err != nil {
		return err
	}
	defer nextF.Close()

	// Deserialize both artifacts.
	var prev, next mpcsetup.Phase2
	if _, err := prev.ReadFrom(prevF); err != nil {
		return err
	}

	// Check the operation result and return on error.
	if _, err := next.ReadFrom(nextF); err != nil {
		return err
	}

	// Return the computed result to the caller.
	return prev.Verify(&next)
}

// nextPow2DomainSize rounds constraints to the next power-of-two domain size.
func nextPow2DomainSize(n uint64) uint64 {
	// Evaluate the guard condition for this branch.
	if n <= 1 {
		return 1
	}

	// Double until size >= n.
	size := uint64(1)
	for size < n {
		size <<= 1
	}

	// Return the computed result to the caller.
	return size
}
