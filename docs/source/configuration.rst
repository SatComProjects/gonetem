.. _configuration:

Configuration
=============

Two files drive GoNetEm:

* the **server configuration**, ``/etc/gonetem/config.yaml`` by default
  (``-conf-file`` changes it), read by ``gonetem-server`` at startup;
* the **console configuration**, ``~/.config/gonetem-console/console.yaml``,
  read and written by ``gonetem-console``.

This page is the reference for both. Node-type definitions inside the server
configuration are described in detail in :ref:`nodes`.

Server configuration
--------------------

Location and loading
~~~~~~~~~~~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 30 70

   * - Item
     - Value
   * - Default path
     - ``/etc/gonetem/config.yaml``
   * - Override
     - ``gonetem-server -conf-file <path>``
   * - Missing file
     - The server logs a warning and uses the built-in defaults. It does not
       create the file.
   * - Format
     - YAML. Keys are matched case-insensitively (the file is decoded through
       a JSON/YAML bridge), so ``createConcurrency`` and
       ``createconcurrency`` are equivalent. Unknown keys are ignored.
   * - Unknown keys
     - Ignored silently, which keeps old configuration files usable with new
       releases.

The built-in defaults are applied first, then your file is overlaid. A few
keys have explicit fallbacks after parsing: empty ``update.repository``,
``update.branch`` and ``update.goVersion`` revert to their defaults, and a
zero ``docker.routeConcurrency`` reverts to 32.

Minimal configuration
~~~~~~~~~~~~~~~~~~~~~

The smallest usable file only needs to say where to listen and where to store
projects:

.. code-block:: yaml

   listen: "localhost:10110"
   workdir: /tmp
   docker:
     nodes:
       router:
         image: mroy31/gonetem-frr
       host:
         image: mroy31/gonetem-host
       server:
         image: mroy31/gonetem-server
       p4sw:
         image: mroy31/gonetem-bmv2

Everything else falls back to defaults.

Complete reference
~~~~~~~~~~~~~~~~~~

Below is the full configuration accepted by version 0.8.0. It mirrors the
built-in defaults; the comments explain each key.

