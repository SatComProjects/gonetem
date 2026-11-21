# Deployment

This page describes running GoNetEm in a shared or production-like environment: host preparation, service configuration, network exposure, operations and hardening. For a laptop lab, [Installation](installation.md) is enough.

## Deployment model

One `gonetem-server` manages **one Linux host**. All emulated resources — containers, namespaces, veth pairs, bridges and qdiscs — live on that host. Multiple operators can connect concurrently over gRPC, but they share the same privileged context: there is no per-user isolation, no quota and no ownership of projects. A deployment is therefore best understood as "a shared lab server".

```text
operators (console, scripts, CI)
     | gRPC + mutual TLS
     v
gonetem-server (root, systemd)  --  Docker daemon
     |                                |
     +-- namespaces, veth, tc, bridges
     +-- OVS container per project
     +-- per-project temp directories under workdir
```

## Host preparation

1.  **Linux, Docker Engine, root.** See [Installation](installation.md).

2.  **Kernel modules** used by the node families you need:

    ```bash
    modprobe openvswitch     # if you use ovs nodes
    modprobe mpls_router     # if you use mpls: true
    modprobe mpls_iptunnel
    modprobe vrf             # if you use vrfs
    modprobe macvlan         # if you use vrrps
    ```

    Persist them in `/etc/modules-load.d/gonetem.conf`.

3.  **Kernel tuning.** The packaged `conf/99-custom-network.conf` raises the neighbour table and inotify limits, which matters for topologies with many nodes:

    ```bash
    sudo cp conf/99-custom-network.conf /etc/sysctl.d/
    sudo sysctl --system
    ```

4.  **Dedicated working directory.** `/tmp` is often a tmpfs: project directories, saved configurations and copy buffers live under `workdir`. Use a persistent filesystem:

    ```yaml
    workdir: /var/lib/gonetem
    ```

    ```bash
    sudo install -d -m 0750 /var/lib/gonetem
    ```

5.  **Docker images.** Pull them once; do not rely on the server to pull images implicitly (it does not):

    ```bash
    gonetem-console pull
    # or, per image:
    docker pull mroy31/gonetem-host:0.6.0
    ```

6.  **Disk space.** Budget for: project directories (small), copy buffers (file size), and Docker's own storage for containers and images (images are several hundred MB each, especially the bmv2 image).

## Service configuration

The packaged unit is minimal:

```ini
[Unit]
Description=Gonetem server
After=network.target

[Service]
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStartPre=/bin/touch /var/log/gonetem-server.log
ExecStartPre=/bin/chown root:adm /var/log/gonetem-server.log
ExecStart=/usr/local/bin/gonetem-server -log-file /var/log/gonetem-server.log

[Install]
WantedBy=multi-user.target
```

Operational notes:

- the service runs as root (required);

- it does **not** wait for Docker explicitly. `After=network.target` is not enough on boot: add `After=docker.service` (or `Requires=docker.service`) if the server starts before the daemon and projects fail with Docker errors;

- logs go to `/var/log/gonetem-server.log`, which is not rotated by the unit. Add a `logrotate` rule:

  ```bash
  sudo tee /etc/logrotate.d/gonetem >/dev/null <<'EOF'
  /var/log/gonetem-server.log {
      weekly
      rotate 8
      compress
      missingok
      notifempty
      copytruncate
  }
  EOF
  ```

- use `-verbose` only for troubleshooting: debug logs include per-operation timings and grow quickly.

## Network exposure

| Scenario | Configuration |
|----|----|
| Same host | `listen: localhost:10110`, TLS disabled. |
| Remote operators | `listen: 0.0.0.0:10110` (or a specific address), mutual TLS enabled, firewall restricted to the operator network. |
| Appliance / CI | `listen: 127.0.0.1:10110` and a local reverse tunnel or an operator-only jump host. |

Firewall example (nftables):

```bash
nft add rule inet filter input tcp dport 10110 ip saddr 192.0.2.0/24 accept
nft add rule inet filter input tcp dport 10110 drop
```

TLS is mandatory for anything other than loopback: the API can execute commands in nodes and replace the server binary. See [TLS/SSL configuration](tls.md).

### Remote consoles

For operators on other machines:

```bash
gonetem-console config set server gonetem.example.org:10110
gonetem-console config set tls.enabled true
gonetem-console config set tls.ca   ~/certs/ca-cert.pem
gonetem-console config set tls.cert ~/certs/client-cert.pem
gonetem-console config set tls.key  ~/certs/client-key.pem
```

Remember that node consoles are opened as **local terminals** on the operator machine: the console spawns xterm and the terminal runs `gonetem-console console ...`, which connects back to the server. The operator machine therefore needs the console binary and the same TLS configuration (it is read from the same user configuration file).

## Security hardening checklist

- [ ] mutual TLS enabled, dedicated CA, client keys protected (`600`);

- [ ] `listen` bound to a specific address, firewall in place;

- [ ] `update.enabled` false unless the repository/branch is trusted;

- [ ] images pulled from trusted registries, tags pinned (no floating `latest`);

- [ ] `workdir` on a dedicated filesystem with restrictive permissions;

- [ ] Docker daemon not exposed over TCP; only the local socket;

- [ ] operators are trusted: they can run privileged containers through the API;

- [ ] host kernel patched; node images rebuilt for security updates;

- [ ] log retention and rotation configured.

### What an operator can do

