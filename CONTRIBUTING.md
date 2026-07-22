# Contributing

Contributions that improve correctness, reproducibility, documentation, tests, accessibility, or operational safety are welcome.

## Development workflow

1. Create a focused branch from the default branch.
2. Keep model data, generated runtime output, `.env`, `config/config.yaml`, and `secrets/` out of Git.
3. Add or update tests for behavioral changes.
4. Run:

   ```sh
   gofmt -w ./cmd ./internal
   go test ./...
   go vet ./...
   golangci-lint run ./...
   go build -o /tmp/bot_astrosferum ./cmd/bot_astrosferum
   ```

5. Explain the physical basis, assumptions, units, data provenance, and uncertainty of algorithm changes.

## Scientific changes

Changes to formulas or coefficients must include a source or derivation, before/after regression cases, algorithm-version and cache-key updates where required, and a clear statement of whether validation is synthetic, model-to-model, or observational. Do not tune and evaluate a coefficient on the same observing cases without saying so.

## Privacy

Use only Saint Petersburg or Moscow in public examples. Never submit real user IDs, tokens, private coordinates, database dumps, complete platform updates, or production credentials. See [PRIVACY.md](PRIVACY.md).
