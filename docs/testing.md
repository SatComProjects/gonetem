# Testing

GoNetEm has two test suites:

- **Go tests** (`go test ./...`) — unit tests for validation, configuration, batching, locking and data structures, plus kernel-level tests that create real namespaces, veth pairs and qdiscs;
- **Python gRPC integration tests** (`tests/`) — end-to-end tests against a running server, measuring kernel qdisc parameters and real traffic behaviour.

Both are expected to pass before a pull request is merged.

## Go tests

### Running

```bash
make test-unit                  # hermetic: no root, Docker images or daemon
make test-race                  # hermetic suite under -race
make cover-check                # coverage profile + ratchet
sudo go test ./...              # full suite (same as: make test-integration)
sudo go test ./internal/server/... ./internal/link/... -v
sudo go test -run TestLinkSyncMany ./internal/server/ -v
```

Root is required for everything that touches namespaces, netlink, qdiscs or Docker. Tests that need it call `skipUnlessRoot` and/or `testing.Short()` and are skipped without the capability instead of failing. The unit suite must stay hermetic; the guarded tests still run in the privileged suite.

The race detector is useful for the concurrency tests; it is slower, so run it on the server and docker packages rather than on everything.

Requirements:

- Docker images referenced by the default configuration must be present for the Docker tests (they create real containers);
- a kernel with namespaces, veth, bridge, netem and tbf;
- the TSO/MPLS tests need a network interface that supports feature queries (veth pairs are used, so this is normally fine).

### Test helpers

`internal/link`:

- `skipUnlessRoot(t)` — skip when not root;
- `setUpNetlinkTest(t) (netns.NsHandle, func())` — create a temporary namespace (and lock the OS thread), returning a teardown that closes it and unlocks the thread. Using a private namespace keeps the host clean;
- `checkLinkExistence` — assert interfaces exist;
- `txChecksumActive` — read netdev features to assert the TSO state.

`internal/docker`:

- `setupClient` / `setupContainer` / `setupStartedContainer` — create and clean up real containers from the configured images.

`internal/testutil`:

- `RequireRoot(t)` — skip unless the test runs as root;
- `RequireDocker(t)` — skip unless a Docker daemon is reachable;
- `RequireImage(t, image)` — skip unless the image is present locally;
- `RequireMPLS(t)` — skip unless the MPLS sysctls are available (`sudo modprobe mpls_router mpls_iptunnel`).

These helpers skip with an explicit capability message. Set `GONETEM_REQUIRE_INTEGRATION=1` (the privileged workflow does) to turn those skips into failures, so CI cannot silently lose integration coverage.

`internal/server` tests construct `NetemTopologyManager` values directly and inject fake nodes, appliers and a node factory. Namespace-node fixtures cover project open/save/close and topology loading without Docker or root; Docker/OVS fixtures remain in the privileged suite.

### Coverage by package

| Package | What is tested |
|----|----|
| `internal/options` | image tag/digest resolution (`GetDockerImageId`), configuration parsing and update defaults, `extraNodes` TSO default. |
| `internal/utils` | archive round-trip, nested extraction, rejection of path traversal, absolute paths and unsupported entry types, writer error propagation. |
| `internal/link` | namespace switching and restoration, veth creation (same and across namespaces), bridges, VRRP macvlan, VRFs, batched deletion, interface state, netem/tbf installation and clearing, queue limits, txqueuelen defaults, time conversion, route validation and install/delete, TSO/MPLS preparation, route spec formatting. |
| `internal/docker` | shared client, image presence caching, remove of a missing container as no-op, create/rm, exec, copy, PID lookup, container listing, node interface bookkeeping, PID caching and invalidation, lazy creation, start/stop with and without interfaces, config read/save, image override, volumes, VRRP naming. |
| `internal/ovs` | instance lifecycle, node without instance, detach semantics, interface state keying, concurrent interface access, start/stop, attach. |
| `internal/namespace` | metadata, interface bookkeeping, file copy, close without a namespace. |
| `internal/server` | regexes and topology validation, QoS validation, malformed peers/types, queue length parsing and rejection of negatives, management/namespace constraints, VRRP validation, project open/close/save, project map and topology state concurrency, progress batcher aggregation, configurable concurrency limits, run without OVS, close resets state, node exec closing, copy protocol (DATA before INIT), copy from/from missing file, link add/delete concurrency and duplicate rejection, link index panic regression, synchronisation concurrent with link changes, atomic concurrent writes, batch link operations (order, counts, duplicates, kernel batches, per-link errors), link update diffs and orientation, start/stop requiring a running topology, netns cache collapsing concurrent lookups, node short-name generation, namespace node creation, lazy OVS creation, image list for pull, route installation (bounded concurrency, missing node, unsupported/stopped nodes), and the `LinkSyncMany` handler's status/count mapping. |
| `internal/console` | prompt robustness on unknown/comment-only input. |
| `internal/update` | disabled/unsupported cases, go arch mapping, safe tar extraction and escape rejection, Go environment, cache home, atomic copy, install and missing binary, restart without service, step logging. |

### Writing a new test

- Prefer a unit test with an injected seam when the behaviour is not about the kernel. The server has `routeApplier`, `qosBatchApplier` and a node factory; `Updater` exposes its configuration.
- If the test needs the kernel, use a private namespace (`setUpNetlinkTest`) and `t.Cleanup`/defer to remove everything.
- If it needs root, Docker, an image or MPLS sysctls, use the `testutil` capability helpers instead of a raw `testing.Short` block or a bespoke `skipUnlessRoot`.
- Concurrency tests should run under `-race` and use timeouts/channels rather than sleeps where possible.
- Name tests `Test<Unit>_<Behaviour>` and keep one behaviour per test.