Through the API, an operator can:

- create containers on the host (privileged, all capabilities);
- create and delete network namespaces, veth pairs, bridges and qdiscs;
- mount host paths into containers with `volumes`;
- execute arbitrary commands in containers;
- replace `/usr/local/bin/gonetem-{server,console}` if the update is enabled.

Treat access to the gRPC endpoint as equivalent to root on the host.

## Capacity and tuning

| Resource | Guidance |
|----|----|
| CPUs | 4+ cores for small labs, 16+ for hundreds of nodes. Link setup and container operations are mostly kernel/Docker bound, so cores help up to a point. |
| Memory | Each container has its own memory footprint (tens of MB for hosts, more for FRR/bmv2). Namespace nodes are nearly free. |
| Disk | Docker images dominate; per-project data is small. Configuration folders (TFTP/BIND data) can grow. |
| Kernel limits | Thousands of interfaces and namespaces stress the neighbour tables; apply the provided sysctl file. Watch `dmesg` for netlink/namespace errors. |
| Concurrency | Raise the pools moderately (2–4× CPU count). See [Configuration](configuration.md). |

## Monitoring

GoNetEm does not expose metrics or a health endpoint. Practical checks:

```bash
# service and process
systemctl is-active gonetem
pgrep -af gonetem-server

# API reachable (any command that lists projects)
gonetem-console list

# server version (also detects a wrong binary after an update)
gonetem-console version

# leftover containers (should be empty when no project is open)
docker ps --filter name=ntm --format '{{.Names}}'

# stale namespaces
ip netns list | grep ntm || true

# disk usage of the workdir and Docker
du -sh /var/lib/gonetem
docker system df
```

A simple external check is to run `gonetem-console list` every minute and alert on failure. Alert on the container count when it is not explained by open projects.

## Backup and restore

The durable state is the `.gnet` project files, stored wherever operators keep them — **not** on the server. The server's temp directories are deleted when projects close and are not a backup.

Recommended practice:

- keep projects in a version-controlled directory (`git`);
- save before any server restart (open projects are closed and deleted on shutdown);
- back up the server configuration, TLS material and node images (or their registry references);
- for long-running experiments, use `config <dir>`/`saveAs` periodically to capture configurations on the operator side.

## Restart and crash recovery

On a clean stop (`systemctl restart`), the server closes every open project, which removes containers, namespaces and bridges.

After a hard crash, leftovers are possible:

```bash
gonetem-console clean                          # gonetem containers
docker ps -aq --filter name=ntm | xargs -r docker rm -f
ip netns list | grep ntm | awk '{print $1}' | xargs -r -n1 ip netns delete
ip link show type bridge | grep ntm            # bridges
ip link show type veth | grep ntm              # veths
```

Then restart the service. Unsaved project state is lost on a crash: this is inherent to the in-memory model.

Avoid automatic restarts in the middle of experiments. If systemd restarts the service (`Restart=always` is **not** configured by default), open projects are closed on the next shutdown, not preserved.

## Upgrades

Three paths, in order of operational safety:

1.  **Debian package** — atomic from the operator's point of view; the service restarts and the configuration file is preserved.
2.  **Self-update** (if enabled) — convenient for remote/embedded hosts; no configuration overwrite; requires git/make/systemctl and network access to the repository and `go.dev`.
3.  **From source** (`make install-<arch>`) — overwrites `/etc/gonetem/config.yaml`; avoid on production hosts unless you keep the configuration elsewhere.

Before upgrading:

- save all open projects;
- note the server and image versions;
- verify the new version after restart (`gonetem-console version` and `docker images`);
- keep the old binaries for rollback if self-updating.

Images and binary versions are independent: upgrading the server does not upgrade node images, and vice versa. The image tag is appended automatically when the configuration omits one (currently `0.6.0`).

## CI and automation

Typical pattern:

```bash
# on the runner: install gonetem-console, configure server + TLS
gonetem-console open --name "ci-$CI_JOB_ID" ./topology.gnet
# ... run tests through gRPC or console exec ...
gonetem-console connect    # or use the gRPC client directly
# always close the project to release resources
```

Recommendations:

- give each job a unique project name (`--name`);
- always close the project, even on failure (`ProjectClose`);
- run a cleanup job that calls `clean` and checks leftover containers;
- keep `update.enabled` disabled on CI servers;
- prefer `namespace` or `host` nodes for speed; reserve FRR/bmv2 for the tests that need them;
- pin the image tags in the configuration so runs are reproducible.

## High availability

GoNetEm has no HA mode. Two servers on the same host would fight over Docker containers and kernel object names; two hosts give independent emulators. For availability, keep projects as files (they can be reopened on another host) and automate deployment of configuration and images.

## Scaling limits

Practical observations from the design:

- container creation and removal dominate project open/close;
- namespace nodes scale much further than containers;
- link setup is parallel and fast (netlink), but each link creates a veth and at least one qdisc, which is visible in `dmesg` if you exhaust netlink buffers;
- very large topologies (hundreds of nodes) benefit from raising `createConcurrency`/`startConcurrency` and from running with `-verbose` once to find the slow phase.

## See also

- [Configuration](configuration.md) — all keys and concurrency tuning.
- [TLS/SSL configuration](tls.md) — securing remote access.
- [Troubleshooting](troubleshooting.md) — failure diagnosis.
- [Topology engine](internals/topology.md) — what the server does during run and close.
