.. _topology:

Topology file
=============

A project contains a single topology file named ``network.yml``. It is written
in YAML and has four top-level sections:

.. code-block:: yaml

   nodes:      # devices of the emulated network (required)
   links:      # point-to-point links between node interfaces
   bridges:    # connections to physical host interfaces
   mgntnet:    # optional management network

An empty topology is simply:

.. code-block:: yaml

   nodes:
   links:
   bridges:

The server parses the file, validates it, then instantiates the topology. The
same schema is used for the initial profile and for every later synchronisation
(``NodeAdd``, ``LinkAdd``, ... with ``sync: true``), so comments and formatting
are **not** preserved when the server rewrites the file; only semantics are.

Nodes
-----

Every node is declared as a key (its name) mapped to a configuration object.
Two fields are mandatory: the name and the ``type``.

.. code-block:: yaml

   nodes:
     host1:
       type: docker.host
     R1:
       type: docker.router
     sw1:
       type: ovs
     ns1:
       type: namespace

Node names must match ``^\w+$`` (letters, digits and underscore). An
Open vSwitch node name is additionally limited to 10 characters because it is
used as a Linux/OVS bridge name.

Node types
~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 20 80

   * - Type
     - Meaning
   * - ``docker.host``
     - Container emulating a user machine.
   * - ``docker.server``
     - Container emulating a service machine (HTTP/DHCP/TFTP/DNS/FTP/NTP).
   * - ``docker.router``
     - Container running FRR, emulating a router.
   * - ``docker.p4sw``
     - Container running the bmv2 P4 software switch.
   * - ``docker.<extra>``
     - Any type declared in ``docker.extraNodes`` in the server configuration
       (for example ``docker.vyos`` or a custom image).
   * - ``ovs``
     - Open vSwitch bridge (one port per connected interface).
   * - ``namespace``
     - Bare Linux network namespace; no container.

The type pattern accepted by validation is ``docker.\w+``, ``ovs`` or
``namespace``. A ``docker.`` type that is not configured on the server is
rejected when the topology loads.

Common node options
~~~~~~~~~~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 14 10 14 62

   * - Key
     - Type
     - Default
     - Description
   * - ``type``
     - string
     - —
     - Node type (see above). Mandatory.
   * - ``launch``
     - bool
     - ``true``
     - Start the node when the topology is run. With ``false`` the container is
       created lazily on ``start <node>`` and the node stays down when the
       topology starts.
   * - ``ipv6``
     - bool
     - ``true``
     - When true, the container is created with
       ``net.ipv6.conf.all.disable_ipv6=0`` so IPv6 is available. When false the
       sysctl is left untouched (the image default applies).
   * - ``mpls``
     - bool
     - ``false``
     - Enable MPLS on the node: sets ``net.mpls.platform_labels=100000`` and
       ``net.mpls.conf.lo.input=1`` in the container and enables MPLS input on
       every ``eth*`` interface. Requires the MPLS kernel modules on the host.
   * - ``image``
     - string
     - node type default
     - Override the Docker image for this node only. A version tag is added
       when the name has none.
   * - ``volumes``
     - list
     - ``[]``
     - Bind mounts added to the container, in ``/host/path:/container/path``
       form. The host path must exist. Merged with the volumes of the node type.
   * - ``vrfs``
     - list
     - ``[]``
     - Linux VRFs created at node configuration time, using routing tables
       10, 11, … in declaration order. Docker nodes only.
   * - ``vrrps``
     - list
     - ``[]``
     - VRRP groups to prepare (``docker.router`` only). Each entry has
       ``interface`` (int), ``group`` (1–255) and ``address`` (CIDR).
   * - ``mgnt``
     - object
     - disabled
     - Connect the node to the management network:
       ``enable`` (bool) and ``address`` (CIDR of the node's ``mgnt``
       interface).

Docker-specific behaviour:

* Containers run ``privileged`` with all capabilities and
  ``NetworkMode: none``; GoNetEm attaches them to the emulated topology by
  moving veth endpoints into the container network namespace.
* Interfaces are named ``eth0``, ``eth1``, … in the container, matching the
  interface index used in links and bridges.
* When a node is stopped, the server moves its interfaces to a private named
  network namespace and moves them back on start, so links and their QoS
  survive a stop/start cycle.
* ``vrfs`` are created before configuration files are loaded; ``vrrps`` create
  a macvlan interface named ``vrrp-<interface>-<group>`` with the standard VRRP
  MAC address ``00:00:5e:00:01:<group>`` and set
  ``net.ipv4.conf.<if>.ignore_routes_with_linkdown=1``.

