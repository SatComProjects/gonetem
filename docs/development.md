# Development guide

This guide is for contributors working on GoNetEm itself. It covers the environment, the repository layout, the development loop, coding conventions and the release process.

GoNetEm uses **specification-first development** and **Red-Green-Refactor**. AI agents must also follow `AGENTS.md` at the repository root. The phase plan for the current SOLID/TDD refactor, including exit criteria and measurable ratchets, is [SOLID and TDD refactor plan](refactor-plan.md).

## Environment

Required:

- Go **1.27.4+** (the version declared in `go.mod`);

- GNU Make;

- `golangci-lint` v2.15+ — required by `make check` and CI:

  ```bash
  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
  ```

- optionally `govulncheck` (`make govulncheck`) and `actionlint` (when changing workflows);

- Docker Engine (for node tests and manual testing);

- `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc` (only when changing the API);

- a Linux host with root access — the server creates namespaces, veth pairs and qdiscs, and most integration-level tests need root;

- optionally Python 3 with `grpcio`/`grpcio-tools`/`pytest` for the integration tests ([Testing](testing.md));

- optionally a Markdown viewer or editor; the documentation check needs only Python 3.

```bash
git clone https://github.com/satcomprojects/gonetem.git
cd gonetem
go mod download
make check
make build-amd64
```

The binaries land in `bin/`:

```bash
sudo ./bin/gonetem-server_amd64 -verbose
./bin/gonetem-console_amd64 list
```

## Branch model

| Branch | Purpose |
|----|----|
| `master` | Stable branch, receives merges from `develop` at release time. |
| `develop` | Integration branch. **Target your pull requests here.** |
| `release/x.y.z` | Release stabilisation branches, merged into `master`. |
| `feat/...`, `fix/...`, `perf/...` | Topic branches. |

## Repository layout

```text
cmd/
  gonetem-server/main.go       server entry point
  gonetem-console/main.go      console entry point
internal/
  proto/netem.proto            gRPC contract (+ generated stubs)
  server/                      RPC handlers, topology engine, validation
  console/                     CLI, prompt, PTY, UI
  docker/                      Docker client + Docker node
  ovs/                         OVS instance + switch node
  namespace/                   network-namespace node
  link/                        netns, veth, tc, routes, offloads
  options/                     configuration, version, TLS
  update/                      self-update pipeline
  utils/                        project archive, helpers
  testutil/                     shared test capability checks and fixtures
conf/                          default config, systemd unit, sysctl
docker/                        image sources (host, server, frr, bmv2, ovs, vyos)
scripts/                       certificate generation, project generation, migration
tests/                         Python gRPC integration tests
docs/                          Markdown manual (local, no site build)
Makefile                       build/install/package targets
VERSION                        project version
```

## Development loop

Build and run:

```bash
make build-amd64
sudo ./bin/gonetem-server_amd64 -verbose          # terminal 1
./bin/gonetem-console_amd64 -s localhost:10110 list   # terminal 2
```

The server needs root. Running it in the foreground with `-verbose` gives the per-phase timings (`Run: all link setups took ...`) that make performance work practical.

Fast iteration patterns:

- restart the server between changes — the project registry is in memory, and open projects are closed on shutdown;
- keep a project open with `--no-start` while working on the console, and use `run`/`reload` to iterate;
- use `namespace` nodes for fast tests: they avoid container startup;
- keep `workdir` on a real filesystem, not `tmpfs`, when testing large projects.

## Development principles

Every change, including a refactor, follows six stages:

1.  **Understand** - write the problem, acceptance criteria, edge cases, constraints and non-goals; inspect the code before proposing a solution.
2.  **Design** - assign responsibilities, choose consumer-owned interfaces and dependency direction, and list the tests before editing.
3.  **Test (RED)** - write the smallest failing test first and confirm that it fails for the expected reason.
4.  **Implement (GREEN)** - make the smallest change that passes; do not edit tests to fit the code.
5.  **Refactor** - apply SOLID pragmatically, remove duplication and keep behaviour identical.
6.  **Validate** - run the fast and relevant privileged checks, review the complete diff, and report trade-offs and unverified assumptions.

Rules:

