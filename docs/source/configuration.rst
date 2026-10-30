.. _configuration:

Configuration
=============

Server Configuration
--------------------

Configuration file
``````````````````

After the installation, the configuration of gonetem-server is done with the file
``/etc/gonetem/config.yaml``. Below, you will find the configuration by default :

.. code-block:: yaml

    listen: "localhost:10110"
    tls:
      enabled: false
      ca: ""
      cert: ""
      key: ""
    workdir: /tmp
    docker:
      nodes:
        router:
          image: mroy31/gonetem-frr
        host:
          image: mroy31/gonetem-host
        server:
          image: mroy31/gonetem-server
        p4sw:
          image: mroy31/gonetem-bmv2
      extraNodes: []

Description of available options:

- ``listen``: ip address (or dns name) / port on which the server listen when launched
- ``wordir``: the directory used by the server to store open project folders (with topology/configurations)
- ``tls.*``: options to enable and confgure a secure gRPC connection betwwen the console and the server. See :ref:`tls` for more detail to secure this connection
- ``docker.*``: options for docker nodes. See :ref:`nodes` for detail to configure existing nodes and define new one
- ``update.*``: options used by ``gonetem-console update`` to rebuild and reinstall gonetem on the server:

  - ``enabled``: must be set to ``true`` to allow updates (default to ``false``)
  - ``repository``: git repository cloned to build gonetem (default to ``https://github.com/satcomprojects/gonetem``)
  - ``branch``: git branch (or tag) checked out in ``repository`` (default to ``master``)
  - ``goVersion``: version of go downloaded to build gonetem (default to ``1.24.0``)
  - ``workdir``: folder used to clone and build gonetem. If empty, a temporary folder is used


Pull docker images
``````````````````

Before using gonetem, you have to pull docker images built for it
and available on docker hub. For that, you can use the following command after
the launch of gonetem-server:

.. code-block:: bash

    $ gonetem-console pull

Update gonetem
``````````````

``gonetem-server`` can update itself: it pulls the gonetem sources, downloads
the configured version of go, builds gonetem with ``make build-<arch>`` and
installs the new binaries in ``/usr/local/bin``. The update is launched from
the console with:

.. code-block:: bash

    $ gonetem-console update

The update must be enabled in the server configuration file (``update.enabled``).
It requires ``git``, ``make``, ``systemctl`` and the rights to install gonetem
(the server runs as root when installed with the systemd service).

At the end of the update, ``gonetem-server`` is restarted with
``systemctl restart gonetem.service``. The restart is scheduled without
blocking, because it stops the server process which runs the update. If the
service cannot be restarted, restart it manually to use the new version.

The ``make install-<arch>`` target is not used: it stops ``gonetem.service``
(which would kill the server running the update) and overwrites
``/etc/gonetem/config.yaml``.

Launch server
`````````````

If you install gonetem manually, you have lo launch gonetem-server with the root
right. It is required to execute docker or netlink actions. You can use
the following command for example:

.. code-block:: bash

    $ sudo gonetem-server

Below, you will find available arguments to launch gonetem-server

.. code-block:: bash

    Usage of gonetem-server:
        -conf-file string
                Configuration path (default "/etc/gonetem/config.yaml")
        -log-file string
                Path of the log file (default: stdout)
        -verbose
                Display more messages

If you use debian package, gonetem-server is launch thanks to systemd.


MPLS support
````````````

gonetem supports MPLS. To work, you must enable MPLS features
in linux kernel by loading the following modules :

- mpls_iptunnel
- mpls_router

Console Configuration
---------------------

The first time you launch `gonetem-console`, a configuration file is created
at this location: ``~/.config/gonetem-console/console.yaml``
You can view the current configuration with the following command:

.. code-block:: bash

    $ gonetem-console config show

For each parameter, you can modify the configuration with the command:

.. code-block:: bash

    $ gonetem-console config set <param-key> <param-value>

For example, to use `nano` as topology editor, simply enter the command:

.. code-block:: bash

    $ gonetem-console config set editor nano

For now, the following options are available:

- ``server`` to set the server uri used for connection (default to localhost:10110)
- ``editor`` to select the editor used to edit the topology file (default to vim)
- ``terminal`` to set the command line used to launch a console, default to

.. code-block:: bash

    xterm -xrm 'XTerm.vt100.allowTitleOps: false' -title {{.Name}} -e {{.Cmd}}

- ``tls.[enabled|ca|cert|key]`` to enabled and set tls options. See :ref:`tls` for more detail to secure gRPC connection

For example, if you want to change the font family/size used by xterm, you can enter the following command:

.. code-block:: bash

    gonetem-console config set terminal "xterm -fa 'Monospace' -fs 13 -xrm 'XTerm.vt100.allowTitleOps: false' -title {{.Name}} -e {{.Cmd}}"
