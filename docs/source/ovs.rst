.. _ovs:

Switch console
==============

An ``ovs`` node is a Layer-2 switch. All switches of a project live in a
single Open vSwitch container created for that project; each switch is a bridge
inside it. The bridge is named after the switch, and its ports are named
``<switch>.<ifIndex>``, where ``ifIndex`` is the index used in the topology
(``sw1.0`` is port 0).

Opening a console on an OVS node does not run a shell: it starts
``ovs-console.py``, a command interpreter dedicated to switch configuration.
This page documents it.

.. code-block:: text

   [lab]> console sw1
   Welcome to switch console. Type help or ? to list commands.
   [sw1]>

The console is based on ``cmd2``; ``help`` and ``?`` list the registered
commands and subcommands, and ``exit``/``quit`` leave the console (the switch
stays configured).

Command overview
----------------

.. list-table::
   :header-rows: 1
   :widths: 46 54

   * - Command
     - Purpose
   * - ``show mac address-table``
     - Display the forwarding database.
   * - ``delete mac address-table``
     - Flush the forwarding database.
   * - ``show interfaces``
     - Show ports, MAC addresses and administrative state.
   * - ``show stp``
     - Show the spanning-tree state, or report that STP is disabled.
   * - ``set stp enable|disable``
     - Enable or disable STP on the bridge.
   * - ``show vlan [list|table]``
     - Show per-port VLAN configuration.
   * - ``set vlan port <port> mode access|trunk``
     - Set the VLAN mode of a port.
   * - ``set vlan port <port> access <vlan-id>``
     - Assign an access VLAN to a port.
   * - ``set vlan port <port> trunk <id[,id...]>``
     - Set the authorized trunk VLAN list.
   * - ``delete vlan port <port> access <vlan-id>``
     - Remove an access VLAN tag.
   * - ``delete vlan port <port> trunk <id[,id...]>``
     - Remove VLANs from the trunk list.
   * - ``show bonding <name>``
     - Show a bond, its mode and its members.
   * - ``set bonding <name> port <i1> <i2> [...]``
     - Create a bond (LACP active) from two or more ports.
   * - ``delete bonding <name>``
     - Delete a bond and return its ports to the bridge.

Ports are identified by their interface index inside the switch:
``set vlan port 0 ...`` acts on ``<switch>.0``. Only ports of the current
switch are accepted; another switch's port is rejected.

MAC address table
-----------------

.. code-block:: text

   [sw1]> show mac address-table
   Interface   VLAN   MAC                 Age
   0           1      00:00:00:00:01:01   12
   1           1      00:00:00:00:02:01   3

The table is produced from ``ovs-appctl fdb/show``; port numbers are translated
to interface indexes, and the ``Age`` column is the entry age in seconds.

To flush the table (useful after moving a MAC between ports):

.. code-block:: text

   [sw1]> delete mac address-table

Interfaces
----------

.. code-block:: text

   [sw1]> show interfaces
   Interface          MAC                 State
   0                  6a:0b:4c:11:22:33   UP
   1                  6a:0b:4c:11:22:34   UP
   sw1 (internal)     6a:0b:4c:11:22:35   UP

The listing comes from ``ovs-dpctl show``; the bridge's internal port is shown
with an ``(internal)`` suffix. ``State`` is the administrative state reported by
the kernel for the corresponding interface.

Spanning Tree
-------------

Show the state:

.. code-block:: text

   [sw1]> show stp

If STP is disabled on the bridge, the console answers:

.. code-block:: text

   STP is disabled on this bridge

Enable or disable it:

.. code-block:: text

   [sw1]> set stp enable
   [sw1]> set stp disable

STP is disabled by default on a new bridge. In a topology with redundant
switch links, enable it on every switch of the loop, exactly as you would on
physical equipment.

VLANs
-----

VLAN configuration is per port and follows the Open vSwitch port model:

* an **access** port carries a single VLAN (``tag``);
* a **trunk** port carries a set of authorized VLANs (``trunks``), and traffic
  is tagged on the wire.

Show the configuration as a list:

.. code-block:: text

   [sw1]> show vlan

   Interface 0
     Mode: access
     VLAN ID: 10
   Interface 1
     Mode: trunk
     Authorized VLAN IDs: 20,30

or as a table:

.. code-block:: text

   [sw1]> show vlan table

Set the mode of a port:

.. code-block:: text

   [sw1]> set vlan port 0 mode access
   [sw1]> set vlan port 1 mode trunk

Assign an access VLAN:

