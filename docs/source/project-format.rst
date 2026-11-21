.. _project-format:

Project file format
===================

A GoNetEm project is a single file with the ``.gnet`` extension. It is a
**gzipped tar archive** containing the topology and the captured node
configuration. This page documents the layout, the naming conventions, the
save/load lifecycle and how to work with projects outside the console.

Why an archive
--------------

* one portable artifact: copy it, attach it to a bug report, version it;
* deterministic content: text files only (YAML, configuration files, optional
  tarballs of configuration directories);
* no server-side state: the server can be restarted or replaced without losing
  projects, as long as the file was saved.

Archive layout
--------------

.. code-block:: text

   lab.gnet
   ├── network.yml                 # topology (mandatory)
   └── configs/                    # optional, created by the first save
       ├── R1.frr.conf
       ├── R2.frr.conf
       ├── host1.init.conf
       ├── host1.net.conf
       ├── host1.ntp.conf
       ├── host2.init.conf
       ├── host2.net.conf
       ├── host2.ntp.conf
       ├── sw1.conf
       └── server1.tftp-data.tgz

* ``network.yml`` is the topology described in :ref:`topology`.
* ``configs/`` holds one file per declared node configuration entry, using the
  naming convention ``<node>.<destSuffix>``.
* Configuration **folders** are stored as gzipped tarballs in the same
  directory, named ``<node>.<destSuffix>`` where ``destSuffix`` usually ends in
  ``.tgz``.
* Open vSwitch switches store their configuration as ``<switch>.conf`` (JSON,
  see :ref:`ovs`).

Nothing else is required. Additional files placed in the archive are extracted
by the server but ignored by GoNetEm; they are preserved when the console
downloads a saved project, which can be used to keep notes or scripts next to
the topology.

The archive is read and written with Go's ``archive/tar`` and
``compress/gzip``; any tool that produces a gzip-compressed tar can create or
inspect one.

Creating a project
------------------

From the console:

.. code-block:: bash

   gonetem-console create ./lab.gnet

From a shell:

.. code-block:: bash

   mkdir -p lab/configs
   cat > lab/network.yml <<'EOF'
   nodes:
     host1:
       type: docker.host
   links:
   bridges:
   EOF
   tar -C lab -czf lab.gnet .

Both produce an archive whose entries are relative to the project root
(``network.yml``, ``configs/...``). Avoid storing the parent directory itself
(``tar czf lab.gnet lab`` would create entries prefixed with ``lab/``), because
the server opens ``network.yml`` at the archive root.

Extracting a project
--------------------

.. code-block:: bash

   gonetem-console extract ./lab.gnet ./lab-src
   # or
   mkdir lab-src && tar -C lab-src -xzf lab.gnet

``extract`` requires the destination directory to exist. Use this to inspect
saved configurations, or to edit the topology outside ``edit``.

Editing a project by hand
~~~~~~~~~~~~~~~~~~~~~~~~~

#. extract the archive;
#. edit ``network.yml`` and/or ``configs/``;
#. re-create the archive:

   .. code-block:: bash

      rm lab.gnet
      tar -C lab-src -czf lab.gnet .

#. open it as usual.

Alternatively, open the project, use ``edit`` to modify the topology and
``saveAs`` to write a new file.

.. note::

   The server rewrites ``network.yml`` when a live change is applied with
   ``sync: true``. Comments and formatting are lost at that point: only the
   parsed content is preserved. Do not keep critical comments in a project you
   plan to modify through the API.

Runtime representation
----------------------

Opening a project uploads the archive to the server with ``ProjectOpen``. The
server:

#. creates a temporary directory ``<workdir>/gonetem-<id>-<random>``;
#. extracts the archive there, rejecting absolute paths and entries escaping
   the directory (path traversal protection) as well as entry types other than
   regular files and directories (symlinks are rejected);
#. validates and loads ``network.yml``;
#. keeps the directory for the lifetime of the open project.

The working directory contains:

.. code-block:: text

   gonetem-XyZ-123456789/
   ├── network.yml
   └── configs/
       ├── R1.frr.conf
       └── ...

Saving writes new configuration files into the same ``configs/`` directory,
then archives the whole directory for download. Closing the project deletes
the directory, so unsaved changes are lost.

Saving semantics
----------------

``save`` performs two independent operations:

#. **Capture** — for every running node, the server runs the ``saveConfig``
   commands of its node type and copies the declared configuration files and
   folders from the node into ``configs/``.
#. **Archive** — the server serialises the in-memory topology to
   ``network.yml``, then tars the project directory and streams it to the
   console, which writes the local ``.gnet`` file.

As a consequence:

* a node that is not running is skipped during capture; its previously saved
  files remain untouched in the project directory;
