.. _faq:

Frequently asked questions
==========================

General
-------

**Do I need root?**

The server, yes: it creates network namespaces, veth pairs, bridges and
qdiscs, and talks to the Docker daemon. The console never needs privileges.

**Can I run the server inside a container?**

Not officially. The server needs the Docker socket, privileged namespace
operations and kernel control. Running it in a privileged container that shares
the host network and PID namespaces may work, but it is unsupported and easy to
break (nested namespaces, kernel object names, cleanup).

**Can two servers run on the same host?**

They would collide: container names include the project ID only, and short
random IDs can collide, while bridge/veth names live in the same root
namespace. Run one server per host. Multiple hosts give independent emulators.

**Is there a graphical interface?**

Yes, a separate project: https://github.com/mroy31/gonetem-ui. It drives the
same gRPC API.

**Is Windows/macOS supported?**

The server requires Linux. The console builds for ``darwin-arm64``
(``make build-console-darwin-arm64``) and can connect to a remote Linux server
with TLS; see :ref:`tls` and :ref:`installation`.

**Which license?**

GNU General Public License v3.0 or later (``COPYING``).

Projects and files
------------------

**Where is my project stored?**

At the path you pass to ``gonetem-console create``/``open``. The server only
keeps an extracted copy in a temporary directory while the project is open.
Use ``save``/``saveAs`` to write changes back; closing a project discards the
server copy.

**What happens if the server restarts or crashes?**

Open projects are closed (clean restart) and their temporary directories are
removed. Unsaved runtime state is lost. Reopen the ``.gnet`` file. After a
crash, run ``gonetem-console clean`` to remove leftovers.

**Can I version a project in Git?**

Yes — it is a gzipped tar with text files. A useful practice is to keep the
project extracted in Git (``network.yml`` and ``configs/``) and build the
``.gnet`` in CI with ``tar -C dir -czf project.gnet .``.

**Why did my comments disappear?**

When the server rewrites ``network.yml`` (live change with ``sync``, or a
save), it serialises its in-memory model: comments and formatting are lost.
Keep documentation in a separate file or in the project's extra files.

**How do I move a project between servers?**

Copy the ``.gnet`` file. Images must exist on the target server
(``gonetem-console pull``) and the node types must be configured. Address
conflicts on the target (bridges, host interfaces) must be resolved first.

Nodes
-----

**What is the difference between a namespace node and a Docker host node?**

A ``namespace`` node is only a Linux network namespace: it shares the host
filesystem and processes, starts almost instantly and uses no image. A
``docker.host`` node is a container with its own filesystem and a full toolset
(ping, tcpdump, Scapy, iperf3, DHCP client, …), and can save/restore its
network configuration. Use namespace nodes for scale, Docker nodes for
realistic hosts and services.

**Do nodes have Internet access?**

No. Containers are created with ``NetworkMode: none`` and only see the emulated
links. To reach the outside, declare a ``bridges:`` entry connecting node
interfaces to a physical host interface. Be careful: enslaving the host's
uplink to a bridge can disconnect the host; use a dedicated interface or a
carefully configured bridge/NAT setup.

**Can I use my own Docker image?**

Yes. Either override ``image`` on a node in the topology, or declare a new
``docker.extraNodes`` type for a reusable device (see :ref:`nodes`). The image
must keep running, and ideally provide ``bash``, ``tcpdump`` and ``tar``.

**Can I use VyOS?**

Yes, as a custom node type. The repository documents how to build the image and
declare ``docker.vyos`` (:ref:`nodes`, :ref:`docker-images`).

**Why are containers privileged?**

GoNetEm needs to configure interfaces, addresses, routes and qdiscs inside the
node namespace, and to move interfaces across namespaces. That requires
capabilities and a unrestricted kernel context. It also means the gRPC API is
root-equivalent: protect it (:ref:`tls`).

**How many nodes can I run?**

There is no hard-coded limit, but containers cost memory and Docker operations
dominate start/close time. Namespace nodes scale much further. Tune the
concurrency pools and the kernel limits (:ref:`deployment`).

**How do I persist changes made inside a node?**

Only what the node type captures is saved: ``configurationFiles`` and
``configurationFolders`` after the ``saveConfig`` commands. For FRR, run
``write memory`` (or use ``save``, which runs ``vtysh -w``). For host/server
nodes, ``network-config.py`` saves addresses and routes. For anything else, use
an init script (``init.conf``) or copy the file into the project and declare it.

Emulation and QoS
-----------------

**Why is my ping RTT twice the configured delay?**

