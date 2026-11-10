.. _development:

Development guide
=================

This guide is for contributors working on GoNetEm itself. It covers the
environment, the repository layout, the development loop, coding conventions and
the release process.

Environment
-----------

Required:

* Go **1.24+**;
* GNU Make;
* Docker Engine (for node tests and manual testing);
* `protoc` with ``protoc-gen-go`` and ``protoc-gen-go-grpc`` (only when changing
  the API);
* a Linux host with root access — the server creates namespaces, veth pairs and
  qdiscs, and most integration-level tests need root;
* optionally Python 3 with ``grpcio``/``grpcio-tools``/``pytest`` for the
  integration tests (:ref:`testing`);
* optionally Sphinx to build this documentation.

.. code-block:: bash

   git clone https://github.com/satcomprojects/gonetem.git
   cd gonetem
   go mod download
   make build-amd64

The binaries land in ``bin/``:

.. code-block:: bash

   sudo ./bin/gonetem-server_amd64 -verbose
   ./bin/gonetem-console_amd64 list

Branch model
------------

.. list-table::
   :header-rows: 1
   :widths: 18 82

   * - Branch
     - Purpose
   * - ``master``
     - Stable branch, receives merges from ``develop`` at release time.
   * - ``develop``
     - Integration branch. **Target your pull requests here.**
   * - ``release/x.y.z``
     - Release stabilisation branches, merged into ``master``.
   * - ``feat/...``, ``fix/...``, ``perf/...``
     - Topic branches.

Repository layout
-----------------

.. code-block:: text

   cmd/
     gonetem-server/main.go       server entry point
     gonetem-console/main.go      console entry point
   internal/
     proto/netem.proto            gRPC contract (+ generated stubs)
     server/                      RPC handlers, topology engine, validation
     console/                     CLI, prompt, PTY, UI
     docker/                      Docker client + Docker node
     ovs/                         OVS instance + switch node
     namespace/                   network-namespace node
     link/                        netns, veth, tc, routes, offloads
     options/                     configuration, version, TLS
     update/                      self-update pipeline
     utils/                        project archive, helpers
   conf/                          default config, systemd unit, sysctl
   docker/                        image sources (host, server, frr, bmv2, ovs, vyos)
   scripts/                       certificate generation, project generation, migration
   tests/                         Python gRPC integration tests
   docs/                          Sphinx documentation (this manual)
   Makefile                       build/install/package targets
   VERSION                        project version

Development loop
----------------

Build and run:

.. code-block:: bash

   make build-amd64
   sudo ./bin/gonetem-server_amd64 -verbose          # terminal 1
   ./bin/gonetem-console_amd64 -s localhost:10110 list   # terminal 2

The server needs root. Running it in the foreground with ``-verbose`` gives the
per-phase timings (``Run: all link setups took ...``) that make performance work
practical.

Fast iteration patterns:

* restart the server between changes — the project registry is in memory, and
  open projects are closed on shutdown;
* keep a project open with ``--no-start`` while working on the console, and use
  ``run``/``reload`` to iterate;
* use ``namespace`` nodes for fast tests: they avoid container startup;
* keep ``workdir`` on a real filesystem, not ``tmpfs``, when testing large
  projects.

Debugging with VS Code / Delve
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

The repository ships a debug configuration that runs Delve through ``sudo``:

* ``.vscode/launch.json`` starts ``cmd/gonetem-server/main.go`` with
  ``DEBUG_AS_ROOT=true``;
* ``.vscode/settings.json`` maps ``dlv-dap`` to ``.vscode/dlv-sudo.sh``, which
  re-executes Delve under ``sudo -E`` when that variable is set;
* ``.vscode/dlv-sudo.sh`` also adds ``$GOPATH/bin`` to ``PATH`` if
  ``dlv-dap`` is not found.

Install Delve with ``go install github.com/go-delve/delve/cmd/dlv@latest`` and
make sure ``sudo`` can run it.

Manual inspection
~~~~~~~~~~~~~~~~~

Useful while developing:

.. code-block:: bash

   # containers of a project
   docker ps --filter name=ntm

   # interfaces of a project (project id appears in every name)
   ip -br link | grep ntm

   # qdiscs in a node namespace
   sudo nsenter --net=/var/run/docker/netns/<id> tc -qdisc show
   # or, for a namespace node
   sudo nsenter --net=/run/netns/ntm<id>.<node> tc qdisc show

   # bridges and veths in the root namespace
   ip -d link show type bridge
   ip -d link show type veth

   # server log
   sudo tail -f /var/log/gonetem-server.log

For Docker nodes, ``docker inspect <container> | jq '.[0].State.Pid'`` gives the
PID whose ``/proc/<pid>/ns/net`` is the node namespace.

Code conventions
----------------

* ``gofmt`` all Go code; ``go vet ./...`` must pass.
* Wrap errors with context and ``%w``:
  ``fmt.Errorf("unable to create node %s: %w", name, err)``. Batch operations
  aggregate with ``errors.Join``.
* Keep the layering: ``internal/server`` does not call netlink directly,
  ``internal/link`` does not call Docker, only ``internal/docker`` talks to the
  Docker API, and protobuf types do not leak into the topology engine.