.. code-block:: yaml

   # ---------------------------------------------------------------------------
   # gRPC endpoint
   # ---------------------------------------------------------------------------
   # host:port listened on by gonetem-server. Use 0.0.0.0:10110 to accept
   # remote consoles (combine with tls, see the tls page).
   listen: "localhost:10110"

   # ---------------------------------------------------------------------------
   # Mutual TLS for the gRPC endpoint
   # ---------------------------------------------------------------------------
   tls:
     enabled: false
     ca: ""      # PEM certificate of the CA used to sign client certificates
     cert: ""    # PEM server certificate
     key: ""     # PEM server private key

   # ---------------------------------------------------------------------------
   # Working directory
   # ---------------------------------------------------------------------------
   # Root of the temporary directories created for open projects and for
   # configuration/copy operations. Must be writable by the server (root).
   workdir: /tmp

   # ---------------------------------------------------------------------------
   # Docker
   # ---------------------------------------------------------------------------
   docker:
     # Timeout, in seconds, applied to configuration load/save operations and
     # to Docker copy operations. 0 disables the timeout.
     timeoutop: 60

     # Bounded worker pools. 0 (or a negative value) falls back to the
     # built-in default of 30, except routeConcurrency (default 32).
     createConcurrency: 30      # container creation during project load
     startConcurrency: 30       # node start during TopologyRun
     removeConcurrency: 30      # node removal during project close
     configConcurrency: 30      # interface setup + LoadConfig, and Save
     linkConcurrency: 30        # veth/qdisc creation, link batches, bridges
     routeConcurrency: 32       # RouteInstall node batches

     # Node type definitions. See the nodes page for every field.
     nodes:
       router:
         type: router
         image: mroy31/gonetem-frr
         volumes: []
         options:
           log: false
           tso: true
         commands:
           console: /usr/bin/vtysh
           shell: /bin/bash
           loadConfig:
           - command: /usr/lib/frr/frrinit.sh start
             checkFiles: []
           saveConfig:
           - command: vtysh -w
             checkFiles: []
         configurationFiles:
         - destSuffix: frr.conf
           source: /etc/frr/frr.conf
           label: FRR
       host:
         type: host
         image: mroy31/gonetem-host
         volumes: []
         options:
           log: true
           tso: false
         commands:
           console: /bin/bash
           shell: /bin/bash
           loadConfig:
           - command: network-config.py -l /tmp/custom.net.conf
             checkFiles:
             - /tmp/custom.net.conf
           - command: /bin/bash /gonetem-init.sh
             checkFiles:
             - /gonetem-init.sh
           saveConfig:
           - command: network-config.py -s /tmp/custom.net.conf
             checkFiles: []
         configurationFiles:
         - destSuffix: init.conf
           source: /gonetem-init.sh
           label: Init
         - destSuffix: net.conf
           source: /tmp/custom.net.conf
           label: Network
         - destSuffix: ntp.conf
           source: /etc/ntpsec/ntp.conf
           label: NTP
       server:
         type: server
         image: mroy31/gonetem-server
         volumes: []
         options:
           log: true
           tso: false
         commands:
           console: /bin/bash
           shell: /bin/bash
           loadConfig:
           - command: network-config.py -l /tmp/custom.net.conf
             checkFiles:
             - /tmp/custom.net.conf
           - command: /bin/bash /gonetem-init.sh
             checkFiles:
             - /gonetem-init.sh
           saveConfig:
           - command: network-config.py -s /tmp/custom.net.conf
             checkFiles: []
         configurationFiles:
         - destSuffix: init.conf
           source: /gonetem-init.sh
           label: Init
         - destSuffix: net.conf
           source: /tmp/custom.net.conf
           label: Network
         - destSuffix: ntp.conf
           source: /etc/ntpsec/ntp.conf
           label: NTP
         - destSuffix: dhcpd.conf
           source: /etc/dhcp/dhcpd.conf
           label: DHCP
         - destSuffix: tftpd-hpa.default
           source: /etc/default/tftpd-hpa
           label: TFTP
         - destSuffix: isc-relay.default
           source: /etc/default/isc-dhcp-relay
           label: DHCP-RELAY
         - destSuffix: bind.default
           source: /etc/default/named
           label: Bind
         configurationFolders:
         - destSuffix: tftp-data.tgz
           source: /srv/tftp
           label: TFTP-Data
         - destSuffix: bind-etc.tgz
           source: /etc/bind
           label: Bind-Data
       p4sw:
         type: p4sw
         image: mroy31/gonetem-bmv2
         options:
           log: false
           tso: true
         commands:
           console: /usr/local/bin/simple_switch_CLI
           shell: /bin/bash
           loadConfig:
           - command: /usr/local/bin/launch-sw.py
             checkFiles: []
           saveConfig: []
         configurationFiles: []

     # Additional node types, available as docker.<type> in topologies.
     extraNodes: []

     # Image used for the shared Open vSwitch container.
     ovsImage: mroy31/gonetem-ovs

   # ---------------------------------------------------------------------------
   # Self-update (gonetem-console update)
   # ---------------------------------------------------------------------------
   update:
     enabled: false
     repository: https://github.com/satcomprojects/gonetem
     branch: master
     goVersion: "1.24.0"
     workdir: ""     # empty: a temporary directory is used and removed on success

Top-level keys
~~~~~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 18 12 12 58

   * - Key
     - Type
     - Default
     - Description
   * - ``listen``
     - string
     - ``localhost:10110``
     - ``host:port`` for the gRPC listener. The server always listens on TCP;
       a filesystem path is not supported.
   * - ``tls.enabled``
     - bool
     - ``false``
     - Enables mutual TLS. Requires ``ca``, ``cert`` and ``key``.
   * - ``tls.ca``
     - string
     - ``""``
     - CA certificate (PEM) used to verify client certificates.
   * - ``tls.cert``, ``tls.key``
     - string
     - ``""``
     - Server certificate chain and private key (PEM).
   * - ``workdir``
     - string
     - ``/tmp``
     - Directory under which per-project temporary directories are created
       (``gonetem-<id>-*``), as well as temporary directories for copy and
       configuration operations. Use a filesystem with enough space: saved
       node configurations live there while the project is open.

