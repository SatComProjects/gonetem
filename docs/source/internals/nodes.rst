.. _internals-nodes:

Node implementations
====================

The topology engine manipulates nodes exclusively through the ``INetemNode``
interface. Three implementations exist: Docker nodes, Open vSwitch nodes and
network-namespace nodes. This page documents the contract and each
implementation.

The interface
-------------

.. code-block:: go

   type INetemNode interface {
       GetName() string
       GetShortName() string
       GetType() string
       GetFullType() string
       IsRunning() bool

       Start() error
       Stop() error
       Kill() error
       Close() error

       GetNetns() (netns.NsHandle, error)
       GetInterfaceName(ifIndex int) string
       AttachMgntInterface(ifName string, ns netns.NsHandle, IPAddress string) error
       AttachInterface(ifName string, ifIndex int, configure bool) error
       DetachInterface(ifName string) error
       ConfigureInterfaces() error
       SetInterfaceState(ifIndex int, state link.IfState) error
       GetInterfacesState() map[string]link.IfState

       LoadConfig(confPath string, timeout int) ([]string, error)
       Save(dstPath string, timeout int) error
       ReadConfigFiles(confDir string, timeout int) (map[string][]byte, error)

       ExecCommand(ctx context.Context, cmd []string, in io.ReadCloser, out io.Writer,
           tty bool, ttyHeight uint, ttyWidth uint, resizeCh chan term.Winsize) error
       RunCommand(cmd []string) ([]byte, []byte, int, error)
       GetConsoleCmd(shell bool) ([]string, error)
       Capture(ctx context.Context, ifIndex int, out io.Writer) error
       CopyFrom(srcPath, destPath string) error
       CopyTo(srcPath, destPath string) error
   }

.. list-table::
   :header-rows: 1
   :widths: 32 68

   * - Group
     - Contract
   * - identity
     - ``GetName`` is the topology name; ``GetShortName`` is the kernel-name
       fragment; ``GetType`` returns ``docker``/``ovs``/``namespace`` and
       ``GetFullType`` the full topology type (``docker.router``).
   * - lifecycle
     - ``Start`` is idempotent; ``Stop`` is idempotent; ``Close`` removes all
       resources and is safe to call once, even on a failed node; ``Kill``
       stops without removing.
   * - namespaces
     - ``GetNetns`` returns an open handle that the caller must close.
   * - interfaces
     - ``AttachInterface``/``DetachInterface`` maintain the node's interface
       bookkeeping; ``GetInterfaceName`` maps an index to the in-node name.
   * - configuration
     - ``LoadConfig`` is called once per start; ``Save`` captures; a node that
       is not running returns no-op success for save and empty data for reads.
   * - interaction
     - ``ExecCommand`` streams with an optional PTY; ``RunCommand`` captures
       output; ``GetConsoleCmd`` returns the argv for a console.
   * - files
     - ``CopyFrom``/``CopyTo`` transport single files.

``CreateNode`` (``internal/server/node.go``) dispatches on the topology type:

.. code-block:: go

   docker.<type>  -> docker.NewDockerNode(prjID, <type>, options)
   ovs            -> ovs.NewOvsNode(prjID, name, shortName)
   namespace      -> namespace.NewNamespaceNode(prjID, name, shortName)

Any other type is rejected. A Docker type that is not defined in the server
configuration is rejected by ``NewDockerNode``.

Docker nodes
------------

``internal/docker/node.go`` implements a container-backed device.

Creation
~~~~~~~~

``NewDockerNode``:

#. resolves the node type definition from ``docker.nodes`` or
   ``docker.extraNodes``;
#. computes the image (`image` from the topology overrides the type's image;
   the ``:0.6.0`` tag is appended when missing);
#. creates the container immediately unless ``launch: false``
   (``LazyCreate``).

``Create`` verifies that the image is present locally — it does **not** pull
images implicitly, so a missing image is an explicit error. The container is
created with:

.. code-block:: go

   HostConfig{
       NetworkMode: "none",
       Privileged:  true,
       CapAdd:      []string{"ALL"},
       Binds:       type.volumes + node.volumes,
       Sysctls:     {...},
   }
   Config{Image: image, Hostname: nodeName}