.. code-block:: text

   [sw1]> set vlan port 0 access 10

Set the authorized VLANs of a trunk port (comma-separated, no spaces):

.. code-block:: text

   [sw1]> set vlan port 1 trunk 20,30

Remove a VLAN assignment:

.. code-block:: text

   [sw1]> delete vlan port 0 access 10
   [sw1]> delete vlan port 1 trunk 20

Notes:

* the access VLAN identifier must be numeric;
* the trunk list must contain only digits and commas;
* ``mode`` only changes ``vlan_mode``; it does not set a tag or trunk list;
* a port with no explicit configuration shows up as ``mode: access``,
  ``VLAN ID: 0`` in the console's interpretation, matching OVS defaults.

A typical access/trunk scenario:

.. code-block:: text

   # port 0 is an access port for VLAN 10 (a host)
   [sw1]> set vlan port 0 mode access
   [sw1]> set vlan port 0 access 10

   # ports 1 and 2 form a trunk carrying VLANs 10 and 20 (other switches)
   [sw1]> set vlan port 1 mode trunk
   [sw1]> set vlan port 1 trunk 10,20
   [sw1]> set vlan port 2 mode trunk
   [sw1]> set vlan port 2 trunk 10,20

Bonding
-------

A bond aggregates several switch ports into one logical interface. GoNetEm
creates bonds with **LACP active** mode.

Create a bond from two existing ports:

.. code-block:: text

   [sw1]> set bonding my-bond port 0 2

The ports are removed from the bridge individually and re-added as members of
the bond; at least two ports are required, and each must belong to the switch.

Show the bond:

.. code-block:: text

   [sw1]> show bonding my-bond
   Mode: <bond_mode reported by Open vSwitch>
   Members:
      0: active (active)
      2: backup

The output comes from ``ovs-appctl bond/show``: mode, member status and which
member is currently active.

Delete a bond:

.. code-block:: text

   [sw1]> delete bonding my-bond

The bond port is removed and each member is re-added to the bridge as an
individual port, so connectivity is not lost.

Configuration persistence
-------------------------

Switch configuration is part of the project. When the project is saved, the
server runs:

.. code-block:: text

   ovs-config.py -a save -c /tmp/<switch>.conf <switch>

inside the OVS container and copies the file into ``configs/<switch>.conf``.
The file is JSON:

.. code-block:: json

   {
     "ports": [
       {
         "name": "sw1.0",
         "tag": "10",
         "trunks": "[]",
         "vlan_mode": "access"
       },
       {
         "name": "sw1.1",
         "tag": "[]",
         "trunks": "[20,30]",
         "vlan_mode": "trunk"
       }
     ],
     "stp_enable": "false"
   }

A bonded port additionally carries:

.. code-block:: json

   {
     "name": "sw1.my-bond",
     "tag": "[]",
     "trunks": "[]",
     "vlan_mode": "access",
     "bonding": {
       "members": ["sw1.0", "sw1.2"]
     }
   }

On start, if ``configs/<switch>.conf`` exists, the server runs
``ovs-config.py -a load``:

#. every port of the bridge is reset to ``vlan_mode=access``;
#. the saved STP state is applied;
#. each saved port is reconfigured (VLAN mode, tag, trunks);
#. bonded ports are recreated with ``add-bond ... lacp=active``;
#. the operations are retried up to ten times, with a short delay, to tolerate
   ports that are not yet visible while the topology starts.

If no configuration file exists, OVS defaults apply (ports behave as trunks
carrying all VLANs) and ``show vlan`` reflects that interpretation.

Persistence covers VLANs, trunks, STP and bonds; it does not capture arbitrary
``ovs-vsctl`` settings made by hand (for example OpenFlow rules). Use an init
script or the API for those.

Limitations
-----------

* ``copy`` is not supported on OVS nodes.
* ``RouteInstall`` is not supported on OVS nodes: they have no IP stack of
  their own. Configure routing on the routers behind them.
* OVS nodes cannot join the management network.
* The console disables dangerous ``cmd2`` built-ins (``py``, ``shell``,
  ``run_script``, ``run_pyscript``, ``macro``, ``alias``, ``shortcuts``,
  ``edit``) so it cannot be used to escape the switch context.
* Port numbering in the console is the *switch* interface index, not the Linux
  interface name. It always matches the peer index used in ``network.yml``.

See also
--------

* :ref:`nodes` — declaring switches in a topology.
* :ref:`internals-nodes` — OVS node and instance implementation.
* :ref:`project-format` — where ``<switch>.conf`` lives in the project.
