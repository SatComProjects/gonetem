.. _commands:

Commands
========

GoNetEm has two command surfaces:

* the **CLI** (``gonetem-console``), used to manage projects and the server;
* the **interactive prompt** (``[project]>``), used to act on a running
  project.

This page documents both. Command names are case-sensitive.

``gonetem-console`` CLI
-----------------------

Global usage:

.. code-block:: text

   gonetem-console [command] [flags]

Global flags:

.. list-table::
   :header-rows: 1
   :widths: 20 80

   * - Flag
     - Description
   * - ``-s, --server string``
     - Override the server URI from the console configuration
       (for example ``-s 192.0.2.10:10110``).
   * - ``-h, --help``
     - Help for the command or subcommand.

.. list-table::
   :header-rows: 1
   :widths: 14 46 40

   * - Command
     - Purpose
     - Requires server
   * - ``create``
     - Create an empty ``.gnet`` project locally.
     - no
   * - ``open``
     - Upload a project, optionally run it, and enter its prompt.
     - yes
   * - ``connect``
     - Attach to a project already open on the server.
     - yes
   * - ``list``
     - List projects open on the server.
     - yes
   * - ``console``
     - Open a console to one node of a running project.
     - yes
   * - ``extract``
     - Extract the files of a ``.gnet`` project locally.
     - no
   * - ``pull``
     - Pull all Docker images referenced by the server.
     - yes
   * - ``clean``
     - Remove gonetem containers no open project owns.
     - yes
   * - ``update``
     - Self-update the server.
     - yes
   * - ``version``
     - Print the console version.
     - no
   * - ``config``
     - Show or modify the console configuration.
     - no

create
~~~~~~

.. code-block:: bash

   gonetem-console create <path>.gnet

Creates a ``.gnet`` archive containing an empty ``network.yml``. The extension
must be ``.gnet`` and the file must not already exist. This command does not
contact the server.

extract
~~~~~~~

.. code-block:: bash

   gonetem-console extract <path>.gnet <existing-directory>

Extracts the project archive (``network.yml`` and any ``configs/`` files) into
the destination directory. The destination must exist and be a directory.

open
~~~~

.. code-block:: bash

   gonetem-console open [--no-start] [--name <name>] <path>.gnet

Reads the project, sends it to the server with ``ProjectOpen`` and enters the
interactive prompt. By default the topology is started immediately, with the
same progress bars as the ``run`` prompt command.

.. list-table::
   :header-rows: 1
   :widths: 22 78

   * - Flag
     - Description
   * - ``--no-start``
     - Open the project without starting it. Use the ``run`` prompt command
       later.
   * - ``--name <name>``
     - Name used to identify the project on the server. Defaults to the file
       name without ``.gnet``. The server refuses to open two projects with the
       same name; this flag lets you open the same file twice under different
       names.

If the project opens but starting fails, the prompt is still entered so you can
inspect the topology (``status``, ``check``) and retry.

connect
~~~~~~~

.. code-block:: bash

   gonetem-console connect

Lists the projects open on the server, prompts for one (with completion) and
enters its prompt. Because the console did not open the project, it has no
local project path: ``save`` is unavailable and you must use ``saveAs``.

list
~~~~

.. code-block:: bash

   gonetem-console list

Prints one line per open project with its name and opening time, or a message
when no project is open.

console
~~~~~~~

.. code-block:: bash

   gonetem-console console [--shell] <project-id>.<node-name>

Opens a console to a node of a running project. This is the command spawned in
xterm by the prompt's ``console`` command; it is rarely typed by hand, but it
is useful in scripts and terminal multiplexers.

``--shell`` forces the node's shell command instead of its dedicated console
(for example ``bash`` instead of ``vtysh``).

pull
~~~~

.. code-block:: bash

   gonetem-console pull