Coverage ratchet:

```bash
make cover-check
go tool cover -html=coverage.out
```

`coverage.out` is generated by the hermetic suite. `.coverage-thresholds` holds the per-package floors; the script fails if any floor is missed. Raise a floor when a phase lands, never lower it. Phases 3-9 raised the internal floor to 46 %, `internal/server` to 64.5 %, console to 9 %, Docker to 34 %, namespace to 52 %, link to 12 % and OVS to 83 % (measured 46.2 %, 64.9 %, 9.4 %, 35.3 %, 53.2 %, 12.4 % and 84.1 %). The kernel and Docker paths still dominate the uncovered lines because the privileged suite is not part of the unit profile.

## Python integration tests

`tests/test_grpc_link_qos.py` drives a **running** `gonetem-server` over gRPC. It requires no privileges on the client side: the server creates all namespaces, veths and qdiscs.

### What it does

For each topology flavour (`namespace` nodes and `docker.host` nodes):

1.  builds a project archive containing two nodes and a link;
2.  opens it (`ProjectOpen`), starts it (`TopologyRun`);
3.  reads the kernel state with `NodeRun`: `tc -j qdisc show` and `ip -j link show`;
4.  configures addresses and measures behaviour:
    - **latency** — a 5 ms one-way delay yields ~10 ms RTT, checked before and after `LinkUpdate` and on a link created with `LinkAdd`;
    - **rate** — TCP throughput is capped by the configured tbf rate;
    - **loss** — 10 % per-direction loss gives ~19 % round-trip loss for ping and ~10 % for one-way UDP traffic, before and after `LinkUpdate`;
    - **queues** — the netem `limit` and the interface `txqueuelen` are verified on creation and after `LinkUpdate`;
5.  prints a colored `Value / Before / After / Expected` table per check;
6.  closes the project.

The update and add cases use intentionally asymmetric per-direction QoS (different delay, jitter and loss for `peer1qos`/`peer2qos`) so that an accidental peer swap in `LinkUpdate`/`LinkAdd` is detected.

### Running

```bash
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r tests/requirements.txt
python -m pytest -s tests/test_grpc_link_qos.py -v
```

By default the tests connect to `localhost:10110`. Override with:

```bash
GONETEM_GRPC_TARGET=host:port python -m pytest -s tests/test_grpc_link_qos.py -v
```

Requirements:

- `grpcio`, `grpcio-tools`, `pytest` (`tests/requirements.txt`);
- a running server;
- the Docker host image present on the server for the `docker.host` cases;
- `tc`, `ip`, `ping` and `iperf3` available **inside the node environment** (the shipped host image provides them; namespace nodes use the host tools).

The tests generate their own Python stubs from `internal/proto/netem.proto` with `grpc_tools.protoc`, so they always exercise the current contract.

### Output and flakiness

- Use `-s` to see the result tables; without it they are captured by pytest.
- Colors are enabled on a TTY or with `FORCE_COLOR=1`; disable with `NO_COLOR=1`.
- Latency and throughput checks use tolerances (the emulator runs on real time and shares the host CPU). A heavily loaded host can make throughput tests flaky; run them on an otherwise idle machine.
- `iperf3` needs both endpoints running; the tests start the server side with `NodeRun` and use a bounded duration.

## Continuous integration

Every pull request to `develop` or `master` must pass:

- **Lint** - `gofmt`, `go vet ./...`, `golangci-lint run ./...` and `go mod tidy -diff`;
- **Unit tests** - `go test -short -race -shuffle=on ./...` plus a coverage artifact and the `.coverage-thresholds` ratchet;
- **Build** - `make build-amd64`;
- **Docs** - `scripts/check-docs.py`: Markdown links, anchors, page structure and version consistency.

The **vulnerability scan** (`make govulncheck`) is a required check. It runs `govulncheck` through `scripts/check-vulnerabilities.sh` and fails on any reachable advisory not listed in `.govulncheck-allow`. Each exception must have a written justification. The privileged integration workflow (`.github/workflows/integration.yml`) is defined for nightly and manual runs. It pulls the node images, runs the full Go suite as root, starts `gonetem-server` and executes the Python gRPC suite. GitHub only schedules workflows from the default branch, so the nightly run targets `master`; run `make test-integration` locally before merging privileged changes. The workflow becomes a required check once its pass rate is stable. A privileged runner needs:

- Linux with Docker (or Docker-in-Docker) and privileges to create namespaces;
- the gonetem images pre-pulled;
- root for the Go kernel tests and for the server;
- optional MPLS kernel modules for the node images.

Do not replace a failing check with a skip. Unit tests that need privileges must guard themselves with `testing.Short()` and still run in the integration workflow; see [SOLID and TDD refactor plan](refactor-plan.md) phase 1.

## Manual smoke test

After a build, a five-minute check catches most regressions:

```bash
gonetem-console create /tmp/smoke.gnet
gonetem-console open /tmp/smoke.gnet      # empty topology runs and closes
# then a two-node topology:
#   edit -> nodes host1/R1, link host1.0-R1.0 with delay
#   reload, status, exec ping, capture, save, extract, quit
gonetem-console clean
```

See [Getting started](getting-started.md) for the full walkthrough.

## See also

- [Development guide](development.md) — environment and release process.
- [gRPC API](api.md) — the RPCs the integration tests use.
- [Troubleshooting](troubleshooting.md) — when tests or runs fail.
