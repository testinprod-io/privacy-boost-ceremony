// Package finalize exports Groth16 proving and verifying keys from final phase2 artifacts.
package finalize

import (
	"fmt"
	"os"

	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/groth16/bn254/mpcsetup"
	cs_bn254 "github.com/consensys/gnark/constraint/bn254"
)

// ExportKeysFromPhase2 verifies contribution chain and writes resulting pk/vk files.
func ExportKeysFromPhase2(
	phase1Path,
	r1csPath string,
	contributionPhase2Paths []string,
	pkOutPath,
	vkOutPath string,
) error {
	// Load phase1 artifact and seal commons.
	phase1File, err := os.Open(phase1Path)
	if err != nil {
		return err
	}
	defer phase1File.Close()

	// Declare local variables used in this stage.
	var ph1 mpcsetup.Phase1
	if _, err := ph1.ReadFrom(phase1File); err != nil {
		return err
	}

	// Compute values for the next processing step.
	commons := ph1.Seal([]byte("privacy-boost-phase1"))

	// Load r1cs constraint system.
	r1csFile, err := os.Open(r1csPath)
	if err != nil {
		return err
	}
	defer r1csFile.Close()

	// Declare local variables used in this stage.
	var r1cs cs_bn254.R1CS
	if _, err := r1cs.ReadFrom(r1csFile); err != nil {
		return err
	}

	// Deserialize all contribution artifacts in order.
	contribs := make([]*mpcsetup.Phase2, 0, len(contributionPhase2Paths))
	for _, phase2Path := range contributionPhase2Paths {
		phase2File, err := os.Open(phase2Path)
		if err != nil {
			return err
		}

		// Compute values for the next processing step.
		p2 := new(mpcsetup.Phase2)
		if _, err := p2.ReadFrom(phase2File); err != nil {
			phase2File.Close()
			return err
		}
		// Close the file handle after use.
		phase2File.Close()

		// Append the item to the accumulated collection.
		contribs = append(contribs, p2)
	}

	// Verify chain and extract proving/verifying keys.
	pk, vk, err := mpcsetup.VerifyPhase2(&r1cs, &commons, nil, contribs...)
	if err != nil {
		return err
	}

	// Return the computed result to the caller.
	return writeKeys(pk, vk, pkOutPath, vkOutPath)
}

// ExportKeysFromPhase2WithCachedEvaluations uses setup-time cached evaluations to skip re-initialize.
func ExportKeysFromPhase2WithCachedEvaluations(
	phase1Path,
	originPhase2Path string,
	contributionPhase2Paths []string,
	evals *mpcsetup.Phase2Evaluations,
	pkOutPath,
	vkOutPath string,
) error {
	// Evaluate the guard condition for this branch.
	if evals == nil {
		return fmt.Errorf("cached evaluations are required")
	}

	// Load phase1 artifact and seal commons.
	phase1File, err := os.Open(phase1Path)
	if err != nil {
		return err
	}
	defer phase1File.Close()

	// Declare local variables used in this stage.
	var ph1 mpcsetup.Phase1
	if _, err := ph1.ReadFrom(phase1File); err != nil {
		return err
	}

	// Compute values for the next processing step.
	commons := ph1.Seal([]byte("privacy-boost-phase1"))

	// Load origin phase2 and walk contribution chain.
	origin, err := readPhase2Artifact(originPhase2Path)
	if err != nil {
		return err
	}

	// Compute values for the next processing step.
	prev := origin

	// Iterate through each item in this collection.
	for _, phase2Path := range contributionPhase2Paths {
		next, err := readPhase2Artifact(phase2Path)
		if err != nil {
			return err
		}

		// Check the operation result and return on error.
		if err := prev.Verify(next); err != nil {
			return err
		}

		// Advance the pointer to the latest verified state.
		prev = next
	}

	// Seal final contribution with cached evals and persist keys.
	pk, vk := prev.Seal(&commons, evals, nil)
	return writeKeys(pk, vk, pkOutPath, vkOutPath)
}

// readPhase2Artifact loads and deserializes a phase2 artifact from disk.
func readPhase2Artifact(path string) (*mpcsetup.Phase2, error) {
	// Open the required input file from disk.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Compute values for the next processing step.
	p2 := new(mpcsetup.Phase2)
	if _, err := p2.ReadFrom(f); err != nil {
		return nil, err
	}

	// Return the computed result to the caller.
	return p2, nil
}

// writeKeys persists proving and verifying keys to the given output paths.
func writeKeys(pk groth16.ProvingKey, vk groth16.VerifyingKey, pkOutPath, vkOutPath string) error {
	// Write proving key.
	pkFile, err := os.Create(pkOutPath)
	if err != nil {
		return err
	}
	defer pkFile.Close()

	// Check the operation result and return on error.
	if _, err := pk.WriteTo(pkFile); err != nil {
		return err
	}

	// Write verifying key.
	vkFile, err := os.Create(vkOutPath)
	if err != nil {
		return err
	}
	defer vkFile.Close()

	// Check the operation result and return on error.
	if _, err := vk.WriteTo(vkFile); err != nil {
		return err
	}

	// Finish successfully after all steps complete.
	return nil
}
