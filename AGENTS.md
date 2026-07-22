# Project instructions

## Architecture

- Keep the design KISS. Prefer a direct function or a consumer-owned interface over a framework, service locator, event bus, or speculative abstraction.
- Preserve the current dependency direction:
  - `cmd/bot_astrosferum` is the composition root and CLI;
  - `internal/forecast` and `internal/astronomy` contain provider-neutral calculations;
  - `internal/model` and its subpackages acquire and extract model data;
  - `internal/render` renders already-computed data and does not perform network access;
  - `internal/platform` adapts external messaging platforms and defines the interfaces it consumes.
- Do not create a package only to hold one wrapper or one interface. Introduce a shared provider abstraction when the real ICON Global implementation defines the common contract, not before.
- Keep scientific formulas provider-neutral and covered by regression tests. Do not hide calibration changes inside transport or rendering code.
- Model data, caches, generated charts, credentials, `.env`, and live configuration never belong in Git.

## Required checks before every push

Run all of the following from the repository root and fix every finding:

```bash
test -z "$(gofmt -l ./cmd ./internal)"
go test ./...
go vet ./...
golangci-lint run ./...
go build ./cmd/bot_astrosferum
```

The pinned CI version is `golangci-lint v2.12.2`. If it is not installed locally, use the official temporary container:

```bash
docker run --rm -v "$PWD:/app:ro" -w /app \
  golangci/golangci-lint:v2.12.2 golangci-lint run ./...
```

Review the final diff for architecture drift before pushing. In particular, reject new cross-layer imports, duplicated provider contracts, and abstractions without a current caller.
