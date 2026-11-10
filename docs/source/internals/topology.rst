.. _internals-topology:

Topology engine
===============

``NetemTopologyManager`` (``internal/server/topology.go``) is the heart of the
server. It owns the in-memory topology, the runtime resources, and the
synchronisation of ``network.yml``. This page documents its data structures,
algorithms and concurrency guarantees.

Data structures
---------------

.. code-block:: go

   type NetemTopologyManager struct {
       prjID  string
       path   string
       IdGenerator *NodeIdentifierGenerator

       nodes   []NetemNode              // *docker.DockerNode | *ovs.OvsNode | *namespace.NamespaceNode
       links   []*NetemLink
       bridges []*NetemBridge
       mgntNet *MgntNetwork
       ovsInstance *ovs.OvsProjectInstance
       needsOvs bool
       running  bool

       logger *logrus.Entry
       mu     sync.RWMutex   // data structures
       opMu   sync.RWMutex   // topology-wide operations
       linkIndex   map[linkKey]*NetemLink  // unordered peer pair -> link
       pendingLinks map[linkKey]struct{}   // keys being added/deleted
       updateBatchMu sync.Mutex            // serialises LinkUpdateMany batches
       qosBatchApplier func(...)           // test seam
       syncMu     sync.Mutex
       lastSynced []byte     // last content written to network.yml
   }

``NetemLink`` records, per direction, whether a tbf and a netem qdisc are
currently installed (``HasPeer1Tbf``, ``HasPeer1Netem``, …). The flags avoid
guessing the kernel state when a QoS change only needs a ``change`` instead of
a full reset.

``NetemLinkPeer`` is ``{Node INetemNode, IfIndex int}``. The interface name is
derived from the node (``eth<index>`` for containers/namespaces,
``<switch>.<index>`` for OVS).

Loading
-------

``Load`` is the only path that builds the model from the file:

#. ``CheckTopology(path/network.yml)`` parses and validates the file, returning
   the topology and all errors. Any error aborts the load with a message listing
   them all.
#. ``needsOvs`` is set if any node has type ``ovs``. The OVS container is
   **not** created here: it is created lazily by ``Run`` or by the first
   ``NodeAdd`` of an OVS node. ``ovsInstance`` is reset to ``nil``.
#. **Nodes** are created concurrently (``createConcurrency``). For each node
   the identifier generator produces a short name; ``CreateNode`` instantiates
   the right implementation. Docker nodes are created immediately unless
   ``launch`` is false (lazy creation). A failure aborts the load; nodes
   already created are closed by the caller (``ProjectOpen`` closes the
   topology and removes the directory).
#. **Links** are built sequentially from the validated list. Each peer string
   is split into ``(name, index)``; when ``rate`` is set and ``buffer`` is
   zero, the buffer defaults to ``1.0`` BDP. Nodes are resolved through
   ``GetNode``. The link index is rebuilt.
#. **Bridges** get a kernel name ``ntm<prjID>.<short-name>`` (the short name is
   allocated through the same generator, so bridge and node short names cannot
   collide). Each interface reference is resolved to a node.
#. **Management network**: if ``mgntnet.enable``, a ``MgntNetwork`` is created
   with bridge name ``<prjID>.mgnt``, the host-side address and the root
   namespace. Nothing is created in the kernel until ``Run``.
#. ``lastSynced`` is cleared so the first live synchronisation writes the file.

``Load`` is called at project open and at the end of a failed run (rollback).
It does not touch the kernel except for Docker container creation of
non-lazy nodes.

Running
-------

``Run`` takes ``opMu`` for writing and calls ``runLocked``:

.. code-block:: text

   runLocked:
     progress.Set(NODE_COUNT/BRIDGE_COUNT/LINK_COUNT)
     if already running: warn and return
     install rollback defer (close + reload) unless runSucceeded
     cache := new nodeNetnsCache()
     1. if needsOvs: ensureOvsInstance(start=true)
     2. if mgntNet != nil: mgntNet.Create()
     3. start nodes                        [startConcurrency]
          - Start() if LaunchAtStartup
          - setupMgntLink() if mgnt.enable
     4. setup links                        [linkConcurrency]
          setupLink(): create veth, apply both QoS, set up, attach
     5. setup bridges                      [linkConcurrency]
     6. configure interfaces + LoadConfig  [configConcurrency]
          - skip nodes that are not running
          - ConfigureInterfaces() (TSO/MPLS preparation)
          - LoadConfig(configs dir, timeout)
     7. running = true
     flush progress