Streams the pull of every image referenced by the server configuration:
router, host, server, P4 switch, OVS image and each ``extraNodes`` image.
Duplicates are pulled once. For each image the console prints either
``Image <name> has been pulled`` or the error, and continues with the next one.

clean
~~~~~

.. code-block:: bash

   gonetem-console clean

Asks for confirmation, then removes every Docker container whose name matches
the gonetem naming scheme (``ntm<project-id>.<node>``) and whose project is no
longer open. Use it after a server crash or an unclean shutdown.

Names are parsed with ``^ntm(\w+)\.\w+``; containers of open projects are never
removed, and the OVS container of an open project is protected as well.

update
~~~~~~

.. code-block:: bash

   gonetem-console update

Runs the self-update on the server and prints each step as it happens (``git``,
``go``, ``build``, ``install``, ``restart``, ``complete``), plus the output
lines of the executed commands. Requires ``update.enabled: true`` in the server
configuration. The server restarts at the end, so a dropped stream is normal.
See :ref:`internals-update`.

version
~~~~~~~

.. code-block:: bash

   gonetem-console version

Prints ``Gonetem network emulator v<version>``.

config
~~~~~~

.. code-block:: bash

   gonetem-console config show
   gonetem-console config set <key> <value>

Accepted keys: ``server``, ``editor``, ``terminal``, ``tls.enabled``,
``tls.ca``, ``tls.cert``, ``tls.key``. See :ref:`configuration` for their
meaning. An unknown key is a fatal error.

Interactive prompt
------------------

The prompt is entered with ``open`` or ``connect``:

.. code-block:: text

   Welcome to gonetem console 0.8.0
   Please use `exit` to close the project

   [lab]>

Features:

* command and node-name completion (press ``Tab``);
* shell-like quoting of arguments;
* per-command argument validation with a usage reminder;
* ``help`` lists all commands;
* ``quit``/``exit`` close the project and leave the console.

.. list-table::
   :header-rows: 1
   :widths: 14 26 60

   * - Command
     - Usage
     - Description
   * - ``capture``
     - ``capture <node>.<if>``
     - Capture traffic on an interface into Wireshark.
   * - ``check``
     - ``check``
     - Validate the topology file and print the errors.
   * - ``config``
     - ``config <dir>``
     - Save the node configuration files into a local directory.
   * - ``console``
     - ``console <node>|all``
     - Open a console to one node or to every running node.
   * - ``copy``
     - ``copy <src> <dst>``
     - Copy a file between the host and a node.
   * - ``edit``
     - ``edit``
     - Edit the topology file with the configured editor.
   * - ``exec``
     - ``exec <node> "<cmd>"``
     - Execute a command on a node and print its output.
   * - ``ifState``
     - ``ifState <node>.<if> up|down``
     - Enable or disable a node interface.
   * - ``reload``
     - ``reload``
     - Stop, reload the topology file and start again.
   * - ``restart``
     - ``restart <node>``
     - Stop then start a node.
   * - ``run``
     - ``run``
     - Start a project that is not running.
   * - ``save``
     - ``save``
     - Save the topology and node configurations into the local project file.
   * - ``saveAs``
     - ``saveAs <path>.gnet``
     - Save the project to a new local file.
   * - ``shell``
     - ``shell <node>|all``
     - Open a shell console, whatever the node type.
   * - ``start``
     - ``start <node>|all``
     - Start one node or all stopped nodes.
   * - ``stop``
     - ``stop <node>|all``
     - Stop one node or all running nodes.
   * - ``status``
     - ``status``
     - Show the project state, nodes and interface states.
   * - ``viewConfig``
     - ``viewConfig <node>``
     - Display the current configuration file(s) of a node.
   * - ``quit`` / ``exit``
     - ``quit``
     - Close the project and exit the console.
   * - ``help``
     - ``help``
     - List the available commands.

capture
~~~~~~~

.. code-block:: text

   capture <node_name>.<if_number>

Example: ``capture R1.1``.

