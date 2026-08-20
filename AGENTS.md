# Project instructions

## Architecture

- Keep the design KISS. Prefer a direct function or a consumer-owned
  interface over a framework, service locator, event bus, or speculative
  abstraction.

- Preserve the current dependency direction:
  - `cmd/bot_astrosferum` is the composition root and CLI;
  - `internal/app/bot` owns the shared platform-neutral command, forecast,
    localization, and persistence workflow;
  - `internal/forecast` and `internal/astronomy` contain provider-neutral
    calculations;
  - `internal/model` and its subpackages acquire and extract model data;
  - `internal/render` renders already-computed data and does not perform
    network access;
  - `internal/platform` contains peer Telegram/VK adapters; neither adapter
    may import the other, and both implement the small messenger interfaces
    owned by `internal/app/bot`.

- Do not create a package only to hold one wrapper or one interface.

- Introduce a shared provider abstraction when the real ICON Global
  implementation defines the common contract, not before.

- Keep scientific formulas provider-neutral and covered by regression tests.

- Do not hide calibration changes inside transport, persistence, platform,
  or rendering code.

- Raw or live provider model data, runtime caches, ad-hoc generated charts,
  credentials, `.env`, secrets, and live configuration never belong in Git.

- Small synthetic or explicitly approved immutable test fixtures and golden
  artifacts may be committed only under designated test-data paths when
  required for reproducible regression testing. They must not contain
  secrets, live configuration, restricted provider data, or production
  payloads.

## Scientific integrity

Treat a change as scientifically significant when it can affect any of the
following:

- computed forecast or astronomy values;
- interpretation or extraction of model data;
- timestamps, time zones, coordinates, projections, or locations;
- physical units, dimensions, constants, signs, or conventions;
- calibration, thresholds, tolerances, or defaults;
- interpolation, missing-data handling, clamping, or fallback behavior;
- floating-point behavior, stability, convergence, or reproducibility;
- statistical processing or uncertainty;
- labels, units, precision, or presentation that can alter interpretation
  of a scientific result.

The size of the diff and the number of affected files do not determine
scientific risk.

The main agent owns implementation and interpretation of scientifically
significant changes.

`quick_explorer` may gather read-only evidence for such work.
`quick_worker` and `standard_worker` must not implement scientifically
significant behavior. They must return `ESCALATE` if scientific impact
cannot be confidently excluded.

Use `scientific_reviewer` before making the final correctness claim when a
change affects formulas, model-data interpretation, units, constants,
calibration, tolerances, numerical behavior, statistical processing,
persisted scientific semantics, or scientific provenance.

The reviewer is optional for documentation-only corrections that do not
change scientific behavior or interpretation.

If a named custom agent is unavailable, the main agent must retain the task
and apply the same scientific, verification, and remote-operation
restrictions directly.

Agent unavailability must not block the task or justify using an unsuitable
agent.

Passing tests is necessary but is not by itself proof of scientific
correctness. Also examine relevant formulas, units, assumptions, invariants,
boundary cases, reference cases, tolerances, and reproducibility metadata.

## Scientific change protocol

Apply this protocol only to a change classified as scientifically
significant under the preceding section.

Before editing scientifically significant behavior, the main agent must
write a concise scientific change note in its working plan or task record.

Do not create a new repository document solely for this note unless the
repository already defines a location for such records or durable scientific
provenance is required.

The change note must identify:

- affected computed outputs, serialized fields, user-visible statuses,
  labels, units, precision, and plots;
- governing formulas, physical units, signs, coordinate systems, time
  conventions, and provider assumptions;
- whether each affected rule is:
  - published physics or mathematics;
  - a provider data contract;
  - a numerical implementation choice;
  - or a project-specific calibration;
- the expected direction and approximate scale of output changes;
- invariants, monotonicity properties, boundary cases, and unavailable-data
  behavior that must remain true;
- independent reference cases, analytic cases, or calculations that will be
  used to evaluate the result;
- affected algorithm, data-contract, cache, fixture, provenance,
  configuration, and documentation versions;
- the production verification plan required by the sections below.

