.. _api:

gRPC API
========

``gonetem-server`` exposes the ``netem.Netem`` service. The contract is defined
in ``internal/proto/netem.proto``; Go stubs are generated into
``internal/proto/netem.pb.go`` and ``internal/proto/netem_grpc.pb.go``.
Everything the console does is an RPC on this service.

This page is the reference for API consumers. It covers the transport, the
error model, every RPC, the streaming protocols and the message types.

Transport and connection
------------------------

.. list-table::
   :header-rows: 1
   :widths: 26 74

   * - Property
     - Value
   * - Service
     - ``netem.Netem``
   * - Default endpoint
     - ``localhost:10110`` (``listen`` in the server configuration)
   * - Transport
     - HTTP/2, gRPC. Plaintext by default; mutual TLS when ``tls.enabled`` is
       set on both ends (:ref:`tls`).
   * - Authentication
     - None in plaintext mode. Mutual TLS with a client certificate otherwise.
   * - Server reflection
     - Not enabled. Clients need the ``.proto`` file (or generated stubs).
   * - Max message size
     - gRPC defaults (4 MiB receive). Project uploads and configuration
       downloads are single messages, so very large projects may need a raised
       limit on the client side.
   * - Proto package
     - ``netem``; Go package ``github.com/mroy31/gonetem/internal/proto``.

Generate stubs for your language with the standard toolchain; for Python:

.. code-block:: bash

   python -m grpc_tools.protoc \
     -I internal/proto \
     --python_out=./gen --grpc_python_out=./gen \
     internal/proto/netem.proto

Minimal Python client:

.. code-block:: python

   import grpc
   from google.protobuf import empty_pb2
   import netem_pb2 as pb
   import netem_pb2_grpc as pb_grpc

   channel = grpc.insecure_channel("localhost:10110")
   stub = pb_grpc.NetemStub(channel)

   # server version
   print(stub.ServerGetVersion(empty_pb2.Empty()).version)

   # open a project from a local .gnet archive
   with open("lab.gnet", "rb") as fh:
       opened = stub.ProjectOpen(pb.OpenRequest(name="lab", data=fh.read()))
   prj_id = opened.id

   # run it, consuming progress
   for msg in stub.TopologyRun(pb.ProjectRequest(id=prj_id)):
       print(msg.code, msg.total)

   # query status
   status = stub.ProjectGetStatus(pb.ProjectRequest(id=prj_id))
   for node in status.nodes:
       print(node.name, node.running)

   # close it
   for msg in stub.ProjectClose(pb.ProjectRequest(id=prj_id)):
       pass

With mutual TLS, create the channel with credentials:

.. code-block:: python

   import grpc
   creds = grpc.ssl_channel_credentials(
       root_certificates=open("ca-cert.pem", "rb").read(),
       private_key=open("client-key.pem", "rb").read(),
       certificate_chain=open("client-cert.pem", "rb").read(),
   )
   channel = grpc.secure_channel("gonetem.example.org:10110", creds)

Error model
-----------

The API uses two complementary mechanisms.

**gRPC status errors** (the RPC fails) are returned for conditions that make
the response meaningless: unknown project or node, invalid JSON-ish payloads
that cannot be decoded, internal failures. The server returns Go errors, which
gRPC maps to status code ``Unknown``; do not rely on a specific code, treat any
non-OK status as an error and surface ``details()``.

.. code-block:: text

   rpc error: code = Unknown desc = Project XyZ: not found

**In-band status messages** (``Status``) are returned inside the response for expected,
user-facing failures. ``Status.code`` is ``OK`` or ``ERROR`` and
``Status.error`` carries the message. This is used by:

* ``ProjectOpen`` — a project with the same name is already open;
* ``TopologyCheck`` — validation errors;
* ``NodeRun`` — the command could not be prepared or started;
* ``NodeReadConfigFiles`` — configuration files could not be read;
* every batch RPC — one ``Status`` per item.

Streaming RPCs report errors either as an ``ERROR`` message in the stream or as
a gRPC error, depending on when the failure happens. Clients must handle both.

Project identifiers
-------------------

A project is identified by a short, random identifier generated when it is
opened (three alphanumeric characters, guaranteed unique among open projects).
It is valid only while the project is open: after ``ProjectClose`` the
identifier is gone, even if the project was saved. Never persist project IDs;
persist the ``.gnet`` file.