- Never weaken, delete or skip a test to make a suite green. If a test encodes the wrong behaviour, fix it deliberately and explain why in the pull request.
- Never claim a test passed without running it. Report the command, exit status and relevant output.
- Keep changes small and scoped. Do not mix a feature with an unrelated refactor.
- Define interfaces where they are consumed. Do not grow the `Node` composite; use or add a narrow role interface.
- Keep configuration and collaborators explicit. New package-level mutable state is not accepted.
- The architecture rules in `.golangci.yml` (`depguard`) are enforced in CI; see [Architecture](architecture.md) for the package map.

## Quality gates

The fast, hermetic gate is:

```bash
make check       # gofmt, go vet, golangci-lint, docs, unit, race, build
make cover-check # hermetic coverage profile + per-package ratchet
```

The privileged gate is separate because it needs root, Docker and kernel support:

```bash
make test-integration
python -m pytest -s tests/test_grpc_link_qos.py -v   # against a running server
```

`make test-unit` runs `go test -short ./...`; a test that needs root, Docker, kernel modules, network access or a running server must guard itself and run only in the privileged workflow. The unit suite must pass on a clean machine.

Required pull-request checks are lint, unit tests, build, documentation and the vulnerability scan. The scan is blocking; only reviewed exceptions listed in `.govulncheck-allow` are accepted, each with a justification. The privileged integration workflow runs nightly and can be triggered manually; it is promoted to a required check once its pass rate is stable. Branch protection on `develop` and `master` requires these checks, forbids force pushes and deletions, and requires a pull request. See [SOLID and TDD refactor plan](refactor-plan.md) for the coverage and lint ratchets.

### Debugging with VS Code / Delve

The repository ships a debug configuration that runs Delve through `sudo`:

- `.vscode/launch.json` starts `cmd/gonetem-server/main.go` with `DEBUG_AS_ROOT=true`;
- `.vscode/settings.json` maps `dlv-dap` to `.vscode/dlv-sudo.sh`, which re-executes Delve under `sudo -E` when that variable is set;
- `.vscode/dlv-sudo.sh` also adds `$GOPATH/bin` to `PATH` if `dlv-dap` is not found.

Install Delve with `go install github.com/go-delve/delve/cmd/dlv@latest` and make sure `sudo` can run it.

### Manual inspection

Useful while developing:

```bash
# containers of a project
docker ps --filter name=ntm

# interfaces of a project (project id appears in every name)
ip -br link | grep ntm

# qdiscs in a node namespace
sudo nsenter --net=/var/run/docker/netns/<id> tc -qdisc show
# or, for a namespace node
sudo nsenter --net=/run/netns/ntm<id>.<node> tc qdisc show

# bridges and veths in the root namespace
ip -d link show type bridge
ip -d link show type veth

# server log
sudo tail -f /var/log/gonetem-server.log
```

For Docker nodes, `docker inspect <container> | jq '.[0].State.Pid'` gives the PID whose `/proc/<pid>/ns/net` is the node namespace.

## Code conventions

- Write the failing test first; a behaviour change without a test is incomplete.
- Apply SOLID pragmatically: one reason to change per type, consumer-owned interfaces, dependencies pointing inward, explicit composition in `cmd/`.
- Do not add package-level mutable state. Inject configuration and collaborators.
- Keep protobuf types at the gRPC boundary; convert to domain types before calling the topology or node layers.
- `gofmt` all Go code; `go vet ./...` and `golangci-lint run ./...` must pass.
- Wrap errors with context and `%w`: `fmt.Errorf("unable to create node %s: %w", name, err)`. Batch operations aggregate with `errors.Join`.
- Keep the layering: `internal/server` does not call netlink directly, `internal/link` does not call Docker, only `internal/docker` talks to the Docker API, and protobuf types do not leak into the topology engine.
- Log with the package's logrus entry and include `project`/`node` fields; use `Debugf` for timings and per-object detail, `Infof` for lifecycle milestones, `Warnf` for recoverable anomalies.
- Protect shared state with the existing locks; do not add new global mutexes without documenting the ordering in the code and in [Topology engine](internals/topology.md).
- Bound goroutines with an `errgroup` limit taken from the configuration; never spawn unbounded per-node goroutines.
- Use the test seams (`routeApplier`, `qosBatchApplier`) instead of building kernel-dependent tests when the logic can be tested in isolation.
- Add user-visible configuration, commands and API changes to this documentation in the same pull request.

### Commit messages

