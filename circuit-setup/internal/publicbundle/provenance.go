package publicbundle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultMinAnchorConfirmations = 12

// VerifyOptions configures optional strict anchor verification behavior.
//
// All anchor-specific fields are caller-supplied at verify time so ceremony
// config/finalize flows remain independent from post-ceremony anchoring.
type VerifyOptions struct {
	RequireAnchor    bool
	RPCURL           string
	AnchorChainID    string
	AnchorTxHash     string
	MinConfirmations int
	Logf             func(format string, args ...any)
}

func logf(opts VerifyOptions, format string, args ...any) {
	if opts.Logf == nil {
		return
	}
	opts.Logf(format, args...)
}

// VerifyEthereumAnchor validates that one Ethereum transaction anchors bundle root.
//
// The function checks caller-provided chain/tx parameters, confirms RPC chain
// identity, verifies transaction input contains the anchored value, and enforces
// minimum confirmation depth.
func VerifyEthereumAnchor(
	ctx context.Context,
	m *Manifest,
	rpcURL, chainID, txHash string,
	minConfirmations int,
) error {
	// Normalize caller inputs first so empty-value checks and comparisons behave
	// consistently regardless of whitespace around CLI arguments.
	txHash = strings.TrimSpace(txHash)
	chainID = strings.TrimSpace(chainID)
	if txHash == "" {
		return fmt.Errorf("missing anchor tx hash")
	}
	if chainID == "" {
		return fmt.Errorf("missing anchor chain id")
	}
	if m.BundleRootSHA256 == "" {
		return fmt.Errorf("missing bundle root hash")
	}
	if rpcURL == "" {
		return fmt.Errorf("rpc url is required to verify ethereum anchor")
	}
	if minConfirmations <= 0 {
		minConfirmations = defaultMinAnchorConfirmations
	}

	// If manifest already carries anchor hints, ensure they are consistent with
	// caller-supplied strict verification parameters.
	if m.Anchor.TxHash != "" && !strings.EqualFold(m.Anchor.TxHash, txHash) {
		return fmt.Errorf("anchor tx hash mismatch")
	}
	if m.Anchor.ChainID != "" && normalizeChainID(m.Anchor.ChainID) != normalizeChainID(chainID) {
		return fmt.Errorf("anchor chain id mismatch")
	}

	// Verify RPC endpoint really serves the requested chain to avoid cross-chain
	// validation mistakes from misconfigured providers.
	rpcChainID, err := resolveRPCChainID(ctx, rpcURL)
	if err != nil {
		return err
	}
	if normalizeChainID(rpcChainID) != normalizeChainID(chainID) {
		return fmt.Errorf("rpc chain id mismatch")
	}

	// Resolve transaction+block metadata and verify anchored value linkage.
	anchorMeta, err := ResolveEthereumAnchorMetadata(ctx, rpcURL, txHash, m.BundleRootSHA256, minConfirmations)
	if err != nil {
		return err
	}
	if m.Anchor.BlockHash != "" && !strings.EqualFold(m.Anchor.BlockHash, anchorMeta.BlockHash) {
		return fmt.Errorf("anchor block hash mismatch")
	}
	if m.Anchor.BlockNumber != "" && !strings.EqualFold(m.Anchor.BlockNumber, anchorMeta.BlockNumber) {
		return fmt.Errorf("anchor block number mismatch")
	}
	if m.Anchor.BlockTime != "" && m.Anchor.BlockTime != anchorMeta.BlockTime {
		return fmt.Errorf("anchor block time mismatch")
	}
	if m.Anchor.AnchoredValue != "" && !strings.EqualFold(m.Anchor.AnchoredValue, m.BundleRootSHA256) {
		return fmt.Errorf("anchor anchoredValue mismatch")
	}
	return nil
}

// resolveRPCChainID fetches chain id from the configured JSON-RPC endpoint.
func resolveRPCChainID(ctx context.Context, rpcURL string) (string, error) {
	chainResp := struct {
		Result string `json:"result"`
	}{}
	if err := rpcCall(ctx, rpcURL, "eth_chainId", []any{}, &chainResp); err != nil {
		return "", err
	}
	if strings.TrimSpace(chainResp.Result) == "" {
		return "", fmt.Errorf("rpc eth_chainId returned empty value")
	}
	return chainResp.Result, nil
}