Sysctls:

* ``ipv6: true`` (default) sets
  ``net.ipv6.conf.all.disable_ipv6=0``;
* ``mpls: true`` sets ``net.mpls.platform_labels=100000`` and
  ``net.mpls.conf.lo.input=1``.

The container name is ``ntm<prjID>.<nodeName>`` and the hostname is the node
name.

Namespace dance
~~~~~~~~~~~~~~~

A Docker node has two possible network namespaces:

* the **container namespace** while the container runs;
* a **private named namespace** (``<prjID><nodeName>``) used to park the node's
  interfaces while the container is stopped.

This is the mechanism that preserves links across stop/start:

.. code-block:: text

   Start:
     create container if needed
     docker start
     resolve and cache the container PID
     currentNS = local named netns (created on demand)
     targetNS  = netns of the container PID
     MoveInterfacesNetns(interfaces, currentNS -> targetNS)
     prepare pending interfaces (TSO/MPLS)
     Running = true

   Stop:
     currentNS = container netns
     targetNS  = local named netns
     MoveInterfacesNetns(interfaces, currentNS -> targetNS)
     docker stop
     Running = false, pid = 0

Moving interfaces out before stopping is what keeps the veth pairs (and their
qdiscs) alive: the container's namespace disappears when it stops, and a veth
endpoint in it would be destroyed.

Both directions are rolled back on failure: if the stop fails, the interfaces
are moved back to the container namespace; if the start fails, they are moved
back to the local namespace and the container is stopped.

PID handling and namespace access
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

* The container PID is cached (``n.pid``) while it runs; it is invalidated on
  start, stop, kill and close. This avoids a Docker API call for every route
  installation or exec.
* ``GetRunningNetns`` uses ``netns.GetFromPid(pid)`` when the PID is cached and
  falls back to the Docker API on the first call after a start.
* ``GetNetns`` returns the running namespace when the container runs, otherwise
  the local named namespace.

Interface bookkeeping
~~~~~~~~~~~~~~~~~~~~~

``DockerNode.Interfaces`` maps the in-node interface name to:

.. code-block:: go

   type DockerInterface struct {
       Configured bool
       State      link.IfState
   }

* ``AttachInterface`` records the interface and, when ``configure`` is true and
  the node runs, immediately prepares it (TSO/MPLS);
* ``ConfigureInterfaces`` prepares every interface not yet configured;
* ``DetachInterface`` removes the bookkeeping entry (the kernel interface is
  deleted by the link layer);
* ``GetInterfacesState`` returns a copy of the map for link operations;
* ``SetInterfaceState`` updates the real interface and the recorded state.

Configuration lifecycle
~~~~~~~~~~~~~~~~~~~~~~~

``LoadConfig(confPath, timeout)`` is serialised by ``configMu`` and guarded by a
``ConfigLoaded`` flag, so multiple callers (run, start, restart) load the
configuration only once per start:

#. create the VRFs (tables 10, 11, …);
#. create VRRP macvlan interfaces (``vrrp-<if>-<group>``), set them up, add
   their address with ``ip addr add`` and set
   ``net.ipv4.conf.<if>.ignore_routes_with_linkdown=1``;
#. copy each declared configuration file that exists in ``configs/``;
#. copy and extract each configuration folder archive;
#. run the ``loadConfig`` commands whose ``checkFiles`` all exist;
#. collect non-empty output (forwarded to the user when ``options.log`` is
   true) and mark the node configured.

Docker exec calls respect the ``timeout`` (``docker.timeoutop``) via
``context.WithTimeoutCause``, so a stuck command cannot block a project run
forever.

``Save`` runs the ``saveConfig`` commands, then copies files and archives
folders out. It requires the node to be running and configured; otherwise it
returns success without doing anything (the previously saved files in the
project remain).

``ReadConfigFiles`` implements ``viewConfig``: if the node is running or was
configured, it saves into a temporary directory first; otherwise it reads the
project directory. Missing declared files are returned as empty blobs.