The project uses conventional-commit-style subjects with a scope, mixed with plain messages for small changes:

```text
feat(server): add LinkSyncMany batch RPC
fix(server): make link add/delete concurrency-safe
perf(server): configure node interfaces without docker exec
test(qos): cover queue fallbacks and fix grpc stub generation
docs: bump version to 0.8.0
```

Explain the *why* in the body; keep the subject under ~72 characters.

## Adding features

| Task | Steps |
|----|----|
| New RPC | `netem.proto` → `make proto` → handler on `*netemServer` → conversion helpers → tests → [gRPC API](api.md). |
| New node type (existing image) | `docker.extraNodes` entry; no code. |
| New node family | Implement the node roles in a new package, add creation to `NodeFactory.Create`, update validation if needed. |
| New node/QoS option | Internal struct + YAML tag + validation + protobuf field + conversion + kernel application + documentation. |
| New console command | Register in `RegisterCommands`, implement the function, add argument regexes, extend completion when node-related, document in [Commands](commands.md). |
| New image | `docker/<name>/Dockerfile`, publish, reference in configuration, document in [Docker images](docker-images.md). |

Prefer additive changes: new fields and new RPCs. Changing the meaning of an existing field or the ordering of a batch is a breaking change and needs a discussion on an issue first.

## Documentation

The manual is plain Markdown under `docs/`, with [docs/README.md](README.md) as the index. Browse it on GitHub or in any local Markdown viewer; there is no site build and no hosted copy.

Check links, anchors and structure locally:

```bash
python3 scripts/check-docs.py
```

`make docs-check` runs the same check, and the `Docs` CI job runs it on every pull request.

Guidelines:

- link with relative Markdown links (`[page](page.md)`), adding an `#anchor` when useful;
- every page starts with a single `#` title and is reachable from `docs/README.md`;
- use fenced code blocks with a language (`yaml`, `bash`, `protobuf`, `go`, `text`);
- keep the reference pages exhaustive and the tutorial pages task-oriented;
- document defaults and constraints exactly as the code enforces them;
- keep the version in `docs/README.md` in sync with `VERSION`; the docs check enforces it.

## Release process

1.  Update `VERSION`, the `VERSION` default in `internal/options/server.go`, and the documented version in `docs/README.md`. `make docs-check` verifies they agree.

2.  Update the changelog/release notes on GitHub.

3.  Build and test all targets:

    ```bash
    make check
    make build-amd64 build-arm64 build-armv7
    make test-integration
    python -m pytest -s tests/test_grpc_link_qos.py -v   # against a server
    ```

4.  Confirm every required CI check is green on `develop` and that the coverage/lint ratchets in [SOLID and TDD refactor plan](refactor-plan.md) did not regress.

5.  Build Debian packages (`make build-deb-amd64` etc.) if relevant.

6.  If node behaviour changed, bump `IMAGE_VERSION` in `internal/options/server.go` **and** rebuild/publish the images; otherwise keep image tags stable.

7.  Merge `develop` into `master` (or via the release branch), tag the release, attach binaries/packages to the GitHub release.

8.  Run `make docs-check` and confirm the Markdown manual and its links are valid.

Compatibility checklist for a release:

- old `.gnet` files still open;
- old configuration files still load (unknown keys ignored, defaults applied);
- the console version and server version can differ without breaking basic commands;
- the gRPC contract is backward compatible (fields only added).

## Troubleshooting the development environment

| Symptom | Fix |
|----|----|
| `operation not permitted` when creating namespaces | Run as root (`sudo`), including tests. |
| `docker image ... not present` | Pull the images with `gonetem-console pull` or `docker pull`. |
| `protoc-gen-go: program not found` | `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` and ensure `$GOPATH/bin` is in `PATH`. |
| tests skipped | Capability tests use `internal/testutil`; run with `sudo` and load MPLS with `sudo modprobe mpls_router mpls_iptunnel` when needed. |
| stale containers after a crash | `gonetem-console clean` or `docker rm -f $(docker ps -aq --filter name=ntm)`. |
| stale named namespaces | `sudo ip netns list \| grep ntm` then `sudo ip netns delete <name>`. |

## See also

- [Testing](testing.md) — unit and integration tests.
- [Architecture](architecture.md) — where to change what.
- [Server internals](internals/server.md) — service layer extension guide.
