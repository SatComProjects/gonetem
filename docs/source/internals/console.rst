.. _internals-console:

Console internals
=================

``gonetem-console`` is the operator-facing binary. It has no privileges and no
kernel access: everything goes through the gRPC API. This page documents its
structure and the mechanics behind consoles, captures, copies and the prompt.

Entry point and configuration
-----------------------------

``cmd/gonetem-console/main.go`` is three calls:

.. code-block:: go

   options.InitConsoleConfig()   // load ~/.config/gonetem-console/console.yaml
   console.Init()                // flags + cobra command tree
   console.Execute()             // run

``InitConsoleConfig``:

* loads built-in defaults, including the default terminal template;
* resolves the current user and the config path;
* creates the configuration directory if needed;
* reads the file when it exists, otherwise writes the defaults;
* any failure is fatal (``log.Fatalf``).

The console configuration is a plain struct (``options.NetemConsoleConfig``)
read once at startup. ``config set`` mutates it and rewrites the file
immediately; subsequent commands in the same process see the new value.

CLI structure
-------------

``internal/console/commands.go`` builds a cobra command tree:

.. code-block:: text

   rootCmd (gonetem-console)
     |- version
     |- clean
     |- list
     |- create
     |- connect
     |- open      --no-start, --name
     |- console   --shell
     |- pull
     |- update
     |- extract
     |- config
          |- show
          |- set

Global flag ``--server/-s`` is bound to ``serverFlag`` and takes precedence over
the configuration (``getServerUri``). Every command that needs the server calls
``NewClient(getServerUri())``, which builds:

* insecure credentials by default;
* TLS credentials when ``tls.enabled``, loading the CA and client key pair.

Connections are short-lived: each command opens one connection and closes it
with ``defer client.Conn.Close()``. There is no connection pooling; at the
scale of interactive use this is irrelevant, and it keeps error handling
simple.

Long-running commands (``pull``, ``update``, ``open``, ``save``, ``close``)
consume server streams and render progress with ``mpb`` or ``spinner``.

Interactive prompt
------------------

``NetemPrompt`` (``internal/console/prompt.go``) is a small command
interpreter:

.. code-block:: go

   type NetemPrompt struct {
       server    string
       prjID     string
       prjPath   string
       processes []*exec.Cmd          // terminals and captures to clean up
       commands  map[string]*NetemCommand
       nodes     []NetemNode          // completion
   }

   type NetemCommand struct {
       Desc  string
       Usage string
       Args  []string                  // regexp per positional argument
       Run   func(p *NetemPrompt, cmdArgs []string)
   }

Execution path
~~~~~~~~~~~~~~

``Execute(line)``:

#. trims the input and ignores empty lines;
#. handles ``quit``/``exit`` immediately: closes the project, then exits the
   process with status 0;
#. handles ``help``: prints every command with description and usage;
#. splits the line with ``shlex`` (shell-like quoting);
#. looks up the command; unknown commands print a hint;
#. checks the argument count against ``len(Args)+1``;
#. checks each argument against its regular expression;
#. runs the command function.

Argument validation lives in this central loop, so a malformed command never
reaches the server. Each command's ``Run`` either performs a local action
(``edit``, ``saveAs``, ``copy``) or wraps a server call with
``execWithClient``, which opens a connection, runs the function and closes the
connection.

Node names for completion are refreshed at prompt creation and after ``run``
and ``reload`` via ``ProjectGetStatus``. When the refresh fails (for example the
server went away), completion simply stays empty; commands still report their
own errors.

Completion
~~~~~~~~~~

``PromptCompleter`` (``internal/console/completer.go``):

* first word: command names with their descriptions;
* second word for ``console``, ``start``, ``stop``, ``restart``, ``shell`` and
  ``viewConfig``: node names, plus ``all`` except for ``restart``;
* the prompt is configured with ``completer.FilePathCompletionSeparator``, so
  file paths complete for ``copy``, ``config`` and ``saveAs``.

Other completers: ``ConnectCompleter`` (open projects with their open time),
``ConfirmComplete`` (yes/no) and ``ViewConfigCompleter`` (configuration labels).

Terminal consoles
-----------------

``startConsole`` does not run a command on the node directly; it spawns a
terminal on the operator machine. Sequence:

#. fetch the project status and refuse if the project or the node is not
   running;
#. build the command ``gonetem-console console [--shell] <prjID>.<node>``;
#. render the terminal template with ``html/template``:

   .. code-block:: go

      consoleCmd := struct{ Name, Cmd string }{nodeName, command}
      tmpl.Execute(&buf, consoleCmd)

   The default template opens xterm with a fixed title and the console
   command;
#. split the rendered string with ``shlex``;
#. ``exec.LookPath`` the terminal binary (a clear error if it is missing);
#. start the process and record it for cleanup.

``startConsoleAll`` iterates the running nodes and opens one terminal each.

The spawned process runs the console CLI with ``console <prj>.<node>``, which
calls ``StartRemoteConsole`` (``internal/console/remote.go``): it validates the
``prj.node`` identifier, asks the server for the console argv
(``NodeGetConsoleCmd``) and runs it through ``nodeExec``.

Interactive exec protocol (client side)
---------------------------------------

``nodeExec`` (``internal/console/exec.go``) implements the client half of
``NodeExec``:

#. put stdout in raw terminal mode and restore it on exit;
#. open the bidirectional stream;
#. send ``CMD`` with the project, node, argv, ``tty: true`` and the current
   terminal size;