RPC reference
-------------

Server
~~~~~~

``ServerGetVersion``
   ``Empty`` → ``VersionResponse``

   Returns the server binary version (``internal/options.VERSION``). Useful to
   detect a client/server mismatch.

   .. code-block:: protobuf

      rpc ServerGetVersion(google.protobuf.Empty) returns (VersionResponse) {}

``ServerPullImages``
   ``Empty`` → stream ``PullSrvMsg``

   Pulls every image referenced by the server configuration: router, host,
   server, P4 switch, OVS image and each ``extraNodes`` image (duplicates
   removed, empty names skipped). For each image the server sends ``START``,
   then ``OK`` or ``ERROR`` with the image name, and continues with the next
   image. Errors are per image, not fatal.

``ServerCleanContainers``
   ``Empty`` → ``AckResponse``

   Removes Docker containers named ``ntm<project-id>.<node>`` whose project is
   not currently open. Used by ``gonetem-console clean``. Returns a gRPC error
   if the Docker client cannot be created, the container list cannot be read,
   or a removal fails.

``ServerUpdate``
   ``Empty`` → stream ``UpdateSrvMsg``

   Runs the self-update. Messages have a ``step`` (``git``, ``go``, ``build``,
   ``install``, ``restart``, ``complete``) and a ``message``. ``START`` marks
   the beginning of a step, ``LOG`` carries command output, ``OK`` announces
   success and ``ERROR`` a failure. Only one update runs at a time; a
   concurrent request receives ``an update is already running``. Requires
   ``update.enabled: true``.

Projects
~~~~~~~~

``ProjectGetMany``
   ``Empty`` → ``PrjListResponse``

   Lists open projects. ``Info`` contains ``id``, ``name`` and ``openAt``
   formatted as ``YYYY-MM-DD HH:MM:SS``.

``ProjectOpen``
   ``OpenRequest`` → ``PrjOpenResponse``

   Uploads a project archive (``data``, gzipped tar) and opens it with the
   logical name ``name``. The server extracts the archive under its
   ``workdir``, validates and loads ``network.yml``, and returns a generated
   ``id``. Two open projects cannot share a name; the duplicate case is
   reported in ``status`` with ``code = ERROR``. Loading errors are returned
   as gRPC errors.

   Opening a project does **not** start it. Send ``TopologyRun`` for that.

``ProjectClose``
   ``ProjectRequest`` → stream ``ProjectCloseMsg``

   Stops and removes every resource of the project, deletes its temporary
   directory and forgets the identifier. Progress messages report the number
   of nodes and bridges (``NODE_COUNT``, ``BRIDGE_COUNT``) and one tick per
   node/bridge removed (``NODE_CLOSE``, ``BRIDGE_CLOSE``).

``ProjectSave``
   ``ProjectRequest`` → stream ``ProjectSaveMsg``

   Saves the configuration of every running node into the project directory
   and returns the resulting project archive. ``NODE_COUNT`` announces how many
   nodes will be saved, ``NODE_SAVE`` is sent after each node, and the final
   ``DATA`` message contains the gzipped tar archive.

``ProjectGetNodeConfigs``
   ``ProjectRequest`` → ``FileResponse``

   Saves the node configurations, then returns an archive of the ``configs``
   directory only. Used by ``config <dir>``.

``ProjectGetStatus``
   ``ProjectRequest`` → ``StatusResponse``

   Returns the project metadata, whether the topology is running, and the node
   list. For each node: name, running flag, and — only while the topology is
   running — the interface names and up/down states.

``ReadNetworkFile``
   ``ProjectRequest`` → ``FileResponse``

   Returns the raw content of ``network.yml``.

``WriteNetworkFile``
   ``WNetworkRequest`` → ``AckResponse``

   Replaces ``network.yml`` atomically. This does not validate or apply the
   file; send ``TopologyCheck`` and ``TopologyReload`` afterwards. The write
   also updates the server's cache of the last synchronised content, so an
   immediately following live update does not rewrite the file needlessly.

Topology
~~~~~~~~

``TopologyCheck``
   ``ProjectRequest`` → ``AckResponse``

   Validates the topology file. Returns ``status.code = ERROR`` and the
   collected messages when invalid; it is not a gRPC error. This is exactly
   what the ``check`` prompt command uses.

