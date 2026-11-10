.. _tls:

TLS/SSL configuration
=====================

By default the gRPC connection between ``gonetem-console`` and
``gonetem-server`` is plaintext. That is acceptable when both run on the same
machine and the server listens on the loopback interface. As soon as the
server listens on a network address, the connection must be secured: the API
can execute commands inside nodes and copy files, so an unauthenticated
connection is equivalent to a remote shell.

GoNetEm supports **mutual TLS**: the server presents a certificate and requires
a client certificate signed by the same certificate authority (CA). Both ends
verify the other.

.. warning::

   Plaintext gRPC on a non-loopback address grants full control of the
   emulated network — and of the node containers — to anyone who can reach the
   port. Enable TLS and restrict the port with a firewall.

Certificates
------------

The repository ships ``scripts/generate-cert.sh`` to create a private CA and a
server/client pair. It produces 4096-bit RSA keys valid for 3650 days.

Prepare two extension files in the directory where you run the script:

``server-ext.cnf``
   Must contain at least a ``subjectAltName`` covering the names and addresses
   used by consoles to reach the server. Example:

   .. code-block:: ini

      subjectAltName=DNS:localhost,DNS:gonetem.example.org,IP:192.0.2.10

``client-ext.cnf``
   Extensions for the console certificate. It may be empty; a common value is:

   .. code-block:: ini

      subjectAltName=DNS:operator.example.org

Then run:

.. code-block:: bash

   sh scripts/generate-cert.sh

The script deletes every ``*.pem`` and ``*.srl`` file in the current directory
before starting; run it in an empty directory. It generates:

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - File
     - Content
   * - ``ca-cert.pem``
     - CA certificate, to be distributed to both ends.
   * - ``server-cert.pem``, ``server-key.pem``
     - Server certificate and private key, signed by the CA.
   * - ``client-cert.pem``, ``client-key.pem``
     - Console certificate and private key, signed by the CA.
   * - ``ca-key.pem``
     - CA private key, deleted at the end of the script. Keep a backup if you
       need to issue new certificates later.

Server configuration
--------------------

Copy ``ca-cert.pem``, ``server-cert.pem`` and ``server-key.pem`` to the server,
for example under ``/etc/gonetem``, restrict their permissions, and edit
``/etc/gonetem/config.yaml``:

.. code-block:: yaml

   listen: "0.0.0.0:10110"
   tls:
     enabled: true
     ca: /etc/gonetem/ca-cert.pem
     cert: /etc/gonetem/server-cert.pem
     key: /etc/gonetem/server-key.pem

.. code-block:: bash

   sudo install -m 600 -o root -g root server-key.pem /etc/gonetem/
   sudo install -m 644 ca-cert.pem server-cert.pem /etc/gonetem/
   sudo systemctl restart gonetem

The server builds its TLS configuration with:

* ``Certificates`` set to the configured server certificate/key;
* ``ClientAuth: tls.RequireAndVerifyClientCert``;
* ``ClientCAs`` set to the configured CA.

Consequences:

* a console without a client certificate is rejected during the handshake;
* a client certificate signed by another CA is rejected;
* if ``ca``/``cert``/``key`` are empty or unreadable, the server refuses to
  start the listener and exits with an error in the log.

The server log reports ``TLS enabled: true`` at startup; verify with:

.. code-block:: bash

   sudo journalctl -u gonetem -n 20 | grep -i tls

Console configuration
---------------------

Copy ``ca-cert.pem``, ``client-cert.pem`` and ``client-key.pem`` to the
operator machine and configure the console:

.. code-block:: bash

   gonetem-console config set server gonetem.example.org:10110
   gonetem-console config set tls.enabled true
   gonetem-console config set tls.ca   /home/me/gonetem-certs/ca-cert.pem
   gonetem-console config set tls.cert /home/me/gonetem-certs/client-cert.pem
   gonetem-console config set tls.key  /home/me/gonetem-certs/client-key.pem

