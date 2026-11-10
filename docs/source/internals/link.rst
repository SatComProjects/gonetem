.. _internals-link:

Link, QoS and routing internals
===============================

``internal/link`` is the kernel-facing package. It creates veth pairs, bridges
and macvlans, moves interfaces between namespaces, applies traffic control and
installs routes. Nothing in this package shells out: everything goes through
netlink, the ``tc`` netlink API, or ioctls.

Namespace handling
------------------

Two primitives underpin everything:

.. code-block:: go

   func withNetns(ns netns.NsHandle, fn func() error) error {
       runtime.LockOSThread()
       defer runtime.UnlockOSThread()
       orig, _ := netns.Get()
       defer orig.Close()
       netns.Set(ns)
       defer netns.Set(orig)
       return fn()
   }

   func withNetlinkHandle(ns netns.NsHandle, fn func(*netlink.Handle) error) error

* ``withNetns`` switches the **current OS thread** to a namespace and restores
  it before the goroutine is rescheduled. Locking the thread is mandatory: the
  namespace is a per-thread property in Linux.
* ``withNetlinkHandle`` opens a netlink handle bound to a namespace
  (``netlink.NewHandleAt``) without switching the thread. This is the preferred
  form for read or single operations; it is used for interface lookups,
  deletion, state changes, address assignment and bridge operations.
* Several bulk helpers (``DeleteLinks``, ``MoveInterfacesNetns``,
  ``PrepareInterfaces``, ``ApplyQoSBatch``, ``ApplyRoutes``) enter the namespace
  once and process many interfaces/routes, which is the main performance
  optimisation of the package.

Link creation
-------------

``CreateVethLink`` sets both endpoint names and namespaces in the netlink
attributes, so the pair is created from the root namespace in one request
without a ``setns`` round trip:

.. code-block:: go

   veth := &netlink.Veth{
       LinkAttrs: netlink.LinkAttrs{
           Name: name, MTU: 1500, TxQLen: 1000,
           Namespace: netlink.NsFd(namespace),
       },
       PeerName:      peerName,
       PeerNamespace: netlink.NsFd(peerNamespace),
   }
   netlink.LinkAdd(veth)

Notes:

* MTU defaults to 1500 and the transmit queue to 1000 packets; ``txqueuelen``
  from the link QoS later changes the queue length if needed.
* A link is always created **between two network namespaces**: node-to-node
  links use the two node namespaces; bridge attachments use the root namespace
  and the node namespace; management links use the root namespace and the node
  namespace with the node-side name ``mgnt``.

Other primitives:

.. list-table::
   :header-rows: 1
   :widths: 28 72

   * - Function
     - Behaviour
   * - ``CreateNetns`` / ``DeleteNetns``
     - Named namespace in ``/run/netns``; the original namespace is restored
       after creation.
   * - ``CreateBridge``
     - Linux bridge created in a namespace and set up.
   * - ``CreateMacVlan`` / ``CreateVRRPMacVlan``
     - macvlan on a parent interface, optional MAC; VRRP uses
       ``00:00:5e:00:01:<group>`` and bridge mode.
   * - ``CreateVrf``
     - VRF with a routing table, set up.
   * - ``AttachToBridge``
     - ``LinkSetMaster``.
   * - ``DeleteLink`` / ``DeleteLinks``
     - Delete one or many interfaces; the batch variant enters the namespace
       once and aggregates errors with ``errors.Join``.
   * - ``RenameLink``
     - Rename then set up (used when an interface has to be moved).
   * - ``SetInterfaceState``
     - Up/down.
   * - ``MoveInterfacesNetns``
     - Move every interface of a map from one namespace to another, one
       netlink handle.
   * - ``IpAddressAdd``
     - Parse a CIDR with ``netlink.ParseAddr`` and add it.

Interface preparation (TSO and MPLS)
------------------------------------

Docker nodes used to run ``ethtool`` and ``sysctl`` through ``docker exec`` for
every interface. ``PrepareInterfaces`` replaces that with direct calls in the
node namespace:

.. code-block:: go

   PrepareInterfaces(ifNames, ns, disableTso, enableMpls)

