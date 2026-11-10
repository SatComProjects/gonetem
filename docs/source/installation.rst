.. _installation:

Installation
============

GoNetEm is made of two binaries plus a set of Docker images:

``gonetem-server``
   The emulator daemon. It needs root access to create network namespaces,
   veth pairs, bridges and qdiscs, and to talk to the Docker daemon. Installed
   in ``/usr/sbin`` by the Debian package or ``/usr/local/bin`` by
   ``make install-*``.

``gonetem-console``
   The CLI client. It does not need special privileges. Installed in
   ``/usr/bin`` or ``/usr/local/bin``.

Docker images
   One image per node family (host, server, FRR router, bmv2 P4 switch, OVS).
   They are pulled from Docker Hub by ``gonetem-console pull``; see
   :ref:`docker-images`.

System requirements
-------------------

Server host
~~~~~~~~~~~

* Linux. GoNetEm is developed and tested on Debian/Ubuntu-like distributions;
  any distribution with a modern kernel works.
* Root privileges for the daemon (namespaces, netlink, Docker socket).
* Docker Engine (``docker-ce`` or the distribution's ``docker.io``), running
  and reachable through ``/var/run/docker.sock``.
* ``util-linux`` (``nsenter``) and ``tar``, used for command execution in
  containers and for configuration archives.
* Kernel features:

  .. list-table::
     :header-rows: 1
     :widths: 30 70

     * - Feature
       - Needed for
     * - network namespaces
       - Every node (containers and ``namespace`` nodes).
     * - ``veth`` pairs
       - Every link, bridge attachment and management interface.
     * - ``bridge``
       - Node-facing bridges (``bridges:``) and the management network.
     * - ``sch_netem``, ``sch_tbf``
       - Delay/jitter/loss (netem) and rate shaping (tbf). Usually loaded
         automatically on first use.
     * - ``openvswitch``
       - ``ovs`` nodes. The OVS container runs ``ovsdb-server`` and
         ``ovs-vswitchd``; the host kernel must provide the datapath
         (``modprobe openvswitch``) unless the image is configured for a
         userspace datapath.
     * - ``vrf``
       - The ``vrfs:`` option of Docker nodes.
     * - ``macvlan``
       - The ``vrrps:`` option of ``docker.router`` nodes.
     * - ``mpls_router``, ``mpls_iptunnel``
       - The ``mpls: true`` option.

* Optional tuning: increase the ARP neighbour table and inotify limits with
  the provided ``conf/99-custom-network.conf`` (installed by
  ``make install-*``), see :ref:`deployment`.

Operator workstation
~~~~~~~~~~~~~~~~~~~~

* ``gonetem-console`` for the same platform.
* ``xterm`` to open node consoles (the terminal command is configurable, so
  any X terminal works).
* ``wireshark`` for the ``capture`` command.
* ``vim`` (or any editor) for ``edit``.

Supported build targets
-----------------------

.. list-table::
   :header-rows: 1
   :widths: 20 20 25 35

   * - Target
     - GOARCH
     - Binaries
     - Notes
   * - ``amd64``
     - amd64
     - server + console
     - Primary target; Debian package available.
   * - ``arm64``
     - arm64
     - server + console
     - Debian package available; ``make install-arm64`` exists.
   * - ``armv7``
     - arm (GOARM=7)
     - server + console
     - Debian package available; no ``install-armv7`` target.
   * - ``darwin-arm64``
     - arm64, Darwin
     - console only
     - For a Mac operator machine; the server remains Linux-only.

The self-update feature (``gonetem-console update``) only supports ``amd64``
and ``arm64`` because those are the architectures for which the Makefile
defines an install target.

Build from source
-----------------

Toolchain
~~~~~~~~~

* Go **1.24** or newer (``go.mod`` declares ``go 1.24.0``).
* GNU Make.
* ``protoc``, ``protoc-gen-go`` and ``protoc-gen-go-grpc`` if you modify the
  gRPC contract.

Build
~~~~~

From the repository root:

.. code-block:: bash

   make build-amd64     # bin/gonetem-server_amd64, bin/gonetem-console_amd64
   make build-arm64     # bin/gonetem-server_arm64, bin/gonetem-console_arm64
   make build-armv7     # bin/gonetem-server_armv7, bin/gonetem-console_armv7

Every target builds statically (``CGO_ENABLED=0``) and injects the version
from the ``VERSION`` file:

.. code-block:: bash

   -ldflags "-X github.com/mroy31/gonetem/internal/options.VERSION=$(cat VERSION)"

Without the linker flag, the server and console fall back to the compiled-in
default version. ``gonetem-console version`` and
``gonetem-server -verbose`` report it.

Regenerating the gRPC code
~~~~~~~~~~~~~~~~~~~~~~~~~~

.. code-block:: bash

   make proto

This runs ``protoc`` on ``internal/proto/netem.proto`` with
``paths=source_relative`` and regenerates ``netem.pb.go`` and
``netem_grpc.pb.go``. Both files are committed. Install the plugins with:

.. code-block:: bash

   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

Install from source
-------------------

``make install-*`` installs the binaries, the default server configuration, the
systemd unit and the kernel tuning file, then restarts the service:

.. code-block:: bash

   sudo make install-amd64
   # or
   sudo make install-arm64

The target performs the following steps:

#. stops ``gonetem.service`` if it exists;
#. builds the binaries and copies them to ``/usr/local/bin``;
#. creates ``/etc/gonetem`` and copies ``conf/config.yaml`` there;
#. installs ``conf/gonetem.service`` into ``/etc/systemd/system``;
#. runs ``systemctl daemon-reload``, ``systemctl enable`` and
   ``systemctl start``;
#. copies ``conf/99-custom-network.conf`` into ``/etc/sysctl.d`` and applies
   it with ``sysctl --system``.

To remove these files (binaries, configuration and sysctl file):

.. code-block:: bash

   sudo make uninstall

.. warning::

   ``make install-*`` overwrites ``/etc/gonetem/config.yaml`` with the default
   configuration. Back up a customised configuration before upgrading this
   way. The self-update command (``gonetem-console update``) deliberately does
   **not** use ``make install-*`` for this reason; it only replaces the
   binaries.

Debian packages
---------------

The Makefile can build ``.deb`` packages with `fpm
<https://github.com/jordansissel/fpm>`_ run inside a Docker image:

.. code-block:: bash

   docker build -t gonetem-build docker/build
   make build-deb-amd64     # gonetem_<version>_amd64.deb
   make build-deb-arm64
   make build-deb-armv7

Package contents:

.. list-table::
   :header-rows: 1
   :widths: 30 70

   * - Path
     - Content
   * - ``/usr/sbin/gonetem-server``
     - Server binary.
   * - ``/usr/bin/gonetem-console``
     - Console binary.
   * - ``/etc/gonetem/config.yaml``
     - Default server configuration (registered as a config file, kept on
       upgrade if modified).
   * - ``/etc/systemd/system/gonetem.service``
     - Service unit, enabled and started on install.

The package recommends ``xterm``, ``wireshark`` and ``docker-ce | docker.io``.
Install it with:

.. code-block:: bash

   sudo apt install ./gonetem_<version>_amd64.deb

.. note::

   The packaged ``gonetem.service`` unit starts
   ``/usr/local/bin/gonetem-server`` (the source-install path), while the
   Debian package installs the server in ``/usr/sbin/gonetem-server``. If the
   service fails with ``status=203/EXEC`` after a ``.deb`` install, adjust
   ``ExecStart`` in ``/etc/systemd/system/gonetem.service`` to
   ``/usr/sbin/gonetem-server`` (and keep the same ``-log-file`` argument),
   then run ``sudo systemctl daemon-reload``.

Docker images
-------------

Before the first project, pull the images referenced by the server
configuration:

.. code-block:: bash

   gonetem-console pull

The server resolves each image name through ``GetDockerImageId``: a name that
already contains a tag (``repo/name:tag``) or a digest (``repo/name@sha256:...``)
is used as-is, otherwise ``:0.6.0`` (``IMAGE_VERSION``) is appended. Pin exact
tags in the configuration if you need reproducibility across image releases.

Images can also be pulled manually:

.. code-block:: bash

   docker pull mroy31/gonetem-host:0.6.0
   docker pull mroy31/gonetem-router:0.6.0

See :ref:`docker-images` for the full catalogue, the contents of each image
and instructions to build them yourself.

Server configuration and service
--------------------------------

The server reads ``/etc/gonetem/config.yaml`` by default
(``-conf-file`` overrides it). If the file does not exist, the server logs a
warning and runs with the built-in defaults, which listen on
``localhost:10110``, use ``/tmp`` as working directory and reference the
standard images.

The packaged systemd unit runs:

.. code-block:: bash

   /usr/local/bin/gonetem-server -log-file /var/log/gonetem-server.log

with ``PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin``.
This is the binary path produced by ``make install-*``. For a Debian package
install, see the note in the package section: the binary lives in
``/usr/sbin`` and ``ExecStart`` may need to be adjusted.
Logs therefore go to ``/var/log/gonetem-server.log``; follow them with:

.. code-block:: bash

   sudo tail -f /var/log/gonetem-server.log

Service management:

.. code-block:: bash

   sudo systemctl status gonetem
   sudo systemctl restart gonetem
   sudo journalctl -u gonetem -n 100

See :ref:`configuration` for every configuration key.

Post-installation checks
------------------------

.. code-block:: bash

   # 1. the binaries are installed and the console answers
   command -v gonetem-server gonetem-console
   gonetem-console version

   # 2. the service is up and listening
   sudo systemctl status gonetem
   ss -ltnp | grep 10110

   # 3. the console reaches the server
   gonetem-console list

   # 4. images are present
   docker images | grep gonetem

   # 5. an end-to-end smoke test
   gonetem-console create /tmp/smoke.gnet
   gonetem-console open /tmp/smoke.gnet

Enabling MPLS support
---------------------

MPLS is optional. To use the ``mpls: true`` node option, load the kernel
modules on the server host:

.. code-block:: bash

   sudo modprobe mpls_router
   sudo modprobe mpls_iptunnel

To make this persistent:

.. code-block:: bash

   echo -e "mpls_router\nmpls_iptunnel" | sudo tee /etc/modules-load.d/mpls.conf

For each interface of an MPLS-enabled node, the server writes
``net.mpls.conf.<if>.input=1`` inside the node namespace, which enables MPLS
input processing on that interface. No host-wide sysctl is required.

Enabling Open vSwitch
---------------------

For ``ovs`` nodes, ensure the kernel module is available on the host:

.. code-block:: bash

   sudo modprobe openvswitch

The shared OVS container starts ``ovsdb-server`` and ``ovs-vswitchd``; each
project gets exactly one container, created lazily when the first OVS node is
started (or added to a running project) and removed when the project closes.

Upgrading
---------

Three options exist:

#. **Self-update** (recommended when enabled): set ``update.enabled: true`` in
   ``/etc/gonetem/config.yaml`` and run ``gonetem-console update``. The server
   clones the configured repository/branch, downloads the configured Go
   toolchain, builds with ``make build-<arch>``, atomically replaces
   ``/usr/local/bin/gonetem-{server,console}`` and asks systemd to restart
   itself. The server process restarts, so the RPC stream may end abruptly —
   that is expected. See :ref:`internals-update`.
#. **Debian package**: install the new ``.deb``; systemd restarts the service
   and your modified configuration file is preserved.
#. **From source**: ``make install-<arch>`` (remember it overwrites the
   configuration file).