Key points:

* **Ordering matters.** The OVS container must exist before nodes that are OVS
  bridges start; the management bridge must exist before nodes attach to it;
  interfaces must exist and be up before configuration files are loaded.
* **Parallelism is bounded** by the configured limits. Link setup is safe in
  parallel because every operation uses a thread-local namespace switch
  (``withNetns`` locks the OS thread) and restores it.
* **Progress** uses the batching described in :ref:`internals-server`.
* **Rollback**: if any step returns an error, the deferred function closes the
  runtime state and reloads the model. The project stays open in a
  not-running state, with a valid model — the operator can fix the topology and
  retry.

``Reload`` is ``close`` + ``Load`` + ``runLocked`` if the project was running,
all under ``opMu``.

Closing
-------

``Close`` takes ``opMu`` and calls ``closeLocked``:

#. ``running = false`` (so status queries immediately report the truth);
#. **nodes** are closed concurrently (``removeConcurrency``). A Docker node's
   ``Close`` force-removes the container, which destroys its netns; a namespace
   node deletes its named netns; an OVS node only deletes its bridge;
#. **bridges**: for every bridge peer, the root-side veth is deleted unless the
   peer's node already destroyed it by closing its netns (Docker and namespace
   nodes do; OVS peers do not). Failed nodes are tracked so their veths are
   still cleaned up. Then the bridge interfaces themselves are deleted. All
   deletions are attempted and errors are aggregated;
#. the model is emptied (nodes, links, indexes, bridges, pending links,
   ``needsOvs``) and the identifier generator is reset;
#. **OVS instance**: if this project created one, ``CloseOvsInstance`` removes
   the container;
#. **management network**: link deletion (only for interfaces that still
   exist), bridge deletion, namespace handle close.

Every phase logs its duration at info level, which is useful when a project
with hundreds of nodes takes too long to close.

Node add
--------

``NodeAdd`` takes ``opMu`` for writing because it may start a node:

#. reject an existing name;
#. ``checkNodeConfig(name, config, existingNames)`` applies the same validation
   as the file parser;
#. allocate a short name; for an OVS node, ensure the OVS instance exists (and
   start it if the topology is running);
#. ``CreateNode`` (container created immediately unless lazy);
#. append to the model;
#. if the topology is running: start the node when ``launch`` is true, then
   create its management link when requested;
#. on any failure, close the node and remove it from the model;
#. with ``sync``, release the data lock and call ``SynchroniseTopology``.

Node delete
-----------

``NodeDel`` takes ``opMu`` for writing. It is more involved because links and
bridges reference the node:

#. find the node and collect:

   * every link that has the node as a peer;
   * the bridge peer interface names belonging to the node;
   * the management interface name, if the node uses the management network;
   * whether the topology is running.
#. if running, delete the link veths **batched per node**: the interfaces are
   grouped by the link's peer1 node and each group is deleted with a single
   netlink handle (``DeleteLinks``). Deleting either end of a veth removes the
   pair, so deleting from the peer1 side is sufficient even when the removed
   node is peer2. Bridge-side veths live in the root namespace and are deleted
   in one batch there.
#. detach the interfaces from both peers in the model;
#. commit the removals: drop links referencing the node, rebuild the link
   index, remove the node's peers from bridges and from bridge configurations;
#. detach the management interface from the management bridge;
#. close the node **outside the data lock** (force-removing a container can take
   seconds and must not block status or read operations);
#. remove the node from the slice;
#. optionally synchronise the topology file.

Failure behaviour: if link or bridge teardown fails, the model is **not**
changed (the function returns early), so the operation can be retried. If the
container removal fails after the model was committed, the node stays removed
from the model but its container may linger; ``gonetem-console clean`` handles
that case.

Links and QoS
-------------