* Log with the package's logrus entry and include ``project``/``node`` fields;
  use ``Debugf`` for timings and per-object detail, ``Infof`` for lifecycle
  milestones, ``Warnf`` for recoverable anomalies.
* Protect shared state with the existing locks; do not add new global mutexes
  without documenting the ordering in the code and in
  :ref:`internals-topology`.
* Bound goroutines with an ``errgroup`` limit taken from the configuration;
  never spawn unbounded per-node goroutines.
* Use the test seams (``routeApplier``, ``qosBatchApplier``) instead of
  building kernel-dependent tests when the logic can be tested in isolation.
* Add user-visible configuration, commands and API changes to this
  documentation in the same pull request.

Commit messages
~~~~~~~~~~~~~~~

The project uses conventional-commit-style subjects with a scope, mixed with
plain messages for small changes:

.. code-block:: text

   feat(server): add LinkSyncMany batch RPC
   fix(server): make link add/delete concurrency-safe
   perf(server): configure node interfaces without docker exec
   test(qos): cover queue fallbacks and fix grpc stub generation
   docs: bump version to 0.8.0

Explain the *why* in the body; keep the subject under ~72 characters.

Adding features
---------------

.. list-table::
   :header-rows: 1
   :widths: 30 70

   * - Task
     - Steps
   * - New RPC
     - ``netem.proto`` → ``make proto`` → handler on ``*netemServer`` →
       conversion helpers → tests → :ref:`api`.
   * - New node type (existing image)
     - ``docker.extraNodes`` entry; no code.
   * - New node family
     - Implement ``INetemNode`` in a new package, add creation to
       ``CreateNode``, update validation if needed.
   * - New node/QoS option
     - Internal struct + YAML tag + validation + protobuf field +
       conversion + kernel application + documentation.
   * - New console command
     - Register in ``RegisterCommands``, implement the function, add argument
       regexes, extend completion when node-related, document in
       :ref:`commands`.
   * - New image
     - ``docker/<name>/Dockerfile``, publish, reference in configuration,
       document in :ref:`docker-images`.

Prefer additive changes: new fields and new RPCs. Changing the meaning of an
existing field or the ordering of a batch is a breaking change and needs a
discussion on an issue first.

Documentation
-------------

The manual is Sphinx/reStructuredText under ``docs/source``:

.. code-block:: bash

   python3 -m venv /tmp/docsvenv
   /tmp/docsvenv/bin/pip install -r docs/requirements.txt
   /tmp/docsvenv/bin/sphinx-build -b html docs/source docs/build/html

Then open ``docs/build/html/index.html``. Read the Docs builds the same
configuration (``.readthedocs.yaml``).

Guidelines:

* cross-reference with ``:ref:`label```; every new page needs a label and an
  entry in the toctree of ``index.rst``;
* use ``code-block`` directives with a language (``yaml``, ``bash``,
  ``protobuf``, ``go``, ``text``);
* keep the reference pages exhaustive and the tutorial pages task-oriented;
* document defaults and constraints exactly as the code enforces them.

Release process
---------------

#. Update ``VERSION`` and ``docs/source/conf.py`` (``version`` and ``release``).
#. Update the changelog/release notes on GitHub.
#. Build and test all targets:

   .. code-block:: bash

      make build-amd64 build-arm64 build-armv7
      sudo go test ./...
      python -m pytest -s tests/test_grpc_link_qos.py -v   # against a server

#. Build Debian packages (``make build-deb-amd64`` etc.) if relevant.
#. If node behaviour changed, bump ``IMAGE_VERSION`` in
   ``internal/options/server.go`` **and** rebuild/publish the images; otherwise
   keep image tags stable.
#. Merge ``develop`` into ``master`` (or via the release branch), tag the
   release, attach binaries/packages to the GitHub release.
#. Verify the Read the Docs build.

Compatibility checklist for a release:

* old ``.gnet`` files still open;
* old configuration files still load (unknown keys ignored, defaults applied);
* the console version and server version can differ without breaking basic
  commands;
* the gRPC contract is backward compatible (fields only added).

Troubleshooting the development environment
-------------------------------------------

.. list-table::
   :header-rows: 1
   :widths: 36 64

   * - Symptom
     - Fix
   * - ``operation not permitted`` when creating namespaces
     - Run as root (``sudo``), including tests.
   * - ``docker image ... not present``
     - Pull the images with ``gonetem-console pull`` or ``docker pull``.
   * - ``protoc-gen-go: program not found``
     - ``go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`` and
       ensure ``$GOPATH/bin`` is in ``PATH``.
   * - tests skipped
     - Kernel tests call ``skipUnlessRoot``; run with ``sudo``.
   * - stale containers after a crash
     - ``gonetem-console clean`` or ``docker rm -f $(docker ps -aq --filter
       name=ntm)``.
   * - stale named namespaces
     - ``sudo ip netns list | grep ntm`` then ``sudo ip netns delete <name>``.
   * - Sphinx warnings about missing ``_static``/``_templates``
     - The repository provides ``docs/source/_static``; ``_templates`` is
       optional and those warnings are harmless.

See also
--------

* :ref:`testing` — unit and integration tests.
* :ref:`architecture` — where to change what.
* :ref:`internals-server` — service layer extension guide.
