.. _getting-started:

Getting started
===============

This tutorial builds a four-node network from scratch and exercises the main
features of GoNetEm: project creation, topology editing, running, command
execution, QoS, capture, configuration snapshots and saving.

It assumes a working installation (see :ref:`installation`) and a
``gonetem-server`` listening on ``localhost:10110``.

Step 0 — prerequisites
----------------------

#. ``gonetem-server`` is running (``sudo systemctl start gonetem`` or
   ``sudo gonetem-server``) and the host has Docker.
#. ``gonetem-console version`` prints the client version and returns without
   error.
#. The Docker images are present on the server. Pull them once:

   .. code-block:: bash

      gonetem-console pull

   The command pulls every image referenced by the server configuration
   (router, host, server, P4 switch, OVS and all custom nodes) and prints one
   line per image. Re-running it is safe.

#. ``xterm`` and ``wireshark`` are installed on the operator machine if you
   want node consoles and packet capture.

If the console cannot reach the server, the error message reports the URI it
tried. Check the configuration with ``gonetem-console config show`` or pass
``--server host:port`` on the command line.

Step 1 — create a project
-------------------------

A project file (``.gnet``) is a gzipped tar archive. Creating one writes the
empty topology:

.. code-block:: bash

   gonetem-console create ./lab.gnet

You can confirm the content without opening it:

.. code-block:: bash

   tar tzf ./lab.gnet
   # network.yml

Step 2 — describe the topology
------------------------------

You can write ``network.yml`` directly, but the intended workflow is to open
the project and use the ``edit`` command, which uses the editor configured for
the console (``vim`` by default).

.. code-block:: bash

   gonetem-console open --no-start ./lab.gnet

The prompt appears as ``[lab]>``. Type ``edit`` and replace the content with:

.. code-block:: yaml

   nodes:
     host1:
       type: docker.host
     host2:
       type: docker.host
     R1:
       type: docker.router
     R2:
       type: docker.router
   links:
     - peer1: host1.0
       peer2: R1.0
     - peer1: R1.1
       peer2: R2.1
       delay: 20        # one-way delay, ms (applied to both directions)
       jitter: 2        # ms
     - peer1: R2.0
       peer2: host2.0

Save the editor buffer and leave it. The console uploads the new
``network.yml`` to the server. Before starting anything, validate it:

.. code-block:: text

   [lab]> check
   Network is OK

``check`` parses the file, verifies node names and types, peer syntax,
uniqueness, bridges, management network addresses, VRRP/VRF constraints and
QoS ranges. It is the fastest way to catch a typo.

Step 3 — start the network
--------------------------

Because the project was opened with ``--no-start`` it is not running yet:

.. code-block:: text

   [lab]> status
   Project lab
   - Id: XyZ
   - OpenAt: 2025-01-01 12:00:00
   - State: Not Running
   - Nodes:
      - host1: Not Running
      ...

Start it with ``run``:

.. code-block:: text

   [lab]> run
   Node - Start: 1/4 ...
   Node - Configuration: 1/4 ...
   Link - Create: 1/3 ...

The progress bars report node start, node configuration, link creation and
bridge creation. When ``run`` returns, ``status`` shows every node as
``Running`` and lists its interfaces:

.. code-block:: text

   [lab]> status
   ...
   - Nodes:
      - R1: Running
        - eth0: Up
        - eth1: Up

Opening the project without ``--no-start`` (``gonetem-console open ./lab.gnet``)
performs the run automatically.

Step 4 — configure addresses and routing
----------------------------------------

GoNetEm does not impose an addressing model: configure nodes with the tools
present in their image. For the tutorial, use ``exec``:

.. code-block:: text

   [lab]> exec host1 "ip addr add 10.0.1.1/24 dev eth0"
   [lab]> exec R1 "ip addr add 10.0.1.2/24 dev eth0"
   [lab]> exec R1 "ip addr add 10.0.12.1/24 dev eth1"
   [lab]> exec R2 "ip addr add 10.0.12.2/24 dev eth0"
   [lab]> exec R2 "ip addr add 10.0.2.2/24 dev eth1"
   [lab]> exec host2 "ip addr add 10.0.2.1/24 dev eth0"

Enable forwarding on the routers and install static routes:

.. code-block:: text

   [lab]> exec R1 "sysctl -w net.ipv4.ip_forward=1"
   [lab]> exec R2 "sysctl -w net.ipv4.ip_forward=1"
   [lab]> exec host1 "ip route add 10.0.2.0/24 via 10.0.1.2"
   [lab]> exec R1 "ip route add 10.0.2.0/24 via 10.0.12.2"
   [lab]> exec R2 "ip route add 10.0.1.0/24 via 10.0.12.1"
   [lab]> exec host2 "ip route add 10.0.1.0/24 via 10.0.2.2"

