.. _refactor-plan:

SOLID and TDD refactor plan
===========================

Status: active. Baseline: ``develop`` at ``e784547`` (2026-11-21).

This plan brings GoNetEm to the development standard defined in
:ref:`development`: specification first, Red-Green-Refactor, pragmatic SOLID,
enforced architecture boundaries, static analysis, and CI gates. It is a
behaviour-preserving refactor: user-visible behaviour, the gRPC contract and the
``.gnet`` project format stay compatible unless a phase explicitly says
otherwise.

Scope
-----

In scope:

* test architecture and hermetic unit tests;
* interface ownership, dependency inversion and removal of hidden global state;
* decomposition of the largest server, node, topology and console types;
* configuration injection and a real composition root;
* lint, security, coverage and CI gates;
* documentation of the resulting boundaries.

Out of scope:

* new user features, new RPCs, new node types or new image versions;
* changing the Docker Hub image names or the external UI repository;
* rewriting the Python end-to-end suite or the gRPC contract;
* replacing Docker, netlink, OVS or the console UI toolkit;
* speculative package splits that are not forced by a responsibility or a
  dependency rule.

Baseline evidence
-----------------

Measured on 2026-11-21 with Go 1.24.13, before this plan was applied. The
commands are reproducible; the numbers are the starting point for the ratchets.

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Check
     - Baseline
   * - ``go build ./...``
     - Pass.
   * - ``go vet ./...``
     - Pass.
   * - ``go test ./...`` without root
     - Fail. 26 tests fail without the node images; with the images present,
       7 tests still fail without root or MPLS-capable networking.
   * - Coverage
     - 22.6 % of all statements including generated protobuf and ``cmd``.
       Phase 0 left the hermetic suite at 26.5 % of ``internal`` statements;
       Phase 1 raised it to 32.8 % (1781/5431). Before classification, the
       non-root suite covered 30.6 % of non-generated statements.
   * - Package coverage, hermetic suite
     - Phase 1: ``server`` 59.5 %, ``update`` 53.1 %, ``utils`` 57.3 %,
       ``options`` 39.6 %, ``namespace`` 28.3 %, ``docker`` 8.9 %,
       ``ovs`` 8.4 %, ``link`` 9.3 %, ``console`` 0.9 %.
   * - Test classification
     - ``go test -short ./...`` passes without root, Docker images or a Docker
       daemon. Capability helpers guard 21 Docker/kernel tests; the full
       non-root suite passes by skipping them.
   * - ``golangci-lint`` default set
     - 63 issues: 50 ``errcheck``, 11 ``staticcheck``, 1 ``ineffassign``,
       1 ``unused``.
   * - ``golangci-lint`` Phase 0 curated set
     - 43 issues after excluding generated code, test ``errcheck`` and
       documented presets.
   * - ``gofmt`` / ``go mod tidy -diff``
     - Clean.
   * - CI, lint configuration, coverage gate, PR template
     - Absent.
   * - Code size
     - 18,706 non-generated Go lines. Largest files: ``server/topology.go``
       2321, ``server/server.go`` 1465, ``console/prompt.go`` 1076,
       ``docker/node.go`` 1046, ``docker/client.go`` 588,
       ``update/update.go`` 552, ``link/tc.go`` 433.

The failure without root/Docker is itself a finding: the current suite mixes
unit and privileged integration tests in the same package and does not expose a
hermetic unit target. Phase 1 fixes that; it does not remove the integration
coverage.

Security baseline (after the dependency upgrade)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Go **1.27.4**, Docker **v28.5.2**, ``x/net`` **v0.63.0**, gRPC **v1.85.0** and
OpenTelemetry **v1.47.0**. ``govulncheck`` is a required CI check through
``scripts/check-vulnerabilities.sh``. Two Docker advisories remain in
``.govulncheck-allow``: they affect Moby daemon AuthZ/plugin code, while GoNetEm
imports only the Docker client; the module has no fixed release. Re-review them
when the Docker client module picks up the fix.

Target architecture
-------------------

Dependency direction:

.. code-block:: text

   cmd/gonetem-server, cmd/gonetem-console      composition roots (wiring only)
        |
        +--> internal/server                     application services, gRPC, project registry
        |         |
        |         +--> internal/docker           adapter
        |         +--> internal/namespace        adapter
        |         +--> internal/ovs              adapter
        |         +--> internal/update           adapter
        |         +--> internal/link             foundation / kernel primitives
        |         +--> internal/options          foundation / configuration
        |         +--> internal/utils            foundation / helpers
        |
        +--> internal/console                    client application
                  |
                  +--> internal/options, internal/utils

   internal/proto is a transport detail. It may be used by gRPC handlers and
   the console client, never by adapters or domain policy.

