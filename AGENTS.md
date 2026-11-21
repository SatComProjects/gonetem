# AGENTS.md

Operating contract for AI coding agents working on GoNetEm. Human contributors
should read [`CONTRIBUTING.md`](CONTRIBUTING.md); the detailed manual is
[`docs/development.md`](docs/development.md) and the refactor
roadmap is [`docs/refactor-plan.md`](docs/refactor-plan.md).

## Read first

1. This file.
2. The phase issue or specification for the task.
3. The relevant package and its tests.
4. `docs/architecture.md` and the matching `internals/*` page.

If the task has no acceptance criteria, write them down and get agreement before
coding. Specification-first is not optional.

## Non-negotiable rules

- **Specification before code.** State the goal, acceptance criteria, edge
  cases, constraints and non-goals. Ask when an ambiguity changes behaviour.
- **Tests before implementation.** Write the failing test first, run it, and
  confirm it fails for the expected reason. Then write the smallest change that
  makes it pass.
- **Never weaken a test to make it pass.** Do not delete assertions, loosen
  tolerances, add skips, or change expected values without an explicit,
  reviewed reason. If a test encodes the wrong behaviour, fix the test and say
  why in the pull request.
- **Never claim a test passed without running it.** Report the exact command,
  exit status and a relevant excerpt. If the environment cannot run it, report
  it as unverified.
- **Keep changes small.** One responsibility per commit and pull request. Do
  not mix a refactor with a feature or an unrelated cleanup.
- **Respect the architecture.** The dependency rules below are enforced in CI;
  do not add an import or a package-level global that breaks them.
- **Preserve behaviour unless the specification says otherwise.** Refactors
  change structure, not outcomes.
- **Explain trade-offs and unverified assumptions** at the end of the task.

## Six-stage workflow

### 1. Understand

Produce a short written specification:

- problem and motivation;
- observable acceptance criteria (each maps to a test);
- edge cases: empty input, permissions, cancellation, concurrency, partial
  failure, backward compatibility;
- constraints: API/project-format compatibility, performance, root/Docker needs;
- explicit non-goals.

Inspect the existing implementation and tests before proposing a design.

### 2. Design

Decide, before editing:

- which unit owns each responsibility (single-responsibility principle);
- which interface each consumer needs, and who implements it (interface
  segregation, dependency inversion);
- dependency direction and whether new configuration must be injected;
- the test seams and the test list (unit, characterization, integration);
- whether an ADR or documentation update is required.

Prefer a simple design with one clear extension point over speculative
abstractions. If two designs are viable, state the trade-off and recommend one.

### 3. Test (RED)

- Write the smallest test that expresses the next behaviour.
- Use table-driven tests where the cases share one behaviour.
- Run the test and verify it fails for the intended reason, not a compile error
  or missing fixture.
- Keep test helpers deterministic; use `t.Cleanup` for namespaces, containers,
  files and goroutines.
- Record the red command and failure excerpt for the pull request.

### 4. Implement (GREEN)

- Make the smallest change that satisfies the tests.
- Do not add unrequested features, options or interfaces.
- Keep error handling explicit: wrap with `%w`, add context, and use
  `errors.Join` for batch operations.
- Do not edit tests to fit the implementation. If the test is genuinely wrong,
  stop and document why before changing it.

### 5. Refactor

With the suite green:

- review SOLID responsibilities and remove duplication;
- simplify interfaces and naming;
- check that the change did not introduce hidden coupling or global state;
- rerun the same tests; behaviour must be identical.

Refactoring is not a licence for a broad rewrite. Stay inside the task's scope.

### 6. Validate

Run the fast gate:

```bash
make check          # gofmt, vet, golangci-lint, docs, unit, race, build
make cover-check    # coverage profile + .coverage-thresholds ratchet
```

Run the relevant integration work when behaviour touches the kernel, Docker,
OVS, namespaces or the gRPC surface:

```bash
make test-integration                         # root + Docker + images
python -m pytest -s tests/test_grpc_link_qos.py -v
```

Then:

- inspect the complete `git diff` (including tests and generated files);
- confirm no debug output, dead code or unrelated formatting;
- report commands, exit statuses, trade-offs and assumptions that remain
  unverified.

## Repository map

| Path | Responsibility |
| --- | --- |
| `cmd/gonetem-server`, `cmd/gonetem-console` | Composition roots only: parse flags, load config, wire dependencies, start. |
| `internal/server` | gRPC handlers, project registry, topology engine, validation. |
| `internal/docker` | Docker client and Docker-backed node adapter. |
| `internal/namespace` | Network-namespace node adapter. |
| `internal/ovs` | Open vSwitch instance and switch node adapter. |
| `internal/link` | netns/veth/tc/route primitives; no Docker and no server imports. |
| `internal/options` | Configuration parsing, defaults, TLS, version. |
| `internal/update` | Self-update pipeline. |
| `internal/utils` | Archive and small shared helpers. |
| `internal/testutil` | Capability checks (`RequireRoot`/`Docker`/`Image`) and test fixtures. |
| `internal/proto` | Generated protobuf/gRPC code. **Never edit by hand.** |
| `tests/` | Python gRPC end-to-end tests against a running server. |
| `scripts/check-coverage.sh` | Per-package coverage ratchet used by `make cover-check` and CI. |
| `docs/` | Local Markdown manual, internals pages and the refactor roadmap. |

Run `make proto` only when `internal/proto/netem.proto` changes, and commit the
generated files together with the contract change.

## Architecture boundaries

The intended direction is:

```text
cmd -> server -> adapters (docker, namespace, ovs, update) -> foundation (link, options, utils)
cmd -> console -> foundation
proto is a transport detail: it may appear in server handlers, never in adapters or foundation
```