Full Docker node example:

.. code-block:: yaml

   nodes:
     R1:
       type: docker.router
       ipv6: true
       mpls: true
       volumes:
         - /tmp:/tmp
       vrfs: [VRFA, VRFB]
       vrrps:
         - interface: 0
           group: 1
           address: 192.168.1.252/24
       mgnt:
         enable: true
         address: 192.168.0.1/24

Namespace nodes
~~~~~~~~~~~~~~~

A ``namespace`` node is a named Linux network namespace. It is much lighter
than a container and is intended for large topologies that only need L2/L3
forwarding or traffic shaping.

* The node shares the host filesystem and process namespace.
* Commands (``exec``, ``NodeRun``, consoles, capture) run on the host
  filesystem inside the node namespace, through ``nsenter``.
* ``image``, ``volumes``, ``mpls`` and ``vrfs`` are rejected.
* ``vrrps`` are rejected as well (they require a Docker node).
* ``ipv6`` is accepted but has no effect.
* ``mgnt`` is supported.
* Stopping a namespace node only marks it stopped: the namespace and its
  interfaces are kept for a later start. Removing the node or closing the
  project deletes the namespace.
* ``copy`` supports regular files only.

.. code-block:: yaml

   nodes:
     ns1:
       type: namespace
     ns2:
       type: namespace
       mgnt:
         enable: true
         address: 192.168.0.2/24
   links:
     - peer1: ns1.0
       peer2: ns2.0

Open vSwitch nodes
~~~~~~~~~~~~~~~~~~

An ``ovs`` node is a bridge inside the single Open vSwitch container of the
project. All OVS nodes of a project share that container; it is created lazily
on the first start and removed when the project closes.

Validation rejects ``mpls`` and ``vrfs`` on OVS nodes, and the management
network cannot be attached to them.

.. code-block:: yaml

   nodes:
     sw1:
       type: ovs
     sw2:
       type: ovs

Opening a console on an OVS node runs the switch console instead of a shell;
see :ref:`ovs`.

Links
-----

Links are declared as a list. Each link connects two interfaces, each
designated by ``<node>.<interface-index>``.

.. code-block:: yaml

   links:
     - peer1: host1.0
       peer2: R1.0

Interface naming:

* ``docker.*`` and ``namespace`` nodes expose ``eth<N>``, so peer ``host1.0``
  is ``eth0`` inside the node.
* ``ovs`` nodes expose ports named ``<switch>.<N>`` inside the OVS container.

Each ``<node>.<N>`` may be used **once** across all links and bridges; the
validator rejects duplicates. A link may not have identical peers.

Link keys:

.. list-table::
   :header-rows: 1
   :widths: 14 10 12 64

   * - Key
     - Type
     - Default
     - Description
   * - ``peer1``
     - string
     - —
     - First endpoint, ``<node>.<ifIndex>``. Mandatory.
   * - ``peer2``
     - string
     - —
     - Second endpoint, ``<node>.<ifIndex>``. Mandatory.
   * - ``delay``
     - int (ms)
     - ``0``
     - One-way egress delay applied to each direction.
   * - ``jitter``
     - int (ms)
     - ``0``
     - Delay variation. Requires a non-zero delay.
   * - ``loss``
     - float (%)
     - ``0``
     - Packet loss percentage, ``0``–``100``.
   * - ``rate``
     - int (kbit/s)
     - ``0``
     - Token-bucket rate limit. Requires a non-zero delay.
   * - ``buffer``
     - float
     - ``1.0`` when ``rate > 0``
     - Token-bucket limit expressed in bandwidth-delay products: the limit is
       ``buffer × rate × delay / 8`` bytes (rate in kbit/s, delay in ms).
       Requires ``rate``.
   * - ``burst``
     - int (bytes)
     - ``rate / 2``
     - Token-bucket burst size. Requires ``rate``.
   * - ``limit``
     - int (packets)
     - ``1000``
     - netem queue limit. Only used when netem is active (delay, jitter or
       loss). Bounds the packets held in the emulated queue.
   * - ``txqueuelen``
     - int (packets)
     - ``1000``
     - Transmit queue length of the interface(s) terminating the link.
   * - ``peer1qos``
     - object
     - —
     - Overrides all QoS values for the peer1 → peer2 direction.
   * - ``peer2qos``
     - object
     - —
     - Overrides all QoS values for the peer2 → peer1 direction.

The ``peer1qos`` and ``peer2qos`` objects accept ``delay``, ``jitter``,
``loss``, ``rate``, ``buffer``, ``burst``, ``limit`` and ``txqueuelen``.

