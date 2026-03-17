# Contributor Guide

This guide explains how participants contribute to the Privacy Boost trusted setup ceremony.

## Quickstart (Recommended)

Download and run the standalone contributor script — no need to clone the repository first:

```bash
curl -fsSLO https://raw.githubusercontent.com/testinprod-io/privacy-boost-ceremony/main/circuit-setup/contribute.sh
bash contribute.sh
```

The script presents an interactive menu:

```
  1) Download pre-built release          (fastest)
  2) Build from source (local Go)        (requires Go 1.24+)
  3) Build from source (Docker)          (no local toolchain needed)
```

- **Option 1** downloads a verified pre-built binary from GitHub Releases.
- **Option 2** clones the repo at the release tag and builds with your local Go toolchain.
- **Option 3** clones the repo at the release tag and builds inside a Docker container. The contribution also runs inside the container.

You will be prompted for the **coordinator URL** (provided by the ceremony coordinator). The circuit list is built into the binary.

What to expect:

- The script handles downloading or building the ceremony binary.
- You will see a message like: `Open https://github.com/login/device and enter code XXXX-XXXX`.
  Follow the link, enter the code, and return to the terminal.
- The CLI will proceed circuit-by-circuit until complete.

## Requirements

- **Option 1 (pre-built)**: `curl`, `tar`, and a checksum tool (`shasum` or `sha256sum`).
- **Option 2 (local Go)**: Go 1.24+.
- **Option 3 (Docker)**: Docker.

## Manual Flow

```bash
go build -o ./bin/ceremony ./cmd/ceremony

./bin/ceremony contribute --coordinator-url <COORDINATOR_URL>
```

To reduce output, add `--quiet`. To change the temp artifact directory, use
`--state-dir <dir>` (defaults to `./ceremony-state`).

The command:

- runs GitHub Device Flow and mints a session token
- claims queue lease for each configured circuit
- downloads current input `.ph2` from coordinator
- computes phase2 locally on contributor machine
- uploads output `.ph2` to coordinator for verification and persistence
- asks whether to save a detailed local receipt and keep local artifacts at the end of the run
- prints contribution records

## Best Practices

- Run with a stable machine and avoid interrupting the command.
- Save command output for your local audit record.
- Answer `yes` when prompted if you want to keep the richer receipt together
  with the downloaded input and generated output in `ceremony-state`.
- If interrupted, rerun `contribute`.

## Common Errors

- `participant is not at front of queue`: another contributor is ahead; retry.
- `session expired` or `session revoked`: rerun `contribute` to re-authenticate.
- verification errors: retry once; if repeated, contact coordinator with output log.

## Security Notes

- Do not share auth/session output with others.
- Run from a trusted environment.
- Avoid adding custom scripts that modify contribution files.
