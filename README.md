gonetem
=======

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
------------

The complete manual is in [`docs/`](docs/) (Sphinx, reStructuredText) and is
configured for Read the Docs. Highlights:

* [Introduction](docs/source/introduction.rst) — concepts and feature overview.
* [Getting started](docs/source/getting-started.rst) — a guided first lab.
* [Installation](docs/source/installation.rst) — build, `.deb`, service, images.
* [Configuration](docs/source/configuration.rst) — every server and console key.
* [Topology reference](docs/source/topology.rst) — nodes, links, QoS, bridges,
  management network and validation rules.
* [Node types and images](docs/source/nodes.rst),
  [Docker images](docs/source/docker-images.rst).
* [Commands](docs/source/commands.rst) — CLI and prompt reference.
* [Switch console](docs/source/ovs.rst) — VLANs, STP, bonding.
* [TLS](docs/source/tls.rst) — securing a remote console/server link.
* [gRPC API](docs/source/api.rst) — every RPC and streaming protocol.
* [Project file format](docs/source/project-format.rst).
* [Architecture](docs/source/architecture.rst) and internals
  ([server](docs/source/internals/server.rst),
  [topology](docs/source/internals/topology.rst),
  [links/QoS](docs/source/internals/link.rst),
  [nodes](docs/source/internals/nodes.rst),
  [console](docs/source/internals/console.rst),
  [update](docs/source/internals/update.rst)).
* [Development](docs/source/development.rst) and
  [testing](docs/source/testing.rst).
* [Deployment](docs/source/deployment.rst),
  [troubleshooting](docs/source/troubleshooting.rst),
  [FAQ](docs/source/faq.rst), [glossary](docs/source/glossary.rst).

Build the HTML locally:

```bash
python3 -m venv /tmp/docsvenv
/tmp/docsvenv/bin/pip install -r docs/requirements.txt
/tmp/docsvenv/bin/sphinx-build -b html docs/source docs/build/html
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
[docs/source/docker-images.rst](docs/source/docker-images.rst) for their
contents and build instructions (including VyOS).

Updating
--------

`gonetem-server` can update itself: with `update.enabled: true` in
`/etc/gonetem/config.yaml`, run `gonetem-console update`. The server clones the
configured repository/branch, downloads the configured Go toolchain, builds and
installs the new binaries, then restarts the service. See
[docs/source/internals/update.rst](docs/source/internals/update.rst).

Testing
-------

```bash
sudo go test ./...                                    # unit + kernel tests
python -m pytest -s tests/test_grpc_link_qos.py -v    # against a running server
```

See [docs/source/testing.rst](docs/source/testing.rst).

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
docs/               Sphinx documentation
Makefile            build, install, packaging targets
VERSION             project version
```

License
-------

GNU General Public License v3.0 or later. See [COPYING](COPYING) for the full
text.
