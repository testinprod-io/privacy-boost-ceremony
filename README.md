# Privacy Boost Ceremony

Contributor CLI and verification tools for the [Privacy Boost](https://github.com/testinprod-io) trusted setup ceremony.

The ceremony uses Groth16 multi-party computation (MPC) via [gnark](https://github.com/Consensys/gnark). Each contributor generates local randomness, mixes it into the phase2 parameters, and submits the result. As long as at least one participant is honest and destroys their randomness, the final parameters are secure.

## Quick Start

```bash
git clone https://github.com/testinprod-io/privacy-boost-ceremony.git
cd privacy-boost-ceremony
bash circuit-setup/contribute_quickstart.sh \
  --config <CONFIG_PATH> \
  --coordinator-url <COORDINATOR_URL>
```

The coordinator will provide the config file and URL before the ceremony begins.

See [Contributor Guide](circuit-setup/contributor-guide.md) for detailed instructions.

## Verify

After the ceremony is finalized, verify the published bundle:

```bash
go build -o ./bin/ceremony ./cmd/ceremony
./bin/ceremony verify-public --config <CONFIG_PATH> --bundle-dir <BUNDLE_DIR>
```

## License

See [LICENSE](LICENSE).
