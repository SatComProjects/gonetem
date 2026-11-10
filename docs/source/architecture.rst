.. _architecture:

Architecture
============

GoNetEm is a two-process system: a privileged server that owns the emulated
resources and a console that drives it over gRPC. This page describes the
components, the data model, the naming scheme and the main design decisions.
Implementation depth is in the ``internals`` pages.

System view
-----------

.. code-block:: text

   +--------------------------- operator workstation ---------------------------+
   |                                                                           |
   |  gonetem-console                                                          |
   |    +-- CLI (cobra): create/open/connect/list/extract/pull/update/...     |
   |    +-- interactive prompt (go-prompt): console/exec/capture/save/...     |
   |    +-- gRPC client (plaintext or mutual TLS)                             |
   |    +-- local tools: xterm, wireshark, editor, filesystem                  |
   |                                                                           |
   +------------------------------------ | ------------------------------------+
                                        | gRPC netem.Netem
                                        | (tcp 10110, optional mTLS)
   +------------------------------------ | ------------------------------------+
   | gonetem-server (root)              v                                      |
   |                                                                           |
   |  gRPC service layer  (internal/server)                                    |
   |    project registry -> topology manager -> node implementations           |
   |                                                                           |
   |  +----------------+   +-------------------+   +------------------------+  |
   |  | Docker driver  |   | netlink / netns   |   | Open vSwitch driver    |  |
   |  | (docker pkg)   |   | (link pkg)        |   | (ovs pkg)              |  |
   |  +----------------+   +-------------------+   +------------------------+  |
   |           |                     |                        |                |
   +-----------|---------------------|------------------------|----------------+
               |                     |                        |
        Docker daemon          kernel netns/veth/tc      OVS container
        (containers)           (links, bridges, QoS)     (per project)

Components
----------

.. list-table::
   :header-rows: 1
   :widths: 22 78

   * - Component
     - Responsibility
   * - ``cmd/gonetem-server``
     - Flag parsing, logging setup, configuration loading, gRPC listener,
       signal handling, graceful shutdown.
   * - ``internal/server``
     - The ``Netem`` service implementation, the project registry, the
       topology manager, validation, node factory, route installation and the
       batch operations.
   * - ``internal/console``
     - The CLI and the interactive prompt, progress rendering, terminal
       consoles, capture plumbing, file copy, configuration commands.
   * - ``internal/proto``
     - The protobuf contract and generated Go stubs.
   * - ``internal/docker``
     - A thin Docker API client (create/start/stop/remove, exec, copy, image
       pull) and the Docker node implementation.
   * - ``internal/ovs``
     - The project-scoped OVS container and the OVS switch node.
   * - ``internal/namespace``
     - The bare network-namespace node.
   * - ``internal/link``
     - Everything kernel-side: veth pairs, bridges, macvlan, VRF, namespaces,
       netem/tbf qdiscs, routes, TX offload and MPLS toggles.
   * - ``internal/options``
     - Build version, server and console configuration structures, TLS
       credentials, image tag resolution.
   * - ``internal/update``
     - The server self-update pipeline.
   * - ``internal/utils``
     - Archive (project) and small helpers.

The dependency direction is strictly layered:

.. code-block:: text

   cmd -> console -> proto
        -> server -> {docker, ovs, namespace, link, options, update, utils}
        -> docker -> link
        -> ovs -> docker, link
        -> namespace -> link

``link`` is the only package that talks to netlink, ``tc`` and namespaces;
``docker`` is the only package that talks to the Docker API.

Process model
-------------

Server
   One process, one gRPC server, one goroutine per in-flight RPC (plus stream
   goroutines). There is no worker daemon and no persistent queue: all
   operations are synchronous within their RPC. Heavy phases use bounded
   ``errgroup`` pools. The server is expected to run as root.

Console
   One process per invocation. The CLI either performs one request and exits
   (``list``, ``pull``, …) or enters the prompt, where each command opens a new
   short-lived gRPC connection. Node consoles are separate
   ``gonetem-console console`` processes spawned inside terminals, each with its
   own bidirectional ``NodeExec`` stream.

Data model
----------

.. code-block:: text

   NetemProject                      (server/project.go)
     Id, Name, Dir, OpenAt
     Topology *NetemTopologyManager

   NetemTopologyManager              (server/topology.go)
     nodes   []NetemNode
     links   []*NetemLink
     bridges []*NetemBridge
     mgntNet *MgntNetwork
     ovsInstance *ovs.OvsProjectInstance
     running bool
     + locks and indexes

   NetemNode                         NetemLink
     Instance INetemNode               Peer1/Peer2 {Node, IfIndex}
     LaunchAtStartup bool              Config LinkConfig
     Config NodeConfig                 HasPeerNTbf / HasPeerNNetem flags

   NetemBridge                       MgntNetwork
     Name, ConfigName                  NetId, IPAddress
     HostInterface, Peers, Config      Interfaces, netns

   INetemNode (interface, server/node.go)
     implemented by *docker.DockerNode, *ovs.OvsNode, *namespace.NamespaceNode
     lifecycle: Start/Stop/Kill/Close
     namespace: GetNetns
     interfaces: Attach/Detach/Configure/SetState/GetInterfacesState
     config: LoadConfig/Save/ReadConfigFiles
     interaction: ExecCommand/RunCommand/GetConsoleCmd/Capture
     files: CopyFrom/CopyTo

