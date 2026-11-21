# Architecture decision records

Short records of decisions that outlived the refactor phases. Status values are *accepted* unless noted.

## ADR-0001: Module path

**Status:** accepted (2026-11-21).

**Decision:** use `github.com/satcomprojects/gonetem` as the Go module path.

**Context:** the repository is owned by `satcomprojects` while the original module declared `github.com/mroy31/gonetem`. Imports, linker flags, protobuf `go_package` and depguard rules must agree.

**Consequences:** all imports and release tooling use the repository owner. Docker Hub image names (`mroy31/gonetem-*`) and the external UI repository remain distribution names, not Go import paths. The protobuf stubs were regenerated with the versions recorded in their headers.

## ADR-0002: Consumer-owned node roles

**Status:** accepted (2026-11-21).

**Decision:** define small node roles where they are consumed (`LinkNode`, `NodeNetns`, `RouteNode`, `StartableNode`, handler roles) and keep the full `Node` composite only for the topology engine/factory boundary.

**Context:** the original 26-method `INetemNode` forced every fake and consumer to depend on the whole adapter surface.

**Consequences:** adapters do not import `internal/server`; tests can fake the minimum behaviour. New node behaviour goes into the smallest role, not the composite.

## ADR-0003: Docker/OVS adapters receive collaborators

**Status:** accepted (2026-11-21).

**Decision:** inject `docker.ContainerNodeAPI`, `docker.RuntimeConfig` and the OVS workdir into node/OVS constructors. No adapter constructs a Docker client.

**Context:** the original adapters opened a shared Docker client in almost every method, hiding dependencies and making unit tests impossible.

**Consequences:** `NodeFactory` is the only construction boundary; tests use fakes. The OVS package keeps a documented private default registry with compatibility wrappers for now.

## ADR-0004: Explicit configuration and composition root

**Status:** accepted (2026-11-21).

**Decision:** parse configuration once in `internal/options`, snapshot it into `server.Config` / `docker.RuntimeConfig` in the binary, and inject it. `options.ServerConfig` is read only by `cmd/` and `internal/options`.

**Context:** deep global reads made tests order-dependent and prevented two servers with different configurations from coexisting.

**Consequences:** two servers can run in one process in tests with different workdirs/concurrency. The console `config set/show` manager still owns its mutable file and passes values into the prompt/client.

## ADR-0005: Coverage ratchet and release deviation

**Status:** accepted with deviation (2026-11-21).

**Decision:** enforce a coverage ratchet at 46 % internal / 64.5 % server, with component floors (Docker 34 %, namespace 52 %, link 12 %, OVS 83 %, console 9 %, options 39 %, update 52 %, utils 56 %).

**Context:** the initial plan targeted 60 % overall. The remaining gap is dominated by the interactive console/CLI and privileged adapter paths that the hermetic suite cannot execute; critical pure logic is already at 80-100 % (link QoS 100 %, topology validation/model 94 %, persistence 86 %, OVS 84 %).

**Consequences:** the ratchet is honest and prevents regressions. Raising the overall figure requires fakes for the remaining CLI/client paths or a privileged coverage profile; that is tracked as post-release work rather than weakening the gate.
