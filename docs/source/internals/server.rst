.. _internals-server:

Server internals
================

``gonetem-server`` is a single Go binary exposing the ``netem.Netem`` gRPC
service. This page covers its startup, the service layer, the streaming
protocols and the lifecycle details that matter when operating or extending it.

Startup
-------

``cmd/gonetem-server/main.go`` performs, in order:

#. **flag parsing** — ``-verbose``, ``-conf-file`` (default
   ``/etc/gonetem/config.yaml``), ``-log-file`` (empty: stderr);
#. **logging** — logrus with a text formatter; level ``info``, or ``debug``
   with ``-verbose``; output to the log file if given;
#. **configuration** — built-in defaults are loaded, then the configuration
   file is parsed if it exists (missing file: warning, defaults are used);
#. **signal handling** — ``SIGINT`` and ``SIGTERM`` cancel the root context;
#. **server construction** — ``server.NewServer()`` (an empty service struct);
#. **gRPC listener** in a goroutine:

   * TLS credentials are loaded if ``tls.enabled``; a failure exits with
     status 2;
   * the service is registered;
   * ``net.Listen("tcp", listen)``; a failure exits with status 2;
   * ``grpcServer.Serve(socket)``; a failure cancels the context.

On shutdown:

#. the listener is stopped with ``grpcServer.GracefulStop()``, so no handler can
   start after the projects are closed;
#. ``netemServer.Close()`` closes every open project, which removes all
   containers, namespaces, bridges and OVS instances;
#. if the listen address is a filesystem path (Unix socket), the leftover file
   is removed.

The ordering is deliberate: closing projects while RPCs are still accepted
would let a handler mutate the project map during iteration.

Service layer
-------------

``netemServer`` embeds ``proto.UnimplementedNetemServer`` so new RPCs added to
the proto do not break the build until implemented. It carries one injectable
field:

.. code-block:: go

   type netemServer struct {
       proto.UnimplementedNetemServer
       routeApplier func(context.Context, INetemNode, []link.RouteSpec) (int, error)
   }

``routeApplier`` is a test seam for ``RouteInstall``; production uses
``applyRoutesToNode``. Similar seams exist inside the topology manager for qdisc
batches (``qosBatchApplier``).

Handlers are thin by design. They:

#. look up the project (``ProjectGetOne``) and return a
   ``ProjectNotFoundError`` gRPC error when absent;
#. convert protobuf messages to internal structures
   (``getLinkConfig``, ``getNodeConfigFromRequest``, ``routeSpecsFromProto``,
   ``linkSyncActionFromProto``);
#. delegate to a topology manager method;
#. convert results back, including per-item statuses for batch RPCs.

The conversion layer is important for compatibility: internal structs use Go
types and YAML tags, protobuf messages use proto field names, and the two are
kept independent so that the wire contract and the file format can evolve
separately.

Project registry
----------------

``internal/server/project.go`` holds:

.. code-block:: go

   var (
       openProjects = make(map[string]*NetemProject)
       projectsMu   sync.RWMutex
       projectOpsMu sync.Mutex
   )

* ``projectsMu`` guards the map; reads (``ProjectGetOne``, ``ProjectGetMany``,
  ``ProjectIsExist``, ``ProjectIsIdExist``) take the read lock.
* ``projectOpsMu`` serialises open, save, get-configs and close, so a project
  cannot be saved while it is closing.
* ``ProjectOpen`` additionally checks name uniqueness before and after creating
  the project object, returns an error if the ID or name was taken.
* ``ProjectSave`` returns the archive as a ``bytes.Buffer`` while holding
  ``projectOpsMu``.
* ``ProjectClose`` closes the topology, deletes the registry entry, then removes
  the temporary directory (even when the topology close returned an error).

Project IDs are generated with ``utils.RandString(3)`` and retried until they
are unique among open projects.

Streaming protocols
-------------------

The server implements five streaming patterns.

Progress streams
~~~~~~~~~~~~~~~~

``TopologyRun``, ``TopologyReload`` and ``ProjectClose`` need to report progress
while a blocking operation runs. The pattern is:

.. code-block:: text

   handler
     progressCh := make(chan T)          // buffered for Run (100)
     ctx, cancel := context.WithCancel(background)
     defer cancel()
     go forward(progressCh -> stream)    // exits on ctx.Done()
     err := topology.Run(progressCh)     // blocking
     stream.Send(final message)
     return err

A ``progressBatcher`` coalesces per-node ticks: it accumulates counts per code
and flushes at most every 100 ms (and once at the end), so a 500-node topology
does not send 500 messages. ``progress.Set`` sends totals immediately.

The forwarder goroutine exits when the handler returns (deferred ``cancel``),
so no goroutine is leaked even if the client disconnects.

Save streams
~~~~~~~~~~~~

``ProjectSave`` sends ``NODE_COUNT``, one ``NODE_SAVE`` per node from the
topology save routine, then a single ``DATA`` message with the archive bytes.

Byte streams
~~~~~~~~~~~~

``NodeCapture`` and ``NodeCopyFrom`` stream raw bytes:

* capture: an ``io.Pipe`` connects the handler to the node's ``tcpdump``
  writer; a goroutine reads 256-byte chunks and sends ``STDOUT`` messages; an
  ``io.EOF`` from the pipe is a normal end;
* download: the server first copies the node file into a temporary directory
  under ``workdir`` (then removes it) and streams it in 1024-byte chunks. The
  copy-to-temp step means a large or slow node filesystem does not hold a gRPC
  send while the file is being read from the container.

Upload streams
~~~~~~~~~~~~~~