``TopologyRun``
   ``ProjectRequest`` → stream ``TopologyRunMsg``

   Starts the topology. Progress messages: ``NODE_COUNT``, ``BRIDGE_COUNT``,
   ``LINK_COUNT`` (totals), then ``NODE_START``, ``LINK_SETUP``,
   ``BRIDGE_START`` and ``NODE_LOADCONFIG`` ticks (``total`` carries the
   increment, batched every 100 ms). The final ``NODE_MESSAGES`` message
   contains the output collected from nodes whose type logs its configuration
   commands. Running an already-running topology returns immediately.

   On failure the server rolls back the runtime state (closes resources and
   reloads the topology model) and the stream ends with an error.

``TopologyReload``
   ``ProjectRequest`` → stream ``TopologyRunMsg``

   Closes the runtime resources, reloads ``network.yml``, and runs it again if
   the project was running. Same message protocol as ``TopologyRun``.
   Validation errors abort the reload before anything is started.

``TopologyStartAll``
   ``ProjectRequest`` → ``AckResponse``

   Starts every stopped node and loads its configuration. Fails if the
   topology is not running (``topology is not running; use TopologyRun
   first``).

``TopologyStopAll``
   ``ProjectRequest`` → ``AckResponse``

   Stops every running node. Links are kept: Docker interfaces are moved to the
   node's private namespace and back on the next start.

Nodes
~~~~~

``NodeAdd``
   ``NodeAddRequest`` → ``AckResponse``

   Creates a node in the project's topology:

   .. code-block:: protobuf

      message NodeAddRequest {
          string prjId = 1;
          string node = 2;          // node name, ^\w+$
          NodeConfigMsg config = 3;
          bool sync = 4;            // persist in network.yml
      }

      message NodeConfigMsg {
          string type = 1;          // docker.router, docker.host, ovs, namespace, ...
          bool ipv6 = 2;
          bool mpls = 3;
          repeated string vrfs = 4;
          repeated VrrpOptions vrrps = 5;
          repeated string volumes = 6;
          string image = 7;
          bool launch = 8;
          MgntOptions mgnt = 9;
      }

   If the topology is running and ``launch`` is true, the node is started and
   its interfaces configured. ``sync`` rewrites ``network.yml``. Configuration
   is validated with the same rules as the topology file.

``NodeDel``
   ``NodeDelRequest`` → ``AckResponse``

   Removes a node. All links and bridge attachments referencing its interfaces
   are deleted, the container or namespace or OVS bridge is removed, and with
   ``sync`` the topology file is rewritten.

``NodeStart`` / ``NodeStop`` / ``NodeRestart``
   ``NodeRequest`` → ``AckResponse``

   Start, stop or restart one node. ``NodeStart`` also configures interfaces and
   loads the configuration; ``NodeRestart`` is stop followed by start. The
   project must be running.

``NodeSetIfState``
   ``NodeIfStateRequest`` → ``AckResponse``

   Sets an interface up or down inside the node. ``ifIndex`` is the interface
   index used in the topology (``eth<ifIndex>``).

``NodeGetConsoleCmd``
   ``ConsoleCmdRequest`` → ``ConsoleCmdResponse``

   Returns the argv of the console command for a node. ``shell = true`` returns
   the shell command instead of the dedicated console (for a router: ``bash``
   instead of ``vtysh``).

``NodeRun``
   ``NodeRunRequest`` → ``NodeRunResponse``

   Runs one command and returns its captured ``stdout``, ``stderr`` and
   ``exitCode``. On Docker nodes the server executes
   ``nsenter -t <pid> -a -- <cmd>``; on namespace nodes
   ``nsenter --net=<ns> -- <cmd>``. For interactivity use ``NodeExec``; for
   routes prefer ``RouteInstall``.

   .. code-block:: protobuf

      message NodeRunRequest {
          string prjId = 1;
          string node = 2;
          repeated string cmd = 3;
      }

      message NodeRunResponse {
          Status status = 1;
          bytes stdout = 2;
          bytes stderr = 3;
          int32 exitCode = 4;
      }

``NodeReadConfigFiles``
   ``NodeRequest`` → ``ConfigFilesResponse``

   Returns the configuration files of a node as named byte blobs. ``source``
   is ``RUNNING`` when the node was running (the server runs the save commands
   first) or ``ARCHIVE`` when it read the files stored in the project. Labels
   are the ``label`` fields declared by the node type.

