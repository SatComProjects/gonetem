.. _docker-images:

Docker images
=============

GoNetEm emulates every device with a container or a namespace. This page
catalogues the images maintained in the ``docker/`` directory of the
repository, explains what each contains, how to build and publish them, and how
to derive your own.

Image versioning
----------------

Node types reference images by name in the server configuration
(:ref:`configuration`). When the name has no tag or digest, the server appends
``:0.6.0`` (the ``IMAGE_VERSION`` constant). Three forms are accepted:

.. code-block:: yaml

   image: mroy31/gonetem-host               # -> mroy31/gonetem-host:0.6.0
   image: mroy31/gonetem-host:0.6.0         # used as-is
   image: myrepo/host@sha256:0123abcd...    # digest, used as-is

The image version is independent of the binary version. Upgrading
``gonetem-server`` does not require new images, and rebuilding images does not
require a new server — but check the release notes when the node contract
changes (commands, configuration file paths).

Catalogue
---------

.. list-table::
   :header-rows: 1
   :widths: 20 24 56

   * - Image
     - Node type
     - Purpose
   * - ``mroy31/gonetem-host``
     - ``docker.host``
     - End-user machine with network testing tools.
   * - ``mroy31/gonetem-server``
     - ``docker.server``
     - Service machine: HTTP, DHCP, TFTP, DNS, FTP, NTP, QUIC.
   * - ``mroy31/gonetem-frr``
     - ``docker.router``
     - Router running FRR.
   * - ``mroy31/gonetem-bmv2``
     - ``docker.p4sw``
     - P4 software switch (bmv2 + PI + gRPC).
   * - ``mroy31/gonetem-ovs``
     - ``ovs`` (``docker.ovsImage``)
     - Open vSwitch instance shared by a project.
   * - ``gonetem-vyos`` (example)
     - ``docker.vyos`` (extra node)
     - VyOS router, built from a VyOS ISO.

All images are Debian Bookworm based except the VyOS image.

``gonetem-host``
~~~~~~~~~~~~~~~~

Multi-stage build: a Go stage compiles ``quic-client`` from
``github.com/mroy31/quic-go-example``; the runtime stage is
``debian:bookworm-slim``.

Installed tools (selection):

* network: ``iproute2``, ``net-tools``, ``iputils-ping``, ``iputils-arping``,
  ``iputils-tracepath``, ``traceroute``, ``hping3``, ``iperf``, ``iperf3``,
  ``tcpdump``, ``ethtool``, ``iptables``;
* clients: ``isc-dhcp-client`` (with a DNS update hook), ``tftp-hpa``
  (``tnftp``), ``bind9-dnsutils`` (``dig``/``nslookup``), ``curl``, ``wget``,
  ``telnet``, ``links``, ``ntp``, ``git``, ``snmp``;
* scripting: ``python3`` with ``pyroute2``, ``scapy`` and ``netifaces``;
* utilities: ``vim``, ``nano``, ``less``, ``file``, ``man``, ``htop``,
  ``psmisc``, ``at``.

Custom programs:

.. list-table::
   :header-rows: 1
   :widths: 26 74

   * - Program
     - Purpose
   * - ``/usr/local/bin/quic-client``
     - QUIC client used for transport experiments.
   * - ``/usr/bin/mult_send``
     - Multicast sender (``-g`` group, ``-i`` interval, port 5222, TTL 64).
   * - ``/usr/bin/mult_receive``
     - Multicast receiver.
   * - ``/usr/local/bin/traffic_ethernet``
     - Scapy-based Ethernet frame generator (``eth-gen.py``): destination MAC,
       ether type, message, count, interval, L2/L3/ICMP modes, LACP redundancy
       test.
   * - ``/usr/bin/network-config.py``
     - Saves/loads interface addresses, routes, bonds and VLANs as JSON.
   * - ``/usr/bin/iinit.py``
     - PID 1 replacement: sleeps until ``SIGTERM``/``SIGINT``.

``gonetem-server``
~~~~~~~~~~~~~~~~~~

Same multi-stage pattern, with ``quic-server`` instead of ``quic-client``.
Services and tools:

* ``lighttpd`` (HTTP), ``isc-dhcp-server`` and ``isc-dhcp-relay``,
  ``tftpd-hpa``, ``bind9``, ``pure-ftpd``, ``ntp``, ``snmp``, ``iperf``,
  ``iperf3``;
* ``network-config.py`` (same as host), ``iproute2``, ``tcpdump``, ``ethtool``,
  ``vim``, ``git``;
* an empty ``/var/lib/dhcp/dhcpd.leases`` is created at build time.