``NodeCopyTo`` is client-streaming. The server requires ``INIT`` first
(project, node, destination), then ``DATA`` chunks written to a temporary file,
and performs the actual copy on ``io.EOF``. Protocol violations
(``INIT`` twice, ``DATA`` before ``INIT``, no ``INIT`` at all) produce an
``AckResponse`` with ``status.code = ERROR``.

Interactive streams
~~~~~~~~~~~~~~~~~~~

``NodeExec`` is bidirectional. The handler:

#. receives the first message and requires code ``CMD``;
#. resolves the project and node;
#. creates two ``io.Pipe`` pairs (stdin, stdout) and a resize channel;
#. runs three things concurrently with an ``errgroup``:

   * a reader goroutine that dispatches ``DATA`` to the stdin pipe,
     ``RESIZE`` to the resize channel and ``CLOSE`` by cancelling the context;
   * a writer goroutine that reads the stdout pipe and sends ``STDOUT``
     messages;
   * ``node.ExecCommand``, which drives the actual process/PTY;

#. sends ``CLOSE`` on success or ``ERROR`` with the message on failure;
#. closes the pipes and waits for the goroutines.

Backpressure is handled by the pipes: if the client stops reading, the server
stops sending, and the node's process eventually blocks — the expected
behaviour for an interactive terminal.

Capture timing
~~~~~~~~~~~~~~

``NodeCapture`` sends ``OK`` **before** starting ``tcpdump`` so the client can
launch Wireshark and start reading immediately. Any error after that point is
sent as an ``ERROR`` message and the stream ends.

Batch operations
----------------

``RouteInstall``
~~~~~~~~~~~~~~~~

.. code-block:: text

   results := {each requested node: status ERROR "route installation was not executed"}
   pool := errgroup with limit routeInstallLimit(request.maxConcurrency)
   for each node batch: pool.Go(installNodeRoutes)
   pool.Wait()          // errors are swallowed; they are in results
   return all results

``installNodeRoutes`` is per node: check ``ctx``, resolve the node, parse the
routes (CIDR, gateway family, device), call the applier, record
``applied``/``durationMicros``. A node that is not running or an OVS node gets
an explanatory error. The pool limit is the server's
``docker.routeConcurrency`` capped by the request: a client can reduce
concurrency but never exceed the server limit.

``LinkUpdateMany``
~~~~~~~~~~~~~~~~~~

The topology method does the work; the handler only converts and counts:

* outcomes are in request order;
* ``Status.code`` is ``ERROR`` for a failed item, with the message;
* ``updated`` counts successful items;
* the envelope ``status`` is ``OK`` as long as the batch itself ran.

``LinkSyncMany``
~~~~~~~~~~~~~~~~

Same shape, plus per-action counters ``added``/``deleted``/``updated`` and a
per-item ``action`` echoed back. An unknown ``LinkSyncAction`` value from a
client is rejected before any work.

Configuration reads
-------------------

``NodeReadConfigFiles`` reports ``source = RUNNING`` when the node is currently
running (the server saves live configuration into a temporary directory) or
``ARCHIVE`` otherwise (it reads the project's ``configs`` directory). It always
returns ``status = OK`` even when reading failed; the failure is in the status
message. This lets the console display an empty or stale configuration instead
of dropping the whole prompt.

Shutdown
--------

``netemServer.Close`` iterates a snapshot of the project IDs and calls
``ProjectClose(id, nil)`` for each. Errors are logged and the remaining projects
are still closed. ``ProjectClose``:

* takes ``projectOpsMu``;
* calls ``Topology.Close(nil)`` (removes nodes, bridges, management network and
  OVS instance);
* deletes the registry entry;
* removes the temporary directory.

Because containers are force-removed, a crash of the server may leave containers
behind. ``ServerCleanContainers`` (``gonetem-console clean``) removes them by
name, and only for projects that are not open.

Logging and observability
-------------------------

* logrus is the only logging facility; there is no metrics endpoint and no
  tracing.
* Per-operation debug logs include timings, which makes ``-verbose`` the first
  tool for performance work: ``Run: all link setups took ...``, ``Close: node
  removal took ...``, ``Load: CheckTopology took ...``.
* Handlers attach ``project`` and ``node`` fields to their log entries.
* The server writes nothing to stdout; the console renders all user-facing
  output.

Testing hooks
-------------

The service layer is exercised by unit tests with fake nodes and injectable
functions:

* ``routeApplier`` replaces the netlink path in ``RouteInstall`` tests;
* ``qosBatchApplier`` (topology) replaces ``link.ApplyQoSBatch`` in link batch
  tests;
* topology tests construct managers directly and use the fallback linear link
  lookup, which is why ``getLinkLocked`` keeps a non-indexed path;
* tests that need namespaces or qdiscs are skipped unless run as root
  (``skipUnlessRoot``).

See :ref:`testing` for how to run them.

Extending the service
---------------------

To add an RPC:

#. add the RPC and messages to ``internal/proto/netem.proto``;
#. run ``make proto``;
#. implement the method on ``*netemServer``;
#. convert request/response types in ``internal/server`` (keep protobuf out of
   the topology manager);
#. add tests; use a seam if the logic needs the kernel;
#. document it in :ref:`api`.

Guidelines:

* return gRPC errors for "cannot do this at all" and in-band ``Status`` for
  expected per-item failures;
* preserve request order in batch results;
* never hold the topology write lock while doing Docker or netlink work that
  does not need it;
* use the existing worker-pool limits rather than unbounded goroutines.

See also
--------

* :ref:`architecture` — components and data model.
* :ref:`internals-topology` — the engine behind most handlers.
* :ref:`api` — the wire contract.
