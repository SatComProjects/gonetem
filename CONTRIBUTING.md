# Contributing to GoNetEm

Thanks for contributing. GoNetEm follows specification-first development and
Red-Green-Refactor. This file is the short version; the full manual is in
[`docs/source/development.rst`](docs/source/development.rst), the quality
roadmap is [`docs/source/refactor-plan.rst`](docs/source/refactor-plan.rst),
and AI agents must also follow [`AGENTS.md`](AGENTS.md).

## Quick start

Requirements: Go 1.24+, GNU Make, Docker, and a Linux host. Root is required
for the server and for kernel-level tests.

```bash
git clone https://github.com/satcomprojects/gonetem.git
cd gonetem
git switch develop
go mod download

make check                    # fmt + vet + lint + unit + race + build
make build-amd64              # binaries in bin/
sudo make test-integration    # root/Docker/kernel suite
```

Node images used by the tests and the default configuration:

```bash
docker pull mroy31/gonetem-frr:0.6.0
docker pull mroy31/gonetem-host:0.6.0
docker pull mroy31/gonetem-server:0.6.0
docker pull mroy31/gonetem-ovs:0.6.0
docker pull mroy31/gonetem-bmv2:0.6.0
```

## Branch model

| Branch | Purpose |
| --- | --- |
| `master` | Stable releases only. |
| `develop` | Integration branch. **Target pull requests here.** |
| `release/x.y.z` | Release stabilisation. |
| `feat/...`, `fix/...`, `refactor/...`, `test/...`, `docs/...`, `chore/...` | Topic branches. |

Create your branch from an up-to-date `develop`, keep it focused, and open a
pull request against `develop`.

## Development workflow

Every change, including a refactor, follows six stages.

1. **Understand** — write the problem, acceptance criteria, edge cases,
   constraints and non-goals. Ask before guessing.
2. **Design** — identify responsibilities, interfaces and dependency direction.
   Note architectural trade-offs. Significant decisions get a short ADR or a
   design section in the pull request.
3. **Test (RED)** — write the smallest failing test first. Run it and confirm it
   fails for the expected reason. Record the command and failure.
4. **Implement (GREEN)** — make the smallest change that passes. Do not touch
   tests to fit the code.
5. **Refactor** — remove duplication, simplify abstractions, apply SOLID
   pragmatically, and keep behaviour identical.
6. **Validate** — run `make check`, the relevant integration tests, review the
   full diff, and report trade-offs and unverified assumptions.

Pull requests that do not contain test evidence for new behaviour will be sent
back. Tests must never be weakened, deleted or skipped to make a build green.

## SOLID and architecture

- One reason to change per type or package; split orchestration, domain policy
  and I/O adapters.
- Interfaces are defined by consumers and kept narrow. Do not extend the
  `INetemNode` god interface.
- Dependencies point inward. See the dependency rules in `AGENTS.md` and
  `.golangci.yml`; `depguard` enforces them in CI.
- Configuration and collaborators are injected at the composition root
  (`cmd/`). Avoid new package-level mutable state.
- Keep protobuf types at the gRPC boundary; convert to domain types before
  calling the server internals.
- Avoid speculative abstractions. A simple, tested design beats a framework.

## Tests

```bash
make test-unit          # hermetic: no root, Docker, kernel or network
make test-race          # unit suite under -race
make test-integration   # root + Docker + kernel namespaces/qdiscs
make cover              # coverage profile and total
```

- Unit tests run with `-short`; integration tests must guard themselves with
  `testing.Short()` and/or `skipUnlessRoot`.
- Kernel-facing tests use private namespaces and clean up with `t.Cleanup`.
- Concurrency changes require `-race` and deterministic synchronization (no
  sleeps).
- End-to-end behaviour is covered by the Python gRPC suite in `tests/`.
- Coverage is a ratchet; do not lower thresholds and do not add tests that
  assert nothing.

## Static analysis and CI

Required checks on pull requests:

- Lint: gofmt, `go vet`, `golangci-lint`, `go mod tidy -diff`.
- Unit tests: `go test -short -race`, coverage artifact.
- Build: both binaries.
- Docs: Sphinx with warnings as errors.

`govulncheck` runs on every pull request but is advisory during the Phase 0
baseline; it becomes blocking in the hardening phase. The privileged
integration suite runs nightly and can be triggered manually.
Do not bypass a failing check; fix the cause or document a reviewed exception.

## Commits and pull requests

Use conventional-commit style subjects with a scope:

```text
feat(server): add LinkSyncMany batch RPC
fix(link): handle a deleted netns during cleanup
test(qos): cover queue-length fallbacks
refactor(topology): extract link index state
docs: describe the TDD workflow
chore(ci): pin golangci-lint
```

Keep the subject under ~72 characters and explain the *why* in the body. The
pull request template asks for the specification, SOLID notes, RED/GREEN
evidence, validation commands and residual risk. Fill it in; do not replace it
with a link to a chat.

Review checklist:

- [ ] Behaviour matches the acceptance criteria.
- [ ] Tests are meaningful, deterministic and actually fail without the change.
- [ ] No unrelated changes, dead code or debug output.
- [ ] Dependency rules and SOLID responsibilities respected.
- [ ] Error paths, cancellation, concurrency and permissions considered.
- [ ] Documentation and API reference updated in the same change.
- [ ] Compatibility: old `.gnet` files, old configuration, gRPC additive changes.
- [ ] Rollback is possible.

## Documentation

The manual is Sphinx/reStructuredText under `docs/source`:

```bash
python3 -m venv /tmp/docsvenv
/tmp/docsvenv/bin/pip install -r docs/requirements.txt
/tmp/docsvenv/bin/sphinx-build -E -n -W -b html docs/source docs/build/html
```

User-visible configuration, commands and API changes must be documented in the
same pull request. Every new page needs a label and a toctree entry.

## Reporting security issues

Do not open a public issue for a vulnerability. Contact the maintainers
privately, or use GitHub's private vulnerability reporting for the repository.
Include the affected version, a reproduction, and the impact you believe it has.