GoNetEm does not start these services automatically. Start and configure them
from the node, capture their configuration files, and use init scripts if they
must start on every node boot.

``gonetem-frr``
~~~~~~~~~~~~~~~

Installs FRR from the official ``deb.frrouting.org`` repository (Bookworm,
``frr-stable``), plus ``frr-pythontools``, ``frr-snmp`` and troubleshooting
tools (``tcpdump``, ``ping``, ``arping``, ``tracepath``, ``ethtool``,
``vim``, ``psmisc``, ``procps``).

Embedded configuration:

* ``/etc/frr/daemons`` enables ``bgpd``, ``ospfd``, ``pimd``, ``ldpd``,
  ``pbrd``, ``bfdd`` and ``vrrpd``; zebra and watchfrr are always started;
  OSPFv3, RIP, RIPng, IS-IS, NHRP, EIGRP, Babel, sharpd and fabricd are
  disabled;
* ``vtysh_enable=yes`` and ``frr_profile="traditional"``;
* daemons log to ``/var/log/frr/*.log``;
* ``/etc/frr/frr.conf`` contains ``service integrated-vtysh-config``;
* ``root`` is added to the ``frr`` group, so the server's ``vtysh`` calls work;
* ``/usr/bin/iinit.py`` is the entrypoint.

FRR is **not** started by the image: the node type runs
``/usr/lib/frr/frrinit.sh start`` as its ``loadConfig`` command after the saved
``/etc/frr/frr.conf`` has been copied in.

``gonetem-bmv2``
~~~~~~~~~~~~~~~~

A large multi-stage build compiling:

* **libyang** (commit ``13b20f9``);
* **sysrepo** (commit ``cbd2a30``), built with
  ``CALL_TARGET_BINS_DIRECTLY=Off`` so that installation does not execute
  ``sysrepoctl`` against the build host;
* **PI** (commit ``5689c91``), with ``--with-proto --with-sysrepo``;
* **behavioral-model** (commit ``0456d64``), with Thrift, PI, pdfixed, stress
  tests, debugger and gRPC support.

The runtime image installs the corresponding runtime libraries and
``python3-pyroute2``, ``tcpdump``, ``ethtool``, ``procps``, ``psmisc``,
``net-tools``, ``iproute2``, ``iptables`` and ``vim``.

Contents:

* ``/models/default.json`` — the compiled basic IPv4 forwarding pipeline
  (``basic.p4``); the ingress table is ``ipv4_lpm`` with the
  ``ipv4_forward`` action, default action drop;
* ``/usr/local/bin/simple_switch_grpc`` — the bmv2 switch (from the
  behavioral-model build);
* ``/usr/local/bin/launch-sw.py`` — collects ``eth*`` interfaces and starts
  ``simple_switch_grpc -i <n>@<if> ... /models/default.json``;
* ``/usr/local/bin/simple_switch_CLI`` — used as the node console;
* ``/usr/bin/iinit.py``.

``gonetem-ovs``
~~~~~~~~~~~~~~~

``debian:bookworm-slim`` with ``openvswitch-switch``, ``python3-cmd2``,
``python3-rich``, ``python3-pyroute2``, ``iproute2``, ``tcpdump``, ``net-tools``
and diagnostic tools. At build time it creates the OVS database
(``ovsdb-tool create /etc/openvswitch/conf.db``) and ``/var/run/openvswitch``.

Entrypoint ``/bin/run-ovs.sh`` starts:

.. code-block:: sh

   /usr/sbin/ovsdb-server --detach \
       --remote=punix:/var/run/openvswitch/db.sock \
       --pidfile=ovsdb-server.pid --remote=ptcp:6640
   /usr/sbin/ovs-vswitchd --pidfile

Additional programs:

* ``/usr/bin/ovs-console.py`` — the switch console (:ref:`ovs`);
* ``/usr/bin/ovs-config.py`` — loads/saves the switch configuration JSON.

One container is started per project when the topology contains an ``ovs``
node; it hosts every switch of that project.

VyOS image (extra node example)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

VyOS cannot be installed from packages inside a plain Dockerfile: it is built
from the official ISO. The procedure (also in ``docker/README.md``):

.. code-block:: bash

   # 1. import the ISO filesystem as a Docker image
   mkdir vyos-docker-build && cd vyos-docker-build
   mkdir rootfs
   sudo mount -o loop vyos-1.4.0-generic-amd64.iso rootfs
   sudo apt-get install -y squashfs-tools
   mkdir unsquashfs
   sudo unsquashfs -f -d unsquashfs/ rootfs/live/filesystem.squashfs
   sudo tar -C unsquashfs -c . | docker import - vyos:1.4
   sudo umount rootfs
   cd .. && sudo rm -rf vyos-docker-build

   # 2. build the gonetem image on top of it
   cd docker/vyos
   docker build -t gonetem-vyos:1.4 .

The image:

* replaces ``/opt/vyatta/etc/config/config.boot`` with a minimal VyOS
  configuration (user ``vyos``, console on ``ttyS0``);
* removes the systemd wants symlinks so the container does not try to start
  the full boot sequence as PID 1;
* uses ``/sbin/init`` as entrypoint;
* provides ``/usr/bin/start-vyos.sh`` (starts ``vyos-configd`` and
  ``vyos-router``, waits until the ``vyos`` user can log in) and
  ``/usr/bin/save-vyos-config.sh`` (``configure; save``).

It is not enabled by default; declare it in ``docker.extraNodes`` as shown in
:ref:`nodes`.

Building images
---------------

Single platform
~~~~~~~~~~~~~~~

Go into the image directory and build:

.. code-block:: bash

   cd docker/host
   docker build -t mroy31/gonetem-host:0.6.0 .

The same pattern applies to ``server``, ``frr``, ``bmv2``, ``ovs`` and
``build`` (the fpm packaging image):

.. code-block:: bash

   docker build -t gonetem-build docker/build

Multi-platform
~~~~~~~~~~~~~~

With Docker buildx:

.. code-block:: bash

   docker run --privileged --rm tonistiigi/binfmt --install all
   docker buildx create --use --name gonetem-builder

   cd docker/host
   docker buildx build \
     --platform linux/amd64,linux/arm64,linux/arm/v7 \
     -t mroy31/gonetem-host:0.6.0 --push .

Notes:

* ``gonetem-bmv2`` is slow to build (several C++ projects from source) and is
  best built natively per architecture;
* the ``frr`` image follows the FRR repository for Bookworm; if the repository
  layout changes, update the ``sources.list`` fragment in the Dockerfile;
* ``host`` and ``server`` images copy ``conf/sources.list``, which points at
  ``ftp.fr.debian.org``. Adapt it to a mirror close to your build
  infrastructure or to an internal mirror for offline builds.

Publishing and private registries
---------------------------------

Publish with ``docker push``. To use a private registry, set the full image
reference in the server configuration (``docker.nodes.<type>.image``,
``docker.extraNodes[].image``, ``docker.ovsImage``) and authenticate Docker on
the server host (``docker login``). GoNetEm uses the local Docker daemon, so
the daemon's credentials and registry configuration apply.

For air-gapped deployments, load images manually:

.. code-block:: bash

   docker save mroy31/gonetem-host:0.6.0 | gzip > host-0.6.0.tar.gz
   # on the server host
   gunzip -c host-0.6.0.tar.gz | docker load

``gonetem-console pull`` only pulls the images referenced by the server
configuration; it does not discover tags.

Deriving your own image
-----------------------

A node image needs very little:

#. a long-running PID 1 (the ``iinit.py`` pattern is enough);
#. the tools your experiments need (``ip``, ``ping``, ``tcpdump``, ``bash``,
   ``tar``);
#. optionally an application and a way to save its configuration.

Then declare a node type (:ref:`nodes`) pointing at it. Recommended
conventions:

* keep configuration files at predictable absolute paths so
  ``configurationFiles`` can capture them;
* provide ``/bin/bash`` and ``tcpdump``;
* install ``ethtool``-relevant features only if you need them; GoNetEm toggles
  TX checksum offload through an ioctl and does not require the ``ethtool``
  binary;
* do not start services in the entrypoint unless you always want them: the
  ``loadConfig`` commands run after GoNetEm has attached interfaces and copied
  configuration files.

Security notes
--------------

* GoNetEm creates every Docker node with ``Privileged: true`` and
  ``CapAdd: ALL``, and attaches it to no Docker network
  (``NetworkMode: none``). The container can therefore reconfigure its own
  kernel namespace freely, which is required for routing and qdiscs, but it
  means **anyone who can open a project can run privileged code on the host**
  through the API. Protect the gRPC endpoint accordingly (:ref:`tls`) and do
  not expose it to untrusted users.
* Images are executed with the host Docker daemon as root. Treat image pull
  credentials and registry access as privileged.
* ``volumes`` bind mounts give containers access to host paths; only mount what
  the experiment needs.

See also
--------

* :ref:`nodes` — node type configuration.
* :ref:`ovs` — using the switch image.
* :ref:`deployment` — production considerations.
