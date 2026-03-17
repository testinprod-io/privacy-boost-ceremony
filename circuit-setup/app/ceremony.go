// Package app hosts the ceremony CLI workflows shared by root command entrypoints.
package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/phase2"
	"github.com/testinprod-io/privacy-boost-ceremony/circuit-setup/internal/publicbundle"
)

// circuit defines the minimal info the contributor client needs per circuit.
type circuit struct {
	ID   string
	Type string
}

// circuits is the fixed production circuit list.
var circuits = []circuit{
	{ID: "s1", Type: "epoch"},
	{ID: "s4", Type: "epoch"},
	{ID: "s8", Type: "epoch"},
	{ID: "s16", Type: "epoch"},
	{ID: "s32", Type: "epoch"},
	{ID: "s64", Type: "epoch"},
	{ID: "s100", Type: "epoch"},
	{ID: "m1", Type: "epoch"},
	{ID: "m4", Type: "epoch"},
	{ID: "m8", Type: "epoch"},
	{ID: "l1", Type: "epoch"},
	{ID: "l4", Type: "epoch"},
	{ID: "l8", Type: "epoch"},
	{ID: "sp1", Type: "epoch"},
	{ID: "d1", Type: "deposit"},
	{ID: "d8", Type: "deposit"},
	{ID: "d32", Type: "deposit"},
	{ID: "f8", Type: "forced"},
}

const defaultStateDir = "./ceremony-state"

const ceremonyUsageText = `ceremony CLI

Commands:
  contribute --coordinator-url <url> [--state-dir <dir>] [--quiet]
  verify-public --bundle-dir <dir> [--quiet]
    [--require-anchor] [--rpc-url https://rpc.example]
    [--anchor-chain-id 1] [--anchor-tx-hash 0x...] [--min-confirmations 12]
`

type verbosity struct {
	quiet bool
}

// circuitResult captures the local and coordinator-visible record for one circuit.
//
// The contributor receipt is built from this structure after the full run
// finishes. It intentionally records only non-secret material such as paths,
// digests, and coordinator-issued transcript hashes so contributors retain a
// richer audit trail without persisting the randomness used during contribution.
type circuitResult struct {
	id                string
	status            string
	hashHex           string
	createdAt         string
	leaseID           string
	inputDownloadPath string
	inputPath         string
	outputPath        string
	inputSHA256       string
	outputSHA256      string
	inputBytes        int64
	outputBytes       int64
}

func (v verbosity) Printf(format string, args ...any) {
	if v.quiet {
		return
	}
	fmt.Printf(format, args...)
}

func startHeartbeat(v verbosity, label string, interval time.Duration) func() {
	if v.quiet || interval <= 0 {
		return func() {}
	}
	stop := make(chan struct{})
	start := time.Now()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				v.Printf("%s elapsed=%s\n", label, time.Since(start).Round(time.Second))
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}

// RunCeremonyCLI dispatches ceremony CLI commands.
func RunCeremonyCLI(args []string) error {
	// Top-level command is required so we can route to a subcommand handler.
	if len(args) < 1 {
		usage()
		return fmt.Errorf("missing command")
	}

	// Route into independent command workflows.
	switch args[0] {
	case "contribute":
		return runContribute(args[1:])
	case "verify-public":
		return runVerifyPublic(args[1:])
	default:
		usage()
		return fmt.Errorf("unknown command %s", args[0])
	}
}

// usage prints the ceremony CLI help text listing all commands and flags.
func usage() {
	fmt.Print(ceremonyUsageText)
}

