gonetem
=======

[![CI](https://github.com/satcomprojects/gonetem/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/satcomprojects/gonetem/actions/workflows/ci.yml)

GoNetEm is a **network emulator written in Go**. It builds reproducible virtual
networks from Linux network namespaces, Docker containers, veth pairs, Linux
traffic control (`netem`/`tbf`) and Open vSwitch, and controls them through a
command-line console and a gRPC API.

Use it to prototype routing and switching designs, reproduce network conditions
(delay, jitter, loss, rate limits, small buffers), automate experiments from
scripts or CI, and teach networking with a textual, versionable topology.

Features
--------

* **Topology as code** — a YAML file stored in a single portable `.gnet`
  archive, together with the captured node configurations.
* **Several node technologies** — Docker hosts, servers, FRR routers, P4
  switches, VyOS or your own images; Open vSwitch switches; lightweight pure
  network-namespace nodes.
* **Kernel-accurate QoS** — per-direction delay, jitter, loss, rate, BDP-based
  buffer sizing, burst, netem queue limit and `txqueuelen`.
* **Live changes** — add/delete nodes and links, update QoS and toggle
  interfaces without restarting the project, including batch RPCs designed for
  large topologies.
* **Configuration persistence** — node configuration files and folders are
  captured into the project and restored on the next run.
* **Operations** — node consoles (`bash`, `vtysh`, P4 CLI, switch console),
  command execution, packet capture to Wireshark, file copy, project save and
  extract, image pulling, self-update, optional mutual TLS.
* **Programmable** — the whole API is a documented gRPC service.

Architecture
------------

GoNetEm is a client/server application:

| Binary | Role |
| --- | --- |
| `gonetem-server` | Runs as root on the Linux host. Owns containers, namespaces, veth pairs, qdiscs, bridges and the OVS instance. Implements the `netem.Netem` gRPC service and keeps open projects in memory. |
| `gonetem-console` | Runs on the operator workstation. Implements the CLI, the interactive `[project]>` prompt, consoles, capture and project management. |

```text
console (CLI + prompt) --gRPC/TLS--> server (root)
                                        |- Docker nodes / OVS container
                                        |- network namespaces + veth pairs
                                        `- netem/tbf qdiscs, bridges, routes
```

Quick start
-----------

```bash
# on the server host (installed from source or .deb)
sudo systemctl start gonetem
gonetem-console pull                       # pull node images

# on the operator machine
gonetem-console create ./lab.gnet
gonetem-console open ./lab.gnet
```

Minimal topology (`edit`, then `reload`):

```yaml
nodes:
  host1:
    type: docker.host
  R1:
    type: docker.router
  host2:
    type: docker.host
links:
  - peer1: host1.0
    peer2: R1.0
    delay: 10        # ms, each direction
    loss: 0.5        # percent
    rate: 10000      # kbit/s
  - peer1: R1.1
    peer2: host2.0
```

```text
[lab]> check
[lab]> status
[lab]> console all
[lab]> capture R1.1
[lab]> save
[lab]> quit
```

Documentation
-------------

The complete manual is in [`docs/README.md`](docs/README.md), as plain Markdown
browsable on GitHub or in any local editor. Highlights:

* [Introduction](docs/introduction.md) — concepts and feature overview.
* [Getting started](docs/getting-started.md) — a guided first lab.
* [Installation](docs/installation.md) — build, `.deb`, service, images.
* [Configuration](docs/configuration.md) — every server and console key.
* [Topology reference](docs/topology.md) — nodes, links, QoS, bridges,
  management network and validation rules.
* [Node types and images](docs/nodes.md),
  [Docker images](docs/docker-images.md).
* [Commands](docs/commands.md) — CLI and prompt reference.
* [Switch console](docs/ovs.md) — VLANs, STP, bonding.
* [TLS](docs/tls.md) — securing a remote console/server link.
* [gRPC API](docs/api.md) — every RPC and streaming protocol.
* [Project file format](docs/project-format.md).
* [Architecture](docs/architecture.md) and internals
  ([server](docs/internals/server.md),
  [topology](docs/internals/topology.md),
  [links/QoS](docs/internals/link.md),
  [nodes](docs/internals/nodes.md),
  [console](docs/internals/console.md),
  [update](docs/internals/update.md)).
* [Development](docs/development.md),
  [testing](docs/testing.md) and the
  [SOLID/TDD refactor plan](docs/refactor-plan.md).
* [Deployment](docs/deployment.md),
  [troubleshooting](docs/troubleshooting.md),
  [FAQ](docs/faq.md), [glossary](docs/glossary.md).

Check the documentation locally:

```bash
python3 scripts/check-docs.py
```

Requirements
------------

Server:

* Linux with network namespaces, veth, bridge, `sch_netem`, `sch_tbf` (and
  `openvswitch`, `vrf`, `macvlan`, MPLS modules for the corresponding features);
* Docker Engine, running;
* root privileges;
* `nsenter` (util-linux) and `tar`.

Console: `xterm` for node consoles, `wireshark` for capture, `vim` (or another
editor) for `edit`.

Building from source
--------------------

```bash
make build-amd64      # bin/gonetem-server_amd64, bin/gonetem-console_amd64
make build-arm64
make build-armv7
```

Install on the server host (binaries, default configuration, systemd unit,
kernel tuning):

```bash
sudo make install-amd64      # or install-arm64
```

Debian packages (built with fpm in Docker):

```bash
docker build -t gonetem-build docker/build
make build-deb-amd64
```

The gRPC stubs are committed. After editing `internal/proto/netem.proto`:

```bash
make proto
```

Docker images
-------------

| Image | Node type |
| --- | --- |
| `mroy31/gonetem-host` | `docker.host` |
| `mroy31/gonetem-server` | `docker.server` |
| `mroy31/gonetem-frr` | `docker.router` |
| `mroy31/gonetem-bmv2` | `docker.p4sw` |
| `mroy31/gonetem-ovs` | `ovs` |

Images are pulled with `gonetem-console pull`; see
[docs/docker-images.md](docs/docker-images.md) for their
contents and build instructions (including VyOS).

Updating
--------

`gonetem-server` can update itself: with `update.enabled: true` in
`/etc/gonetem/config.yaml`, run `gonetem-console update`. The server clones the
configured repository/branch, downloads the configured Go toolchain, builds and
installs the new binaries, then restarts the service. See
[docs/internals/update.md](docs/internals/update.md).

Testing
-------

```bash
make check                                            # hermetic: fmt, lint, docs, unit, race, build
make test-integration                                 # root + Docker + kernel tests
python -m pytest -s tests/test_grpc_link_qos.py -v    # against a running server
```

The project follows specification-first development and Red-Green-Refactor.
See [CONTRIBUTING.md](CONTRIBUTING.md), [AGENTS.md](AGENTS.md) for AI agents,
[docs/development.md](docs/development.md) and
[docs/refactor-plan.md](docs/refactor-plan.md).

Technical debt
--------------

Known, accepted debt. Each item states why it is tolerated and what would pay it
down; open an issue before starting one. Details are in
[ADR-0005](docs/adr/index.md) and the [refactor plan](docs/refactor-plan.md).

* **Coverage outside critical logic.** Overall unit coverage is 46 % (server
  64.5 %, OVS 84 %, Docker 34 %, link 12 %, console 9 %); critical pure logic is
  80-100 %. The remainder is dominated by interactive console and privileged
  adapter paths that the hermetic suite cannot execute. **Follow-up:** add fakes
  for the remaining CLI/client paths or a privileged coverage profile.
* **Privileged CI is advisory.** The kernel/Docker integration suite and the
  Python gRPC end-to-end test need root, Docker and kernel modules, so they run
  nightly/manual rather than on every pull request. **Follow-up:** promote the
  integration workflow to a required check once its pass rate is stable.
* **Console configuration is process-global.** `options.ConsoleConfig` and the
  Cobra flag variables are still package-level mutable state; server
  configuration is fully injected. **Follow-up:** pass console configuration and
  flags through command construction when that code is next touched.
* **OVS compatibility registry.** `internal/ovs` keeps a package-level default
  registry and wrappers while the explicit `Registry` is already injected.
  **Follow-up:** thread `*Registry` through composition and remove the wrappers
  (ADR-0003).
* **The `Node` composite still exists.** The topology factory needs the broad
  interface to create heterogeneous nodes, while consumers use narrow roles.
  **Follow-up:** do not add methods to `Node`; introduce a role interface instead
  (ADR-0002).
* **Vulnerability exceptions.** `GO-2026-4883` and `GO-2026-4887` are
  allow-listed Docker daemon advisories that are not reachable from GoNetEm
  paths. **Follow-up:** re-review them on every Docker module upgrade and remove
  them when upstream fixes land.
* **No automated proto compatibility gate.** gRPC changes are reviewed for
  additive compatibility, but `netem.proto` has no `buf breaking`-style check.
  **Follow-up:** add a descriptor or breaking-change check to CI.

Contributing
------------

Development happens on `develop`; `master` receives releases only. Branch from
`develop`, open pull requests against it, and follow the six-stage
specification-first/TDD workflow. The pull request template asks for acceptance
criteria, RED/GREEN evidence, validation commands and residual risk.

Start with [CONTRIBUTING.md](CONTRIBUTING.md). AI agents must also read
[AGENTS.md](AGENTS.md).

Graphical user interface
------------------------

A separate project: <https://github.com/mroy31/gonetem-ui>.

Project layout
--------------

```text
cmd/                server and console entry points
internal/           server, console, docker, ovs, namespace, link, options, update, proto, utils
conf/               default server configuration, systemd unit, sysctl tuning
docker/             Docker image sources
scripts/            certificate/generation/migration helpers
tests/              Python gRPC integration tests
docs/               Markdown manual (local)
Makefile            build, install, packaging targets
VERSION             project version
```

License
-------

GNU General Public License v3.0 or later. See [COPYING](COPYING) for the full
text.