For every item, use `NOT APPLICABLE`, `UNKNOWN`, or `MISSING EVIDENCE` when
appropriate and explain why.

Do not invent a formula, provider contract, expected direction, reference
case, version identifier, provenance record, or acceptance tolerance merely
to complete the change note.

Establish expected behavior independently before changing golden files,
fixtures, snapshots, regression outputs, or acceptance tolerances.

Do not treat the current implementation, newly produced output, or modified
expected output as the source of scientific truth.

Do not begin by changing expected outputs to match a new implementation.

A scientifically significant change is incomplete unless all applicable
artifacts among the actual diff, tests, fixtures, documentation, version
identifiers, cache identity, serialized provenance, and stated scientific
method describe the same behavior.

Artifacts that are not applicable must be identified as such in the change
note rather than created solely to satisfy this protocol.

### Scientific versioning

Bump the narrowest existing version identifier when a change can alter:

- computed or interpreted scientific results;
- the meaning of persisted or serialized data;
- compatibility with an existing cache;
- scientific provenance;
- or results beyond documented numerical nondeterminism and tolerances.

Relevant changes include modifications to:

- formulas and physical constants;
- units, signs, coordinate systems, and time conventions;
- interpolation, reconstruction, and missing-data behavior;
- event ownership and boundary assignment;
- floating-point evaluation order when it can affect accepted results;
- tolerances and classification thresholds;
- grid geometry and numerical integration;
- quality classification;
- provider assumptions;
- project calibration.

A version bump is not required for a refactor that is demonstrated to
preserve scientific behavior and serialized interpretation. Record the
evidence supporting that conclusion in the change note.

Do not introduce a broad versioning framework solely to satisfy this rule.
If no applicable version identifier exists and historical interpretation
or cache compatibility matters, escalate to the main agent and introduce
the smallest explicit provenance mechanism that solves the actual problem.

### Cache and serialization compatibility

Any persisted scientific cache whose key or interpretation depends on the
changed behavior must include the applicable scientific or data-contract
version and be invalidated by construction.

Do not rely only on manual cache deletion.

Old serialized payloads must follow one explicit policy:

- retain their exact historical interpretation under their recorded version;
- be migrated by an explicit and tested migration;
- or be rejected with a clear incompatibility result.

Never silently reinterpret or promote an old payload as belonging to a new
scientific version.

This requirement applies to persisted or shared scientific-result caches.
It does not require invalidating unrelated ephemeral implementation caches
whose keys and meaning are unaffected.

### Documentation consistency

In the same change, update every affected canonical source of documentation,
including as applicable:

- the canonical scientific method;
- implementation-status documentation;
- configuration and calibration documentation;
- serialized-format or data-contract documentation;
- cache and provenance documentation;
- both supported documentation languages.

Do not make no-op documentation edits. For each potentially relevant
document that is not changed, record briefly in the scientific change note
why it is unaffected.

Documentation must distinguish published scientific rules, provider
contracts, numerical implementation choices, and project calibrations.

## Production verification environment

Production is the authoritative environment for all executable checks in
this project.

All authoritative checks listed below are run:

- on the production host;
- through SSH;
- from the remote repository root;
- by the main agent only.

Local checks and checks performed by subagents are preliminary evidence only.
They do not satisfy the required verification gate.

The main agent is the only agent allowed to:

- execute SSH, SCP, SFTP, or remote rsync;
- synchronize files to production;
- run remote builds, tests, linters, or security checks;
- execute remote privilege escalation;
- deploy;
- restart or reload services;
- change production configuration or data.

Subagents may:

- inspect and modify the local worktree within their assigned scope;
- prepare exact verification commands;
- state expected results and possible side effects;
- analyze bounded command output already provided by the main agent.

Full Access is a runtime capability, not implicit authorization for a
subagent to perform remote or production operations.

Before running a newly added or modified test on production, inspect it for
possible live side effects.

Do not execute it if it may cause any effect listed below unless either:

- the applicable project runbook explicitly authorizes that exact effect;
- or the user explicitly approves the exact command after reviewing its
  side effects, cleanup, and rollback plan.

Risky effects include:

