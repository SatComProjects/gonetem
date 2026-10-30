# gRPC integration tests

These tests exercise a **running** `gonetem-server` over gRPC. They require no
root privileges on the client side: the server creates the namespaces, veth
pairs and qdiscs.

The tests create a project with two nodes, start it, then verify the kernel
qdisc parameters with `tc -j qdisc show` executed inside the node namespaces
through the `NodeRun` RPC. They also configure IPv4 addresses and measure
end-to-end behavior:

- latency with `ping`: a 5 ms one-way delay yields a ~10 ms RTT, before and
  after `LinkUpdate`, and on a link created with `LinkAdd`;
- rate with `iperf3`: TCP throughput is capped by the configured tbf rate;
- loss with `ping` and UDP `iperf3`: 10% loss per direction gives ~19%
  round-trip loss for ping, while one-way UDP traffic sees ~10% loss. Both are
  checked before and after `LinkUpdate`.

Every test runs once with `namespace` nodes and once with `docker.host` nodes.
Each qdisc, RTT, loss and throughput result is printed as a colored
Value/Before/After/Expected table; run pytest with `-s` to display the tables.
Colors are enabled on a terminal, or with `FORCE_COLOR=1`; use `NO_COLOR=1` to
disable them.

The `docker.host` cases require the Docker image configured for the host node
type to be present on the server. `tc`, `ping` and `iperf3` must be available
in the node environment. No root privileges are needed on the client side: the
server performs all privileged operations.

## Run

```bash
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r tests/requirements.txt
python -m pytest -s tests/test_grpc_link_qos.py -v
```

By default the tests connect to `localhost:10110`. Override the target with:

```bash
GONETEM_GRPC_TARGET=host:port python -m pytest -s tests/test_grpc_link_qos.py -v
```