The ``INetemNode`` interface is the central abstraction: the topology manager
never knows whether it is driving a container, a switch or a namespace. Adding
a new node family means implementing this interface (or reusing the Docker node
with a new image and type definition).

Topology model invariants
~~~~~~~~~~~~~~~~~~~~~~~~~

* a node name is unique within a project and matches ``^\w+$``;
* an interface ``<node>.<index>`` is used by at most one link or bridge;
* links are identified by an unordered pair of peers; ``A.0-B.1`` and
  ``B.1-A.0`` are the same link;
* every link has one ``NetemLink`` object while it exists, guarded by its own
  mutex, and qdisc state is tracked per direction (``HasPeer1Tbf``, …);
* the topology file is a projection of the in-memory model; it is only written
  on demand (``sync`` or save), atomically, and only when the content changes.

Naming scheme
-------------

Kernel object names are derived from the project ID and the node short name,
because Linux limits interface names to 15 characters (``IFNAMSIZ - 1``) and
because several projects can coexist on one host.

.. list-table::
   :header-rows: 1
   :widths: 34 30 36

   * - Object
     - Pattern
     - Example (project ``Ab3``)
   * - Docker container
     - ``ntm<prj>.<node>``
     - ``ntmAb3.R1``
   * - OVS container
     - ``ntm<prj>.ovs``
     - ``ntmAb3.ovs``
   * - OVS bridge
     - ``<switch name>``
     - ``sw1``
   * - OVS port
     - ``<switch>.<ifIndex>``
     - ``sw1.0``
   * - Linux bridge (bridges/mgnt)
     - ``ntm<prj>.<short>`` / ``<prj>.mgnt``
     - ``ntmAb3.b1`` / ``Ab3.mgnt``
   * - Bridge veth, root side
     - ``ntm<prj><short>.<ifIndex>``
     - ``ntmAb3b1.0``
   * - Node interface
     - ``eth<ifIndex>``
     - ``eth0``
   * - Management interface
     - ``mgnt`` (node side), ``ntm<prj><short>.m`` (root side)
     - ``mgnt`` / ``ntmAb3R1.m``
   * - Docker node netns while stopped
     - ``<prj><node>``
     - ``Ab3R1``
   * - Namespace node netns
     - ``ntm<prj>.<node>``
     - ``ntmAb3.ns1``
   * - qdisc root handle
     - ``1:0`` (netem child ``2:0`` under ``1:1``)
     - —

The **short name** is generated per project by ``NodeIdentifierGenerator``: a
name of at most five characters is used as-is if free; otherwise the generator
builds a compact name from a prefix and suffix of the node name, prefixed by a
character, and retries until it is unique. It keeps names below the kernel
limit while remaining readable in ``ip link`` output.

Request lifecycle
-----------------

A typical ``exec`` from the prompt:

.. code-block:: text

   prompt "exec R1 \"ip addr show\""
     -> new gRPC connection to the server
     -> Server.NodeExec (bidi stream)
        -> ProjectGetOne(prjId)                  (registry read lock)
        -> Topology.GetNode("R1")                (topology read lock)
        -> DockerNode.ExecCommand
           -> DockerClient.ExecTty
              -> daemon: exec with TTY
        -> stream STDOUT chunks, then CLOSE
     -> console prints output, closes the connection

A ``run`` from the prompt:

.. code-block:: text

   TopologyRun
     -> Topology.Run (opMu write lock)
        -> runLocked
           1. OVS instance start (if any ovs node)
           2. management bridge creation (if mgntnet enabled)
           3. start nodes           [startConcurrency]
           4. create links + qdiscs [linkConcurrency]
           5. create bridges        [linkConcurrency]
           6. configure interfaces + load configs [configConcurrency]
           -> running = true
     -> progress messages streamed while phases run
     -> final NODE_MESSAGES

Concurrency model (summary)
---------------------------

.. list-table::
   :header-rows: 1
   :widths: 30 70

   * - Mechanism
     - Purpose
   * - ``projectsMu`` (RWMutex)
     - Protects the open-project registry (reads dominate).
   * - ``projectOpsMu`` (Mutex)
     - Serialises open/save/close/get-config operations.
   * - topology ``mu`` (RWMutex)
     - Protects the topology data structures (nodes, links, bridges,
       ``running``, indexes).
   * - topology ``opMu`` (RWMutex)
     - Serialises topology-wide operations (run, reload, close, node add/del,
       start/stop) when taken for writing; non-sync link operations take it for
       reading and can run concurrently.
   * - ``NetemLink.mu`` (Mutex)
     - Serialises updates to one link's configuration and qdisc state.
   * - ``updateBatchMu`` (Mutex)
     - Prevents overlapping ``LinkUpdateMany`` batches (they lock several links
       at once).
   * - ``syncMu`` + ``lastSynced``
     - Serialises topology-file writes and skips unchanged content.
   * - ``pendingLinks``
     - Reserves a link key during add/delete so concurrent operations cannot
       race on the same link.
   * - ``nodeNetnsCache``
     - Caches namespace handles per operation and uses ``singleflight`` so a
       namespace is opened once even under parallel link setup.
   * - bounded ``errgroup`` pools
     - Limits Docker and netlink pressure: create/start/remove/config/link/
       route concurrency (see :ref:`configuration`).