- mutating production data;
- sending real Telegram or VK messages;
- modifying live configuration;
- stopping, restarting, or reloading a service;
- writing outside the documented test or build locations;
- invoking paid or externally visible operations beyond existing documented
  test behavior.

For a scientifically significant change, the production verification must
execute the cases defined in the scientific change note and record the exact
tested source state, configuration, dataset version or hash, random seed,
acceptance tolerance, expected observables, and actual results.

## Safe server synchronization

The local worktree is the source of truth for source changes. Do not
hand-edit synchronized source files on production as a substitute for a
local change.

Before synchronization, the main agent must:

1. Verify the local repository root and current working directory.
2. Inspect the complete local diff, including staged, unstaged, and relevant
   untracked files.
3. Verify the remote hostname and effective user.
4. Verify the remote repository root and current working directory.
5. Record the remote base revision and pre-existing remote worktree state.
6. Distinguish pre-existing remote changes from changes belonging to the
   current task.

If pre-existing remote changes overlap the current task or can affect build,
test, lint, runtime, or scientific results, do not synchronize into that
worktree.

Use a dedicated remote git worktree on the production host when available.
Otherwise stop before synchronization and obtain explicit user direction.

Testing on the production host does not require using the active deployment
directory. Prefer an isolated verification worktree over the live deployed
source tree.

Run and inspect an itemized dry run before every synchronization.

Confirm that:

- nested files retain their expected relative paths;
- files are sent to the intended repository;
- files do not land accidentally in the repository root;
- unrelated remote changes are not overwritten;
- `.env`, credentials, secrets, raw or live provider model data, runtime
  caches, ad-hoc generated charts, and live configuration are excluded;
- ownership, group, permissions, executable bits, and symlink targets remain
  unchanged unless their modification is an explicit part of the task;
- deletion is not requested unless it is explicitly required and reviewed.

Do not use broad deletion options such as `rsync --delete` unless deletion
is an explicit part of the task and the dry run has been reviewed.

After synchronization:

1. Verify representative destination paths.
2. Inspect the remote diff or synchronized file manifest.
3. Confirm that the remote files match the intended local files.
4. Record enough information to identify the exact tested source state:
   base revision plus hashes or a manifest of synchronized modified and
   untracked files.

If a production check fails, make the correction in the local worktree and
synchronize again.

During iteration, rerun the directly affected checks first for fast
feedback. After the final correction and synchronization, rerun the complete
`Required checks before every push` sequence.

Do not patch the production copy manually.

## Required checks before every push

After the final safe synchronization, run the complete sequence below in one
Bash shell on the production host from the remote repository root.

Run the complete sequence again after any subsequent source, fixture,
configuration, or documentation change that can affect the checked result.