``NodeCapture``
   ``NodeInterfaceRequest`` → stream ``CaptureSrvMsg``

   Starts a packet capture on an interface. The first message is ``OK``, then
   ``STDOUT`` messages carry raw pcap data (256-byte chunks), and an ``ERROR``
   message reports a failure. The capture runs until the client cancels the
   stream or closes the connection; the server then terminates ``tcpdump``.

``NodeCopyFrom``
   ``CopyMsg`` (``INIT``) → stream ``CopyMsg``

   Downloads a file from a node. The server copies the file into a temporary
   directory, then streams it as ``DATA`` messages. ``ERROR`` reports a
   failure. The request carries ``prjId``, ``node`` and ``nodePath``.

``NodeCopyTo``
   stream ``CopyMsg`` → ``AckResponse``

   Uploads a file to a node. The client first sends ``INIT`` (with ``prjId``,
   ``node`` and ``nodePath`` = destination), then ``DATA`` chunks, then closes
   the stream. The server writes a temporary file and copies it into the node
   on close. The acknowledgement is the final ``AckResponse``.

``NodeExec``
   bidirectional stream ``ExecCltMsg`` ⇄ ``ExecSrvMsg``

   Interactive command execution with an optional PTY. This is the RPC used by
   ``gonetem-console console``. Protocol:

   #. the client sends ``CMD`` with ``prjId``, ``node``, ``cmd`` and the ``tty``
      flag (plus initial ``ttyWidth``/``ttyHeight``);
   #. the client sends ``DATA`` for stdin and ``RESIZE`` on terminal resize;
   #. the server sends ``STDOUT`` chunks (PTY output), ``STDERR`` and ``ERROR``;
   #. the server ends the stream with ``CLOSE`` when the command exits; the
      client may send ``CLOSE`` to terminate it.

   .. code-block:: protobuf

      message ExecCltMsg {
          enum Code { CMD = 0; DATA = 1; RESIZE = 2; ERROR = 3; CLOSE = 4; }
          Code code = 1;
          string prjId = 2;
          string node = 3;
          repeated string cmd = 4;
          bool tty = 5;
          bytes data = 6;
          int32 ttyWidth = 7;
          int32 ttyHeight = 8;
      }

      message ExecSrvMsg {
          enum Code { STDOUT = 0; STDERR = 1; ERROR = 2; CLOSE = 3; }
          Code code = 1;
          bytes data = 3;
      }

Routing
~~~~~~~

``RouteInstall``
   ``RouteInstallRequest`` → ``RouteInstallResponse``

   Applies a batch of route changes to several nodes. Each node's batch is
   executed in that node's namespace through netlink, with one worker pool for
   the whole request.

   .. code-block:: protobuf

      message RouteInstallRequest {
          string prjId = 1;
          map<string, RouteBatch> nodes = 2;
          uint32 maxConcurrency = 3;   // 0 = server default
      }

      message RouteBatch { repeated RouteSpec routes = 1; }

      message RouteSpec {
          RouteAction action = 1;      // ROUTE_REPLACE (0) or ROUTE_DELETE (1)
          string destination = 2;      // CIDR, e.g. 10.0.0.0/15
          string gateway = 3;          // empty for an on-link route
          string device = 4;           // interface name inside the node
      }

      message RouteInstallResult {
          Status status = 1;
          uint32 applied = 2;
          bytes stdout = 3;            // empty on the netlink path
          bytes stderr = 4;
          int32 exitCode = 5;
          int64 durationMicros = 6;
      }

      message RouteInstallResponse {
          Status status = 1;
          map<string, RouteInstallResult> results = 2;
      }

   Semantics:

   * the pool size is ``docker.routeConcurrency`` (default 32); a request value
     can only lower it;
   * per-node failures are reported in ``results`` and do not abort the other
     nodes; a node that was not processed keeps ``route installation was not
     executed``;
   * ``applied`` counts successful route operations in that node;
   * routes are installed with ``Scope: universe`` when a gateway is given and
     ``Scope: link`` otherwise, in the main table, protocol ``boot``;
   * deleting a route that does not exist is a success; if an exact delete does
     not match, the server looks up routes with the same destination/device/
     table and deletes the match;
   * OVS nodes return an error, stopped nodes return an error, and an
     ``ipv4``/``ipv6`` gateway/destination family mismatch is rejected.

   Example with ``grpcurl``:

   .. code-block:: bash

      grpcurl -plaintext -import-path internal/proto -proto netem.proto \
        -d '{
          "prjId": "XyZ",
          "nodes": {
            "R1": {"routes": [
              {"action": "ROUTE_REPLACE", "destination": "10.0.2.0/24",
               "gateway": "10.0.12.2", "device": "eth1"},
              {"action": "ROUTE_DELETE", "destination": "10.0.99.0/24",
               "device": "eth1"}
            ]}
          },
          "maxConcurrency": 4
        }' \
        localhost:10110 netem.Netem/RouteInstall

