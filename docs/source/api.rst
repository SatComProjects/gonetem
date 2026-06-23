.. _api:

gRPC API
========

``gonetem-server`` exposes a gRPC service defined in ``internal/proto/netem.proto``. The following sections describe the methods used to manage nodes in a project topology.

NodeAdd
-------

Create a new node in an open project.

Request: ``NodeAddRequest``

.. code-block:: protobuf

    message NodeAddRequest {
        string prjId = 1;        // project identifier
        string node = 2;         // name of the node to create
        NodeConfigMsg config = 3;
        bool sync = 4;           // if true, persist the change in network.yml
    }

``NodeConfigMsg`` mirrors the node configuration used in the topology file:

.. code-block:: protobuf

    message NodeConfigMsg {
        string type = 1;                  // e.g. "docker.router", "docker.host", "ovs"
        bool ipv6 = 2;
        bool mpls = 3;
        repeated string vrfs = 4;
        repeated VrrpOptions vrrps = 5;
        repeated string volumes = 6;
        string image = 7;
        bool launch = 8;                  // start the node immediately if topology is running
        MgntOptions mgnt = 9;
    }

Response: ``AckResponse``

If the topology is running and ``config.launch`` is ``true``, the node is started immediately. When ``sync`` is ``true``, the ``network.yml`` file is rewritten to include the new node.

NodeDel
-------

Remove a node from an open project.

Request: ``NodeDelRequest``

.. code-block:: protobuf

    message NodeDelRequest {
        string prjId = 1;        // project identifier
        string node = 2;         // name of the node to remove
        bool sync = 3;           // if true, persist the change in network.yml
    }

Response: ``AckResponse``

All point-to-point links and bridge connections attached to the node are removed, the underlying Docker container or Open vSwitch bridge is deleted, and when ``sync`` is ``true`` the ``network.yml`` file is updated.