Design rules:

* **Single responsibility.** A type has one reason to change. Kernel I/O,
  persistence, transport conversion and orchestration live in different units.
* **Open/closed.** Extension happens through the existing node factory and
  narrow interfaces, not by editing a central switch for every new behaviour.
* **Liskov substitution.** Every node implementation honours the same lifecycle
  and error contract; contract tests apply to Docker, namespace and OVS nodes.
* **Interface segregation.** Consumers declare the small interface they need.
  The former ``INetemNode`` is split into lifecycle, network, execution,
  console, persistence and state roles; the topology engine keeps the
  composite where it genuinely needs the union.
* **Dependency inversion.** Policy depends on consumer-owned interfaces;
  Docker, netlink, ``nsenter``, OVS and filesystem access are adapters.
* **Explicit composition.** Configuration and collaborators are passed from
  ``cmd/``. No new package-level mutable state, no hidden constructors.
* **Thin transport.** gRPC handlers validate, convert and delegate. They do not
  run Docker, netlink or archive code.
* **Test pyramid.** Hermetic unit tests for policy and state; characterization
  tests for extracted seams; privileged integration tests for kernel/Docker
  effects; Python gRPC tests for user-visible workflows.

SOLID findings
--------------

.. list-table::
   :header-rows: 1
   :widths: 18 52 14 16

   * - Area
     - Finding
     - Principle
     - Phase
   * - ``server/topology.go``
     - One 2321-line manager mixes YAML schema, validation, link indexing,
       kernel orchestration, progress reporting, archive persistence, netns
       caching and locking. It also imports protobuf types directly.
     - SRP, DIP
     - 5
   * - ``server/server.go``
     - All 36 RPCs, streaming protocols, image pulling, update, node exec,
       copy and filesystem work live on one receiver type.
     - SRP
     - 6
   * - ``server/node.go``
     - ``INetemNode`` has 26 methods. Every fake implements the whole surface;
       consumers depend on behaviour they never call.
     - ISP, DIP
     - 2
   * - ``docker/node.go``
     - One type owns container lifecycle, PID/netns resolution, interface
       configuration, exec, capture, copy and config load/save (1046 lines).
     - SRP
     - 4
   * - ``docker/client.go``
     - Concrete client type, hidden shared client, global image cache, and
       deprecated Docker API usage. Adapters construct collaborators
       internally.
     - DIP, SRP
     - 2, 4
   * - ``server/project.go``
     - ``openProjects`` is a package-level mutable map with package-level
       locks; handlers and tests reach it directly.
     - DIP, SRP
     - 6
   * - ``options``
     - ``options.ServerConfig`` is read deep inside adapters and domain code;
       tests cannot construct an isolated configuration.
     - DIP
     - 8
   * - ``console/prompt.go``
     - Command registry, argument parsing, gRPC calls, terminal UI, packet
       capture and external tool spawning are interleaved (1076 lines).
     - SRP
     - 7
   * - ``link/tc.go``
     - Pure qdisc math and netlink I/O share a file and package; kernel
       operations are hard to fake, though useful seams already exist.
     - SRP, DIP
     - 3
   * - ``ovs/instance.go``
     - Package-level instance registry and direct Docker construction hide
       lifecycle ownership.
     - DIP
     - 4, 6
   * - ``update/update.go``
     - Pipeline, process execution, download, extraction and installation in
       one type. The ``Logger`` seam is good and should be kept.
     - SRP
     - 8
   * - Tests
     - Unit and privileged tests share packages; Docker image and root
       assumptions are implicit; global project state leaks into handler tests.
     - Testability
     - 1, 6

Phase overview
--------------