Links
~~~~~

``LinkAdd``
   ``LinkRequest`` → ``AckResponse``

   Adds a link to a running or stopped topology. If the topology is running,
   the veth pair is created immediately, QoS applied, interfaces set up and
   attached to their nodes; otherwise only the model and the topology file
   change. ``sync`` persists the link in ``network.yml``. Peers must be
   ``<node>.<ifIndex>``, must exist, and must not already be used by another
   link or bridge.

``LinkDel``
   ``LinkRequest`` → ``AckResponse``

   Deletes an existing link (peer order does not matter). If the topology is
   running, the veth pair and its qdiscs are removed and the interfaces
   detached; the link is always removed from the model, and ``sync`` rewrites
   the topology file. Deleting an unknown link is an error.

``LinkUpdate``
   ``LinkRequest`` → ``AckResponse``

   Replaces the QoS configuration of one link. The request may use either peer
   order; the server normalises it to the stored orientation. All QoS fields
   are replaced — including zeros, which clear previous impairments. When the
   request has no top-level QoS and both per-direction sections are equal, the
   values are mirrored into the top-level fields (so a later read reports the
   same profile in both directions). ``sync`` rewrites the topology file.

``LinkUpdateMany``
   ``LinkUpdateManyRequest`` → ``LinkUpdateManyResponse``

   Applies a batch of link QoS updates in one server-side operation:

   .. code-block:: protobuf

      message LinkUpdateManyRequest {
          string prjId = 1;
          repeated LinkConfig links = 2;
          bool sync = 3;
      }

      message LinkUpdateResult {
          Status status = 1;
          string peer1 = 2;
          string peer2 = 3;
          int64 durationMicros = 4;
      }

      message LinkUpdateManyResponse {
          Status status = 1;
          repeated LinkUpdateResult results = 2;
          uint32 updated = 3;
      }

   The server takes the topology lock once, locks each affected link once,
   groups qdisc operations by node, enters each node namespace once and opens
   one rtnetlink session per node. Results preserve request order and include
   per-link status and duration. A link appearing twice in the batch is an
   error for the duplicate. ``sync`` writes ``network.yml`` once at the end.

``LinkSyncMany``
   ``LinkSyncManyRequest`` → ``LinkSyncManyResponse``

   Applies a burst of adds, deletes and updates as one operation, in three
   phases: **deletes**, then **adds**, then **updates**. This ordering lets a
   client replace a link in a single request.

   .. code-block:: protobuf

      message LinkSyncManyRequest {
          string prjId = 1;
          repeated LinkSyncOperation operations = 2;
          bool sync = 3;
      }

      message LinkSyncOperation {
          LinkSyncAction action = 1;   // LINK_SYNC_ADD, LINK_SYNC_DEL, LINK_SYNC_UPDATE
          LinkConfig link = 2;
      }

      message LinkSyncManyResponse {
          Status status = 1;
          repeated LinkSyncResult results = 2;
          uint32 added = 3;
          uint32 deleted = 4;
          uint32 updated = 5;
      }

   Operations are validated and deduplicated before any change: the same link
   may not appear twice with the same action. Each link is processed
   independently and failures are reported per operation without aborting the
   batch. A shared namespace cache and a bounded worker pool
   (``docker.linkConcurrency``) are used. The topology file is written at most
   once, at the end, when ``sync`` is true.