Which can also be done by editing
``~/.config/gonetem-console/console.yaml``:

.. code-block:: yaml

   server: "gonetem.example.org:10110"
   editor: vim
   terminal: "xterm -xrm 'XTerm.vt100.allowTitleOps: false' -title {{.Name}} -e {{.Cmd}}"
   tls:
     enabled: true
     ca: /home/me/gonetem-certs/ca-cert.pem
     cert: /home/me/gonetem-certs/client-cert.pem
     key: /home/me/gonetem-certs/client-key.pem

The console uses the configured CA as ``RootCAs`` and supplies its client
certificate. Verify the connection with any command:

.. code-block:: bash

   gonetem-console list

Certificate names and hostname verification
-------------------------------------------

The console connects to the URI in ``server`` (``host:port``) and verifies the
server certificate against that host name. Go's TLS stack uses the connection
authority as ``ServerName``; there is **no override option** in the console
configuration.

Therefore:

* if ``server: gonetem.example.org:10110``, the server certificate must contain
  ``DNS:gonetem.example.org`` in its ``subjectAltName``;
* if ``server: 192.0.2.10:10110``, it must contain
  ``IP:192.0.2.10``;
* ``localhost`` needs ``DNS:localhost`` (the script's default example does not
  include it — add it to ``server-ext.cnf`` before generating).

A mismatch produces a handshake error such as::

   tls: failed to verify certificate: x509: certificate is valid for
   gonetem.example.org, not 192.0.2.10

Regenerate the server certificate with the correct SANs in that case. Do not
disable verification: the console has no such option, by design.

Troubleshooting
---------------

.. list-table::
   :header-rows: 1
   :widths: 34 66

   * - Symptom
     - Likely cause and fix
   * - ``connection refused``
     - Server not listening on that address/port, or firewall. Check
       ``listen`` and ``ss -ltnp | grep 10110``.
   * - ``certificate signed by unknown authority``
     - The console CA is not the CA that signed the server certificate. Use
       the same ``ca-cert.pem`` on both sides.
   * - ``remote error: tls: certificate required`` / handshake failure
     - The console did not send a client certificate, or the server CA does
       not match. Check ``tls.enabled`` on the console and the three paths.
   * - ``x509: certificate has expired``
     - Certificates are valid 3650 days; check the clock and regenerate.
   * - ``x509: certificate is valid for X, not Y``
     - SAN/hostname mismatch. Connect with the name in the SAN or regenerate
       the certificate.
   * - ``unable to open CA cert`` / ``unable to open cert/key``
     - Wrong path or unreadable file. Remember the server runs as root under
       systemd, with a minimal ``PATH`` but absolute paths work.
   * - Server starts but consoles hang
     - One side has TLS enabled and the other does not. Both must agree.
   * - ``failed to add CA's certificate to cert pool``
     - The CA file is not PEM or is empty.

Operational notes
-----------------

* **One CA per deployment.** Using a public CA is unnecessary; a private CA
  simplifies rotation and keeps the certificates independent of DNS
  infrastructure.
* **Protect the client key.** It grants full API access. Store it with mode
  ``600`` and never put it in a shared home directory.
* **Rotation.** Generate a new CA and certificates, distribute the new CA and
  client files, restart the server with the new server key/certificate and CA,
  then replace operators' client files. Because the server verifies the client
  certificate against the configured CA, an old client keeps working until the
  CA file changes.
* **No hostname override.** Plan the SANs before deployment if consoles will
  connect through several names or addresses (add all of them).
* **Firewall.** Restrict the listening port to the operator network; mutual
  TLS protects against unauthenticated clients, not against traffic analysis or
  denial of service.
* **Reverse proxies are not supported.** The console speaks gRPC with mutual
  TLS directly; if you place a proxy in front of it, it must support gRPC and
  client certificate forwarding.

See also
--------

* :ref:`configuration` — server and console configuration keys.
* :ref:`deployment` — exposing a server to a network.
* :ref:`troubleshooting` — general diagnostics.
