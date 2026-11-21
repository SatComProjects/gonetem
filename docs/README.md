# GoNetEm documentation

GoNetEm is a **network emulator written in Go**. It builds reproducible virtual networks out of Linux network namespaces, Docker containers, veth pairs, Linux traffic control and Open vSwitch, and drives them from a command line console over a gRPC API.

Use GoNetEm to:

- prototype and validate network designs (routing, switching, VPNs, QoS) on a single Linux host;
- reproduce network conditions — delay, jitter, loss, rate limits and queue sizes — with kernel-accurate `tc` semantics;
- automate experiments from scripts or CI through a documented gRPC interface;
- teach networking with a textual, versionable topology file.

```bash
gonetem-console create ./lab.gnet   # empty project
gonetem-console open ./lab.gnet     # upload, start, open console prompts
```

```yaml
# network.yml — the topology stored inside a .gnet project
nodes:
  host1:
    type: docker.host
  R1:
    type: docker.router
  sw1:
    type: ovs
links:
  - peer1: host1.0
    peer2: R1.0
    delay: 10      # ms, both directions
    loss: 0.5      # percent
    rate: 10000    # kbit/s
  - peer1: R1.1
    peer2: sw1.0
```

## Architecture at a glance

GoNetEm is a client/server application:

`gonetem-server`

Runs as root on the Linux host. It owns the emulated resources: Docker containers, network namespaces, veth pairs, qdiscs, Linux bridges and the Open vSwitch instance. It exposes the `netem.Netem` gRPC service, keeps the set of open projects in memory and persists them as `.gnet` archives.

`gonetem-console`

Runs on the operator workstation. It implements the `gonetem-console` CLI (project lifecycle, image pull, update), the interactive `[project]>` prompt, the switch console, remote node consoles, packet capture to Wireshark and file copy to/from nodes.

See [Architecture](architecture.md) for the component and data model, and [Server internals](internals/server.md) for the server implementation.

## Documentation map

The manual is organised in five parts. If you are new to GoNetEm, read [Introduction](introduction.md) and [Getting started](getting-started.md) first.

**User guide**

- [Introduction](introduction.md)
- [Getting started](getting-started.md)
- [Usage patterns](usage.md)
- [Installation](installation.md)
- [Configuration](configuration.md)
- [Topology file](topology.md)
- [Node types and images](nodes.md)
- [Commands](commands.md)
- [Switch console](ovs.md)
- [TLS/SSL configuration](tls.md)

**Reference**

- [gRPC API](api.md)
- [Project file format](project-format.md)
- [Docker images](docker-images.md)

**Operations**

- [Deployment](deployment.md)
- [Troubleshooting](troubleshooting.md)
- [Frequently asked questions](faq.md)

**Architecture and internals**

- [Architecture](architecture.md)
- [Server internals](internals/server.md)
- [Topology engine](internals/topology.md)
- [Link, QoS and routing internals](internals/link.md)
- [Node implementations](internals/nodes.md)
- [Console internals](internals/console.md)
- [Self-update internals](internals/update.md)
- [Architecture decision records](adr/index.md)

**Contributing**

- [Development guide](development.md)
- [Testing](testing.md)
- [SOLID and TDD refactor plan](refactor-plan.md)
- [Glossary](glossary.md)

## Project links

- Source code: <https://github.com/satcomprojects/gonetem>
- Graphical user interface: <https://github.com/mroy31/gonetem-ui>
- License: GNU General Public License v3.0 or later (see `COPYING`)
- Version documented here: **0.9.0**