// runVerifyPublic validates a public export bundle offline from local files.
func runVerifyPublic(args []string) error {
	fs := flag.NewFlagSet("verify-public", flag.ContinueOnError)
	bundleDir := fs.String("bundle-dir", "", "public bundle directory (required)")
	quiet := fs.Bool("quiet", false, "suppress non-essential output")
	rpcURL := fs.String("rpc-url", "", "ethereum rpc url for onchain anchor verification")
	anchorChainID := fs.String("anchor-chain-id", "", "anchor chain id for onchain verification (for example 1)")
	anchorTxHash := fs.String("anchor-tx-hash", "", "anchor transaction hash for onchain verification")
	requireAnchor := fs.Bool("require-anchor", false, "require ethereum anchor verification")
	minConfirmations := fs.Int("min-confirmations", 12, "minimum confirmations for anchor check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	v := verbosity{quiet: *quiet}

	if *bundleDir == "" {
		return fmt.Errorf("--bundle-dir required")
	}

	// In strict anchor mode, require explicit chain/tx parameters so verifier
	// does not accidentally validate against stale manifest hints.
	if *requireAnchor {
		if strings.TrimSpace(*anchorChainID) == "" {
			return fmt.Errorf("--anchor-chain-id required when --require-anchor is set")
		}

		if strings.TrimSpace(*anchorTxHash) == "" {
			return fmt.Errorf("--anchor-tx-hash required when --require-anchor is set")
		}
	}

	// Run local-file verification against the exported public bundle.
	v.Printf("[ceremony][verify-public] start bundleDir=%s requireAnchor=%t\n", *bundleDir, *requireAnchor)
	if err := publicbundle.VerifyIntegrity(*bundleDir, publicbundle.VerifyOptions{
		RequireAnchor:    *requireAnchor,
		RPCURL:           *rpcURL,
		AnchorChainID:    *anchorChainID,
		AnchorTxHash:     *anchorTxHash,
		MinConfirmations: *minConfirmations,
		Logf:             v.Printf,
	}); err != nil {
		return err
	}

	v.Printf("[ceremony][verify-public] verified bundleDir=%s\n", *bundleDir)
	return nil
}

// runContribute executes contributor-local flow: auth, claim, download, compute, submit.
func runContribute(args []string) error {
	fs := flag.NewFlagSet("contribute", flag.ContinueOnError)
	coordinatorURL := fs.String("coordinator-url", "", "coordinator API URL (required)")
	stateDir := fs.String("state-dir", defaultStateDir, "local directory for temporary artifacts")
	quiet := fs.Bool("quiet", false, "suppress non-essential output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	v := verbosity{quiet: *quiet}
	if *coordinatorURL == "" {
		return fmt.Errorf("--coordinator-url required")
	}
	if err := os.MkdirAll(*stateDir, 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	absStateDir, err := filepath.Abs(*stateDir)
	if err != nil {
		return err
	}

	fmt.Printf("\n  Contributing to %d circuits.\n\n", len(circuits))

	startAll := time.Now()
	totalCircuits := len(circuits)

	// Authenticate once, then reuse the same session token across all circuit contributions.
	sessionToken, participantID, err := runAuthFlow(v, *coordinatorURL)
	if err != nil {
		return err
	}
	v.Printf("[ceremony][contribute] session_acquired participant=%s\n", participantID)

	engine := phase2.NewEngine()
	results := make([]circuitResult, 0, totalCircuits)
	processed := 0
	skipped := 0
	contributed := 0
	// Each circuit uses its own lease and input/output artifact pair.
	for i, c := range circuits {
		circuitID := c.ID
		circuitIndex := i + 1
		v.Printf(
			"\n[ceremony][contribute] === circuit %d/%d id=%s type=%s ===\n",
			circuitIndex,
			totalCircuits,
			circuitID,
			c.Type,
		)
		v.Printf(
			"[ceremony][contribute][%s] step=1/4 circuit=%d/%d claim_start\n",
			circuitID,
			circuitIndex,
			totalCircuits,
		)
		claim, err := claimContribution(v, *coordinatorURL, sessionToken, circuitID, time.Hour)
		if err != nil {
			return err
		}
		if claim.Skipped {
			v.Printf(
				"[ceremony][contribute][%s] step=1/4 circuit=%d/%d skipped reason=%s\n",
				circuitID,
				circuitIndex,
				totalCircuits,
				claim.SkipReason,
			)
			results = append(results, circuitResult{id: circuitID, status: "skipped (" + claim.SkipReason + ")"})
			processed++
			skipped++
			percent := (processed * 100) / totalCircuits
			v.Printf(
				"[ceremony][contribute] progress processed=%d/%d contributed=%d skipped=%d percent=%d "+
					"elapsed=%s\n",
				processed,
				totalCircuits,
				contributed,
				skipped,
				percent,
				time.Since(startAll).Round(time.Second),
			)
			continue
		}

		// Use lease-qualified temp names to avoid collisions across retries/contributors.
		inputPath := filepath.Join(
			absStateDir,
			fmt.Sprintf("contribute_input_%s_%s.ph2", circuitID, claim.LeaseID),
		)
		outputPath := filepath.Join(
			absStateDir,
			fmt.Sprintf("contribute_output_%s_%s.ph2", circuitID, claim.LeaseID),
		)

		// Download coordinator-selected input artifact for the claimed lease.
		v.Printf(
			"[ceremony][contribute][%s] step=2/4 circuit=%d/%d input_download_start lease=%s\n",
			circuitID,
			circuitIndex,
			totalCircuits,
			claim.LeaseID,
		)
		if err := downloadInputArtifact(
			*coordinatorURL,
			claim.InputDownloadPath,
			sessionToken,
			inputPath,
			v,
		); err != nil {
			return fmt.Errorf("input download failed for %s: %w", circuitID, err)
		}

		// Capture immutable metadata for the exact input artifact this contributor
		// received before any local phase2 work begins.
		inputSHA256, inputBytes, err := describeLocalArtifact(inputPath)
		if err != nil {
			return fmt.Errorf("describe input artifact for %s: %w", circuitID, err)
		}

		// Phase2 contribution computation is intentionally local on contributor machine.
		v.Printf(
			"[ceremony][contribute][%s] step=3/4 circuit=%d/%d local_contribute_start\n",
			circuitID,
			circuitIndex,
			totalCircuits,
		)
		startLocal := time.Now()
		if err := engine.Contribute(inputPath, outputPath); err != nil {
			return fmt.Errorf("local contribute failed for %s: %w", circuitID, err)
		}

		// Record the locally generated output digest before upload so the receipt
		// preserves what was actually submitted to the coordinator.
		outputSHA256, outputBytes, err := describeLocalArtifact(outputPath)
		if err != nil {
			return fmt.Errorf("describe output artifact for %s: %w", circuitID, err)
		}

		v.Printf(
			"[ceremony][contribute][%s] step=3/4 circuit=%d/%d local_contribute_complete ms=%d\n",
			circuitID,
			circuitIndex,
			totalCircuits,
			time.Since(startLocal).Milliseconds(),
		)

		// Submit lets coordinator verify transition and persist accepted output.
		v.Printf(
			"[ceremony][contribute][%s] step=4/4 circuit=%d/%d submit_start lease=%s\n",
			circuitID,
			circuitIndex,
			totalCircuits,
			claim.LeaseID,
		)
		var submitResp struct {
			HashHex   string `json:"hashHex"`
			CreatedAt string `json:"createdAt"`
		}
		if err := submitOutputArtifact(
			*coordinatorURL,
			sessionToken,
			circuitID,
			claim.LeaseID,
			false,
			outputPath,
			&submitResp,
			v,
		); err != nil {
			return fmt.Errorf("submit failed for %s: %w", circuitID, err)
		}
		results = append(results, circuitResult{
			id:                circuitID,
			status:            "contributed",
			hashHex:           submitResp.HashHex,
			createdAt:         submitResp.CreatedAt,
			leaseID:           claim.LeaseID,
			inputDownloadPath: claim.InputDownloadPath,
			inputPath:         inputPath,
			outputPath:        outputPath,
			inputSHA256:       inputSHA256,
			outputSHA256:      outputSHA256,
			inputBytes:        inputBytes,
			outputBytes:       outputBytes,
		})
		processed++
		contributed++
		percent := (processed * 100) / totalCircuits
		v.Printf(
			"[ceremony][contribute] progress processed=%d/%d contributed=%d skipped=%d percent=%d "+
				"elapsed=%s\n",
			processed,
			totalCircuits,
			contributed,
			skipped,
			percent,
			time.Since(startAll).Round(time.Second),
		)
	}

	// Print contribution summary table.
	fmt.Println("")
	fmt.Println("  Contribution Summary")
	fmt.Println("  " + strings.Repeat("─", 52))
	fmt.Printf("  %-10s %-16s %s\n", "Circuit", "Status", "Hash")
	fmt.Println("  " + strings.Repeat("─", 52))
	for _, r := range results {
		marker := "✓"
		hash := ""
		if r.status != "contributed" {
			marker = "–"
		}
		if r.hashHex != "" && len(r.hashHex) >= 16 {
			hash = r.hashHex[:16] + "..."
		} else if r.hashHex != "" {
			hash = r.hashHex
		}
		fmt.Printf("  %-10s %s %-14s %s\n", r.id, marker, r.status, hash)
	}
	fmt.Println("  " + strings.Repeat("─", 52))
	fmt.Printf("  %d/%d contributed, %d skipped (elapsed %s)\n",
		contributed, totalCircuits, skipped,
		time.Since(startAll).Round(time.Second),
	)
	fmt.Println("")

	receiptPath := filepath.Join(absStateDir, "contribution-receipt.json")
	// Keep the original interactive receipt flow so contributors explicitly
	// choose whether to persist a local receipt at the end of the run.
	fmt.Println("  Save a receipt file? After the ceremony is finalized, you can use")
	fmt.Println("  it to verify your contributions are included in the published bundle.")
	fmt.Print("  Save receipt? [Y/n]: ")
	var answer string
	fmt.Scanln(&answer)
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer == "" || answer == "y" || answer == "yes" {
		// The saved receipt now uses the richer local metadata format even
		// though the user prompt remains the same as before.
		if err := writeContributionReceipt(
			receiptPath,
			participantID,
			*coordinatorURL,
			absStateDir,
			results,
		); err != nil {
			fmt.Printf("  Warning: contribution succeeded, but receipt could not be saved: %v\n", err)
		} else {
			fmt.Printf("\n  Receipt saved to %s\n\n", receiptPath)
		}
	} else {
		// If the contributor declines the receipt, also clean up the local input
		// and output artifacts so the default experience remains close to the
		// original flow of not retaining extra local state.
		if err := cleanupContributionArtifacts(results); err != nil {
			fmt.Printf("  Warning: contribution succeeded, but local cleanup failed: %v\n", err)
		}
	}

	fmt.Println("  Thank you for contributing to the Privacy Boost ceremony!")
	fmt.Println("  Your participation strengthens the security of the protocol.")
	fmt.Println("")

	return nil
}

// writeContributionReceipt persists the contributor's local audit record to disk.
//
// The receipt keeps coordinator-issued transcript information together with local
// artifact digests and file paths so a contributor can later demonstrate what was
// downloaded, what was produced locally, and which accepted contribution hash the
// coordinator returned. All stored fields are intentionally non-secret.
func writeContributionReceipt(
	receiptPath, participantID, coordinatorURL, stateDir string,
	results []circuitResult,
) error {
	type receiptEntry struct {
		CircuitID         string `json:"circuitId"`
		Status            string `json:"status"`
		Hash              string `json:"hash,omitempty"`
		CreatedAt         string `json:"createdAt,omitempty"`
		LeaseID           string `json:"leaseId,omitempty"`
		InputDownloadPath string `json:"inputDownloadPath,omitempty"`
		InputPath         string `json:"inputPath,omitempty"`
		InputSHA256       string `json:"inputSha256,omitempty"`
		InputBytes        int64  `json:"inputBytes,omitempty"`
		OutputPath        string `json:"outputPath,omitempty"`
		OutputSHA256      string `json:"outputSha256,omitempty"`
		OutputBytes       int64  `json:"outputBytes,omitempty"`
	}
	type receipt struct {
		Participant    string         `json:"participant"`
		CoordinatorURL string         `json:"coordinatorUrl"`
		StateDir       string         `json:"stateDir"`
		GeneratedAt    string         `json:"generatedAt"`
		Circuits       []receiptEntry `json:"circuits"`
	}

	// Build a structured, backwards-compatible receipt document that preserves
	// the original participant/circuit summary while enriching each circuit with
	// the local artifact metadata gathered during the run.
	out := receipt{
		Participant:    participantID,
		CoordinatorURL: coordinatorURL,
		StateDir:       stateDir,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		Circuits:       make([]receiptEntry, 0, len(results)),
	}
	for _, r := range results {
		out.Circuits = append(out.Circuits, receiptEntry{
			CircuitID:         r.id,
			Status:            r.status,
			Hash:              r.hashHex,
			CreatedAt:         r.createdAt,
			LeaseID:           r.leaseID,
			InputDownloadPath: r.inputDownloadPath,
			InputPath:         r.inputPath,
			InputSHA256:       r.inputSHA256,
			InputBytes:        r.inputBytes,
			OutputPath:        r.outputPath,
			OutputSHA256:      r.outputSHA256,
			OutputBytes:       r.outputBytes,
		})
	}

	// Serialize once and write atomically enough for a local CLI workflow. The
	// receipt lives alongside the saved artifacts inside the contributor state
	// directory so users can archive or inspect one directory tree.
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(receiptPath, b, 0o644)
}

// cleanupContributionArtifacts removes local input/output files when the user
// declines to save the richer local receipt.
//
// This cleanup is intentionally best-effort and only touches the local
// contributor machine. The accepted coordinator-side contribution record is
// already persisted by the time this function runs, so a cleanup failure does
// not affect ceremony correctness.
func cleanupContributionArtifacts(results []circuitResult) error {
	// Deduplicate paths so one circuit result cannot cause repeated delete
	// attempts for the same local file.
	paths := make([]string, 0, len(results)*2)
	seen := make(map[string]struct{}, len(results)*2)
	for _, r := range results {
		for _, path := range []string{r.inputPath, r.outputPath} {
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}

	// Remove each local artifact and ignore already-missing files so repeated or
	// manual cleanup remains harmless.
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// describeLocalArtifact returns the SHA256 digest and byte size for one file.
//
// The contributor receipt uses these values to bind a saved local artifact path
// to the exact bytes that existed on disk at download or submit time, which is
// more durable than recording only filenames.
func describeLocalArtifact(path string) (string, int64, error) {
	// Open and stat the file first so the helper can return both digest and size
	// without requiring the caller to walk the same file twice.
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}

	// Stream the file through SHA256 rather than loading it into memory so the
	// helper remains safe for large `.ph2` artifacts.
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), info.Size(), nil
}

// runAuthFlow completes GitHub Device Flow and returns session token + participant ID.
func runAuthFlow(v verbosity, coordinatorURL string) (string, string, error) {
	// Start Device Flow and print instructions for browser-based approval.
	var start struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := postJSON(coordinatorURL+"/v1/auth/github/start", map[string]any{}, &start); err != nil {
		return "", "", err
	}

	// User completes approval in browser while coordinator holds polling context.
	const boxW = 54 // inner width between │ and │
	pad := func(s string) string {
		if len(s) >= boxW {
			return s
		}
		return s + strings.Repeat(" ", boxW-len(s))
	}
	fmt.Println("")
	fmt.Println("  ┌" + strings.Repeat("─", boxW) + "┐")
	fmt.Println("  │" + pad("  GitHub Authentication Required") + "│")
	fmt.Println("  │" + pad("") + "│")
	fmt.Println("  │" + pad("  1. Open:  "+start.VerificationURI) + "│")
	fmt.Println("  │" + pad("  2. Enter: "+start.UserCode) + "│")
	fmt.Println("  │" + pad("") + "│")
	fmt.Println("  │" + pad("  Waiting for approval...") + "│")
	fmt.Println("  └" + strings.Repeat("─", boxW) + "┘")
	fmt.Println("")

	// Coordinator polls GitHub and returns coordinator-issued session identity.
	v.Printf("[ceremony][auth] waiting_for_github_approval\n")
	stop := startHeartbeat(v, "[ceremony][auth] waiting", 20*time.Second)
	defer stop()
	var done struct {
		SessionToken string `json:"sessionToken"`
		Participant  string `json:"participantId"`
	}
	if err := postJSON(coordinatorURL+"/v1/auth/github/complete", map[string]any{
		"deviceCode": start.DeviceCode,
		"interval":   start.Interval,
		"timeout":    start.ExpiresIn,
	}, &done); err != nil {
		return "", "", err
	}
	if done.SessionToken == "" {
		return "", "", fmt.Errorf("empty session token")
	}

	// Participant is coordinator-derived from authenticated GitHub identity.
	if done.Participant == "" {
		return "", "", fmt.Errorf("empty participant id")
	}

	return done.SessionToken, done.Participant, nil
}

type claimResponse struct {
	LeaseID           string `json:"leaseId"`
	InputDownloadPath string `json:"inputDownloadPath"`
	Skipped           bool   `json:"skipped,omitempty"`
	SkipReason        string `json:"skipReason,omitempty"`
}

// claimContribution retries lease claim until success/skip or maxWait timeout.
func claimContribution(
	v verbosity,
	coordinatorURL, sessionToken, circuitID string,
	maxWait time.Duration,
) (*claimResponse, error) {
	// Bound total waiting time so CLI does not block forever under queue contention.
	start := time.Now()

	// Start with short delay for responsiveness, then back off to reduce API pressure.
	backoff := 2 * time.Second
	attempt := 0

	for {
		attempt++
		// Claim is lease-based and must be retried when another participant currently owns head.
		var claim claimResponse
		err := postJSON(coordinatorURL+"/v1/contribute/claim", map[string]any{
			"sessionToken": sessionToken,
			"circuitId":    circuitID,
		}, &claim)
		if err == nil {
			// Skip response means this participant already contributed this circuit earlier.
			if claim.Skipped {
				v.Printf(
					"[ceremony][contribute][%s] step=1/4 claim_skipped reason=%s\n",
					circuitID,
					claim.SkipReason,
				)
				return &claim, nil
			}

			// Successful non-skip response must include both lease and download path.
			if claim.LeaseID == "" || claim.InputDownloadPath == "" {
				return nil, fmt.Errorf("invalid claim response for %s", circuitID)
			}

			v.Printf(
				"[ceremony][contribute][%s] step=1/4 claim_acquired lease=%s\n",
				circuitID,
				claim.LeaseID,
			)
			return &claim, nil
		}

		// Non-contention errors are surfaced immediately (auth/config/validation issues).
		if !isRetryableClaimErr(err) {
			return nil, fmt.Errorf("claim failed for %s: %w", circuitID, err)
		}

		// Stop retrying once total wait budget is exhausted.
		if time.Since(start) >= maxWait {
			return nil, fmt.Errorf("claim timed out for %s after %s: %w", circuitID, maxWait, err)
		}

		// Sleep before next attempt; exponential backoff caps at 10s.
		v.Printf(
			"[ceremony][contribute][%s] step=1/4 claim_retry attempt=%d backoff=%s err=%v\n",
			circuitID,
			attempt,
			backoff,
			err,
		)
		time.Sleep(backoff)
		if backoff < 10*time.Second {
			backoff *= 2
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
		}
	}
}

// isRetryableClaimErr reports queue/lock contention errors safe to retry.
func isRetryableClaimErr(err error) bool {
	// Match coordinator/store contention errors that are expected to resolve with time.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "participant is not at front of queue") ||
		strings.Contains(msg, "unique constraint failed: circuit_lock.circuit_id") ||
		strings.Contains(msg, "database is locked")
}

// downloadInputArtifact fetches claimed input artifact bytes to a local file path.
func downloadInputArtifact(coordinatorURL, pathSuffix, sessionToken, dstPath string, v verbosity) error {
	u, err := url.Parse(coordinatorURL + pathSuffix)
	if err != nil {
		return fmt.Errorf("invalid input download path=%s: %w", pathSuffix, err)
	}
	// Keep header auth and also send query auth for compatibility with gateways
	// that strip Authorization on GET requests.
	q := u.Query()
	if q.Get("sessionToken") == "" {
		q.Set("sessionToken", sessionToken)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	// Prefer Authorization header so tokens don't appear in URLs/logs.
	req.Header.Set("Authorization", "Bearer "+sessionToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && ue != nil {
			return fmt.Errorf("input download request failed path=%s: %v", pathSuffix, ue.Err)
		}
		return fmt.Errorf("input download request failed path=%s", pathSuffix)
	}
	defer resp.Body.Close()

	// Convert coordinator error envelope into plain CLI error when possible.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if msg, ok := e["error"].(string); ok {
			return errors.New(msg)
		}
		return fmt.Errorf("request failed status %d", resp.StatusCode)
	}
	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Stream to disk to avoid keeping large artifacts in memory.
	startDl := time.Now()
	n, err := io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	// Sync to reduce risk of partial files before local phase2 compute starts.
	if err := out.Sync(); err != nil {
		return err
	}
	v.Printf(
		"[ceremony][contribute] input_download_complete downloadPath=%s bytes=%d dst=%s ms=%d\n",
		pathSuffix,
		n,
		dstPath,
		time.Since(startDl).Milliseconds(),
	)
	return nil
}

// submitOutputArtifact uploads contributor output artifact and optionally decodes response payload.
func submitOutputArtifact(
	coordinatorURL, sessionToken, circuitID, leaseID string,
	includeTimings bool,
	outputPath string,
	out any,
	v verbosity,
) error {
	// Open file as streaming body to avoid loading full .ph2 into memory.
	f, err := os.Open(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()
	var uploadBytes int64
	if info, err := f.Stat(); err == nil {
		uploadBytes = info.Size()
	}
	startUpload := time.Now()
	u := fmt.Sprintf(
		"%s/v1/contribute/submit?circuitId=%s&leaseId=%s&includeTimings=%t",
		coordinatorURL,
		url.QueryEscape(circuitID),
		url.QueryEscape(leaseID),
		includeTimings,
	)
	req, err := http.NewRequest(http.MethodPost, u, f)
	if err != nil {
		return err
	}
	// Submit raw .ph2 bytes; coordinator verifies and persists on success.
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+sessionToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Surface coordinator-side validation failures as direct CLI errors.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if msg, ok := e["error"].(string); ok {
			return errors.New(msg)
		}
		return fmt.Errorf("request failed status %d", resp.StatusCode)
	}
	if out == nil {
		v.Printf(
			"[ceremony][contribute][%s] submit_complete lease=%s bytes=%d ms=%d\n",
			circuitID,
			leaseID,
			uploadBytes,
			time.Since(startUpload).Milliseconds(),
		)
		return nil
	}
	// Decode optional result payload for callers that need structured response.
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	v.Printf(
		"[ceremony][contribute][%s] submit_complete lease=%s bytes=%d ms=%d\n",
		circuitID,
		leaseID,
		uploadBytes,
		time.Since(startUpload).Milliseconds(),
	)
	return nil
}

// postJSON sends a JSON POST to the coordinator API and optionally decodes the response.
func postJSON(url string, payload any, out any) error {
	// Small request payloads are marshaled eagerly for a simple helper API.
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	// Ensure the HTTP response body is always closed.
	defer resp.Body.Close()

	// On non-2xx, try to surface coordinator error message from JSON body.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if msg, ok := e["error"].(string); ok {
			return errors.New(msg)

		}
		return fmt.Errorf("request failed status %d", resp.StatusCode)
	}

	if out == nil {
		return nil
	}
	// Caller decides output schema; helper only decodes into provided destination.
	return json.NewDecoder(resp.Body).Decode(out)
}

// ExitWithError prints the error to stderr and exits with code 1 if non-nil.
func ExitWithError(err error) {
	// Shared exit helper keeps command entrypoints concise and consistent.
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