```bash
set -euo pipefail

go_version='1.27.0'
golangci_lint_version='v2.13.0'
govulncheck_version='v1.7.0'
trivy_version='v0.74.0'
trivy_digest='sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969'

latest_github_release() {
  local release_url
  release_url="$(
    curl -fsSLI -o /dev/null -w '%{url_effective}' \
      "https://github.com/$1/releases/latest"
  )"
  printf '%s\n' "${release_url##*/}"
}

require_latest() {
  local tool="$1" pinned="$2" latest="$3"
  if [[ -z "$latest" || "$latest" != "$pinned" ]]; then
    printf '%s pin %s is not the latest stable release %s\n' \
      "$tool" "$pinned" "${latest:-unknown}" >&2
    exit 1
  fi
}

require_latest_major() {
  local component="$1" pinned_major="$2" latest="$3"
  if [[ -z "$latest" || "$latest" != "v${pinned_major}."* ]]; then
    printf '%s major v%s is not current; latest stable release is %s\n' \
      "$component" "$pinned_major" "${latest:-unknown}" >&2
    exit 1
  fi
}

require_latest Go "go${go_version}" "$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -n 1)"
require_latest golangci-lint "$golangci_lint_version" "$(latest_github_release golangci/golangci-lint)"
require_latest govulncheck "$govulncheck_version" "$(
  curl -fsSL https://proxy.golang.org/golang.org/x/vuln/@latest |
    sed -nE 's/.*"Version":"([^"]+)".*/\1/p'
)"
require_latest Trivy "$trivy_version" "$(latest_github_release aquasecurity/trivy)"
require_latest_major actions/checkout 7 "$(latest_github_release actions/checkout)"
require_latest_major actions/setup-go 7 "$(latest_github_release actions/setup-go)"
require_latest_major golangci-lint-action 9 "$(latest_github_release golangci/golangci-lint-action)"
require_latest_major govulncheck-action 1 "$(latest_github_release golang/govulncheck-action)"

trivy_runtime_version="$(
  docker run --rm "aquasec/trivy@${trivy_digest}" version --format json |
    sed -nE 's/.*"Version":"([^"]+)".*/v\1/p'
)"
require_latest 'Trivy digest' "$trivy_version" "$trivy_runtime_version"

if ! unformatted="$(gofmt -l ./cmd ./internal)"; then
  printf 'gofmt failed to inspect the configured paths\n' >&2
  exit 1
fi

if [[ -n "$unformatted" ]]; then
  printf 'gofmt is required for:\n%s\n' "$unformatted" >&2
  exit 1
fi

go test -count=1 ./...
go vet ./...

if command -v golangci-lint >/dev/null 2>&1 &&
   golangci-lint --version 2>/dev/null | grep -Fq "version ${golangci_lint_version#v}"; then
  golangci-lint run ./...
else
  docker run --rm -v "$PWD:/app:ro" -w /app \
    "golangci/golangci-lint:${golangci_lint_version}" golangci-lint run ./...
fi

build_dir="$(mktemp -d)"
trap 'rm -rf "$build_dir"' EXIT

go build -o "$build_dir/bot_astrosferum" ./cmd/bot_astrosferum

go run "golang.org/x/vuln/cmd/govulncheck@${govulncheck_version}" ./...
```

Fix every source finding.

Distinguish source findings from environment, network, toolchain, permission,
and infrastructure failures. Do not change source code merely to conceal an
environment failure.

Before every push, compare every pinned verification tool, toolchain, and CI
action with its official latest stable release: Go, golangci-lint,
govulncheck, Trivy, and the GitHub Actions used by the verification workflow.
The Trivy image must remain immutable by digest, and its embedded version must
equal the current stable tag. If any pin is stale, stop: review the upstream
release, update `AGENTS.md`, CI, Docker/build inputs, and versioned
documentation as applicable, then rerun the complete gate. Do not use floating
`latest` images. A different installed version does not satisfy the gate; use
the pinned container instead.

Do not substitute a different golangci-lint or govulncheck version without
an explicit reason recorded in the task result.

Run only one production verification sequence at a time unless the project
procedure explicitly guarantees isolation.

Synchronization and verification do not authorize deployment, service
restart, migration, or production-data modification.

## Final verification gate

Before declaring the task verified or ready to push:

1. Confirm that the complete required production-check sequence passed after
   the final synchronization.

2. Confirm that the current local source, fixtures, and relevant
   configuration exactly match the source state tested on production.

3. If the local diff changed after the complete verification sequence,
   synchronize again and rerun the complete sequence.

4. Review the final diff for unrelated changes and architecture drift.

5. Reject new cross-layer imports, duplicated provider contracts, and
   abstractions without a current caller.

6. For a scientifically significant change:
   - confirm that every applicable invariant, boundary case, independent
     reference case, and compatibility requirement in the scientific change
     note was verified;
   - explicitly report every item that remains `UNKNOWN`, `NOT APPLICABLE`,
     or `MISSING EVIDENCE`;
   - confirm that all applicable versions, caches, serialized provenance,
     fixtures, and documentation describe the implemented behavior.

7. When `scientific_reviewer` is required by the scientific-integrity
   section, evaluate its findings, resolve every blocker, and either obtain
   or explicitly report missing evidence.

8. Report every production check that was not run, was only partially run,
   or produced an infrastructure failure.

A task with missing required production checks or unresolved scientific
blockers must not be described as fully verified.

The verification gate is a precondition for push readiness, not
authorization to perform `git push`.