* only ``eth*`` interfaces are processed (management interfaces are skipped);
* ``disableTso`` clears every **changeable** ``tx-checksum-*`` netdev feature
  by issuing ``ETHTOOL_SFEATURES`` through ``SIOCETHTOOL``. The feature list is
  read with ``ETHTOOL_GSSET_INFO`` + ``ETHTOOL_GSTRINGS`` +
  ``ETHTOOL_GFEATURES``; blocks are 32 features wide and the request is bounded
  (128 blocks);
* ``enableMpls`` writes ``1`` to
  ``/proc/sys/net/mpls/conf/<if>/input`` inside the namespace (the ``/proc``
  path resolves in the namespace selected by ``withNetns``);
* errors are aggregated per interface and the caller logs a warning rather than
  failing the node start.

The ``ifreq`` layout is architecture-dependent, so the ioctl wrapper uses
``unix.NewIfreq`` and copies the interface name into the ``IFNAMSIZ`` prefix
instead of hard-coding the structure size.

Traffic control
---------------

Handles
~~~~~~~

GoNetEm owns the qdisc tree of each link endpoint:

.. code-block:: text

   standalone netem:        root (1:0) -> netem
   standalone tbf:          root (1:0) -> tbf
   tbf + netem:             root (1:0) -> tbf  (inner 1:1) -> netem (2:0)

Constants:

* ``RootParent = tc.HandleRoot`` (``ffff:fff1``);
* ``RootHandle = 1:0``;
* ``TbfInnerParent = 1:1``;
* ``NetemChildHandle = 2:0``.

Because Linux allows only one root qdisc per interface, netem cannot be the root
when rate shaping is active; it is attached under the tbf inner queue instead.

Which qdiscs are installed
~~~~~~~~~~~~~~~~~~~~~~~~~~

.. code-block:: text

   tbf   if rate > 0
   netem if delay > 0 or jitter > 0 or loss > 0

Consequences visible to users:

* ``rate`` alone is rejected by validation: tbf's limit is computed from a
  latency, so a delay is required;
* ``buffer``/``burst`` require ``rate``;
* ``delay``/``jitter``/``loss`` alone install a root netem qdisc;
* changing a value that does not alter the qdisc *shape* uses ``change``;
  changing the shape deletes the root qdisc and rebuilds the tree.

Parameter conversion
~~~~~~~~~~~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 22 20 58

   * - Parameter
     - Kernel unit
     - Conversion
   * - ``delay``
     - tc ticks
     - ``core.Duration2TcTime(delay ms)`` → ``Time2Tick``
   * - ``jitter``
     - tc ticks
     - same conversion; validation requires a non-zero delay
   * - ``loss``
     - 32-bit fraction
     - ``uint32(percent / 100 * 2^32-1)``
   * - ``rate``
     - bytes/s
     - ``kbps * 125``
   * - ``burst``
     - bytes
     - explicit value, otherwise ``rate * 4 / 8`` (half the rate value:
       500 bytes at 1000 kbit/s)
   * - ``buffer``
     - bytes (tbf limit)
     - ``buffer * rate * delay / 8``; minimum 1514 bytes when rate > 0
   * - tbf latency
     - ms
     - ``delay + jitter``
   * - ``limit``
     - packets
     - explicit value, otherwise 1000
   * - ``txqueuelen``
     - packets
     - explicit value, otherwise 1000; applied with ``LinkSetTxQLen``

The formula for the tbf limit follows from
``kbps × 1000 b/s × ms / 1000 s / 8`` = ``buffer × rate × delay / 8`` bytes,
i.e. the configured delay times the configured rate, scaled by the BDP factor.
The default buffer of ``1.0`` therefore holds exactly one bandwidth-delay
product.

Applying QoS
~~~~~~~~~~~~

``ApplyQoS`` is a one-operation wrapper around ``ApplyQoSBatch``. The batch:

#. enters the namespace once;
#. opens **one** rtnetlink session (``tc.Open``);
#. for each operation: resolve the interface, optionally reset qdiscs, set
   ``txqueuelen``, add/change tbf, add/change netem;
#. returns one result per operation with the resulting qdisc flags.

Per-interface failures are reported in the result slice and do not abort the
other interfaces. This is what makes ``LinkUpdateMany`` efficient: all changes
for one node are applied in one namespace switch and one netlink session.

Resetting
~~~~~~~~~

