.. _index:

GoNetEm documentation
=====================

GoNetEm is a **network emulator written in Go**. It builds reproducible virtual
networks out of Linux network namespaces, Docker containers, veth pairs, Linux
traffic control and Open vSwitch, and drives them from a command line console
over a gRPC API.

Use GoNetEm to:

* prototype and validate network designs (routing, switching, VPNs, QoS) on a
  single Linux host;
* reproduce network conditions — delay, jitter, loss, rate limits and queue
  sizes — with kernel-accurate `tc` semantics;
* automate experiments from scripts or CI through a documented gRPC interface;
* teach networking with a textual, versionable topology file.

.. code-block:: bash

   gonetem-console create ./lab.gnet   # empty project
   gonetem-console open ./lab.gnet     # upload, start, open console prompts

.. code-block:: yaml

   # network.yml — the topology stored inside a .gnet project
   nodes:
     host1:
       type: docker.host
     R1:
       type: docker.router
     sw1:
       type: ovs
   links:
     - peer1: host1.0
       peer2: R1.0
       delay: 10      # ms, both directions
       loss: 0.5      # percent
       rate: 10000    # kbit/s
     - peer1: R1.1
       peer2: sw1.0

Architecture at a glance
------------------------

GoNetEm is a client/server application:

``gonetem-server``
   Runs as root on the Linux host. It owns the emulated resources: Docker
   containers, network namespaces, veth pairs, qdiscs, Linux bridges and the
   Open vSwitch instance. It exposes the ``netem.Netem`` gRPC service, keeps
   the set of open projects in memory and persists them as ``.gnet`` archives.

``gonetem-console``
   Runs on the operator workstation. It implements the ``gonetem-console``
   CLI (project lifecycle, image pull, update), the interactive
   ``[project]>`` prompt, the switch console, remote node consoles, packet
   capture to Wireshark and file copy to/from nodes.

See :ref:`architecture` for the component and data model, and
:ref:`internals-server` for the server implementation.

Documentation map
-----------------

The manual is organised in five parts. If you are new to GoNetEm, read
:ref:`introduction` and :ref:`getting-started` first.

.. toctree::
   :maxdepth: 2
   :caption: User guide
   :numbered:

   introduction
   getting-started
   usage
   installation
   configuration
   topology
   nodes
   commands
   ovs
   tls

.. toctree::
   :maxdepth: 2
   :caption: Reference
   :numbered:

   api
   project-format
   docker-images

.. toctree::
   :maxdepth: 2
   :caption: Operations
   :numbered:

   deployment
   troubleshooting
   faq

.. toctree::
   :maxdepth: 2
   :caption: Architecture and internals
   :numbered:

   architecture
   internals/server
   internals/topology
   internals/link
   internals/nodes
   internals/console
   internals/update

.. toctree::
   :maxdepth: 2
   :caption: Contributing
   :numbered:

   development
   testing
   glossary

Indices and tables
------------------

* :ref:`genindex`
* :ref:`search`

Project links
-------------

* Source code: https://github.com/satcomprojects/gonetem
* Graphical user interface: https://github.com/mroy31/gonetem-ui
* License: GNU General Public License v3.0 or later (see ``COPYING``)
* Version documented here: **0.8.0**
