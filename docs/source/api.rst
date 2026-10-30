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
        string type = 1;                  // e.g. "docker.router", "docker.host", "ovs", "namespace"
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

RouteInstall
------------

Apply a batch of route changes to several nodes in one call.

Request: ``RouteInstallRequest``

.. code-block:: protobuf

    message RouteInstallRequest {
        string prjId = 1;
        map<string, RouteBatch> nodes = 2;
        uint32 maxConcurrency = 3; // 0 uses the server configuration
    }

    message RouteBatch {
        repeated RouteSpec routes = 1;
    }

    message RouteSpec {
        RouteAction action = 1;       // ROUTE_REPLACE or ROUTE_DELETE
        string destination = 2;       // CIDR, e.g. 10.0.0.0/15
        string gateway = 3;           // optional for on-link routes
        string device = 4;            // interface inside the node namespace
    }

Response: ``RouteInstallResponse``

The server applies route batches inside each node's network namespace using
netlink, with one bounded worker pool per request. The pool size is
``docker.routeConcurrency`` (default 32) unless the request asks for a lower
value. Per-node failures are returned in ``RouteInstallResult.status`` and do
not abort the other nodes. The RPC is supported on Docker and namespace nodes;
OVS nodes return an error.

``RouteInstallResult`` also reports the number of applied routes and the
server-side duration. The older ``NodeRun`` RPC remains available for
arbitrary commands; clients should use ``RouteInstall`` for route batches.

LinkUpdateMany
--------------

Apply a batch of link QoS updates in one request.

Request: ``LinkUpdateManyRequest``

.. code-block:: protobuf

    message LinkUpdateManyRequest {
        string prjId = 1;
        repeated LinkConfig links = 2;
        bool sync = 3;
    }

Response: ``LinkUpdateManyResponse``

The server takes the topology write lock once, updates all link configs, groups
qdisc operations by node, enters each node namespace once, and uses one
rtnetlink session per node. Per-link failures are returned in
``LinkUpdateResult.status``; other links are still applied. Results preserve
request order and include the peer names and server-side duration. With
``sync`` set, the topology file is written once after the batch.
