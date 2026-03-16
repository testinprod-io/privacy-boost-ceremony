package publicbundle

const ManifestVersion = 1

// Anchor stores optional Ethereum anchoring metadata used in strict verification.
type Anchor struct {
	ChainID          string `json:"chainId,omitempty"`
	TxHash           string `json:"txHash,omitempty"`
	BlockNumber      string `json:"blockNumber,omitempty"`
	BlockHash        string `json:"blockHash,omitempty"`
	BlockTime        string `json:"blockTime,omitempty"`
	Confirmations    int    `json:"confirmations,omitempty"`
	AnchoredValue    string `json:"anchoredValue,omitempty"`
	MinConfirmations int    `json:"minConfirmations,omitempty"`
}

// ContributionManifest records one ordered contribution step within a circuit transcript.
type ContributionManifest struct {
	Index          int    `json:"index"`
	Participant    string `json:"participant"`
	CreatedAt      string `json:"createdAt"`
	InputPath      string `json:"inputPath"`
	InputSHA256    string `json:"inputSha256"`
	OutputPath     string `json:"outputPath"`
	OutputSHA256   string `json:"outputSha256"`
	TranscriptHash string `json:"transcriptHash"`
}

// CircuitManifest commits all exported artifacts and transcript entries for one circuit.
type CircuitManifest struct {
	CircuitID         string                 `json:"circuitId"`
	CircuitSpecJSON   string                 `json:"circuitSpecJson"`
	CircuitSpecSHA256 string                 `json:"circuitSpecSha256"`
	OriginPhase2Path  string                 `json:"originPhase2Path"`
	OriginPhase2SHA   string                 `json:"originPhase2Sha256"`
	FinalPhase2Path   string                 `json:"finalPhase2Path"`
	FinalPhase2SHA    string                 `json:"finalPhase2Sha256"`
	PKPath            string                 `json:"pkPath"`
	PKSHA             string                 `json:"pkSha256"`
	VKPath            string                 `json:"vkPath"`
	VKSHA             string                 `json:"vkSha256"`
	ContributionCount int                    `json:"contributionCount"`
	Contributions     []ContributionManifest `json:"contributions"`
}

// Manifest is the top-level public bundle schema consumed by offline verification.
type Manifest struct {
	Version            int               `json:"version"`
	CeremonyID         string            `json:"ceremonyId"`
	GeneratedAt        string            `json:"generatedAt"`
	ConfigSnapshotPath string            `json:"configSnapshotPath"`
	ConfigSnapshotSHA  string            `json:"configSnapshotSha256"`
	BundleRootSHA256   string            `json:"bundleRootSha256"`
	Participants       []string          `json:"participants"`
	TotalContributions int               `json:"totalContributions"`
	Anchor             Anchor            `json:"anchor,omitempty"`
	Circuits           []CircuitManifest `json:"circuits"`
}