The single-item operations (``LinkAdd``, ``LinkDel``, ``LinkUpdate``) and the
batch operations share internal helpers:

.. list-table::
   :header-rows: 1
   :widths: 28 72

   * - Helper
     - Role
   * - ``linkKeyFor`` / ``linkKeyOf``
     - Order-independent key, peers normalised to ``<node>.<int>`` so ``A.00``
       and ``A.0`` are identical.
   * - ``ensureLinkIndexLocked``
     - Builds the peer index from ``links`` (called with ``mu`` held for
       writing).
   * - ``getLinkLocked``
     - Index lookup; falls back to a linear scan when the index is absent
       (tests construct managers directly).
   * - ``pendingLinks``
     - Reserved keys during add/delete so a concurrent operation cannot act on
       the same link; released on every path.
   * - ``linkAdd``
     - Validates, checks duplicates/pending, creates the veth and applies QoS
       when running, appends to the model and index.
   * - ``linkDel``
     - Removes the index entry and reserves the key **before** kernel work;
       restores the index entry if the kernel teardown fails.
   * - ``linkUpdate``
     - Locks the link, computes old/new QoS, applies only the directions that
       changed.
   * - ``applyLinkUpdateConfig``
     - Normalises the request orientation to the stored orientation and
       replaces all QoS fields, including zeros.

The per-direction QoS diff (``qosDiff``) decides between three kernel actions:

* ``reset`` — the shape of the configuration changes (tbf appears/disappears,
  netem appears/disappears, or the kernel flags disagree with the model):
  delete the root qdisc, then add the new tree;
* ``changeTbf`` — tbf remains, only parameters changed: ``tc qdisc change``;
* ``changeNetem`` — netem remains: ``tc qdisc change``.

The flags are then updated from the actual result returned by
``link.ApplyQoS``, so the model always reflects what was installed.

Locking and concurrency
-----------------------

Two topology locks with different roles:

``mu`` (RWMutex)
   Guards the fields: node/link/bridge slices, ``running``, ``linkIndex``,
   ``pendingLinks``, ``mgntNet``, ``ovsInstance``. Held only for short critical
   sections, never across Docker or netlink calls (with the documented
   exceptions noted below).

``opMu`` (RWMutex)
   Serialises topology-wide operations when taken for **writing**: ``Run``,
   ``Reload``, ``Close``, ``NodeAdd``, ``NodeDel``, ``Start``, ``Stop``, and
   link operations with ``sync: true``. Non-sync link operations take the
   **read** side, which lets multiple independent link adds/deletes/updates
   proceed concurrently with each other. They are excluded from run/reload/
   close/node changes.

Additional rules:

* ``updateBatchMu`` serialises ``LinkUpdateMany`` batches with each other. A
  batch locks several ``NetemLink`` objects; two batches touching the same
  links in opposite order could deadlock, so they never overlap. Adds and
  deletes do not take this mutex and remain concurrent with updates.
* Each ``NetemLink`` has its own ``mu``, taken for the whole duration of a
  per-link operation that touches qdiscs.
* ``syncMu`` serialises writes of ``network.yml``; ``lastSynced`` avoids
  rewriting identical content.
* The namespace cache is per operation (never shared across operations) and
  uses ``singleflight`` so parallel links to the same node open its namespace
  once.
* Lock order is consistent: ``opMu`` → ``mu``/``link.mu``; never the reverse.
  Code paths that must close a container release ``mu`` first by design.

What is safe to do concurrently:

.. list-table::
   :header-rows: 1
   :widths: 40 60

   * - Operations
     - Interaction
   * - ``LinkAdd``/``LinkDel``/``LinkUpdate`` with ``sync: false``
     - Run concurrently with each other and with ``LinkUpdateMany`` non-sync.
   * - ``LinkUpdateMany`` (non-sync) and single link updates
     - Concurrent with adds/deletes; batch vs batch is serialised.
   * - ``LinkSyncMany``
     - One lock for the whole batch; phases are internally parallel.
   * - ``TopologyRun``, ``TopologyReload``, ``Close``
     - Exclusive with every mutation.
   * - ``NodeAdd``, ``NodeDel``, ``Start``, ``Stop``
     - Exclusive (write side), because they can add/remove resources the other
       operations assume stable.
   * - Status/read RPCs
     - Use ``mu`` read side; not blocked by long kernel work except during the
       short data-structure sections.

