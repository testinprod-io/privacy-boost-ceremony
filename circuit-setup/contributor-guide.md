# Contributor Guide

This guide explains how participants contribute to the Privacy Boost trusted setup ceremony.

## Quickstart (Recommended)

From the repository root, run:

```bash
git clone <REPO_URL>
cd privacy-boost-ceremony
bash circuit-setup/contribute_quickstart.sh \
  --coordinator-url <COORDINATOR_URL> \
  --config <CONFIG_PATH>
```

The coordinator will provide the config file and coordinator URL before the ceremony begins.

Useful alternatives:

```bash
# Use a prebuilt ceremony binary instead of building locally.
CEREMONY_BINARY_PATH=/path/to/ceremony \
  bash circuit-setup/contribute_quickstart.sh \
  --coordinator-url <COORDINATOR_URL> --config <CONFIG_PATH>

# Pin quickstart to a specific ceremony release.
CEREMONY_RELEASE_VERSION=1.2.3 \
  bash circuit-setup/contribute_quickstart.sh \
  --coordinator-url <COORDINATOR_URL> --config <CONFIG_PATH>

# Build the ceremony binary in Docker.
CEREMONY_BUILD_MODE=docker \
  bash circuit-setup/contribute_quickstart.sh \
  --coordinator-url <COORDINATOR_URL> --config <CONFIG_PATH>
```

What to expect:

- The script prints setup milestones such as checking tools, downloading/building the CLI, and completion.
- It prefers a provided or existing `ceremony` binary first, then tries an official GitHub release binary, and only then falls back to building locally.
- After setup, it starts the contribution flow.
- You will see a message like: `Open https://github.com/login/device and enter code XXXX-XXXX`.
  Follow the link, enter the code, and return to the terminal.
- The CLI will proceed circuit-by-circuit until complete.

## Requirements

- Ceremony config file provided by the coordinator.
- Either a prebuilt `ceremony` binary, a working local Go installation, or Docker.

## Manual Flow

```bash
go build -o ./bin/ceremony ./cmd/ceremony

./bin/ceremony contribute \
  --config <CONFIG_PATH> \
  --coordinator-url <COORDINATOR_URL>
```

To reduce output, add `--quiet`.

The command:

- runs GitHub Device Flow and mints a session token
- claims queue lease for each configured circuit
- downloads current input `.ph2` from coordinator
- computes phase2 locally on contributor machine
- uploads output `.ph2` to coordinator for verification and persistence
- prints contribution records

## Best Practices

- Run with a stable machine and avoid interrupting the command.
- Save command output for your local audit record.
- If interrupted, rerun `contribute`.

## Common Errors

- `participant is not at front of queue`: another contributor is ahead; retry.
- `session expired` or `session revoked`: rerun `contribute` to re-authenticate.
- verification errors: retry once; if repeated, contact coordinator with output log.

## Security Notes

- Do not share auth/session output with others.
- Run from a trusted environment.
- Avoid adding custom scripts that modify contribution files.