#. start three concurrent activities:

   * **stdin pump** — polls stdin with ``select`` every 100 ms, reads, and sends
     ``DATA`` messages. The polling loop exists so the goroutine can notice that
     the output side closed and exit;
   * **output pump** — receives ``STDOUT``/``STDERR`` and writes to stdout;
     ``CLOSE`` ends it, ``ERROR`` carries the server-side error;
   * **signal monitor** — on ``SIGWINCH`` sends ``RESIZE`` with the new size; on
     ``SIGHUP`` (terminal closed) sends ``CLOSE``.

A mutex (``execSender``) serialises ``Send`` calls because two goroutines can
send on the same stream. When the output ends, the input pump is told to stop;
the terminal state is restored by a deferred call. Interrupts are handled by
the shell inside the node, not by the console.

Packet capture
--------------

``Capture`` (``prompt.go``):

#. split ``<node>.<if>``;
#. verify ``wireshark`` is in ``PATH``;
#. open ``NodeCapture`` and wait for the first message; an ``ERROR`` is
   displayed;
#. create an ``io.Pipe`` and start ``wireshark -o gui.window_title:<if>@<node>
   -k -i -`` with the pipe as stdin;
#. a goroutine reads the stream and writes ``STDOUT`` chunks into the pipe;
   on ``ERROR`` it kills Wireshark and returns;
#. the gRPC connection is closed when the stream ends.

Wireshark reads a pcap stream from stdin; closing its window terminates the
pipe, the write fails, and the console closes the stream, which makes the
server stop ``tcpdump``.

File copy
---------

``copy`` parses both arguments with ``splitCopyArg``:

.. code-block:: go

   func splitCopyArg(arg string) (container, path string) {
       if system.IsAbs(arg) { return "", arg }              // absolute => local
       parts := strings.SplitN(arg, ":", 2)
       if len(parts) == 1 || strings.HasPrefix(parts[0], ".") {
           return "", arg                                   // no colon / ./x => local
       }
       return parts[0], parts[1]                            // node:path
   }

Direction is decided by which sides have a node:

* ``fromNode`` — ``NodeCopyFrom``; the console creates/truncates the local
  destination and writes the received ``DATA`` chunks;
* ``toNode`` — ``NodeCopyTo``; the console checks that the source is a regular
  file, sends ``INIT``, then reads the file in 1024-byte chunks and sends
  ``DATA``, then waits for the acknowledgement;
* both or neither — an explicit error is printed; copying between nodes is not
  supported.

Progress rendering
------------------

``internal/console/progress.go`` maps ``TopologyRunMsg`` codes to four ``mpb``
bars:

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Message
     - Bar
   * - ``NODE_COUNT``
     - creates "Node - Start" and "Node - Configuration" bars
   * - ``LINK_COUNT``
     - creates "Link - Create"
   * - ``BRIDGE_COUNT``
     - creates "Bridge - Create"
   * - ``NODE_START`` / ``NODE_LOADCONFIG`` / ``LINK_SETUP`` / ``BRIDGE_START``
     - increments the matching bar
   * - ``NODE_MESSAGES``
     - completes all bars and prints the collected node messages

``ProjectCloseMsg`` uses two bars (nodes, bridges). ``ProjectSaveMsg`` uses one
bar ("Save nodes"). ``ProgressForceComplete`` sets the current value to the
total so a stream that ends early still leaves a completed display;
``ProgressAbort`` aborts on error.

The server batches progress increments every 100 ms, so on a fast host the bars
may jump several units at once — expected behaviour.

Editor integration
------------------

``edit``:

#. verifies the configured editor exists in ``PATH``;
#. reads ``network.yml`` with ``ReadNetworkFile``;
#. writes it to ``/tmp/gonetem-network-<prjID>``;
#. runs the editor through ``EditFile``, which attaches stdin/stdout for
   ``vi``/``vim``/``nano`` (interactive editors) and runs other editors with
   inherited streams;
#. reads the file back and uploads it with ``WriteNetworkFile``;
#. removes the temporary file.

Failure to upload is fatal (the user would otherwise believe the change was
applied). ``reload`` is a separate, explicit step — intentionally, so a syntax
error can be fixed before the running topology is torn down.

Error display conventions
-------------------------

.. list-table::
   :header-rows: 1
   :widths: 24 76

   * - Helper
     - Behaviour
   * - ``RedPrintf``
     - red error message (validation, RPC errors), command continues.
   * - ``MagentaPrintf``
     - warning-style status messages (for example "node is not running").
   * - ``Fatal``
     - red message then ``os.Exit(1)``; used when the CLI cannot continue.
   * - ``color.GreenString``
     - success messages (``Network is OK``, images pulled, project created).

Server-side ``Status`` errors are printed in red but do not exit the prompt;
fatal exits are reserved for unusable states (unknown config key, invalid
project extension, unreachable server at startup of a command that cannot
proceed).

Process cleanup
---------------

``NetemPrompt.Close`` (invoked by ``quit``/``exit``, not by a normal RPC error):

#. waits up to 100 ms for each spawned terminal/capture process, then kills it;
#. opens a connection and calls ``ProjectClose``, consuming the progress
   stream;
#. ``Execute`` then exits the process.

Terminals left open by a crashed console are independent processes; closing them
simply ends their exec stream.

Testing
-------

The console package is tested with unit tests that do not need a server:

* ``TestExecuteIgnoresCommentOnlyInput`` ensures the prompt does not panic on
  input that is not a known command;
* argument parsing (``splitCopyArg``), completion and progress helpers are
  covered where they are pure functions.

Everything that needs a live server is exercised by the Python integration tests
(:ref:`testing`).

See also
--------

* :ref:`commands` — the user-facing command reference.
* :ref:`api` — the RPCs the console calls.
* :ref:`internals-server` — the other side of the streams.