``delay`` is applied per direction on egress. A 20 ms link delays each packet
20 ms, and the reply another 20 ms: the round trip is ~40 ms. This is how
physical link latency is usually modelled. Use ``peer1qos``/``peer2qos`` for
asymmetric links.

**Why does rate require a delay?**

Rate is implemented with ``tbf``, whose bucket limit is computed from the
configured latency: ``buffer × rate × delay / 8`` bytes. Without a delay there
is no latency budget to size the bucket. Set a small delay (for example 1 ms)
if you only want shaping.

**What does the buffer option mean exactly?**

It is a multiplier on the bandwidth-delay product used as the tbf queue limit.
``1.0`` means the queue holds one BDP. It is **not** a packet count; use
``limit``/``txqueuelen`` for packet-based queue sizing.

**What is the difference between limit and txqueuelen?**

``limit`` is the netem queue length in packets (applies only when delay,
jitter or loss is configured). ``txqueuelen`` is the interface transmit queue
length (always applied). Both default to 1000 packets.

**How do I change QoS without restarting the project?**

Use the gRPC API: ``LinkUpdate`` for one link, ``LinkUpdateMany`` for several,
``LinkSyncMany`` to combine adds/deletes/updates in one call (:ref:`api`). The
console prompt has no QoS command; ``edit`` + ``reload`` also works for
topology-level changes.

**How do I add or remove nodes/links at runtime?**

Same answer: the API (``NodeAdd``, ``NodeDel``, ``LinkAdd``, ``LinkDel``), with
``sync: true`` to persist in ``network.yml``. The console prompt exposes
``ifState``, ``start``/``stop``/``restart`` but not topology editing.

**Does GoNetEm support IPv6?**

Yes at the node level: ``ipv6: true`` (the default) keeps IPv6 enabled in
containers, FRR can route it, and routes/QoS are address-family agnostic (the
route API accepts IPv4 and IPv6 destinations). The management network and most
examples use IPv4; IPv6 management addressing is parsed as a CIDR but is not a
documented/tested configuration.

**Does it support MPLS?**

Yes for Docker nodes with ``mpls: true`` and the kernel modules on the host
(:ref:`installation`). Namespace nodes do not support MPLS.

**Does it support VRF and VRRP?**

Yes for Docker nodes: ``vrfs`` creates Linux VRFs (tables 10, 11, …) and
``vrrps`` creates the macvlan interfaces required by FRR's VRRP (Docker
``docker.router`` only). See :ref:`nodes`.

**How do I emulate a link failure?**

``ifState <node>.<if> down`` in the prompt (or ``NodeSetIfState``). The link
and its QoS stay in place; only the endpoint goes down. The change is not
written to ``network.yml``.

Capture and debugging
---------------------

**Can I capture traffic without Wireshark?**

Yes. Either stream ``NodeCapture`` from your own client and write a pcap file,
or run ``tcpdump`` in the node and copy the file:

.. code-block:: text

   [lab]> exec host1 "tcpdump -i eth0 -w /tmp/cap.pcap -c 100"
   [lab]> copy host1:/tmp/cap.pcap ./cap.pcap

**Why is my capture empty?**

Generate traffic, check that the interface is up (``status``), and verify you
are capturing on the right index. On OVS nodes, ``capture sw1.0`` captures the
switch port named ``sw1.0`` inside the OVS container.

**Where are the logs?**

Server: ``/var/log/gonetem-server.log`` when installed as a service, otherwise
stderr. Console: printed messages only; there is no console log file.

**How do I see what the server is doing?**

Run it with ``-verbose`` and read the debug timings (``Run: all link setups
took ...``). The log includes project and node fields for most operations.

Administration
--------------

**How do I clean up after a crash?**

``gonetem-console clean`` removes gonetem containers that no open project owns.
Stale namespaces, bridges and veth pairs may need manual removal; see
:ref:`troubleshooting`.

**How do I upgrade the images?**

Pull the new tags and reference them explicitly in the server configuration
(or bump ``IMAGE_VERSION`` in a custom build). The server never pulls images on
its own.

**How do I secure remote access?**

Enable mutual TLS on both ends and restrict the port with a firewall
(:ref:`tls`). There is no username/password authentication: authentication is
the client certificate.

**Can several operators work at the same time?**

Yes, they share the server: projects are independent, but any operator can see
and close any project, and all have root-equivalent power. Coordination is
organisational, not enforced.

**Is there an API client library?**

Only the Go stubs generated from ``internal/proto/netem.proto`` and the Python
stubs generated in the integration tests. Any gRPC-supported language works
with the proto file (:ref:`api`).

**How do I report a bug?**

Open an issue at https://github.com/satcomprojects/gonetem/issues with the
information listed in :ref:`troubleshooting`.