// normalizeChainID canonicalizes decimal/hex chain-id strings into decimal text.
func normalizeChainID(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if strings.HasPrefix(v, "0x") {
		u, err := strconv.ParseUint(strings.TrimPrefix(v, "0x"), 16, 64)
		if err == nil {
			return strconv.FormatUint(u, 10)
		}
		return v
	}
	u, err := strconv.ParseUint(v, 10, 64)
	if err == nil {
		return strconv.FormatUint(u, 10)
	}
	return v
}

// ResolveEthereumAnchorMetadata resolves tx/block data and validates anchor linkage.
//
// It verifies transaction existence, ensures calldata contains the anchored value,
// confirms block existence, and computes confirmation depth from chain head.
func ResolveEthereumAnchorMetadata(
	ctx context.Context,
	rpcURL, txHash, anchoredValue string,
	minConfirmations int,
) (*Anchor, error) {
	// Fetch transaction to obtain calldata and mined block reference.
	txResp := struct {
		Result *struct {
			Input       string `json:"input"`
			BlockHash   string `json:"blockHash"`
			BlockNumber string `json:"blockNumber"`
		} `json:"result"`
	}{}
	if err := rpcCall(ctx, rpcURL, "eth_getTransactionByHash", []any{txHash}, &txResp); err != nil {
		return nil, err
	}
	if txResp.Result == nil {
		return nil, fmt.Errorf("anchor tx not found")
	}

	// Require anchored value to appear in tx input so the chain record commits
	// to the exact bundle root being verified.
	payloadHex := strings.ToLower(anchoredValue)
	inputHex := strings.ToLower(strings.TrimPrefix(txResp.Result.Input, "0x"))
	if !strings.Contains(inputHex, payloadHex) {
		return nil, fmt.Errorf("anchor tx input does not contain anchored value")
	}

	// Fetch block metadata for timestamp/hash/number checks.
	blockResp := struct {
		Result *struct {
			Number    string `json:"number"`
			Hash      string `json:"hash"`
			Timestamp string `json:"timestamp"`
		} `json:"result"`
	}{}
	if err := rpcCall(
		ctx,
		rpcURL,
		"eth_getBlockByHash",
		[]any{txResp.Result.BlockHash, false},
		&blockResp,
	); err != nil {
		return nil, err
	}
	if blockResp.Result == nil {
		return nil, fmt.Errorf("anchor block not found")
	}

	// Fetch chain head to compute current confirmation depth.
	headResp := struct {
		Result string `json:"result"`
	}{}
	if err := rpcCall(ctx, rpcURL, "eth_blockNumber", []any{}, &headResp); err != nil {
		return nil, err
	}
	head, err := hexToUint64(headResp.Result)
	if err != nil {
		return nil, err
	}
	blockNum, err := hexToUint64(blockResp.Result.Number)
	if err != nil {
		return nil, err
	}
	confirmations := int(head-blockNum) + 1
	if confirmations < minConfirmations {
		return nil, fmt.Errorf("anchor confirmations too low: got=%d min=%d", confirmations, minConfirmations)
	}

	// Convert block timestamp into RFC3339 for stable manifest representation.
	ts, err := hexToUint64(blockResp.Result.Timestamp)
	if err != nil {
		return nil, err
	}
	blockTime := time.Unix(int64(ts), 0).UTC().Format(time.RFC3339)
	return &Anchor{
		TxHash:        txHash,
		BlockHash:     blockResp.Result.Hash,
		BlockNumber:   blockResp.Result.Number,
		BlockTime:     blockTime,
		Confirmations: confirmations,
		AnchoredValue: anchoredValue,
	}, nil
}

// rpcCall performs one JSON-RPC request and decodes result into caller output.
//
// It treats non-2xx responses and JSON-RPC error envelopes as hard failures so
// anchor verification cannot silently continue on partial provider errors.
func rpcCall(ctx context.Context, rpcURL, method string, params []any, out any) error {
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("rpc %s failed: status=%d body=%s", method, resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var errProbe struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &errProbe)
	if errProbe.Error != nil {
		return fmt.Errorf("rpc %s error: %s", method, errProbe.Error.Message)
	}
	return json.Unmarshal(body, out)
}

// hexToUint64 parses a JSON-RPC hex quantity into uint64.
func hexToUint64(v string) (uint64, error) {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "0x")
	if v == "" {
		return 0, fmt.Errorf("invalid hex uint64 value")
	}
	u, err := strconv.ParseUint(v, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse hex uint64: %w", err)
	}
	return u, nil
}
