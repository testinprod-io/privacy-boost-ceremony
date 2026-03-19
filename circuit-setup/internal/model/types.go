// Package model defines shared ceremony domain types persisted and exchanged across layers.
package model

import "time"

// CeremonyState is the lifecycle state of a trusted setup ceremony.
type CeremonyState string

const (
	CeremonyStateInitialized CeremonyState = "initialized"
	CeremonyStateOpen        CeremonyState = "open"
	CeremonyStateFinalizing  CeremonyState = "finalizing"
	CeremonyStateFinalized   CeremonyState = "finalized"
)

// CircuitType identifies the kind of circuit (epoch, deposit, forced).
type CircuitType string

const (
	CircuitTypeEpoch   CircuitType = "epoch"
	CircuitTypeDeposit CircuitType = "deposit"
	CircuitTypeForced  CircuitType = "forced"
)

// AccessMode controls ceremony visibility (private allowlist vs public).
type AccessMode string

const (
	AccessPrivate AccessMode = "private"
	AccessPublic  AccessMode = "public"
)

// CircuitSpec defines compile-time parameters for a single circuit.
type CircuitSpec struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Type         CircuitType `json:"type"`
	BatchSize    int         `json:"batchSize,omitempty"`
	MaxInputs    int         `json:"maxInputs,omitempty"`
	MaxInPerTx   int         `json:"maxInputsPerTransfer,omitempty"`
	MaxOutPerTx  int         `json:"maxOutputsPerTransfer,omitempty"`
	Depth        int         `json:"depth"`
	AuthDepth    int         `json:"authDepth,omitempty"`
	MaxTrees     int         `json:"maxTrees,omitempty"`
	MaxAuthTrees int         `json:"maxAuthTrees,omitempty"`
	MaxFeeTokens int         `json:"maxFeeTokens,omitempty"`
	Phase1Power  int         `json:"phase1Power"`
}

// Phase1Spec configures phase1 artifact source, URLs, and checksum validation.
type Phase1Spec struct {
	SourceURL             string            `json:"sourceUrl,omitempty"`
	SourcePath            string            `json:"sourcePath,omitempty"`
	ExpectedSHA256ByPower map[string]string `json:"expectedSha256ByPower,omitempty"`
}

// ServerSpec configures API server listen address and session settings.
type ServerSpec struct {
	ListenAddr                  string `json:"listenAddr,omitempty"`
	SessionTTLMinutes           int    `json:"sessionTtlMinutes,omitempty"`
	ContributionLeaseTTLMinutes int    `json:"contributionLeaseTtlMinutes,omitempty"`
	AllowRepeatContributions    bool   `json:"allowRepeatContributions,omitempty"`
}

// GitHubAuthSpec enables and configures GitHub OAuth device flow.
type GitHubAuthSpec struct {
	Enabled  bool   `json:"enabled,omitempty"`
	ClientID string `json:"clientId,omitempty"`
}

// ArtifactStoreSpec configures artifact storage provider and root directory.
type ArtifactStoreSpec struct {
	Provider string `json:"provider,omitempty"`
	RootDir  string `json:"rootDir,omitempty"`
}

// CacheSpec configures cache directory for ptau and intermediate artifacts.
type CacheSpec struct {
	RootDir string `json:"rootDir,omitempty"`
}

// CeremonyConfig is the root configuration for a trusted setup ceremony.
type CeremonyConfig struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	AccessMode  AccessMode        `json:"accessMode"`
	Allowlist   []string          `json:"allowlist,omitempty"`
	StateDir    string            `json:"stateDir"`
	Phase1      Phase1Spec        `json:"phase1"`
	Server      ServerSpec        `json:"server"`
	GitHubAuth  GitHubAuthSpec    `json:"githubAuth"`
	Artifacts   ArtifactStoreSpec `json:"artifacts"`
	Cache       CacheSpec         `json:"cache,omitempty"`
	Production  bool              `json:"production,omitempty"`
	Circuits    []CircuitSpec     `json:"circuits"`
}

// CircuitState tracks per-circuit progress (origin path, latest path, contribution count).
type CircuitState struct {
	CircuitID        string    `json:"circuitId"`
	Status           string    `json:"status"`
	OriginPhase2Path string    `json:"originPhase2Path"`
	LatestPhase2Path string    `json:"latestPhase2Path"`
	Contributions    int       `json:"contributions"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// ContributionRecord is a single phase2 contribution with paths and hash.
type ContributionRecord struct {
	ID          string    `json:"id"`
	CircuitID   string    `json:"circuitId"`
	Participant string    `json:"participant"`
	InputPath   string    `json:"inputPath"`
	OutputPath  string    `json:"outputPath"`
	HashHex     string    `json:"hashHex"`
	Verified    bool      `json:"verified"`
	CreatedAt   time.Time `json:"createdAt"`
}

// AuthSession holds session token and participant identity for contribution flow.
type AuthSession struct {
	SessionToken  string    `json:"sessionToken"`
	ParticipantID string    `json:"participantId"`
	GitHubUserID  string    `json:"githubUserId"`
	GitHubLogin   string    `json:"githubLogin"`
	ExpiresAt     time.Time `json:"expiresAt"`
	CreatedAt     time.Time `json:"createdAt"`
	RevokedAt     time.Time `json:"revokedAt,omitempty"`
}

// ContributionLease tracks an in-progress contribution lock for a circuit.
type ContributionLease struct {
	CircuitID     string    `json:"circuitId"`
	ParticipantID string    `json:"participantId"`
	LeaseID       string    `json:"leaseId"`
	ExpiresAt     time.Time `json:"expiresAt"`
}