Batch semantics at a glance
~~~~~~~~~~~~~~~~~~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 20 26 54

   * - RPC
     - Unit of work
     - Failure behaviour
   * - ``LinkUpdate``
     - one link
     - whole call fails
   * - ``LinkUpdateMany``
     - many links, one lock
     - per-link results, others continue
   * - ``LinkSyncMany``
     - adds/deletes/updates, one lock
     - per-operation results, ordered phases
   * - ``RouteInstall``
     - many nodes, netlink batches
     - per-node results, others continue

Concurrency notes:

* ``sync: true`` serialises the operation against topology-wide operations
  (run, reload, close, node add/delete) because it rewrites the topology file.
  ``sync: false`` operations can proceed concurrently with each other and with
  other non-sync link operations.
* Two ``LinkUpdateMany`` batches never overlap (they lock several links at
  once, so overlapping them could deadlock). Add/delete operations are not
  blocked by that mutex.

Message reference
-----------------

Enums
~~~~~

``StatusCode``
   ``OK = 0``, ``ERROR = 1``.

``IfState``
   ``UP = 0``, ``DOWN = 1``.

``RouteAction``
   ``ROUTE_REPLACE = 0``, ``ROUTE_DELETE = 1``.

``LinkSyncAction``
   ``LINK_SYNC_ADD = 0``, ``LINK_SYNC_DEL = 1``, ``LINK_SYNC_UPDATE = 2``.

Nested stream codes
   ``ExecCltMsg.Code``: ``CMD``, ``DATA``, ``RESIZE``, ``ERROR``, ``CLOSE``.
   ``ExecSrvMsg.Code``: ``STDOUT``, ``STDERR``, ``ERROR``, ``CLOSE``.
   ``CopyMsg.Code``: ``INIT``, ``DATA``, ``ERROR``.
   ``PullSrvMsg.Code``: ``START``, ``OK``, ``ERROR``.
   ``UpdateSrvMsg.Code``: ``START``, ``LOG``, ``OK``, ``ERROR``.
   ``CaptureSrvMsg.Code``: ``STDOUT``, ``STDERR``, ``OK``, ``ERROR``.
   ``TopologyRunMsg.Code``: ``NODE_COUNT``, ``BRIDGE_COUNT``, ``LINK_COUNT``,
   ``NODE_START``, ``LINK_SETUP``, ``BRIDGE_START``, ``NODE_LOADCONFIG``,
   ``NODE_MESSAGES``, ``NODE_STOP``, ``NODE_RM``.
   ``ProjectSaveMsg.Code``: ``NODE_COUNT``, ``NODE_SAVE``, ``DATA``.
   ``ProjectCloseMsg.Code``: ``NODE_COUNT``, ``BRIDGE_COUNT``, ``NODE_CLOSE``,
   ``BRIDGE_CLOSE``.

Requests and responses
~~~~~~~~~~~~~~~~~~~~~~