Docker keys
~~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 24 12 12 52

   * - Key
     - Type
     - Default
     - Description
   * - ``docker.timeoutop``
     - int (s)
     - ``60``
     - Timeout for node configuration load/save and Docker file copies.
       ``0`` disables it.
   * - ``docker.createConcurrency``
     - int
     - ``30``
     - Parallel container creations while a topology is loaded.
   * - ``docker.startConcurrency``
     - int
     - ``30``
     - Parallel node starts during ``TopologyRun``.
   * - ``docker.removeConcurrency``
     - int
     - ``30``
     - Parallel node removals during project close and ``NodeDel``.
   * - ``docker.configConcurrency``
     - int
     - ``30``
     - Parallel interface configuration and ``LoadConfig`` operations, and
       parallel node saves.
   * - ``docker.linkConcurrency``
     - int
     - ``30``
     - Parallel veth/qdisc setup, bridge setup, link batch operations and
       ``LinkSyncMany`` worker pool.
   * - ``docker.routeConcurrency``
     - int
     - ``32``
     - Maximum number of node route batches executed concurrently by
       ``RouteInstall``. A client-provided ``maxConcurrency`` can only lower
       this value.
   * - ``docker.nodes``
     - mapping
     - see above
     - Definitions of the built-in node families: ``router``, ``host``,
       ``server``, ``p4sw``.
   * - ``docker.extraNodes``
     - list
     - ``[]``
     - Additional node type definitions, usable as ``docker.<type>`` in
       topologies.
   * - ``docker.ovsImage``
     - string
     - ``mroy31/gonetem-ovs``
     - Image of the shared OVS container. A version tag is appended when the
       name has none.

Node type schema
~~~~~~~~~~~~~~~~

Each entry of ``docker.nodes`` and each item of ``docker.extraNodes`` accepts:

.. code-block:: yaml

   type: router                 # used in docker.<type>
   image: mroy31/gonetem-frr    # Docker image
   volumes: []                  # extra bind mounts, /host:/container
   options:
     log: false                 # print loadConfig output in the console
     tso: true                  # false disables TX checksum offload on eth*
   commands:
     console: /usr/bin/vtysh    # command for `console <node>`
     shell: /bin/bash           # command for `shell <node>`
     loadConfig:                # commands run at node start
       - command: /usr/lib/frr/frrinit.sh start
         checkFiles: []         # run only if all these files exist
     saveConfig:                # commands run by `save`/`viewConfig`
       - command: vtysh -w
         checkFiles: []
   configurationFiles:          # single files captured into the project
     - destSuffix: frr.conf
       source: /etc/frr/frr.conf
       label: FRR
   configurationFolders:        # directories archived into the project
     - destSuffix: tftp-data.tgz
       source: /srv/tftp
       label: TFTP-Data

The complete semantics — how files are copied in and out, how ``checkFiles``
guards commands, and how custom types are declared — are documented in
:ref:`nodes`.

Concurrency and sizing
~~~~~~~~~~~~~~~~~~~~~~

All heavy phases of the server are parallelised with bounded worker pools. The
defaults (30 for most pools, 32 for route batches) work well on a Linux host
with 8–16 cores. Guidance:

* **Many small nodes** (``namespace`` or ``host``): raise
  ``startConcurrency``, ``createConcurrency`` and ``linkConcurrency`` in
  proportion to CPU count. Container operations are mostly I/O- and
  kernel-bound.
* **Many links with QoS**: ``linkConcurrency`` bounds the number of concurrent
  netlink/qdisc operations. Very high values increase contention on the
  kernel's namespace locks; 2–4× CPU count is a reasonable ceiling.
* **Large route batches**: ``routeConcurrency`` bounds parallel node netlink
  sessions. API clients can request less per call, never more.
* **Close performance**: ``removeConcurrency`` dominates; container removal is
  the slowest operation.

Values are read once at startup; changing them requires a server restart.

Self-update keys
~~~~~~~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 20 12 12 56

   * - Key
     - Type
     - Default
     - Description
   * - ``update.enabled``
     - bool
     - ``false``
     - Must be ``true`` for ``ServerUpdate`` to run; otherwise the RPC fails
       with ``update is disabled``.
   * - ``update.repository``
     - string
     - ``https://github.com/satcomprojects/gonetem``
     - Git repository cloned (``--depth 1``) to build the new version.
   * - ``update.branch``
     - string
     - ``master``
     - Branch or tag checked out during the clone.
   * - ``update.goVersion``
     - string
     - ``1.24.0``
     - Go toolchain downloaded from ``https://go.dev/dl`` if not already
       present in the work directory.
   * - ``update.workdir``
     - string
     - ``""``
     - Clone/build directory. Empty means a temporary directory, removed after
       a successful update and kept for debugging after a failure.