``resetQdiscs`` deletes the qdisc at handle ``1:0`` with parent ``root``.
Deleting the root qdisc also deletes the chained netem, so no qdisc dump is
needed. ``ENOENT``/``EINVAL`` are treated as "nothing to reset".

``ResetQdiscs`` (single interface) is kept for callers that need a clean
interface.

Validation limits
~~~~~~~~~~~~~~~~~

The server validates QoS before it reaches this package. The limits are chosen
so that kernel conversions cannot overflow:

.. code-block:: text

   maxDelayMS      = 274_000      // tick conversion fits uint32
   maxRateKbps     = 17_000_000   // rate * 125 fits int32
   maxQueuePackets = 1_000_000

Routing
-------

``RouteSpec`` is the typed equivalent of:

.. code-block:: text

   ip route replace <destination> via <gateway> dev <device>

with ``gateway`` optional (on-link route) and an action
(``RouteReplace``/``RouteDelete``).

``ApplyRoutes(ctx, ns, specs)``:

#. opens one netlink handle in the namespace;
#. caches device index lookups per device name;
#. validates each spec: destination required, device required, valid action,
   gateway and destination in the same address family;
#. builds a ``netlink.Route`` with:

   * ``Family`` derived from the destination (``AF_INET``/``AF_INET6``);
   * ``Table = RT_TABLE_MAIN``;
   * ``Scope = SCOPE_UNIVERSE`` with a gateway, ``SCOPE_LINK`` otherwise;
   * ``Protocol = RTPROT_BOOT``;

#. calls ``RouteReplace`` or ``deleteRoute``;
#. attempts every spec, collecting failures, and returns the number of
   successful operations plus the joined errors;
#. stops early when the context is cancelled.

``deleteRoute`` first tries an exact delete. If the kernel rejects it (the
stored route differs, for example because it was installed by an older
release), it lists routes filtered by output interface, destination and table
and deletes the first entry matching link index, table, destination and
gateway. A route that is already absent (``ESRCH``/``ENOENT``) is a success, so
route deletion is idempotent.

The per-node wrapper in ``internal/server/route_install.go`` rejects OVS nodes
and stopped nodes before calling this function, and validates/parses the
protobuf specs.

Design notes and trade-offs
---------------------------

**No process spawning.** Interface setup, QoS and routes use netlink/ioctls.
This is much faster than ``docker exec ip ...`` and avoids depending on tools
inside the image. ``nsenter`` is still used for ``RunCommand``/``ExecCommand``
(Docker nodes) and for captures on namespace nodes, where a real process must be
started.

**One namespace switch per batch.** Namespace switching is expensive and, more
importantly, ``setns`` is per-thread; batching amortises it and keeps the thread
locked for the shortest possible time.

**Explicit namespaces in veth attributes.** Creating both endpoints in one
request avoids the classic "create veth, move one end, race with the scheduler"
pattern.

**Flags track kernel state.** The ``HasPeerNTbf``/``HasPeerNNetem`` flags avoid
a qdisc dump before every update. They are updated from the actual return of
the qdisc operations, and a shape change forces a reset, which makes stale
flags self-healing.

**Default queue length of 1000.** Both ``txqueuelen`` and the netem ``limit``
default to 1000 packets, matching the historical Linux defaults and keeping the
emulated buffer realistic. Use ``limit`` to emulate small buffers and
``buffer``/``burst`` to control rate shaping behaviour.

Known limits
------------

* one root qdisc per interface: an interface used for two links at once is
  impossible by construction (each peer is used once);
* ``rate`` must be accompanied by a delay (token-bucket latency);
* ``jitter > 0`` requires ``delay > 0`` (netem requirement);
* loss is a percentage with 32-bit granularity; very small values round;
* the tbf burst defaults to half the configured rate value in bytes, which is
  adequate for typical emulated rates but may be small for very high rates —
  set ``burst`` explicitly in that case;
* MPLS input must be enabled per interface; the server does it for
  ``mpls: true`` nodes, but MPLS on namespace nodes is not supported.

See also
--------

* :ref:`qos` — user-facing QoS reference and validation ranges.
* :ref:`internals-topology` — how QoS diffs and batches are orchestrated.
* :ref:`internals-nodes` — which node types call which primitives.