Full details, including lock ordering and the guarantees of each operation, are
in :ref:`internals-topology`.

Failure handling and consistency
--------------------------------

* A failed ``TopologyRun`` rolls back: the runtime resources are closed and the
  topology model is reloaded, so the project remains usable and consistent.
* A failed node start during ``NodeAdd`` on a running topology removes the node
  from the model and closes it.
* A failed link delete keeps the link in the index (the kernel teardown did not
  happen), so a retry is possible.
* Batch operations report per-item failures and continue; the response carries
  one status per item.
* ``network.yml`` is written through a temporary file, ``fsync``, then
  ``rename``, so a crash cannot leave a truncated topology file.
* Project opening rejects archive paths that escape the destination directory.

Security model
--------------

* The server runs as root and requires root for namespace creation and Docker.
* Docker nodes are privileged containers with all capabilities and no Docker
  network; the emulated topology is the only network they see.
* The gRPC API grants the same power as shell access to the host. Use loopback
  or mutual TLS plus a firewall.
* Node images run arbitrary software: only pull images you trust.
* Project archives are untrusted input and are validated (entry types, path
  traversal) before extraction.

Package map
-----------

.. list-table::
   :header-rows: 1
   :widths: 24 18 58

   * - Package
     - Key files
     - Contents
   * - ``cmd/gonetem-server``
     - ``main.go``
     - Startup, flags, TLS, signals, graceful stop.
   * - ``cmd/gonetem-console``
     - ``main.go``
     - Bootstrap of configuration and CLI.
   * - ``internal/server``
     - ``server.go``, ``topology.go``, ``project.go``, ``node.go``,
       ``check.go``, ``mgnt.go``, ``route_install.go``,
       ``link_*_batch.go``
     - gRPC handlers, topology engine, validation, project registry.
   * - ``internal/console``
     - ``commands.go``, ``prompt.go``, ``exec.go``, ``config.go``,
       ``completer.go``, ``progress.go``, ``remote.go``
     - CLI, prompt, PTY handling, UI.
   * - ``internal/link``
     - ``link.go``, ``tc.go``, ``route.go``, ``features.go``
     - Namespaces, veth, bridges, qdiscs, routes, offloads.
   * - ``internal/docker``
     - ``client.go``, ``node.go``
     - Docker API wrapper and Docker node.
   * - ``internal/ovs``
     - ``instance.go``, ``node.go``
     - Shared OVS container and switch node.
   * - ``internal/namespace``
     - ``node.go``
     - Namespace node.
   * - ``internal/options``
     - ``server.go``, ``console.go``, ``tls.go``
     - Configuration, defaults, version, TLS.
   * - ``internal/update``
     - ``update.go``
     - Self-update pipeline.
   * - ``internal/proto``
     - ``netem.proto``, generated stubs
     - API contract.
   * - ``internal/utils``
     - ``archive.go``
     - Project archive, helpers.

Extension points
----------------

.. list-table::
   :header-rows: 1
   :widths: 26 74

   * - Goal
     - Where to change
   * - New device family with an existing image
     - Add a ``docker.extraNodes`` entry; no code change.
   * - New device family with new behaviour
     - Implement ``INetemNode`` and add it to ``CreateNode``.
   * - New node/session option
     - Extend ``NodeConfig`` (YAML) and ``NodeConfigMsg`` (proto) and plumb it
       through ``getNodeConfigFromRequest``/``CreateNode``.
   * - New API operation
     - Add the RPC and messages to ``netem.proto``, regenerate stubs, implement
       the handler in ``internal/server``.
   * - New QoS parameter
     - Add it to ``QoSConfig``/``QoS``, validation, ``tc`` application and the
       proto ``QoSConfig``.
   * - New console command
     - Register it in ``NetemPrompt.RegisterCommands`` and implement the
       action; extend the completer if it takes a node.
   * - New image
     - Add a ``Dockerfile`` under ``docker/``, publish it, reference it in the
       server configuration.

Next
----

* :ref:`internals-server` — RPC handlers, streaming protocols, shutdown.
* :ref:`internals-topology` — load/run/reload/close, locking, synchronisation.
* :ref:`internals-link` — veth/QoS/routes implementation.
* :ref:`internals-nodes` — Docker, OVS and namespace nodes.
* :ref:`internals-console` — CLI and prompt implementation.
* :ref:`internals-update` — self-update.
