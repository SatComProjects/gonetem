.. _troubleshooting:

Troubleshooting
===============

This page lists the failures most often seen in practice, with the diagnosis
path and the fix. Start with the server log:

.. code-block:: bash

   sudo tail -f /var/log/gonetem-server.log
   # or, when running in the foreground:
   sudo gonetem-server -verbose

Console commands also report server errors in red; ``gonetem-console list`` is
the quickest way to distinguish "server down" from "project problem".

Server startup
--------------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - ``Unable to parse config file ...``
     - YAML syntax error. Validate with ``python3 -c 'import yaml,sys;
       yaml.safe_load(open("/etc/gonetem/config.yaml"))'``. Note that a missing
       file is **not** an error: the server warns and uses defaults.
   * - ``cannot load TLS credentials``
     - One of ``ca``/``cert``/``key`` is missing, unreadable, or not PEM. The
       server exits with status 2. Check paths and permissions (it runs as
       root, but a relative path resolves against ``/``).
   * - ``unable to listen on socket``
     - Address already in use (another server instance) or a privileged port
       without permission. ``ss -ltnp | grep 10110``.
   * - ``unable to init docker client``
     - Docker is not running, or ``/var/run/docker.sock`` is not accessible.
       ``systemctl status docker``.
   * - Service starts then exits immediately
     - Look at the command line in the unit: ``-log-file`` path must be
       writable (``/var/log`` as root is). Check ``systemctl status gonetem``
       and the log file.

Console connection
------------------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - ``Unable to connect to server identified by uri ...``
     - Server not running, wrong URI, or firewall. Test with
       ``gonetem-console -s <uri> list`` and ``ss -ltnp`` on the server.
   * - TLS handshake failure
     - See :ref:`tls`; usual causes are a CA mismatch, a missing client
       certificate, or a SAN/hostname mismatch.
   * - ``Project ... not found``
     - The project was closed (IDs are ephemeral), the server restarted, or the
       ID belongs to another server. Re-open the ``.gnet`` file.
   * - ``Node ... not found in project``
     - Node name typo, or the topology changed. ``status`` lists the current
       nodes.
   * - ``terminal 'xterm' is not installed``
     - Install xterm, or change the ``terminal`` setting
       (:ref:`configuration`).
   * - ``Editor set in config, vim, is not found``
     - Install the editor or ``gonetem-console config set editor <path>``.

Projects
--------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - ``gonetem accepts only project with .gnet extension``
     - Rename the file; ``create``/``open``/``extract`` enforce ``.gnet``.
   * - ``a project named ... is already open``
     - A project with that name is open on the server. Use
       ``open --name <other>`` or close the other project.
   * - ``unable to open project: ...``
     - The archive is corrupt, not gzip/tar, contains an absolute path, an
       escaping path or an unsupported entry type (symlinks). Rebuild it with
       ``tar -C dir -czf project.gnet .``.
   * - ``topology is not valid:``
     - Run ``check`` in the prompt for the full list; the rules are in
       :ref:`topology`. Common causes: duplicate peer, bad node type, OVS name
       longer than 10 characters, missing host interface for a bridge, invalid
       CIDR / VRRP group, QoS constraint violation.
   * - ``docker node of type X does not exist in the configuration``
     - ``docker.<X>`` is not declared in ``docker.nodes``/``docker.extraNodes``,
       or the server was not restarted after editing the configuration.