.. list-table::
   :header-rows: 1
   :widths: 8 34 14 12 32

   * - Phase
     - Goal
     - Size
     - Depends on
     - Exit gate
   * - 0
     - Baseline, governance, CI and lint gates
     - S
     - -
     - Required checks green; baseline recorded
   * - 1
     - Hermetic unit suite and test taxonomy
     - M
     - 0
     - ``go test -short ./...`` passes without root/Docker
   * - 2
     - Interface segregation and adapter injection
     - M
     - 1
     - Consumers use narrow interfaces; no hidden collaborators
   * - 3
     - Link and QoS policy separated from netlink
     - M
     - 2
     - Pure QoS logic fully unit tested; kernel suite green
   * - 4
     - Node adapters decomposed
     - L
     - 2
     - Lifecycle/netns/exec/config separated; contract tests
   * - 5
     - Topology engine decomposed
     - L
     - 3, 4
     - Model/state/orchestration/persistence split; race clean
   * - 6
     - gRPC services and project registry
     - M
     - 5
     - Thin handlers; registry instance; bufconn tests
   * - 7
     - Console command model
     - M
     - 2
     - Parsing/dispatch/UI separated and tested
   * - 8
     - Configuration and composition root
     - M
     - 2, 6
     - No global config reads outside ``cmd``/``options``
   * - 9
     - Hardening and release
     - S
     - all
     - Strict lint, coverage target, security triage, release

Phases 3, 4 and 7 can progress in parallel once Phase 2 lands, provided their
write scopes do not overlap. Phase 5 waits for the link and node seams. Phase 6
depends on the topology state API. Phase 8 finishes the injection work that
Phases 2 to 6 prepare.

Phase 0 - Baseline and guardrails
---------------------------------

**Goal.** Make quality visible and enforceable before changing production
structure.

**Work items.**

* Add ``AGENTS.md``, ``CONTRIBUTING.md`` and this plan; update the development,
  testing and README entry points.
* Add ``.golangci.yml`` with dependency-direction rules (``depguard``), the
  correctness/quality set and documented exclusions for generated code.
* Add CI: lint, hermetic tests with race and coverage, build, documentation and
  ``govulncheck``. Add the privileged integration workflow (nightly/manual).
  GitHub only schedules workflows from the default branch, so the nightly run
  starts after the next ``master`` release; until then use
  ``make test-integration`` locally.
* Add Makefile targets: ``fmt-check``, ``vet``, ``lint``, ``test-unit``,
  ``test-race``, ``test-integration``, ``cover``, ``govulncheck``, ``check``.
* Add the pull request and issue templates that require specification, TDD
  evidence, validation commands and residual risk.
* Fix the baseline lint issues without behaviour changes; record the result.
* Enable branch protection on ``develop`` and ``master``: pull request, required
  checks, no force push, no deletion. Reviews are optional for a solo
  maintainer and become required when a second maintainer joins.

**Tests.** No new behaviour. Twenty-one Docker/kernel tests are guarded with
``testing.Short()`` so the unit suite is hermetic; they still run in the full
privileged suite. Phase 1 replaces the avoidable integration dependencies with
fakes and adds the characterization tests needed by later phases.

**Exit criteria.**

* ``Lint``, ``Unit tests``, ``Build``, ``Docs`` and ``Vulnerability scan`` are
  green on ``develop`` and required by branch protection.
* ``golangci-lint run ./...`` reports zero issues for the configured set.
* ``go test -short ./...`` passes without root, Docker images or a Docker
  daemon, and ``go test -race -short ./...`` passes.
* Baseline metrics are recorded in this document and in the tracking issue.
* No production behaviour changed.

**Primary files.** ``.github/``, ``Makefile``, ``.golangci.yml``, ``AGENTS.md``,
``CONTRIBUTING.md``, ``docs/source/development.rst``,
``docs/source/testing.rst``.

Phase 1 - Hermetic test architecture
------------------------------------

**Status: complete (2026-11-21).** Delivered ``internal/testutil`` capability
helpers, a hermetic namespace-node fixture for project/topology tests, an
injectable topology node factory, link-state characterization tests, and the
``make cover-check`` ratchet (32.5 % internal and 59 % server floors; measured
32.8 % and 59.5 %). The full non-root suite now passes by skipping genuinely
privileged tests; the privileged workflow sets
``GONETEM_REQUIRE_INTEGRATION=1`` to turn those skips into failures.

**Goal.** Build on the Phase 0 classification: a unit test must not need root,
Docker, kernel modules, network access or a running server. Integration tests
still cover those effects, but avoidable integration dependencies are replaced
by fakes.

**Work items.**

* Introduce a small test-support package (or package-local helpers) with
  ``RequireRoot``, ``RequireDocker``, ``RequireImage``, temporary project
  fixtures and fake nodes. Keep helpers small; do not build a test framework.
