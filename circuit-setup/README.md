# Privacy Boost Ceremony Client

Contributor CLI for the Privacy Boost trusted setup ceremony. This tool lets participants authenticate, compute phase2 contributions locally, and submit them to the coordinator. It also includes an offline verification tool for published ceremony bundles.

## Commands

```bash
# Contribute to the ceremony
ceremony contribute --config <path> --coordinator-url <url>

# Verify a published ceremony bundle offline
ceremony verify-public --config <path> [--bundle-dir <dir>] \
  [--require-anchor] [--rpc-url <url>] \
  [--anchor-chain-id <id>] [--anchor-tx-hash <hash>] [--min-confirmations <n>]
```

## Quickstart (Contributor)

```bash
git clone <REPO_URL>
cd privacy-boost-ceremony
bash circuit-setup/contribute_quickstart.sh \
  --coordinator-url <COORDINATOR_URL> \
  --config <CONFIG_PATH>
```

The script will build `./bin/ceremony` using detected local Go, Docker, or a repo-local Go fallback, then run the contribution flow.

See `circuit-setup/contributor-guide.md` for detailed instructions.

## How It Works

1. **Auth**: GitHub Device Flow — open a URL, enter a code, return to terminal.
2. **Claim**: CLI claims a queue lease for each circuit from the coordinator.
3. **Download**: Fetches the current input `.ph2` artifact.
4. **Compute**: Runs phase2 MPC contribution locally on your machine (trust-critical step).
5. **Submit**: Uploads the output `.ph2` to the coordinator for verification.

## Building

```bash
go build -o ./bin/ceremony ./cmd/ceremony
```

## Verification

After the ceremony is finalized, anyone can verify the published bundle:

```bash
ceremony verify-public --config <path> --bundle-dir <path/to/public>
```

This checks manifest integrity, hash chains, transcript linkage, and participant consistency — all offline from local files.