.. list-table::
   :header-rows: 1
   :widths: 26 74

   * - Message
     - Fields
   * - ``ProjectRequest``
     - ``id``
   * - ``OpenRequest``
     - ``name``, ``data`` (project archive bytes)
   * - ``WNetworkRequest``
     - ``id``, ``data`` (network.yml bytes)
   * - ``NodeRequest``
     - ``prjId``, ``node``
   * - ``NodeDelRequest``
     - ``prjId``, ``node``, ``sync``
   * - ``NodeIfStateRequest``
     - ``prjId``, ``node``, ``ifIndex``, ``state`` (``IfState``)
   * - ``NodeInterfaceRequest``
     - ``prjId``, ``node``, ``ifIndex``
   * - ``ConsoleCmdRequest``
     - ``prjId``, ``node``, ``shell``
   * - ``VrrpOptions``
     - ``interface``, ``group``, ``address``
   * - ``MgntOptions``
     - ``enable``, ``address``
   * - ``LinkConfig``
     - ``peer1``, ``peer2``, ``peer1qos``, ``peer2qos``
   * - ``LinkConfig.QoSConfig``
     - ``loss`` (float), ``delay`` (int ms), ``jitter`` (int ms),
       ``rate`` (int kbit/s), ``buffer`` (float), ``burst`` (int bytes),
       ``limit`` (uint32 packets), ``txqueuelen`` (uint32 packets)
   * - ``LinkRequest``
     - ``prjId``, ``link``, ``sync``
   * - ``Status``
     - ``code`` (``StatusCode``), ``error``
   * - ``AckResponse``
     - ``status``
   * - ``FileResponse``
     - ``status``, ``data``
   * - ``VersionResponse``
     - ``status``, ``version``
   * - ``ConsoleCmdResponse``
     - ``status``, ``cmd`` (argv)
   * - ``PrjListResponse``
     - ``status``, ``projects`` (``Info{id,name,openAt}``)
   * - ``PrjOpenResponse``
     - ``status``, ``id``
   * - ``ConfigFilesResponse``
     - ``status``, ``source`` (``ARCHIVE``/``RUNNING``),
       ``files`` (``ConfigFile{name,data}``)
   * - ``StatusResponse``
     - ``status``, ``name``, ``id``, ``openAt``, ``running``, ``nodes``
       (``NodeStatus{name,running,interfaces{name,state}}``)
   * - ``TopologyRunMsg``
     - ``code``, ``total``, ``nodeMessages`` (``NodeMessages{name,messages}``)
   * - ``ProjectSaveMsg``
     - ``code``, ``data``, ``total``
   * - ``ProjectCloseMsg``
     - ``code``, ``total``
   * - ``CopyMsg``
     - ``code``, ``prjId``, ``node``, ``nodePath``, ``data``
   * - ``ExecCltMsg`` / ``ExecSrvMsg``
     - see the ``NodeExec`` protocol above
   * - ``PullSrvMsg``
     - ``code``, ``image``, ``error``
   * - ``UpdateSrvMsg``
     - ``code``, ``step``, ``message``
   * - ``CaptureSrvMsg``
     - ``code``, ``data``
   * - ``RouteInstallRequest`` / ``RouteInstallResponse``
     - see the ``RouteInstall`` section
   * - ``LinkUpdateManyRequest`` / ``LinkUpdateManyResponse``
     - see the ``LinkUpdateMany`` section
   * - ``LinkSyncManyRequest`` / ``LinkSyncManyResponse``
     - see the ``LinkSyncMany`` section

Worked examples
---------------

Replace a link in one batch (delete + add + update others):

.. code-block:: bash

   grpcurl -plaintext -import-path internal/proto -proto netem.proto \
     -d '{
       "prjId": "XyZ",
       "operations": [
         {"action": "LINK_SYNC_DEL",
          "link": {"peer1": "R1.1", "peer2": "R2.1"}},
         {"action": "LINK_SYNC_ADD",
          "link": {"peer1": "R1.1", "peer2": "R2.1",
                   "peer1qos": {"delay": 10, "rate": 10000},
                   "peer2qos": {"delay": 10, "rate": 10000}}},
         {"action": "LINK_SYNC_UPDATE",
          "link": {"peer1": "host1.0", "peer2": "R1.0",
                   "peer1qos": {"loss": 1.5},
                   "peer2qos": {"loss": 1.5}}}
       ],
       "sync": true
     }' \
     localhost:10110 netem.Netem/LinkSyncMany

The response contains one ``results[]`` entry per operation, in request order,
plus ``added``, ``deleted`` and ``updated`` counters.

Add a namespace node live, without persisting:

.. code-block:: bash

   grpcurl -plaintext -import-path internal/proto -proto netem.proto \
     -d '{"prjId":"XyZ","node":"ns9",
          "config":{"type":"namespace","launch":true},
          "sync":false}' \
     localhost:10110 netem.Netem/NodeAdd

Read a node's FRR configuration:

.. code-block:: bash

   grpcurl -plaintext -import-path internal/proto -proto netem.proto \
     -d '{"prjId":"XyZ","node":"R1"}' \
     localhost:10110 netem.Netem/NodeReadConfigFiles

API stability
-------------

* Field numbers and message names are the compatibility contract; new fields
  may be added in future releases and must be treated as optional by clients.
* Enums may gain values; clients should tolerate unknown enum numbers.
* RPC behaviour documented here (ordering of batch phases, per-item error
  reporting, sync semantics) is stable within the 0.x series for the RPCs that
  exist; new batch RPCs are preferred over changing the semantics of the
  single-item ones.

See also
--------

* ``internal/proto/netem.proto`` — the authoritative definition.
* :ref:`internals-server` — implementation of the RPC handlers.
* :ref:`topology` — meaning of the node/link/bridge fields used in messages.
* :ref:`testing` — the Python gRPC integration tests, a worked client example.