* Guard every kernel/Docker/server test with the precise capability check
  (``testutil.RequireRoot``/``RequireDocker``/``RequireImage``). Remove
  implicit "the image happens to be present" skips.
* Split mixed test files where necessary: pure behaviour in the normal file,
  privileged behaviour behind the short guard or a build-tagged integration
  file. Do not reduce the assertions that run in the integration job.
* Add missing seams instead of shelling out in tests. Phase 1 delivered the
  topology node factory; the Docker API, command runner and archive-store seams
  remain in Phases 2/3.
* Add characterization tests for behaviour that will move in Phases 4 to 6:
  topology load/run/save, link add/update/delete, project open/close.
* Enable ``-race -shuffle=on`` in the unit gate. Fix data races, do not suppress
  them.
* Publish a coverage baseline for the hermetic suite (excluding generated code
  and ``cmd``) and add a ratchet that fails if the total drops. Per-package
  thresholds for critical packages follow in Phase 3/5.

**Exit criteria.**

* ``make test-unit`` passes on a clean Linux machine with no Docker daemon,
  no root and no images.
* ``make test-race`` passes.
* The privileged suite still runs all previously covered behaviour; the number
  of integration assertions does not decrease.
* The coverage ratchet is enforced in CI and documented.
* Failure messages distinguish "capability unavailable" from "behaviour
  broken".

**Primary files.** ``internal/testutil``, ``internal/**/*_test.go``,
``scripts/check-coverage.sh``, ``.coverage-thresholds``, ``Makefile`` and
``.github/workflows/ci.yml``.

Phase 2 - Interface segregation and dependency inversion
--------------------------------------------------------

**Status: complete (2026-11-21).** Delivered the role interfaces in
``internal/server/node_roles.go`` (topology composite kept at the manager
boundary), narrow handler/link/route/netns/lifecycle consumers, the Docker API
roles in ``internal/docker/api.go``, injected clients in ``DockerNode`` and
``OvsProjectInstance``, ``NodeFactory``/``ovsFactory`` wiring,
``netemServer.dockerClient`` injection, and a shared node contract test for the
Docker, namespace and OVS implementations. The old ``INetemNode`` name is gone.

**Goal.** Consumers depend on the smallest behaviour they use; adapters are
constructed at the edge.

**Work items.**

* Replace the 26-method ``INetemNode`` with role interfaces declared by
  consumers: lifecycle (start/stop/kill/close), network namespace and interfaces,
  command execution/console, configuration persistence, and interface state.
  Keep a composite only where a factory or the topology engine genuinely needs
  the union, and keep it internal to that boundary.
* Define a Docker API interface (list/get/create/start/stop/remove/pid/copy/exec)
  in the package that consumes it; make ``DockerClient`` implement it. Inject it
  into ``DockerNode`` and the OVS instance instead of constructing it inside.
* Replace package-level image-presence state with an injected cache owned by the
  client, or document and test the cache as an explicit singleton at the
  composition root.
* Keep ``link`` free of Docker and server imports. Introduce a command runner or
  netlink applier only where a unit test needs it; do not wrap every syscall.
* Add contract tests that every node implementation must pass: running/stopped
  transitions, interface naming, missing namespace errors, close idempotence.
* Update fakes to implement only the roles they need.

**Exit criteria.**

* No production consumer references the full ``Node`` composite unless it is a
  factory/topology boundary; handlers, links, routes and lifecycle operations
  use narrow roles.
* No adapter calls ``NewDockerClient`` or another hidden constructor; the
  composition root injects it.
* ``depguard`` remains green and its rules match the documented direction.
* New unit tests use fakes, not root privileges.
* Behaviour is unchanged; the integration suite passes.

