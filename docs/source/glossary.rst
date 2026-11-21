.. _glossary:

Glossary
========

.. glossary::
   :sorted:

   BDP
      Bandwidth-delay product: ``rate × delay``, the amount of data "in flight"
      for a link. GoNetEm expresses the tbf queue limit as a multiple of the
      BDP with the ``buffer`` option.

   bridge (topology)
      A Linux bridge that connects node interfaces to a physical host
      interface, used to reach the outside world. Declared in ``bridges:``.

   buffer
      QoS option: tbf queue limit in BDP multiples. The kernel limit is
      ``buffer × rate × delay / 8`` bytes. Not a packet count.

   burst
      QoS option: tbf bucket size in bytes. When unset, the server uses half
      the configured rate value (for example 500 bytes at 1000 kbit/s).

   console
      The ``gonetem-console`` binary. Also used for a node's interactive
      session (``console <node>``), which is either a shell, ``vtysh``, the P4
      switch CLI or the switch console.

   container
      A Docker container. For Docker node types it is the emulated machine;
      for OVS it hosts all switches of a project.

   Docker node
      A node implemented by a container, declared ``docker.<type>``. Its type
      definition (image, commands, configuration files) is configured on the
      server.

   extra node
      A Docker node type declared by the administrator under
      ``docker.extraNodes`` and used as ``docker.<type>`` (for example VyOS).

   ``.gnet``
      A GoNetEm project archive: a gzipped tar containing ``network.yml`` and
      optionally ``configs/``.

   GoNetEm
      The name of the project in prose. Commands and paths use ``gonetem``.

   interface index
      The number ``N`` in ``<node>.N``. In a Docker or namespace node it maps
      to ``ethN``; in an OVS node it maps to the port ``<switch>.N``.

   link
      A point-to-point veth pair between two node interfaces, with optional
      per-direction QoS.

   management network
      An optional Linux bridge with a host address that gives each attached
      node a dedicated ``mgnt`` interface, used to access nodes from the host.

   netem
      Linux kernel qdisc that emulates delay, jitter and loss. Used for the
      corresponding QoS options.

   node
      An emulated device: Docker container, OVS bridge or network namespace.

   namespace node
      A node that is only a Linux network namespace (type ``namespace``),
      sharing the host filesystem and processes.

   OVS
      Open vSwitch. In GoNetEm, OVS nodes are bridges inside one shared
      Open vSwitch container per project.

   peer
      One end of a link, written ``<node>.<interface-index>``.

   project
      A topology plus its configuration files, stored as a ``.gnet`` file and
      opened on the server under a short runtime identifier.

   prompt
      The interactive ``[project]>`` interpreter of ``gonetem-console``.

   qdisc
      Queueing discipline: the kernel object implementing scheduling, shaping
      and impairment (netem, tbf).

   QoS
      Quality of service applied to a link direction: delay, jitter, loss,
      rate, buffer, burst, limit and txqueuelen.

   rate
      QoS option: token-bucket shaping rate in kbit/s (``tbf``). Applies per
      direction and requires a delay.

   route
      A kernel routing entry installed in a node namespace. ``RouteInstall``
      is the batch RPC; ``RouteSpec`` is the typed route.

   short name
      A compact, unique per-project identifier used in kernel object names to
      respect the 15-character interface name limit.

   ``sync``
      Request flag asking the server to rewrite ``network.yml`` after a live
      topology change.

   tbf
      Token Bucket Filter, the Linux qdisc used for rate shaping.

   topology
      The content of ``network.yml``: nodes, links, bridges and the optional
      management network. Also used for the in-memory model managed by the
      server.

   txqueuelen
      Interface transmit queue length in packets. Applied to the interfaces
      that terminate a link; defaults to 1000.

   VRF
      Virtual Routing and Forwarding. GoNetEm creates Linux VRFs for Docker
      nodes that declare ``vrfs``.

   VRRP
      Virtual Router Redundancy Protocol. GoNetEm prepares the macvlan
      interfaces needed by FRR's VRRP implementation for ``docker.router``
      nodes that declare ``vrrps``.

   workdir
      The server configuration key naming the directory under which project
      temporary directories are created.