.. _qos:

QoS model
~~~~~~~~~

QoS is applied **per direction, on egress**. A link with ``delay: 20`` adds
20 ms on ``peer1`` egress toward ``peer2`` *and* 20 ms on ``peer2`` egress
toward ``peer1``; a ping across that link therefore shows roughly 40 ms RTT.

* ``delay``, ``jitter`` and ``loss`` are implemented with the ``netem`` qdisc.
* ``rate`` is implemented with the ``tbf`` qdisc. The bucket delay used by tbf
  is ``delay + jitter``. When both shaping and netem impairments are
  configured, netem is attached as a child of the tbf inner queue (Linux
  allows only one root qdisc per interface).
* ``buffer`` sizes the tbf queue in bandwidth-delay products, not in packets.
  This makes buffer sizing follow the configured rate and delay.
* ``limit`` sizes the netem queue in packets; it is the right knob to emulate
  small router buffers (bufferbloat experiments).
* ``txqueuelen`` changes the interface transmit queue length and applies even
  when no netem/tbf qdisc is configured.

Per-direction override semantics:

* If ``peer1qos`` contains **at least one non-zero value**, it replaces the
  top-level QoS entirely for the peer1 → peer2 direction.
* If it is absent or all-zero, the top-level values are used.
* The same rule applies to ``peer2qos``.

Consequently, to make a link fast in one direction and impaired in the other,
leave the top-level QoS unset and specify only the direction to impair:

.. code-block:: yaml

   links:
     - peer1: host.0
       peer2: R1.0
       peer1qos:
         delay: 100
         rate: 1000
         buffer: 2.0
       # peer2qos omitted => no QoS in the R1 -> host direction

To have the same profile in both directions, use the top-level keys.

Validation rules for QoS
~~~~~~~~~~~~~~~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 20 22 58

   * - Field
     - Accepted range
     - Additional constraint
   * - ``delay``
     - 0 – 274000 ms
     - —
   * - ``jitter``
     - 0 – 274000 ms
     - Requires ``delay > 0``
   * - ``loss``
     - 0 – 100 %
     - —
   * - ``rate``
     - 0 – 17000000 kbit/s
     - Requires ``delay > 0``
   * - ``buffer``
     - ≥ 0
     - Requires ``rate > 0`` when non-zero
   * - ``burst``
     - ≥ 0
     - Requires ``rate > 0`` when non-zero
   * - ``limit``
     - 0 – 1000000 packets
     - ``0`` means the default (1000)
   * - ``txqueuelen``
     - 0 – 1000000 packets
     - ``0`` means the default (1000)

The same rules apply to the per-direction sections, with the error message
prefixed by ``link peer1`` or ``link peer2``. Violations are reported by
``check`` and abort ``run``/``reload``.

Symmetric example
~~~~~~~~~~~~~~~~~

.. code-block:: yaml

   nodes:
     host:
       type: docker.host
     sw:
       type: ovs
     R1:
       type: docker.router
   links:
     - peer1: host.0
       peer2: sw.0
       loss: 2
     - peer1: R1.0
       peer2: sw.1
       delay: 100       # 100 ms egress in each direction
       jitter: 10
       rate: 1024       # 1 Mbit/s
       buffer: 1.0
       burst: 16000
       limit: 500

Asymmetric example
~~~~~~~~~~~~~~~~~~

.. code-block:: yaml

   links:
     - peer1: R1.0
       peer2: sw.1
       peer1qos:
         delay: 100
         jitter: 10
         rate: 1024
       peer2qos:
         delay: 200
         jitter: 20
         rate: 2048

Bridges
-------

A bridge connects node interfaces to a physical host interface through a Linux
bridge, so the emulated network can reach the outside world.

.. code-block:: yaml

   bridges:
     uplink:
       host: eth0
       interfaces: [R1.2, host1.1]

.. list-table::
   :header-rows: 1
   :widths: 16 12 72

   * - Key
     - Type
     - Description
   * - ``host``
     - string
     - Name of an existing interface on the server host. Validation fails if it
       does not exist. The interface is enslaved to the bridge; be careful not
       to cut the management path of a remote host.
   * - ``interfaces``
     - list
     - Node interfaces to attach, in ``<node>.<ifIndex>`` form. They count as
       used peers and cannot appear in a link.

The bridge itself gets the kernel name ``ntm<project-id>.<short-node-name>``
and is created in the root network namespace while the topology runs.

Management network
------------------