Requires ``wireshark`` in ``PATH``. The server streams a pcap produced by
``tcpdump -w - -s 0 -U -i <if>`` from inside the node's namespace; the console
starts Wireshark with ``-k -i -`` and pipes the stream into it. The Wireshark
window title is set to ``<if>@<node>``. Closing Wireshark stops the capture.

The interface must be running; capturing on an OVS node captures the switch
port (``<switch>.<if>`` inside the OVS container).

check
~~~~~

Validates ``network.yml`` and prints either ``Network is OK`` or the collected
errors. The same validation runs when the topology is loaded; ``check`` lets
you find errors without stopping a running project.

config
~~~~~~

.. code-block:: text

   config <dest_path>

The destination must exist and be a directory. The command asks the server to
save every running node configuration, downloads the resulting ``configs``
archive and extracts it into the destination. Files are named
``<node>.<suffix>`` (see :ref:`project-format`). This is the way to collect
FRR configurations, DHCP files, TFTP/BIND archives and saved network state
without rewriting the project.

console and shell
~~~~~~~~~~~~~~~~~

.. code-block:: text

   console <node_name>
   console all
   shell <node_name>
   shell all

``console`` opens the node-specific console:

* ``docker.host``, ``docker.server``, ``namespace`` → ``/bin/bash``;
* ``docker.router`` → ``vtysh``;
* ``docker.p4sw`` → ``simple_switch_CLI``;
* ``ovs`` → the GoNetEm switch console (see :ref:`ovs`).

``shell`` always opens ``/bin/bash`` (or the node's configured shell command).

Consoles are opened locally by spawning the terminal template configured for
the console. Each terminal runs ``gonetem-console console [--shell]
<project-id>.<node>``, which establishes a new gRPC exec stream with a PTY.
Terminal resize events and hang-ups are forwarded to the server. ``console all``
opens one terminal per running node.

copy
~~~~

.. code-block:: text

   copy <source> <destination>

At least one side must be a node, written ``node:path``:

.. code-block:: text

   copy ./local-file.bin R1:/tmp/file.bin      # host -> node
   copy R1:/etc/frr/frr.conf ./frr.conf        # node -> host
   copy host1:/tmp/a.log ./a.log

Rules:

* ``node:path`` sends the file to the node; ``path`` alone (or an absolute
  path, or a path starting with ``.``) is a local path.
* Copying between two nodes is not supported.
* Local sources must be regular files; local destinations are created or
  truncated.
* On Docker nodes the copy uses the Docker archive API; on ``namespace`` nodes
  the host filesystem is used directly, and only regular files are supported.
* OVS nodes do not support ``copy``.

edit
~~~~

.. code-block:: text

   edit

Downloads ``network.yml`` from the server into a temporary file, opens it with
the configured editor (``vim`` by default), and writes the file back to the
server when the editor exits. It does **not** apply the changes: run ``check``
then ``reload``.

If the editor is not found in ``PATH``, the command reports an error and does
nothing.

exec
~~~~

.. code-block:: text

   exec <node_name> "<command>"

Executes a command inside the node and prints its combined output. The command
string is parsed like a shell command line (quotes are honoured) but no shell
is involved — use ``sh -c "..."`` when you need redirections, pipes or
variables.

.. code-block:: text

   [lab]> exec R1 "vtysh -c 'show ip route'"
   [lab]> exec host1 "sh -c 'ip -br addr > /tmp/addr.txt'"

On Docker nodes the command runs through ``nsenter`` into the container's
namespaces; on ``namespace`` nodes it runs through ``nsenter --net=...`` on the
host. The node must be running.

For interactive sessions and streaming output, use ``console``/``shell``
instead.

ifState
~~~~~~~

.. code-block:: text

   ifState <node_name>.<if_number> up|down

Example: ``ifState R1.0 down``.

Changes the administrative state of one interface inside the node. This is the
fastest way to emulate a link failure: the node keeps its links and QoS, only
the endpoint goes down. The state is reflected by ``status`` and is **not**
written to ``network.yml``; restarting the topology resets interfaces to up.

reload
~~~~~~

.. code-block:: text

   reload

Performs a full restart with the current ``network.yml``:

#. close every node (Docker containers are removed, namespaces and veth pairs
   are deleted);