* topology changes applied live with ``sync: false`` are **not** in
  ``network.yml`` unless the model was synchronised; use ``sync: true`` or edit
  the topology;
* the archive always reflects the server state at save time, not the file you
  originally uploaded.

``viewConfig`` and ``config <dir>`` use the same capture step but do not write
the topology file: ``viewConfig`` saves into a temporary directory, and
``config`` archives only the ``configs`` directory.

Naming conventions
------------------

Docker and namespace nodes
   ``configs/<node>.<destSuffix>``

   ``destSuffix`` is declared by the node type, for example ``frr.conf``,
   ``net.conf``, ``init.conf``, ``ntp.conf``, ``dhcpd.conf``,
   ``tftp-data.tgz``. The node name always comes first, so two nodes of the
   same type never collide:

   .. code-block:: text

      configs/R1.frr.conf
      configs/R2.frr.conf

Open vSwitch nodes
   ``configs/<switch>.conf``

   The file is the JSON produced by ``ovs-config.py`` (VLANs, STP, bonds). It
   has no extra suffix because the switch has a single configuration document.

Configuration folders
   When a node type declares ``configurationFolders``, the directory is
   archived inside the node with ``tar czf`` and copied out under
   ``configs/<node>.<destSuffix>`` (``tftp-data.tgz``, ``bind-etc.tgz``, …).
   Restoring extracts the archive inside the node at ``/``, so absolute paths
   stored in the tar are recreated.

Permissions
~~~~~~~~~~~

Files are written with mode ``0644`` and directories with ``0755``. Keys are
**not** stored encrypted in the archive; treat a ``.gnet`` file as sensitive if
the nodes contain credentials (FRR passwords, BGP keys, DHCP configurations,
…).

What is not saved
-----------------

* the state of running processes (only declared configuration files are
  captured);
* interface addresses configured with ``exec`` on a node whose type does not
  capture them (``namespace`` nodes have no capture mechanism; Docker host
  nodes capture their network configuration only through
  ``network-config.py``);
* ``ovs-vsctl`` or ``simple_switch_CLI`` settings not covered by the declared
  configuration files;
* interface up/down states and runtime QoS values, unless a node type captures
  them (QoS lives in the topology, not in the nodes);
* terminal history, captures and logs.

To persist arbitrary state, use an init script (``init.conf``) or copy the file
into the project and declare it in the node type.

Compatibility and robustness
----------------------------

* The loader accepts ``.gnet`` files created by older releases: new optional
  fields default, and unknown YAML keys are ignored.
* Archives that are not gzip, not tar, contain absolute or escaping paths, or
  contain symlinks/other entry types are rejected with an explicit error.
* A corrupt or truncated archive fails ``ProjectOpen`` before anything is
  created on the host.
* Project IDs are runtime-only. The ``.gnet`` file is the durable artifact.

Tools
-----

``scripts/generate-project.py``
   Generates a synthetic ``ground ↔ satellites`` project (one host, N routers,
   M links each) as a ``.gnet`` archive. Useful for scale testing:

   .. code-block:: bash

      python3 scripts/generate-project.py -o sat.gnet -n 40 -l 2 --links 1

   Options: ``-n/--nodes`` number of routers, ``-l/--launch`` how many start
   with the topology (``-1`` = all), ``--links`` links per router. It writes a
   temporary ``network.yml`` in the current directory.

``scripts/migrate-pynetem.py``
   Converts a legacy PyNetEm project (``.pnet``, a zip archive with
   ``network.ini``) into a ``.gnet`` project. It translates node types
   (``docker.frr``/``docker.quagga`` → ``docker.router``), renumbers switch
   ports, moves configuration files from ``configs/`` (renaming ``quagga`` to
   ``frr``) and writes a YAML topology:

   .. code-block:: bash

      python3 scripts/migrate-pynetem.py old.pnet -o new.gnet

   It requires the Python packages ``configobj`` and ``PyYAML``.

Examples
--------

Inspect a saved project without opening it:

.. code-block:: bash

   tar tzf lab.gnet
   tar xOzf lab.gnet network.yml
   tar xOzf lab.gnet configs/R1.frr.conf | head -40

Add a startup hook to a host and persist it:

.. code-block:: bash

   mkdir -p lab && tar -C lab -xzf lab.gnet
   cat > lab/configs/host1.init.conf <<'EOF'
   #!/bin/bash
   ip route add 10.99.0.0/16 via 10.0.1.2
   EOF
   tar -C lab -czf lab.gnet .
   gonetem-console open lab.gnet

See also
--------

* :ref:`topology` — ``network.yml`` reference.
* :ref:`nodes` — ``configurationFiles``/``configurationFolders`` declaration.
* :ref:`ovs` — the switch configuration document.
* :ref:`commands` — ``create``, ``extract``, ``edit``, ``save``, ``saveAs``.