Verify end-to-end connectivity:

.. code-block:: text

   [lab]> exec host1 "ping -c 3 10.0.2.1"
   PING 10.0.2.1 (10.0.2.1) 56(84) bytes of data.
   64 bytes from 10.0.2.1: icmp_seq=1 ttl=62 time=42.1 ms
   ...

The round-trip time is about twice the configured 20 ms link delay (plus the
default forwarding overhead), which is exactly the expected emulated latency.

For FRR routers you will normally configure OSPF/BGP and static routes in
``vtysh`` instead of raw ``ip`` commands, and persist them with ``save`` so
they are reloaded next time. See :ref:`nodes`.

Step 5 — use consoles, copy and capture
---------------------------------------

Open a console on a node (spawns the terminal configured for the console):

.. code-block:: text

   [lab]> console R1      # vtysh for an FRR router
   [lab]> console host1   # bash for a host
   [lab]> console all     # one terminal per running node
   [lab]> shell host1     # force a bash shell

Copy a file into a node and back out:

.. code-block:: text

   [lab]> copy ./firmware.bin host1:/tmp/firmware.bin
   [lab]> copy host1:/tmp/result.log ./result.log

Capture an interface into Wireshark:

.. code-block:: text

   [lab]> capture R1.1

The server runs ``tcpdump -w -`` on the interface inside the node namespace
and streams the pcap to the console, which feeds it to ``wireshark -k -i -``.

Step 6 — change QoS at runtime
------------------------------

QoS can be set in the topology (as in step 2) or changed live. The console
prompt does not expose a QoS command; use the gRPC API — for example with the
``grpcurl`` tool or the Python client used by the integration tests. The
``LinkUpdate`` RPC replaces the QoS of one link:

.. code-block:: bash

   # using grpcurl with server reflection disabled and the proto file
   grpcurl -plaintext -import-path internal/proto -proto netem.proto \
     -d '{"prjId":"XyZ","link":{"peer1":"R1.1","peer2":"R2.1",
          "peer1qos":{"delay":100,"jitter":10,"loss":1,"limit":200},
          "peer2qos":{"delay":100,"jitter":10,"loss":1,"limit":200}},"sync":true}' \
     localhost:10110 netem.Netem/LinkUpdate

Then observe the effect:

.. code-block:: text

   [lab]> exec host1 "ping -c 5 10.0.2.1"

The RTT should move to roughly 200 ms and about 1 % of replies should be lost.
``LinkUpdateMany`` applies many links in one call and ``LinkSyncMany`` combines
adds, deletes and updates. See :ref:`api` for the complete request schema and
:ref:`qos` for the meaning of each field.

.. note::

   Setting a zero value in an update clears that impairment. ``rate`` without
   ``delay`` is rejected because the token bucket needs a latency budget;
   ``buffer`` is a multiple of the bandwidth-delay product, not a packet count.

Step 7 — save the project
-------------------------

``save`` snapshots the topology and the configuration of every running node,
then rewrites the local ``.gnet`` file:

.. code-block:: text

   [lab]> save

For a project opened with ``connect`` (no local path), use ``saveAs``:

.. code-block:: text

   [lab]> saveAs /home/me/lab-2025-01-01.gnet

Inspect what was captured — the archive now contains ``network.yml`` and a
``configs/`` directory:

.. code-block:: bash

   tar tzf ./lab.gnet
   # network.yml
   # configs/host1.net.conf
   # configs/host1.ntp.conf
   # configs/host1.init.conf
   # configs/R1.frr.conf
   # ...

The naming convention is ``<node-name>.<destSuffix>``, where ``destSuffix``
comes from the node type configuration. See :ref:`project-format` for the
complete layout and :ref:`commands` for ``viewConfig`` and ``config``.

Step 8 — shut down and clean up
-------------------------------

``quit`` (or ``exit``) closes the project. The server removes containers,
network namespaces, veth pairs, bridges, the management network and the shared
OVS container, and deletes the temporary directory:

.. code-block:: text

   [lab]> quit

If a previous server crashed, containers can be left behind. Prune the ones no
open project owns with:

.. code-block:: bash

   gonetem-console clean

Next steps
----------

* :ref:`topology` — complete topology file reference and validation rules.
* :ref:`nodes` — node options, FRR/VRF/VRRP, management network, custom images.
* :ref:`commands` — every console and prompt command.
* :ref:`ovs` — switch console: VLANs, trunks, STP and bonding.
* :ref:`tls` — secure a remote console/server link.
* :ref:`api` — automate GoNetEm from your own programs.
* :ref:`troubleshooting` — when something does not start.