Interaction
~~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 24 76

   * - Method
     - Implementation
   * - ``ExecCommand``
     - ``DockerClient.ExecTty`` — interactive exec with TTY, stdin pipe, output
       writer, resize channel and context cancellation.
   * - ``RunCommand``
     - ``nsenter -t <pid> -a -- <cmd>`` on the host, capturing stdout, stderr
       and the exit code. Using ``nsenter`` gives the command the container's
       mount, network, PID and user namespaces, so it sees the node's
       filesystem.
   * - ``GetConsoleCmd``
     - Reads ``commands.console`` or ``commands.shell`` from the node type and
       splits it with ``shlex`` (arguments allowed).
   * - ``Capture``
     - ``docker exec tcpdump -w - -s 0 -U -i eth<N>`` streamed to a writer.
   * - ``CopyFrom``/``CopyTo``
     - Docker archive API through the client.
   * - ``GetStatus``
     - Inspects the container state through Docker.

``Kill`` stops the container and invalidates the PID. ``Close`` force-removes
the container, which also destroys its namespace, then deletes the local named
namespace and clears the interface map. No separate kill phase is needed before
close.

Open vSwitch nodes
------------------

OVS is split into two objects:

* ``OvsProjectInstance`` (``internal/ovs/instance.go``) — one container per
  project, shared by all switches;
* ``OvsNode`` (``internal/ovs/node.go``) — one bridge inside that container.

Instance
~~~~~~~~

``NewOvsInstance(prjID)``:

#. refuses to create a second instance for the same project (global mutex and
   map);
#. verifies the OVS image is present;
#. creates a container named ``ntm<prjID>.ovs`` with no network and a minimal
   port set.

``Start`` starts the container and waits for the OVS server to be ready by
retrying ``ovs-vsctl show`` up to 10 times with 100 ms pauses. This avoids a
race where the first ``add-br`` reaches a not-yet-initialised ``ovsdb-server``.

Bridge and port operations are serialised by a package-level mutex and recorded
in ``o.bridges``:

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Method
     - Command
   * - ``AddBr`` / ``DelBr``
     - ``ovs-vsctl add-br`` / ``del-br``
   * - ``AddBridgeWithPorts``
     - one ``ovs-vsctl --may-exist add-br ... -- --may-exist add-port ...``
       command with all ports (fast bridge start)
   * - ``AddPort`` / ``DelPort``
     - ``ovs-vsctl add-port`` / ``del-port``
   * - ``LoadConfig``
     - copies the config into ``/tmp/<name>.conf`` and runs
       ``ovs-config.py -a load -c ... <bridge>``
   * - ``SaveConfig``
     - ``ovs-config.py -a save -c /tmp/<name>.conf <bridge>`` then copies the
       file out
   * - ``Capture``
     - ``tcpdump`` inside the OVS container on ``<bridge>.<if>``
   * - ``Close``
     - force-removes the container

``GetNetns`` resolves the container PID and returns its namespace — that is
where all bridges and ports live, so link creation targets it.

Node
~~~~

``OvsNode`` holds the bridge name (= node name), a reference to the project
instance (resolved lazily through ``GetOvsInstance`` because nodes may be
created before the instance), its interface map and a running flag.

* ``Start`` creates the bridge with all known ports
  (``AddBridgeWithPorts``); on failure it deletes the bridge.
* ``Stop`` deletes the bridge but **keeps the interface map**, so a subsequent
  start re-adds the ports.
* ``AttachInterface`` adds the port when running and records the interface.
* ``DetachInterface`` deletes the port (a missing port is only a warning,
  because the kernel veth is usually deleted first) and removes the
  bookkeeping.
* ``ConfigureInterfaces`` is a no-op: the OVS container is already running and
  ports are configured by ``ovs-vsctl``.
* ``Close`` deletes the bridge; the instance itself is closed by the topology
  when the project closes.
* ``CopyFrom``/``CopyTo`` return "not supported".
* ``AttachMgntInterface`` returns an error: switches cannot join the management
  network.
* ``GetConsoleCmd`` returns ``/usr/bin/ovs-console.py <bridge>`` for a console
  and ``/bin/bash`` for a shell.
* ``RunCommand`` uses ``nsenter`` into the OVS container; ``ExecCommand`` uses
  the Docker exec TTY path on the OVS container.
* ``SetInterfaceState`` operates on the port interface inside the OVS
  namespace.