After any upgrade, check ``gonetem-console version`` against the server's
version (the console reports the server version when it connects, and
``ServerGetVersion`` is available to API clients).

Uninstalling
------------

.. code-block:: bash

   sudo systemctl disable --now gonetem
   sudo make uninstall                  # source installs
   # or
   sudo apt remove gonetem              # Debian package

Removing images is optional:

.. code-block:: bash

   docker rmi mroy31/gonetem-host:0.6.0 mroy31/gonetem-router:0.6.0 \
              mroy31/gonetem-server:0.6.0 mroy31/gonetem-bmv2:0.6.0 \
              mroy31/gonetem-ovs:0.6.0

Prune leftover containers from crashed runs with:

.. code-block:: bash

   gonetem-console clean

Installation layout
-------------------

.. list-table::
   :header-rows: 1
   :widths: 34 46 20

   * - Path
     - Purpose
     - Created by
   * - ``/usr/local/bin/gonetem-server``
     - Server binary (source install)
     - ``make install-*``
   * - ``/usr/local/bin/gonetem-console``
     - Console binary (source install)
     - ``make install-*``
   * - ``/usr/sbin/gonetem-server``, ``/usr/bin/gonetem-console``
     - Server and console binaries (Debian package)
     - ``.deb``
   * - ``/etc/gonetem/config.yaml``
     - Server configuration
     - ``make install-*``, ``.deb``
   * - ``/etc/systemd/system/gonetem.service``
     - systemd unit
     - ``make install-*``, ``.deb``
   * - ``/etc/sysctl.d/99-custom-network.conf``
     - Kernel tuning (ARP table, inotify)
     - ``make install-*``
   * - ``/var/log/gonetem-server.log``
     - Server log when started by systemd
     - service unit
   * - ``/tmp/gonetem-<id>-*``
     - Extracted open projects (``workdir``)
     - server at runtime
   * - ``~/.config/gonetem-console/console.yaml``
     - Per-user console configuration
     - console on first run
   * - ``/run/netns/ntm<id>.<node>``
     - Named network namespaces of ``namespace`` nodes
     - server at runtime