Running a topology
------------------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - ``docker image ... not present``
     - Pull images: ``gonetem-console pull`` or ``docker pull <image>``. Check
       that the tag matches (the server appends ``:0.6.0`` when the name has no
       tag).
   * - ``ovswitch image ... not present``
     - ``docker.ovsImage`` is missing locally. Pull it.
   * - ``topology is not running; use TopologyRun first``
     - ``start``/``stop`` require a running project. Use ``run``.
   * - ``topology is already running`` (log warning)
     - ``run`` on a running project is a no-op; use ``reload`` to apply edits.
   * - Node start hangs or fails with a Docker error
     - ``docker ps -a`` and ``docker logs <container>``; check the server log
       for the node name. A privileged container that exits immediately usually
       means a broken image or a missing entrypoint.
   * - ``unable to create link A.0-B.0: ...``
     - The interface already exists (stale veth from a crashed run) or the node
       namespace is gone. Clean leftovers (see below) and retry.
   * - ``unable to set X up``
     - Interface missing in the expected namespace; often a consequence of a
       previous partial failure. Reload the project.
   * - ``bridge ... host interface ... not found``
     - The host interface declared in ``bridges:`` does not exist. Check
       ``ip link``. Beware of enslaving the interface you use for SSH.
   * - Management network fails
     - The ``mgntnet.address`` must be a valid CIDR and the node addresses must
       be in the same subnet without conflicts. OVS nodes cannot join.
   * - ``unable to load config of node ...``
     - A ``loadConfig`` command failed inside the node. Open a console or
       ``exec`` to reproduce; the timeout is ``docker.timeoutop`` (60 s by
       default). Check ``checkFiles`` paths exist in the image.
   * - Run fails and the project rolls back
     - Expected: the server closes resources and reloads the model. Fix the
       topology/configuration and ``run`` again.

QoS
---

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - ``rate requires a non-zero delay``
     - tbf needs a latency to size its bucket. Set ``delay`` with ``rate``.
   * - ``jitter requires a non-zero delay``
     - netem requirement. Set ``delay``.
   * - ``buffer requires a non-zero rate`` / ``burst requires ...``
     - Both only make sense with shaping. Remove them or add ``rate``.
   * - ``delay must be in [0, 274000] ms`` (or similar)
     - Out-of-range value; see the validation table in :ref:`topology`.
   * - QoS has no effect
     - Verify the topology is running, and inspect the kernel:

       .. code-block:: bash

          sudo nsenter --net=/run/netns/ntm<id>.<node> tc qdisc show dev eth0

       For Docker nodes, use the container PID's netns. A missing qdisc means
       the link was added while stopped and not applied — reload or re-apply
       with ``LinkUpdate``.
   * - ``limit`` has no effect
     - ``limit`` sizes the netem queue, so it applies only when netem is active
       (``delay``/``jitter``/``loss``). ``txqueuelen`` always applies to the
       interface.
   * - Throughput lower than expected
     - tbf also drops when the bucket limit is too small: ``buffer`` is in
       bandwidth-delay products. Raise ``buffer`` or ``burst``.
   * - Delay much higher than configured
     - Queueing in the emulated buffer: lower ``limit``/``txqueuelen``, or
       check that the host is not saturated.

Routing
-------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - ``route installation is not supported on OVS node``
     - OVS nodes have no IP stack; install the route in the router behind it.
   * - ``node X is not running``
     - RouteInstall requires running nodes. ``start X`` first.
   * - ``route N: device is required`` / ``invalid destination``
     - The ``device`` must be the in-node interface name (``eth0``), the
       destination a CIDR.
   * - ``gateway and destination address families differ``
     - Do not mix IPv4 and IPv6 in one route.
   * - ``unable to find device ethN``
     - The interface does not exist in that node: check the node's links and
       ``status``.

Capture and consoles
--------------------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - ``wireshark is not installed``
     - Install Wireshark on the operator machine (it reads a pcap stream from
       stdin).
   * - Capture window opens but stays empty
     - The interface may be down or unused; generate traffic. For ``docker.host``
       nodes, TSO is disabled by the node type, which avoids offload artefacts.
   * - ``Console to node ... returns an error: ... not running``
     - Start the node. ``status`` reports it.
   * - Console opens and closes immediately
     - Command missing in the image (``commands.console``). Check the node type
       definition and the image contents.
   * - ``copying between containers is not supported``
     - Use two copies via the host.
   * - ``... is not a regular file``
     - Only regular files are supported, notably for namespace nodes.

Switch console
--------------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - ``Ovs switch '...' not found``
     - The switch is not started. ``start sw1``.
   * - ``Interface N does not exist on this switch``
     - The port index does not exist; ``show interfaces`` lists available
       ports.
   * - ``STP is disabled on this bridge``
     - Enable it with ``set stp enable``.
   * - ``A least 2 interfaces is required``
     - A bond needs two or more ports.
   * - VLAN changes lost after reload
     - Save the project: switch configuration is persisted in
       ``configs/<switch>.conf`` only when the project is saved.

