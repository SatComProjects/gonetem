.. _usage:

Usage patterns
==============

This page describes the day-to-day ways of using GoNetEm and how the three
surfaces relate. Detailed command syntax is in :ref:`commands`; a first
walkthrough is in :ref:`getting-started`.

Three ways to drive GoNetEm
---------------------------

.. list-table::
   :header-rows: 1
   :widths: 22 30 48

   * - Surface
     - Entry point
     - Best for
   * - CLI
     - ``gonetem-console <command>``
     - Project lifecycle, image pulling, attaching to a running project.
   * - Interactive prompt
     - ``[project]>`` after ``open``/``connect``
     - Exploring, editing the topology, consoles, ad-hoc commands, saving.
   * - gRPC API
     - ``netem.Netem`` service
     - Automation, scripted experiments, CI, custom tools (:ref:`api`).

The CLI and the prompt are both clients of the same API. Anything the prompt
does can be scripted; conversely, some API operations (runtime QoS changes,
batch route installation) have no dedicated prompt command and are used from
code.

Project lifecycle
-----------------

.. code-block:: text

   create  ──►  open  ──►  check ──►  run  ──►  interact  ──►  save  ──►  quit
   (local)     (upload)   (validate) (start)   (consoles,   (download)  (cleanup)
                                                exec, ...)

* ``create`` writes an empty project locally; no server involved.
* ``open`` uploads the archive and loads it. Use ``--no-start`` to skip the
  automatic ``run``.
* ``check`` validates ``network.yml`` at any time.
* ``run`` starts the topology; ``reload`` restarts it from the current file.
* Interaction covers consoles, ``exec``, ``ifState``, capture, copy and the
  gRPC API.
* ``save`` captures node configurations and writes the archive back; ``saveAs``
  writes to a new path.
* ``quit`` closes the project on the server and removes all resources.

Common workflows
----------------

Interactive lab
~~~~~~~~~~~~~~~

.. code-block:: bash

   gonetem-console create ./lab.gnet
   gonetem-console open ./lab.gnet

.. code-block:: text

   [lab]> edit          # write/edit network.yml
   [lab]> check
   [lab]> reload
   [lab]> status
   [lab]> console all   # terminals for every running node
   [lab]> save
   [lab]> quit

Use ``--no-start`` when you want to edit the topology before anything is
created:

.. code-block:: bash

   gonetem-console open --no-start ./lab.gnet
   # [lab]> edit
   # [lab]> check
   # [lab]> run

Reconnect to a running project
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

If the console was closed without closing the project (for example the terminal
crashed), attach to it:

.. code-block:: bash

   gonetem-console list
   gonetem-console connect

``connect`` prompts for one of the open projects and enters its prompt. There is
no local project path in this mode: ``save`` is refused and ``saveAs`` must be
used.

Save a copy without touching the original
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

.. code-block:: text

   [lab]> saveAs /archive/lab-2025-01-01.gnet

This captures the current node configuration and topology into a new file; the
path used at ``open`` is left untouched.

Run a scripted experiment
~~~~~~~~~~~~~~~~~~~~~~~~~

Use the API for anything that must be deterministic and repeatable:

#. open the project and start it;
#. configure nodes with ``NodeRun`` or install routes with ``RouteInstall``;
#. change conditions with ``LinkUpdateMany``/``LinkSyncMany``;
#. collect results from your own traffic generators;
#. close the project.

A Python example is in :ref:`api`; the integration tests in ``tests/`` are a
complete working client.

Inspect and reuse configurations
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

.. code-block:: text

   [lab]> viewConfig R1                  # display the live FRR configuration
   [lab]> config /tmp/lab-configs        # extract every node configuration
   [lab]> save                           # store them in the project

The naming and archive layout are documented in :ref:`project-format`.

Work with large topologies
~~~~~~~~~~~~~~~~~~~~~~~~~~

* prefer ``namespace`` nodes for scale; use Docker nodes only where the image
  matters;
* generate the topology with a script (``scripts/generate-project.py``) or
  from templates;
* use ``sync: false`` API calls during the experiment and ``save`` (or a final
  sync) only when you need the file;
* raise the concurrency settings if start/close dominates
  (:ref:`configuration`).

Command cheat sheet
-------------------

.. list-table::
   :header-rows: 1
   :widths: 36 64

   * - Goal
     - Command
   * - Create / extract a project
     - ``gonetem-console create|extract``
   * - Open / reconnect
     - ``gonetem-console open|connect``
   * - List / clean
     - ``gonetem-console list``, ``gonetem-console clean``
   * - Images
     - ``gonetem-console pull``
   * - Validate / start / restart
     - ``check``, ``run``, ``reload``
   * - Inspect
     - ``status``, ``viewConfig``, ``config <dir>``
   * - Interact
     - ``console``, ``shell``, ``exec``
   * - Capture / copy
     - ``capture``, ``copy``
   * - Link state
     - ``ifState <node>.<if> up|down``
   * - Save
     - ``save``, ``saveAs``
   * - Leave
     - ``quit``/``exit``

If a command behaves unexpectedly, ``help`` lists everything with usage and
:ref:`troubleshooting` covers the common failures.