Because all switches share one container, starting a switch is cheap: it is one
``ovs-vsctl`` command, not a container start.

Namespace nodes
---------------

``internal/namespace/node.go`` implements a node that is only a network
namespace.

Namespace lifecycle
~~~~~~~~~~~~~~~~~~~

* The namespace is named ``ntm<prjID>.<nodeName>`` and lives in
  ``/run/netns``.
* ``ensureNetns`` creates it on demand, deletes a stale namespace with the same
  name first, sets ``lo`` up, and records ``initialized``.
* ``Start`` ensures the namespace exists and marks the node running.
* ``Stop`` only marks it stopped: the namespace, its interfaces and their qdiscs
  survive, so a restart reuses them.
* ``Close`` deletes the namespace (if it still exists) and clears interfaces.
* There is no container, so no image, volumes, MPLS, VRFs or VRRP (validation
  rejects them) and no management network restriction.

Because the namespace shares the host filesystem and process namespace,
``copy`` is a plain local copy and ``exec`` runs on the host.

Interaction
~~~~~~~~~~~

* ``RunCommand`` — ``nsenter --net=<path> -- <cmd>``, capturing stdout, stderr
  and exit code.
* ``ExecCommand`` — same, with a PTY (``creack/pty``) when requested. Resize
  events are applied with ``pty.Setsize``; ``EIO`` on the master side after the
  child exits is treated as a normal end of file.
* ``GetConsoleCmd`` — ``/bin/bash`` if present, otherwise ``/bin/sh``.
* ``Capture`` — ``nsenter --net=... tcpdump ...`` with stderr captured to
  enrich the error message.
* ``CopyFrom``/``CopyTo`` — local file copy, regular files only.
* ``LoadConfig``/``Save``/``ReadConfigFiles`` — no-ops returning empty data: a
  namespace node has no configuration management. Interface addresses and
  routes are whatever the operator configures with ``exec``/the API.

Capability comparison
---------------------

.. list-table::
   :header-rows: 1
   :widths: 28 24 24 24

   * - Capability
     - Docker
     - OVS
     - Namespace
   * - Container
     - yes
     - shared per project
     - no
   * - Config files
     - yes
     - yes (``<switch>.conf``)
     - no
   * - Console
     - type command
     - switch console
     - bash
   * - Capture
     - docker exec tcpdump
     - docker exec tcpdump
     - nsenter tcpdump
   * - Copy
     - yes
     - no
     - yes (regular files)
   * - QoS on links
     - yes
     - yes (ports)
     - yes
   * - Routes
     - yes
     - no
     - yes
   * - Management network
     - yes
     - no
     - yes
   * - MPLS / VRF / VRRP
     - yes
     - no
     - no
   * - Stop preserves links
     - yes (namespace parking)
     - yes (ports re-added)
     - yes (namespace kept)

Failure and idempotency rules
-----------------------------

* ``Start`` on a running node returns ``nil`` immediately.
* ``Stop`` on a stopped node returns ``nil`` immediately.
* ``Close`` is written to tolerate missing resources (a container already
  removed, a namespace already deleted) and returns the first real error.
* A failed Docker start rolls back the container state and the interface move.
* A failed OVS bridge creation deletes the partially created bridge.
* A failed namespace creation deletes partial state (``lo`` failure deletes the
  namespace).

Timeouts
--------

Configuration operations receive the configured ``docker.timeoutop`` in
seconds. Docker exec calls use a context with that timeout; Docker save calls
use it too. Operations that are not configuration-related (exec streams,
captures) rely on context cancellation from the RPC stream instead:
cancelling the client stream stops the server-side reader and the process.

Testing seams
-------------

* Docker node tests use the Docker client interface through
  ``internal/docker`` and are skipped when no daemon is available or when not
  root, depending on the test.
* OVS tests focus on the instance bookkeeping and command construction.
* Namespace node tests create real namespaces and therefore require root; they
  are skipped otherwise (``skipUnlessRoot``).

See also
--------

* :ref:`nodes` — user-facing node configuration.
* :ref:`internals-link` — the primitives these nodes call.
* :ref:`internals-topology` — how nodes are driven.