The update requires ``git``, ``make``, ``systemctl`` and write access to
``/usr/local/bin``. It is serialized by a server-side mutex: a second
concurrent update receives ``an update is already running``. Only ``amd64``
and ``arm64`` are supported.

TLS keys
~~~~~~~~

The server and console use the same four keys (``enabled``, ``ca``, ``cert``,
``key``). The server requires and verifies client certificates
(``tls.RequireAndVerifyClientCert``), so a console cannot connect with only
the CA. Certificate generation and the full procedure are in :ref:`tls`.

Example: remote access with TLS
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

.. code-block:: yaml

   listen: "0.0.0.0:10110"
   tls:
     enabled: true
     ca: /etc/gonetem/ca-cert.pem
     cert: /etc/gonetem/server-cert.pem
     key: /etc/gonetem/server-key.pem
   workdir: /var/lib/gonetem

Then on the workstation:

.. code-block:: bash

   gonetem-console config set server gonetem.example.org:10110
   gonetem-console config set tls.enabled true
   gonetem-console config set tls.ca   ~/gonetem-certs/ca-cert.pem
   gonetem-console config set tls.cert ~/gonetem-certs/client-cert.pem
   gonetem-console config set tls.key  ~/gonetem-certs/client-key.pem

Console configuration
---------------------

The console configuration is a per-user file created on first launch:

.. code-block:: text

   ~/.config/gonetem-console/console.yaml

Default content:

.. code-block:: yaml

   server: "localhost:10110"
   editor: vim
   terminal: "xterm -xrm 'XTerm.vt100.allowTitleOps: false' -title {{.Name}} -e {{.Cmd}}"
   tls:
     enabled: false
     ca: ""
     cert: ""
     key: ""

Keys:

.. list-table::
   :header-rows: 1
   :widths: 20 15 65

   * - Key
     - Default
     - Description
   * - ``server``
     - ``localhost:10110``
     - gRPC URI of the server. Overridden by the global ``--server/-s`` flag.
   * - ``editor``
     - ``vim``
     - Editor launched by the ``edit`` prompt command. Must be in ``PATH``.
   * - ``terminal``
     - xterm command
     - Command template used to open a node console. ``{{.Name}}`` is the node
       name and ``{{.Cmd}}`` is the console command to run (a
       ``gonetem-console console ...`` invocation). Any X terminal can be used
       as long as it accepts the expanded arguments.
   * - ``tls.enabled``
     - ``false``
     - Enables mutual TLS for console connections.
   * - ``tls.ca``, ``tls.cert``, ``tls.key``
     - ``""``
     - CA certificate, client certificate and client key (PEM).

Inspect and edit it with:

.. code-block:: bash

   gonetem-console config show
   gonetem-console config set editor nano
   gonetem-console config set server 192.0.2.10:10110
   gonetem-console config set terminal "xterm -fa 'Monospace' -fs 13 -xrm 'XTerm.vt100.allowTitleOps: false' -title {{.Name}} -e {{.Cmd}}"
   gonetem-console config set tls.enabled true
   gonetem-console config set tls.ca   ~/certs/ca-cert.pem
   gonetem-console config set tls.cert ~/certs/client-cert.pem
   gonetem-console config set tls.key  ~/certs/client-key.pem

Only the keys listed above are accepted by ``config set``; an unknown key is a
fatal error and the file is not modified. The file is rewritten after every
successful ``set``.

Precedence
~~~~~~~~~~

For the server URI:

#. the global ``--server/-s`` flag of ``gonetem-console``;
#. ``server`` in ``console.yaml``;
#. the built-in ``localhost:10110``.

For TLS, there is no command-line override: configure
``~/.config/gonetem-console/console.yaml``.

Recommended profiles
--------------------

.. list-table::
   :header-rows: 1
   :widths: 22 26 52

   * - Profile
     - Configuration
     - Rationale
   * - Workstation lab
     - defaults
     - Everything on one host, loopback-only gRPC, no TLS.
   * - Large host
     - ``workdir: /var/lib/gonetem``, concurrency 2× CPUs
     - Keeps project data off ``/tmp`` (which may be tmpfs) and uses the
       available cores.
   * - Shared server
     - ``listen: 0.0.0.0:10110``, mutual TLS enabled
     - Remote operators, certificates mandatory; restrict the port with a
       firewall to the operator network.
   * - CI runner
     - isolated Docker daemon, ``update.enabled: false``
     - Deterministic behaviour, no self-modification.