Topology file synchronisation
-----------------------------

``SynchroniseTopology`` is the only writer of ``network.yml``:

#. snapshot the model under ``mu.RLock``, deep-copying the nested slices
   (``Volumes``, ``Vrfs``, ``Vrrps``, ``Interfaces``) because writers mutate
   them in place;
#. ``yaml.Marshal`` the ``NetemTopology`` projection;
#. under ``syncMu``, compare with ``lastSynced``; if equal, do nothing;
#. ``writeFileAtomic(path, data, 0644)``;
#. update ``lastSynced``.

``writeFileAtomic`` creates a temporary file in the same directory, writes,
``chmod``, ``fsync``, closes, then renames over the target. A crash can leave a
temporary ``.gonetem-network-*`` file, never a truncated ``network.yml``.

``WriteNetworkFile`` (the RPC behind ``edit``) also writes atomically and
updates ``lastSynced`` so a following live change does not produce a redundant
write.

Progress reporting
------------------

``progressBatcher`` (100 ms interval) coalesces increments for the codes
``START_NODE``, ``SETUP_LINK``, ``START_BRIDGE``, ``LOADCONFIG_NODE``,
``CLOSE_NODE`` and ``CLOSE_BRIDGE``. ``Set`` flushes pending counts and sends a
total immediately (used for the count messages at the beginning). The final
deferred ``Flush`` guarantees the last counts are delivered.

Node operations
---------------

``Start``/``Stop`` take ``opMu`` for writing and require the topology to be
running. ``startNode`` performs start + ``ConfigureInterfaces`` + ``LoadConfig``
and returns the node messages. ``stopNode`` only stops.

``Save`` iterates a snapshot of the nodes, creates ``configs/`` if needed, and
saves every **running** node concurrently (``configConcurrency``), reporting
``NODE_SAVE_COUNT`` and one ``NODE_SAVE`` per node.

``ReadConfigFiles`` delegates to the node with the project's ``configs``
directory and the configured operation timeout.

``GetAllNodes``/``GetNode``/``IsNodeLaunchAtStartup`` use the read lock and
return snapshots or interface values.

Node identifier generator
-------------------------

``NodeIdentifierGenerator`` allocates short names used in kernel object names:

* a name of at most 5 characters is used directly when free;
* otherwise, for split positions 2, 3 and 1 (then a single character), it
  builds ``<prefix><name[:idx]><name[-(4-idx):]>`` and prefixes it with each
  character of a 33-character alphabet until a free name is found;
* failure to find a name after all attempts is an error.

Examples for project ``Ab3``: node ``R1`` → short name ``R1``; node
``groundstation`` → something like ``agrng`` (readable prefix/suffix with a
unique leading character). The result keeps
``ntm<prj><short>.<ifIndex>`` below the 15-character interface limit.

Interaction with validation
---------------------------

``Check`` and ``Load`` both call ``CheckTopology``; ``checkNodeConfig`` is also
used by ``NodeAdd``. This guarantees that a node added live cannot be invalid
even though it never went through the file parser. The validation rules are
listed in :ref:`topology`.

Timings and profiling
---------------------

Every phase logs its duration at debug level:

* ``Load: CheckTopology took …``, ``Load: created N nodes in …``,
  ``Load: built N links in …``;
* ``Run: OVS start took …``, ``Run: all node starts took …``,
  ``Run: all link setups took …``, ``Run: node <name> config took …``;
* ``Close: node removal took …``, ``Close: total done in …``.

Enable with ``gonetem-server -verbose``. For CPU profiles, use the standard Go
tooling (``pprof`` is not exposed by the server; a temporary build with
``net/http/pprof`` or a test harness is required).

See also
--------

* :ref:`architecture` — data model and naming.
* :ref:`internals-link` — what happens under ``setupLink``.
* :ref:`internals-nodes` — node implementations.
* :ref:`api` — operations exposed to clients.