**Primary files.** ``internal/server/node.go``, ``internal/docker/*.go``,
``internal/ovs/*.go``, ``internal/namespace/node.go``, test fakes.

Phase 3 - Link and QoS domain
-----------------------------

**Status: complete (2026-11-21).** Delivered the pure ``qos.go`` /
kernel-facing ``qdisc.go`` split, immutable handle functions, shared tbf builder,
``QoSApplier`` server seam, 100 % pure-QoS statement coverage and
``FuzzFormatTime``/``FuzzSplitPeer``. Merged in #28.

**Goal.** Separate pure traffic-control policy from kernel I/O and make both
testable.

**Work items.**

* Split ``link/tc.go`` into a pure QoS parameter model (delay/jitter/loss/rate/
  queue limits, validation and defaults) and a qdisc/route applier.
* Make the applier an interface consumed by the server; keep one production
  implementation backed by ``go-tc``/netlink. Reuse the existing
  ``routeApplier`` and ``qosBatchApplier`` seams.
* Add table-driven tests for all parameter conversions, boundary values,
  percentages, time units, queue-length defaults and tbf buffer calculations.
* Add property/fuzz tests for peer parsing and QoS parsing where inputs are
  untrusted.
* Keep private-namespace integration tests for real qdisc installation,
  replacement, reset and txqueuelen changes.
* Remove package-level mutable state; pass the applier and configuration
  explicitly.

**Exit criteria.**

* Pure QoS and parameter logic has at least 90 % statement coverage and full
  behaviour coverage for boundary cases.
* Kernel tests still pass in the privileged suite.
* The server does not call netlink directly for link QoS; it calls the applier.
* No behaviour change for existing topologies.

**Primary files.** ``internal/link/tc.go``, ``internal/link/route.go``,
``internal/link/link.go``, ``internal/server/topology.go`` (QoS call sites).

Phase 4 - Node adapters
-----------------------

**Status: complete (2026-11-21).** Delivered the Docker/namespace file split,
``pidCache``/``interfaceTable``/``commandExecutor`` collaborators with race
tests, the OVS instance/node split, and a ``Registry`` type with compatibility
wrappers. Fake-backed coverage is 34.7 % Docker, 53.2 % namespace and 84.1 %
OVS. Merged in #29 (OVS) and #30 (Docker/namespace).

**Goal.** Decompose the 1046-line Docker node and the namespace/OVS adapters
along their real responsibilities.

**Work items.**

* Split the Docker node by responsibility: container lifecycle, PID and netns
  resolution, interface attach/configure/state, exec/capture, config load/save,
  and file copy. Prefer small collaborators over one struct with 45 methods.
* Do the same for the namespace node (netns creation, ``nsenter`` execution,
  config/copy) and OVS (instance lifecycle, bridge/port operations, config).
* Inject the Docker API, command runner and filesystem/archive dependencies.
  Keep ``nsenter`` and OVS command lines visible in one adapter each.
* Add contract tests shared by the node families, plus targeted tests for PID
  cache invalidation, lazy creation, stopped-node errors, copy framing and
  timeout handling.
* Remove or encapsulate the OVS package-level instance registry; make project
  ownership explicit.
* Preserve retry, timeout and cleanup semantics exactly.

**Exit criteria.**

* Lifecycle, netns, interface, exec and persistence responsibilities live in
  separate files/types; no new file needs to know all of them.
* Unit tests cover lifecycle decisions and error paths with a fake Docker API.
* Contract tests pass for Docker, namespace and OVS nodes in the integration
  suite.
* Container/sysctl cleanup is unchanged on failure paths.

**Primary files.** ``internal/docker/node.go``, ``internal/docker/client.go``,
``internal/namespace/node.go``, ``internal/ovs/*.go``.

Phase 5 - Topology engine
-------------------------

**Status: complete (2026-11-21).** Delivered a 133-line coordinator with
responsibility files for model, progress, state, links, nodes, run/close,
persistence and load; domain ``NodeRunMessages`` (protobuf conversion moved to
the transport); atomic-write/double-sync/round-trip golden tests; and documented
lock order. Validation/model coverage is 94.6/94.4 %, persistence 85.9 %.

**Goal.** Turn the topology manager into a composition of model, validation,
state, orchestration and persistence, with explicit dependencies.

**Work items.**

* Extract the YAML schema and validation into a pure model/validation unit:
  nodes, links, QoS, bridges, management network, VRF/VRRP rules, name and type
  checks. Keep the accepted YAML shape unchanged.
* Extract runtime state: node list, link list and index, pending-link
  reservations, bridges, management network, running flag, and the locking
  protocol. Document lock order and invariants; add race/stress tests.
* Extract orchestration: run/reload, setup links/bridges/management, start/stop/
  close nodes, progress batching, concurrency limits.
* Extract persistence: archive open/save, network file read/write, atomic writes,
  config directory handling. Keep ``.gnet`` compatibility with golden tests.
* Replace direct protobuf progress messages in the domain with domain progress
  events; convert in the transport layer.
* Inject the node factory, link applier, archive store, logger and configuration.
* Keep a facade for the server/console callers while the internals move; delete
  the facade only when callers have migrated to narrower services.

**Exit criteria.**

* ``server/topology.go`` is a facade or small coordinator, not a 2000-line
  implementation; every extracted unit has a single responsibility.
* Validation and persistence have at least 85 % unit coverage, including
  malformed YAML, duplicate links/nodes, peer orientation, queue limits and
  partial failures.
* Run/save/close characterization tests are green; old project files load.
* ``go test -race`` passes for concurrency and batch tests.
* Lock order is documented in the code and in ``internals/topology``.

**Primary files.** ``internal/server/topology.go``, ``internal/server/check.go``,
``internal/server/mgnt.go``, ``internal/server/link_*.go``,
``internal/server/route_install.go``.

Phase 6 - gRPC services and project registry
--------------------------------------------

**Status: complete (2026-11-21).** Delivered the service split
(``service_server/project/topology/node/link.go``, ``convert.go``), an
instance-based ``ProjectRegistry`` with archive/filesystem work moved out of the
handlers, no package-level project map, per-server update mutex, conversion
table tests and bufconn not-found/invalid/partial-result coverage. Server
coverage is 64.3 %.

**Goal.** Thin, testable transport handlers and an explicit project registry.

**Work items.**

* Split ``server/server.go`` into service files/handlers: server maintenance
  (version, clean, pull, update), projects, topology run/close/save, nodes,
  links, capture/exec/copy. Keep the generated gRPC interface satisfied by one
  registration type that delegates.
* Move request validation and protobuf/domain conversion into a dedicated
  conversion layer with table-driven tests.
* Replace the ``openProjects`` package map and package locks with a
  ``ProjectRegistry`` instance injected into handlers. Keep project lifecycle
  semantics (open, close, save, concurrent access) identical.
* Separate streaming mechanics from business logic; test error mapping,
  cancellation and partial batch results over ``bufconn``.
* Move filesystem/archive work out of handlers into the project service.
* Keep the Python gRPC integration contract unchanged.

**Exit criteria.**

* Handlers validate, convert and delegate; no handler performs Docker, netlink
  or archive work directly.
* The registry is an instance; no package-level project map.
* ``bufconn`` tests cover success, not-found, invalid argument, cancellation and
  partial-failure paths.
* gRPC field names, semantics and streaming order are unchanged.
* The Python e2e suite passes against the refactored server.

**Primary files.** ``internal/server/server.go``, ``internal/server/project.go``,
``internal/server/*_batch.go``, ``internal/server/route_install.go``.

Phase 7 - Console command model
-------------------------------

**Status: complete (2026-11-21).** Delivered the `CommandRegistry` with central
lookup/arity/regex validation, a `ToolRunner`/`ToolProcess` seam for
Wireshark/terminal/editor processes, injected client construction, and a
prompt split into `prompt_commands.go`, `prompt_node_commands.go` and
`prompt_project_commands.go`. Prompt dispatch and an injected-client handler
run without a TTY or server; editor/tool lifecycle tests use fakes.

**Goal.** Separate command definition, parsing, execution and presentation so
the console can be tested without a terminal, gRPC server or external tools.

**Work items.**

* Extract a command registry (name, aliases, argument matchers, help, handler)
  from the prompt loop.
* Separate argument parsing/validation from execution and rendering.
* Put external tool invocation (xterm, Wireshark, editor) behind a small
  interface; tests use fakes.
* Keep the interactive prompt, completion and copy/capture protocols
  behaviourally identical.
* Add tests for command dispatch, malformed arguments, unknown commands,
  cancellation and non-interactive output.

**Exit criteria.**

* Command definitions and handlers are unit tested without a TTY or server.
* External processes are injected; no test spawns Wireshark, xterm or an editor.
* ``prompt.go`` is a coordinator, not the home of every command.
* Manual smoke test in :ref:`testing` still passes.

**Primary files.** ``internal/console/prompt.go``, ``internal/console/commands.go``,
``internal/console/exec.go``, ``internal/console/utils.go``.

Phase 8 - Configuration and composition root
--------------------------------------------

**Goal.** Configuration is parsed once and injected; the composition root is
the only place that knows concrete implementations.

**Work items.**

* Define explicit configuration value objects for server, Docker, concurrency,
  update and TLS. Keep ``options`` responsible for parsing, defaults and
  compatibility, not for global mutable state.
* Pass configuration into the server, topology, node factory and updater
  constructors. Remove deep ``options.ServerConfig`` reads.
* Keep a temporary adapter in ``cmd`` for compatibility while call sites move;
  delete it before the phase exits.
* Wire the Docker client, node factory, link applier, archive store, registry
  and gRPC server in ``cmd/gonetem-server``. Do the equivalent for the console.
* Ensure tests construct configuration explicitly; no test mutates a global and
  restores it.
* Preserve configuration file compatibility, including unknown keys and
  defaults.

**Exit criteria.**

* ``grep`` for ``options.ServerConfig`` finds only ``cmd/`` and ``internal/options``
  (tests may construct the value explicitly).
* The server can be constructed twice in one process with different
  configurations, in tests, without cross-talk.
* Configuration parsing and default tests remain green; old files load.
* No adapter constructs its collaborators.

**Primary files.** ``cmd/gonetem-server/main.go``,
``cmd/gonetem-console/main.go``, ``internal/options/*.go``,
``internal/server/server.go``, ``internal/update/update.go``.

Phase 9 - Hardening and release
-------------------------------

**Goal.** Make the strict quality bar permanent and cut a release from it.

**Work items.**

* Enable the stricter linter set: ``revive`` (curated rules), ``gosec``
  (triaged exclusions with justification), ``dupl``, and the remaining
  ``gocritic`` checks. Remove temporary ``nolint`` directives or attach a
  phase/issue to each survivor.
* Keep ``govulncheck`` blocking and re-review the exceptions in
  ``.govulncheck-allow`` when the Docker client module is upgraded.
* Raise the coverage ratchet to the agreed target: at least 60 % of non-generated
  statements in the unit suite, and at least 80 % for critical pure packages
  (link QoS, topology validation/state, transport conversion).
* Run the unit suite repeatedly (``-count=10`` for fast packages) and the
  integration suite nightly; quarantine or fix flaky tests with an issue. Do
  not add blind retries.
* Review the complete diff of the refactor, update architecture/internals
  documentation and add ADRs for the decisions that outlived a phase.
* Update the release checklist and cut a release from ``develop`` to
  ``master``.

**Exit criteria.**

* Strict lint and vulnerability gates are required and green.
* Coverage target is enforced; no package regressed.
* Nightly integration has a stable pass rate; known flakes have owners.
* Documentation matches the code, including dependency rules and lock order.
* Release checklist includes the new gates.

Metrics and ratchets
--------------------

.. list-table::
   :header-rows: 1
   :widths: 28 18 24 30

   * - Metric
     - Baseline
     - Current gate
     - Target
   * - Blocking lint issues
     - 43 (curated), 63 (default)
     - 0
     - 0 with the strict set
   * - Hermetic unit suite
     - Not available before Phase 0
     - Achieved in Phase 1: ``go test -short ./...`` from a clean machine
     - Keep it hermetic as packages split
   * - Unit coverage (non-generated)
     - 26.5 % after Phase 0
     - Phase 7 ratchet: 45 % total, 63.5 % server, 9 % console, 34 % Docker,
       52 % namespace, 12 % link, 83 % OVS (measured 45.9/64.3/9.3/34.7/53.2/12.4/84.1)
     - At least 60 % total; 80 to 90 % for critical pure logic
   * - Race detector
     - Passes on packages that run
     - Phase 1: ``make test-race`` passes with ``-shuffle=on``
     - Required; no known race
   * - Dependency violations
     - Not enforced
     - 0 via ``depguard``
     - 0 as packages split
   * - Vulnerabilities
     - 40 reachable findings with Go 1.24.13; after the Go 1.27.4/dependency
       upgrade, 2 reviewed Docker daemon-only exceptions remain
     - Blocking via ``scripts/check-vulnerabilities.sh`` and
       ``.govulncheck-allow``
     - 0 unreviewed findings; re-review the Docker exceptions on upgrade
   * - Flaky tests
     - Unknown (environment-dependent failures)
     - Classified
     - Less than 5 % nightly flake rate, each with an issue
   * - PR scope
     - No rule
     - Review guideline
     - At most ~400 changed lines excluding tests/generated, or split

Risk register
-------------

.. list-table::
   :header-rows: 1
   :widths: 22 46 32

   * - Risk
     - Impact
     - Mitigation
   * - Privileged CI is unavailable or unstable
     - Kernel/Docker behaviour is not verified on pull requests
     - Hermetic unit gate is required; nightly privileged workflow; promote to
       required only after a stable pass rate; document manual verification.
   * - Hidden behaviour in the topology manager
     - Subtle regressions during Phase 5
     - Characterization tests first, golden projects, small extractions,
       integration/e2e after each merge.
   * - Global configuration removal is invasive
     - Wide conflicts and missed call sites
     - Do it last, behind explicit dependency structs; codemod; keep
       compatibility defaults.
   * - Long-lived refactor branches conflict with feature work
     - Merge pain and stale tests
     - Small PRs to ``develop``, phase branches, rebase weekly, no feature work
       mixed into refactor PRs.
   * - Coverage or lint gaming
     - Metrics improve without quality
     - Review assertions, per-package thresholds, strict linters, no blanket
       exclusions, test-first evidence in the PR.
   * - Timing-based integration tests
     - Flaky nightly runs
     - Tolerances, idle runners, deterministic waits, quarantine with an issue.
   * - Root test commands affect the host
     - Developer or runner damage
     - Private namespaces, project-scoped cleanup, no host-wide destructive
       commands, code review of test helpers.

Open decisions
--------------

1. **Module path (resolved).** The module is now
   ``github.com/satcomprojects/gonetem``. All Go imports, Makefile linker flags,
   ``depguard`` rules and the protobuf ``go_package`` were updated together.
   Docker Hub image names (``mroy31/gonetem-*``) and the external UI repository
   (``mroy31/gonetem-ui``) are unchanged; they are distribution names, not Go
   import paths.
2. **Privileged runner.** Choose between GitHub-hosted runners with ``sudo`` and
   a self-hosted Linux runner. The integration workflow is written for the
   hosted runner first; promote it to required only after measuring stability.
3. **Coverage target.** Phase 1 froze the ratchet at 32.5 % internal and 59 %
   for ``internal/server`` (measured 32.8 % and 59.5 %). Keep raising the floors
   per phase; the final target remains at least 60 % total and 80 to 90 % for
   critical pure logic.
4. **Package splits.** Phases 3 to 6 may extract files or packages. Prefer files
   and small types until an import cycle or dependency rule forces a package
   boundary. Record any package split as an ADR.
5. **Configuration API.** Choose explicit value objects over a broad
   ``Config`` interface unless a consumer genuinely needs dynamic reload.
6. **Console tool abstraction.** Wrap only the tools the tests need to fake;
   do not build a generic process framework.

Working agreement
-----------------

* One phase is active per package at a time. Parallel lanes must have disjoint
  write scopes.
* Every pull request names its phase and links the tracking issue.
* Phase entry requires the previous phase's exit criteria (or a documented
  exception) and a green ``develop``.
* Phase exit requires the checklist in this document, a green CI run and a
  short retrospective comment on the tracking issue: what changed, what was
  measured, what remains.
* If a phase grows beyond its scope, split it into a new issue rather than
  silently expanding the pull request. Update this plan when the scope changes.

Phase status
------------

.. list-table::
   :header-rows: 1
   :widths: 10 22 68

   * - Phase
     - Status
     - Evidence / note
   * - 0
     - Complete (2026-11-21)
     - Merged in PR #9: governance, required CI, zero lint baseline, hermetic
       test classification, coverage baseline and branch protection.
   * - 1
     - Complete (2026-11-21)
     - testutil capability helpers, hermetic project/topology tests, node
       factory, link characterization, coverage ratchet.
   * - 2
     - Complete (2026-11-21)
     - Role interfaces, Docker API injection, NodeFactory, contract tests.
   * - 3
     - Complete (2026-11-21)
     - QoS split, QoSApplier seam, 100 % pure-QoS coverage, fuzz tests.
   * - 4
     - Complete (2026-11-21)
     - Docker/namespace/OVS decomposition, collaborators, registry, fake tests.
   * - 5
     - Complete (2026-11-21)
     - Topology responsibility split, domain progress, persistence goldens, lock order.
   * - 6
     - Complete (2026-11-21)
     - Service split, ProjectRegistry, conversion layer, bufconn error/partial tests.
   * - 7
     - Complete (2026-11-21)
     - Command registry, tool runner, injected client, prompt file split.
   * - 8
     - Planned
     - Configuration injection and composition root.
   * - 9
     - Planned
     - Strict gates and release.

See also
--------

* :ref:`development` - environment, conventions and release process.
* :ref:`testing` - current test suites and helpers.
* :ref:`architecture` - component and package overview.
* :ref:`internals-topology` - topology engine internals and locking.
* :ref:`internals-server` - service layer extension guide.