``mgntnet`` creates a Linux bridge on the host with an IP address, and gives
each opted-in node a dedicated interface named ``mgnt`` connected to it. It is
the simplest way to SSH into emulated nodes from the host.

.. code-block:: yaml

   nodes:
     host1:
       type: docker.host
       mgnt:
         enable: true
         address: 192.168.0.1/24
     host2:
       type: docker.host
       mgnt:
         enable: true
         address: 192.168.0.2/24
   mgntnet:
     enable: true
     address: 192.168.0.254/24

.. list-table::
   :header-rows: 1
   :widths: 16 12 72

   * - Key
     - Type
     - Description
   * - ``mgntnet.enable``
     - bool
     - Create (and on close, remove) the management bridge.
   * - ``mgntnet.address``
     - string
     - CIDR address assigned to the bridge on the host.
   * - ``mgnt.enable``
     - bool
     - Per node: create the ``mgnt`` interface and connect it to the bridge.
   * - ``mgnt.address``
     - string
     - Per node: CIDR address assigned to the node's ``mgnt`` interface.

Rules:

* All addresses must be in the same subnet and must not conflict with other
  networks on the host.
* OVS nodes cannot join the management network.
* A management interface does not consume an ``eth<N>`` slot.

Validation
----------

The topology is validated by ``CheckTopology`` on load, on ``check`` and
before a run. Validation collects *all* errors and reports them together,
prefixed by ``topology is not valid:``.

.. list-table::
   :header-rows: 1
   :widths: 22 78

   * - Object
     - Rules
   * - Node
     - Name matches ``^\w+$`` and is unique; type matches
       ``^(?:docker\.\w+|ovs|namespace)$``; ``docker.<type>`` must exist on the
       server; OVS names are at most 10 characters; OVS nodes reject ``mpls``
       and ``vrfs``; namespace nodes reject ``image``, ``volumes``, ``mpls``,
       ``vrfs`` and ``vrrps``.
   * - Volumes
     - ``host:container`` syntax; the host path must exist.
   * - VRRP
     - ``docker.router`` only; group 1–255; interface ≥ 0; unique
       (interface, group); address is a valid CIDR.
   * - Management
     - Node ``mgnt.address`` is a valid CIDR; OVS nodes cannot use it;
       ``mgntnet.address`` is a valid CIDR.
   * - Link
     - Peers match ``^\w+\.\d+$``, reference an existing node, are used only
       once, and differ from each other; QoS fields pass the range checks above.
   * - Bridge
     - Name matches ``^\w+$`` and is unique; the host interface exists;
       interface peers follow the link rules.

Full example
------------

.. image:: ./images/topology.png
   :align: center
   :alt: Example of network topology

The topology below corresponds to the figure:

.. code-block:: yaml

   nodes:
     user1:
       type: docker.host
     user2:
       type: docker.host
     sw1:
       type: ovs
     sw2:
       type: ovs
     R1:
       type: docker.router
     R2:
       type: docker.router
   links:
     - peer1: user1.0
       peer2: sw1.0
     - peer1: sw1.1
       peer2: R1.0
     - peer1: R1.1
       peer2: R2.1
       delay: 100
       jitter: 10
       limit: 200        # small queue => observable queueing delay
     - peer1: R2.0
       peer2: sw2.0
     - peer1: sw2.1
       peer2: user2.0

Applying topology changes
-------------------------

There are three ways to change a running topology:

#. **Edit and reload** — ``edit`` modifies ``network.yml``, then ``reload``
   closes the runtime resources, reloads the file and starts again if the
   project was running. This is the simplest and most reproducible way.
#. **Live RPC calls** — ``NodeAdd``, ``NodeDel``, ``LinkAdd``, ``LinkDel``,
   ``LinkUpdate`` and their batch variants mutate the running topology and can
   persist the change with ``sync: true``. See :ref:`api`.
#. **Interface state** — ``ifState <node>.<N> up|down`` toggles a link
   endpoint without touching the topology file.

Live changes and the topology file
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

When an RPC is issued with ``sync: true``, the server regenerates
``network.yml`` from its in-memory model, atomically replacing the file. The
regenerated file:

* preserves all node and link configuration;
* preserves the management network section;
* **drops comments and original ordering**;
* is only written when the serialized content actually changed, to avoid
  pointless writes.

If a save is then performed, the regenerated file and the node configurations
are archived into the ``.gnet`` project.

See also
--------

* :ref:`nodes` — node type and image configuration.
* :ref:`commands` — ``edit``, ``check``, ``reload``, ``ifState``.
* :ref:`api` — programmatic topology changes.
* :ref:`project-format` — archive layout and configuration file naming.