#. reload and validate the topology file, recreating the node objects;
#. if the project was running before the reload, start it again.

Use it after ``edit``. Node configurations saved in the project are reapplied,
so runtime changes made with ``exec`` and not saved are lost.

restart
~~~~~~~

.. code-block:: text

   restart <node_name>

Stops then starts one node. On a Docker node, interfaces are moved out of the
container and back in, so links and their QoS are preserved. ``restart all`` is
not supported; use ``stop all`` followed by ``start all``.

run
~~~

.. code-block:: text

   run

Starts the project if it is not already running. Running an already-running
topology returns immediately (the server logs a warning).

save and saveAs
~~~~~~~~~~~~~~~

.. code-block:: text

   save
   saveAs <path>.gnet

``save`` asks the server to snapshot every running node configuration into the
project's temporary directory, then downloads the project archive and writes it
to the path used at ``open``. It is unavailable on projects opened with
``connect`` (no local path) — use ``saveAs``.

``saveAs`` does the same and writes to the given path. The extension must be
``.gnet``.

The download is streamed: the server reports the number of nodes to save and
then one progress tick per node.

start and stop
~~~~~~~~~~~~~~

.. code-block:: text

   start <node_name>|all
   stop <node_name>|all

``start`` starts one node or every stopped node, then configures its
interfaces and loads its configuration. ``stop`` stops one node or every
running node and keeps their links intact.

The project must be running for ``start`` and ``stop`` (a stopped node is not
a stopped project). Use ``run`` for a stopped project.

status
~~~~~~

.. code-block:: text

   status

Prints:

.. code-block:: text

   Project lab
   - Id: XyZ
   - OpenAt: 2025-01-01 12:00:00
   - State: Running
   - Nodes:
      - host1: Running
        - eth0: Up
      - sw1: Running
        - sw1.0: Up
        - sw1.1: Up

Interface states are reported only while the topology is running. The list is
also used for completion after ``console``, ``start``, ``stop``, ``restart``,
``shell`` and ``viewConfig``.

viewConfig
~~~~~~~~~~

.. code-block:: text

   viewConfig <node_name>

Displays the configuration files of a node. If the node is running, the server
first runs the ``saveConfig`` commands and reads the live configuration;
otherwise it returns the files stored in the project. When several files are
available, the prompt asks which one to display (with completion on the
labels). Files that do not exist are shown empty.

quit and exit
~~~~~~~~~~~~~

Close the project on the server (removing all emulated resources and the
temporary directory), terminate the terminals and captures opened by this
console, then exit with status 0.

help
~~~~

Prints the name, description and usage of every command.

Argument validation
-------------------

The prompt validates the number and format of arguments before executing a
command. The accepted patterns are:

.. list-table::
   :header-rows: 1
   :widths: 20 80

   * - Argument
     - Pattern
   * - node name
     - ``^\w+$``
   * - node interface
     - ``^\w+\.\d+$``
   * - interface state
     - ``^up|down$``
   * - destination path (``config``)
     - ``^.+$``
   * - project path (``saveAs``)
     - ``^.*\.gnet$``

A wrong number of arguments or a pattern mismatch prints the usage and does not
call the server.

Exit codes
~~~~~~~~~~

``gonetem-console`` exits with status 1 on fatal errors (fatal configuration
problems, unreachable server during a command that cannot continue, invalid
project extension, unknown config key). Commands that report a runtime error
from the server print it in red and keep the console running.

See also
--------

* :ref:`getting-started` — a guided session using these commands.
* :ref:`api` — the RPC behind each command.
* :ref:`internals-console` — implementation of the CLI and prompt.
