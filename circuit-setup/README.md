# Privacy Boost Ceremony Client

Contributor CLI for the Privacy Boost trusted setup ceremony. This tool lets participants authenticate, compute phase2 contributions locally, and submit them to the coordinator. It also includes an offline verification tool for published ceremony bundles.

## Quickstart (Contributor)

Download and run the standalone contributor script — no need to clone the repository first:

```bash
curl -fsSLO https://raw.githubusercontent.com/testinprod-io/privacy-boost-ceremony/main/circuit-setup/contribute.sh
bash contribute.sh
```

The script presents an interactive menu to choose how to obtain the ceremony binary:

1. **Download pre-built release** — fastest, downloads a verified binary from GitHub Releases.
2. **Build from source (local Go)** — clones the repo at the release tag and builds with your Go toolchain (requires Go 1.24+).
3. **Build from source (Docker)** — clones the repo and builds + runs inside a Docker container. No local toolchain needed.

For more details, see `circuit-setup/contributor-guide.md`.

## Commands

```bash
# Contribute to the ceremony
ceremony contribute --config <path> --coordinator-url <url>

# Verify a published ceremony bundle offline
ceremony verify-public --config <path> [--bundle-dir <dir>] \
  [--require-anchor] [--rpc-url <url>] \
  [--anchor-chain-id <id>] [--anchor-tx-hash <hash>] [--min-confirmations <n>]
```

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