Save and configuration
----------------------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - ``viewConfig`` shows empty files
     - The node is not running (the project archive is empty for that node) or
       the node type does not declare the corresponding
       ``configurationFiles``/``configurationFolders``.
   * - ``save`` does not capture recent changes
     - Only running nodes and declared files/folders are captured. For host
       nodes, the addresses/routes must be saved through ``network-config.py``
       (the type's ``saveConfig``); use ``save`` while the node runs.
   * - ``Project path is empty, use saveAs``
     - Projects opened with ``connect`` have no local path.

Self-update
-----------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - ``update is disabled``
     - Set ``update.enabled: true`` and restart the server.
   * - ``an update is already running``
     - Wait for the current one (or restart the server if it crashed mid-update).
   * - ``git clone`` fails
     - Network/DNS/proxy on the server, or an invalid repository/branch.
       Reproduce manually as root.
   * - ``unable to download ... go.dev``
     - No egress access. Pre-place a Go toolchain under
       ``<update.workdir>/go`` with the expected version, or update manually.
   * - Build fails
     - The stream shows the last output lines. Common causes: missing
       ``make``/``git`` on the server, no space in ``workdir``, a branch that
       does not build.
   * - ``systemctl not found`` / restart failed
     - The update installed the binaries but could not restart. Restart
       ``gonetem.service`` manually.
   * - Version unchanged after update
     - ``gonetem-console version`` prints the console version. Query the server
       through any RPC (``list`` triggers a connection) or check the binary
       timestamp, then restart the service if needed.

Leftover resources and cleanup
------------------------------

Symptoms: interfaces named ``ntm...`` or ``veth...`` persist, containers remain
after a crash, ``run`` fails with "link already exists".

.. code-block:: bash

   # containers of gonetem
   docker ps -a --filter name=ntm

   # remove those that belong to no open project (asks for confirmation)
   gonetem-console clean

   # list and remove gonetem network namespaces
   ip netns list | grep ntm
   sudo ip netns delete <name>

   # list gonetem bridges and veths in the root namespace
   ip -d link show type bridge | grep -A1 ntm
   ip -d link show type veth | grep ntm

   # delete a leftover bridge or veth explicitly
   sudo ip link del <name>

``clean`` removes **containers only**; it never deletes bridges, namespaces or
veth pairs, including those left behind by a crash. Remove those manually with
the commands above (only when you are sure they belong to no running project).

Performance
-----------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Diagnosis and fix
   * - Slow project start
     - Look for ``Run: ... took ...`` lines with ``-verbose``. Container start
       dominates; raise ``startConcurrency``, prefer namespace nodes, or check
       Docker I/O.
   * - Slow close
     - Container removal dominates; raise ``removeConcurrency``. A stuck
       container removal delays the project close.
   * - Slow link operations
     - Each link creation is a netlink operation; raise ``linkConcurrency``
       moderately. Thousands of links on one node will contend on that
       namespace.
   * - High host load
     - Containers and qdiscs consume host CPU; ``htop``, ``docker stats`` and
       ``tc -s qdisc`` inside nodes show where time goes.

Collecting information for a bug report
---------------------------------------

Please include:

* ``gonetem-console version`` and the server version
  (``ServerGetVersion``/log line at startup);
* the OS, kernel version (``uname -a``), Docker version;
* the relevant ``network.yml`` (redact addresses if needed);
* server logs around the failure (``-verbose`` if possible);
* the console command and its exact error;
* the output of:

  .. code-block:: bash

     docker ps -a --filter name=ntm
     ip -br link | grep ntm
     ip netns list | grep ntm
     tc qdisc show   # inside the failing node namespace, if applicable

* whether the failure is reproducible after ``gonetem-console clean`` and a
  project ``reload``.

Open an issue at
https://github.com/satcomprojects/gonetem/issues with these elements.

See also
--------

* :ref:`faq` — short answers to common questions.
* :ref:`api` — error semantics of each RPC.
* :ref:`testing` — reproducing with the integration tests.
