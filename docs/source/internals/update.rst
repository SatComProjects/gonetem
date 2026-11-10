.. _internals-update:

Self-update internals
=====================

``gonetem-server`` can rebuild and reinstall itself. The feature exists so a
deployed server (including a small ARM device) can be updated without an
interactive build environment, using only the configured Git repository and a
downloaded Go toolchain.

Reached through ``ServerUpdate`` (``gonetem-console update``), implemented in
``internal/update``.

Configuration
-------------

.. code-block:: yaml

   update:
     enabled: true
     repository: https://github.com/satcomprojects/gonetem
     branch: master
     goVersion: "1.24.0"
     workdir: ""

See :ref:`configuration` for the meaning of each key. ``enabled`` defaults to
false and must be set explicitly: enabling it lets the server execute arbitrary
code from the configured repository as root.

Pipeline
--------

``Updater.Run`` executes:

.. code-block:: text

   1. checkPlatform
      - OS: linux or darwin
      - arch: amd64 or arm64 (the Makefile has install targets for these only)
   2. context timeout: 30 minutes for the whole update
   3. prepareWorkdir
      - configured workdir, or a fresh temporary directory
   4. pull
      - git clone --depth 1 --branch <branch> <repository> <workdir>/src
      - logs HEAD (short hash + subject)
   5. installGo
      - reuse <workdir>/go/bin/go if it reports the configured version
      - otherwise download https://go.dev/dl/go<version>.<os>-<arch>.tar.gz
      - extract safely (path traversal check) and remove the archive
   6. build
      - run `make build-<arch>` in <workdir>/src with the go environment below
   7. install
      - copy <workdir>/src/bin/gonetem-{server,console}_<arch> to
        /usr/local/bin/gonetem-{server,console}
      - atomic replace (temporary file + chmod 0755 + rename)
   8. restart
      - systemctl restart --no-block gonetem.service
   9. complete
      - report success

Every step is reported to the console through the ``Logger`` interface
(``Step`` and ``Log``), which the RPC handler forwards as ``UpdateSrvMsg``
messages.

Why the Makefile install target is not used
-------------------------------------------

``make install-<arch>`` would:

* stop ``gonetem.service`` — killing the very process running the update;
* overwrite ``/etc/gonetem/config.yaml`` with the packaged default.

The updater therefore copies only the binaries, then asks systemd to restart
the service with ``--no-block``: the request is queued and the current process
is stopped by systemd, which starts the new binary.

Go build environment
--------------------

``goEnv`` builds the environment for the build command:

.. code-block:: text

   PATH=<downloaded go bin>:<current PATH>
   HOME=<goHome>
   GOPATH=<goHome>
   GOMODCACHE=<goHome>/pkg/mod
   GOCACHE=<goHome>/build-cache
   GOTOOLCHAIN=local

Details:

* ``gonetem-server`` is started by systemd, which does not set ``HOME``. Without
  it, the Go toolchain cannot locate its caches; the updater creates and uses
  its own home.
* ``goHome`` is ``<update.workdir>/go-home`` when a workdir is configured,
  otherwise ``<os.TempDir()>/gonetem-update-go``. It is deliberately **outside**
  the temporary work directory so the module and build caches are reused by the
  next update, making subsequent builds much faster.
* ``GOTOOLCHAIN=local`` prevents Go from downloading another toolchain on its
  own (the version is controlled by ``goVersion``).
* The Makefile invokes ``go`` from ``PATH``, hence the ``PATH`` prefix with the
  downloaded toolchain.

Command execution
-----------------

``run`` starts the command with the given directory and environment, scans
stdout and stderr line by line (1 MiB max line length), forwards each line to
the logger, and keeps the last five lines to enrich the error message if the
command fails. Cancellation (context timeout or client disconnect) aborts the
command and is reported as an interruption.

Failure behaviour
-----------------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Failure
     - Result
   * - update disabled
     - ``ErrDisabled``: ``update is disabled: set update.enabled to true ...``
   * - unsupported OS/arch
     - ``ErrUnsupportedPlatform`` with the reason
   * - concurrent update
     - ``ErrBusy``: ``an update is already running``
   * - git clone / download / build / install
     - the step fails, the stream receives ``ERROR`` with the command's last
       lines; with a temporary workdir the directory is **kept** for debugging
       and its path is logged
   * - restart fails
     - logged; the update is still reported complete with a message asking for
       a manual restart
   * - client disconnects
     - the context is cancelled, the running command is killed, the workdir is
       kept (treated as a failure)

If ``update.workdir`` is configured, the work directory is never deleted; this
is the recommended setting for repeated updates because it preserves the Go
caches and the sources.

Concurrency
-----------

A package-level ``sync.Mutex`` in the handler serialises updates: a second
request receives ``ErrBusy``. Serialisation is necessary because two builds
would write into the same work directory and race on the install step.

Security
--------

The update pipeline is powerful and deliberately simple:

* it clones whatever repository and branch are configured and runs
  ``make build-<arch>``, i.e. **arbitrary code as root**;
* it downloads a Go toolchain over HTTPS from ``go.dev`` and extracts it (with
  path-traversal protection) into the work directory;
* it replaces binaries in ``/usr/local/bin`` atomically, but there is no
  signature verification and no rollback: a bad build is installed and the
  service restarts on it.

Recommendations:

* leave ``update.enabled: false`` unless you control the configured repository
  and branch;
* pin ``branch`` to a stable tag or a branch you protect;
* verify the new version after the restart (``gonetem-console version``);
* keep the previous binaries if you need a manual rollback (the updater does
  not keep them);
* run the server with mutual TLS; anyone who can call ``ServerUpdate`` can
  replace the server binary.

The restart at the end is the reason a successful update may make the RPC stream
end abruptly: the process that would send the final message is being stopped.
The console treats a well-formed ``complete`` message before the close as
success and reports the restart.

Testing
-------

``internal/update/update_test.go`` covers the testable parts without network or
systemd:

* disabled configuration and unsupported architecture;
* ``goArch`` mapping (``armv7`` → ``armv6l``);
* safe extraction (``safeJoin``, tar.gz extraction, escaping entries);
* the Go environment (``goEnv``, cache home without a workdir);
* atomic install (``copyFile``, missing binary error);
* restart without a service configured;
* step logging (``TestUpdater_Run_LogsSteps``).

The ``Updater`` struct takes ``Config``, ``Logger``, ``Arch``, ``InstallDir``
and ``Service`` as fields precisely so tests can redirect the install directory
and disable the service restart.

See also
--------

* :ref:`configuration` — ``update.*`` keys.
* :ref:`installation` — alternative upgrade paths.
* :ref:`deployment` — operational recommendations.