Rules enforced by `depguard` in `.golangci.yml`:

- `internal/link`, `internal/options` and `internal/utils` must not import the
  server, console, adapters or protobuf packages.
- `internal/docker`, `internal/namespace`, `internal/ovs` and `internal/update`
  must not import the server, console or protobuf packages.
- `internal/server` must not import the console.
- `internal/console` must not import server internals.

Additional rules:

- Define interfaces where they are consumed, not where they are implemented
  unless the abstraction is genuinely shared.
- Do not grow the `Node` composite further. New consumers declare the narrow
  role they need in `internal/server/node_roles.go` or their own package.
- Do not add package-level mutable state. Pass configuration and collaborators
  explicitly; composition belongs in `cmd/`.
- Protobuf types stay at the transport boundary; convert to domain types before
  calling the topology or node layers.
- Keep locks local and document lock order when more than one is held. Never
  spawn unbounded goroutines; use the configured concurrency limits.
- Direct netlink, `nsenter`, Docker API and `tc` calls belong in adapters, not
  in handlers or domain policy.

## Testing rules

| Layer | Command | Requirements |
| --- | --- | --- |
| Unit | `make test-unit` (`go test -short`) | No root, no Docker, no kernel privileges, no network. |
| Race | `make test-race` | Same as unit; required for concurrency changes. |
| Coverage | `make cover-check` | Hermetic profile plus the `.coverage-thresholds` ratchet. |
| Integration | `make test-integration` | Root, Docker images, kernel namespaces/qdiscs. |
| End-to-end | `pytest -s tests/test_grpc_link_qos.py` | Running server; validates user-visible behaviour. |

- A test that needs root, Docker, a kernel module or a running server is an
  integration test: guard it with `testutil.RequireRoot`, `RequireDocker`,
  `RequireImage` or `RequireMPLS` so the unit suite stays hermetic and the
  privileged suite fails when `GONETEM_REQUIRE_INTEGRATION=1` is set. It must
  still run in the integration job.
- Prefer fakes at consumer-defined interfaces over mocking external commands.
  Existing seams include `routeApplier`, `qosBatchApplier`, the topology node
  factory, injected nodes and `Updater` configuration.
- Raise coverage floors in `.coverage-thresholds` when a phase lands; never
  lower them.
- Tests must be deterministic. Avoid sleeps; use channels, contexts and bounded
  waits. Do not write to the host network namespace; use private namespaces and
  clean them up.
- Name tests `Test<Unit>_<Behaviour>`; one behaviour per test.
- New behaviour needs a unit test. Cross-layer or kernel-facing behaviour needs
  a characterization test first and an integration/e2e check where feasible.
- Coverage is a ratchet: do not lower the configured threshold. Critical pure
  logic (QoS calculations, validation, state transitions) should approach full
  branch coverage.

## Static analysis and CI gates

Required status checks on `develop` and `master`:

- `Lint` — gofmt, `go vet`, `golangci-lint` (including curated `revive`, `gosec` and `dupl`), `go mod tidy -diff`. The gosec exceptions in `.golangci.yml` are intentional adapter patterns; do not add a new exclusion without a written justification.
- `Unit tests` — `go test -short -race`, coverage artifact.
- `Build` — cross-compiled server and console binaries.
- `Docs` — local Markdown check (`scripts/check-docs.py`): links, anchors, structure and version consistency.
- `Vulnerability scan` — `govulncheck` through
  `scripts/check-vulnerabilities.sh`; only reviewed IDs in
  `.govulncheck-allow` may remain.

The privileged integration workflow runs nightly and on demand against the
default branch; run `make test-integration` locally before merging privileged
changes. Never disable a failing check or add a
blanket exclusion to land a change; fix the cause or get an explicit,
documented exception.

## Definition of Done

- [ ] Specification and acceptance criteria are in the issue or pull request.
- [ ] Tests were written first and observed failing for the right reason.
- [ ] The smallest implementation makes them pass; no tests were weakened.
- [ ] SOLID and dependency rules reviewed; no new global state.
- [ ] `make check` passes locally.
- [ ] `make cover-check` passes when tests or covered code changed.
- [ ] Relevant integration/e2e checks pass, or the pull request says exactly
      what could not be run and why.
- [ ] Documentation, API reference and configuration docs updated in the same
      change when behaviour changes.
- [ ] Complete diff reviewed; commit history is coherent.
- [ ] Trade-offs and unverified assumptions reported.

## Commits and pull requests

- Branch from `develop`; target pull requests at `develop`. `master` receives
  releases only.
- Use conventional-commit subjects with a scope:
  `feat(server): ...`, `fix(link): ...`, `test(qos): ...`, `refactor(api): ...`,
  `docs: ...`, `chore(ci): ...`.
- Explain the *why* in the body. Keep the subject under ~72 characters.
- Pull requests must contain the TDD evidence, validation commands and
  architecture notes requested by the pull request template.
- Do not push directly to protected branches. Do not force-push shared
  branches.

## Safety

- The server runs as root and manipulates host networking. Never run destructive
  host-wide commands (`ip link delete`, `iptables -F`, `pkill` outside the test
  project) to make a test pass.
- Use the provided test helpers to create private namespaces, and always clean
  up on failure.
- Do not commit generated artefacts, `.gnet` files, certificates, binaries or
  local configuration.

## Refactor roadmap

The repository is being brought to the SOLID + TDD standard in phases. Check
`docs/refactor-plan.md` for the current phase, its exit criteria and the
files in scope. If a task belongs to a later phase, do the smallest safe part
now and link the follow-up issue instead of starting a parallel rewrite.
